# Stacks

A **stack** is a registered directory on a VPS that is (a) a git repository and
(b) contains a Docker Compose file. It's the unit the agent manages.

## 1. The stack model

```go
type Stack struct {
    ID       string    // uuid/slug
    Name     string    // unique, human-facing (defaults to dir basename)
    Path     string    // absolute dir on the VPS
    Remote   string    // git remote URL, e.g. git@github.com:owner/repo.git
    Branch   string    // tracked branch (default: current)
    ComposeFile string  // default "docker-compose.yml"
    Allow    []string  // required/allowlisted services
    Exclude  []string  // force-ignored services (e.g. database)
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

## 2. Registration (`stack add`)

Rules enforced at registration:

1. `path` must exist and be a git repo (`.git/` present or `git rev-parse`
   succeeds). Otherwise **refuse** with a clear error.
2. A Compose file must be found (`docker-compose.yml`, `compose.yml`, or an
   override chain). Otherwise refuse (you can still register, but warn — see §5).
3. `Remote` defaults to the repo's current `origin` URL; `--remote` overrides.
4. `--allow` / `--exclude` take comma-separated service names. They are validated
   against the services actually present in the Compose file, with a warning for
   unknown names (typo protection, not a hard error).

```bash
stackman stack add /srv/hotel \
  --remote git@github.com:C-LentoSC/Star-Class-Hotel-system.git \
  --allow next,ws,nginx \
  --exclude mysql
```

## 3. Service policy (allow / exclude)

The agent parses the Compose file to enumerate services, then computes the
**effective managed set**:

```
all       = services in the compose file
allowed   = (Allow is empty ? all : Allow)
managed   = allowed − Exclude
```

- `managed` — the agent may start/stop/restart/build these.
- `Exclude` — **force-ignored**. The agent will never touch these services
  (e.g. `mysql`, so a bad deploy can't drop your database). They also won't be
  torn down by `containers down` unless explicitly requested.
- Services in neither set (when `Allow` is non-empty) are **untouched** by
  default.

This is stored per-stack (see §4). It's validated against the *current* compose
services; if the repo adds a service later, `stack services <name>` will show it
as "new/unspecified" so you can decide its policy.

Compose parsing uses `github.com/compose-spec/compose-go/v2` to load the model,
so `depends_on`, profiles, `extends`, and env interpolation are all honored —
you get the real, effective service list.

## 4. Storage (SQLite)

`/var/lib/stackman/stackman.db`. Schema (illustrative; migrations handled by the
agent on startup):

```sql
CREATE TABLE stacks (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  path         TEXT NOT NULL,
  remote       TEXT,
  branch       TEXT,
  compose_file TEXT NOT NULL DEFAULT 'docker-compose.yml',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE stack_services (
  stack_id TEXT    NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
  service  TEXT    NOT NULL,
  policy   TEXT    NOT NULL CHECK (policy IN ('allow','exclude')),
  PRIMARY KEY (stack_id, service)
);
```

- The DB is the **source of truth** for registration and service policy.
- Repo *commands* are **not** stored here — they live in the repo (`.stackman.yaml`),
  because they travel with the code and are versioned. See `commands.md`.

## 5. Where Compose files and overrides live

The agent looks for, in order: `compose.yml`, `docker-compose.yml`, and merges
`compose.override.yml` / `docker-compose.override.yml` when present. A single
deployed stack can therefore override per-VPS without editing the repo's base
file (e.g. bind mounts that differ between hosts).

## 6. Git operations (`stackman git …`)

Git is wrapped behind a `GitClient` interface (shell implementation backed by the
system `git`), so callers never touch the binary directly and it can be swapped
for `go-git` later.

### `status`
`git status --porcelain=v1` + branch/upstream info. Raw, no side effects.

### `fetch`
`git fetch <remote> <branch>` (or all). Reports new remote head.

### `check` (the important one)
A read-only dry-run that answers "is it safe to pull, and what will change?"

1. `git fetch` the remote.
2. Working-tree cleanliness: `git status --porcelain` must be empty. Non-empty →
   `clean: false`, `safe_to_pull: false` (uncommitted changes could conflict).
3. Divergence: `git rev-list --left-right --count HEAD...FETCH_HEAD` →
   `ahead` / `behind`. If **both** non-zero → `diverged: true`.
4. File delta: `git diff --name-status HEAD...FETCH_HEAD` → `changed_files`.
5. `safe_to_pull = clean && !diverged && behind > 0`.

Returns the full `GitCheck` object (schema in `api.md`).

### `pull`
Proceeds **only** if `check` says safe, otherwise aborts with a conflict error and
no tree changes:

```
stackman git pull hotel
→ error: local working tree is dirty / branches have diverged.
→ handle it manually on the VPS (git stash/merge), then retry.
```

On success: fast-forward, report `from → to`, and (optionally) trigger the
post-pull hook (see `commands.md`, `on_pull`).

## 7. Manual-conflict workflow (what the user does on the VPS)

When `pull` refuses:

```bash
ssh vps-1
cd /srv/hotel
git status                  # see local changes
git stash                   # or commit them
git pull --rebase           # or merge
# resolve conflicts, then:
git stash pop / commit
```

The agent deliberately does **not** try to auto-merge. The philosophy: the
agent's job is to *detect* and *report*; risky resolution stays human.

## 8. Containers

`containers up/down/restart/build/ps/logs/exec` all operate on the **managed**
service set by default, and accept `services[]` to target a subset. The agent
translates the method into `docker compose <verb>` with the right `-f` files and
service list, using argv (no shell string concatenation).

`containers network connect/disconnect` map to `docker network connect/disconnect`,
used to attach a service to an external network (e.g. join an existing shared
`mynet` on the host) or detach it. `containers network ls` shows the networks the
stack defines plus host networks, so you can see what's available to connect to.
