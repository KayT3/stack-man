# Commands

Two complementary extension points:

1. **Go `Command` interface** — for logic that ships in the agent binary (and for
   implementing the DSL executor itself).
2. **`.stackman.yaml` repo commands** — declarative, per-repo, no recompile.

## 1. The Go `Command` interface

This is the "interface-type thing" you asked for. Adding a native command =
implement one interface and register it.

```go
package commands

// Spec describes the inputs a command accepts, so the UI can render a form.
type Param struct {
    Name        string
    Description string
    Required    bool
    Default     string
    // Allowed lists the only acceptable values; empty = free-form.
    Allowed     []string
}

type CommandSpec struct {
    Params []Param
    // NeedsBuild hints the UI to show a "--build" checkbox for compose-backed commands.
    NeedsBuild bool
}

// Command is a named, composable action run against a Stack.
type Command interface {
    Name() string
    Description() string
    Spec() CommandSpec
    // Run executes the command. `out` receives live output (logs/progress).
    // Returning an error marks the command failed.
    Run(ctx context.Context, stack *Stack, params map[string]string, out io.Writer) error
}
```

A `Registry` holds `map[string]Command`; built-ins self-register in `init()`:

```go
package builtin

func init() {
    commands.Register(&BackupCommand{})
    commands.Register(&UpdateCommand{})
}
```

The daemon and CLI both dispatch through the registry, so a command written once
is available from both the desktop app and `stackman command run`.

## 2. `.stackman.yaml` — declarative repo commands

Repo-specific commands (like `backup` and `update`) live in the repo itself so
they're versioned and travel with the code. The agent reads
`<repo>/.stackman.yaml`.

```yaml
version: 1

# Optional: command to auto-run after a successful `git pull`.
on_pull: update

commands:
  backup:
    description: "Run the hotel backup job"
    steps:
      - exec:
          service: backup
          command: hotel-backup run

  update:
    description: "Build images and apply DB migrations"
    steps:
      - compose:
          action: build
      - compose:
          action: up
          flags: ["-d", "--build"]
      - exec:
          service: next
          command: npx prisma migrate deploy
```

### Step types

Each step is one object. Steps run in order; the first failing step aborts the
command with a clear report.

| Type | Fields | Runs |
|------|--------|------|
| `compose` | `action` (`build`\|`up`\|`down`\|`restart`\|`pull`\|`ps`), `flags[]`, `services[]` | `docker compose <action> [flags] [services]` |
| `exec` | `service`, `command`, `workdir?`, `env?` | `docker compose exec -T <service> <command>` |
| `shell` | `command`, `cwd?` | raw shell (escape hatch; avoid when possible) |
| `git` | `action` (`pull`\|`fetch`\|`check`), `branch?` | git op via the `GitClient` |

### How this maps to your two real commands

**`backup`** → `docker compose exec backup hotel-backup run`

```yaml
backup:
  steps:
    - exec: { service: backup, command: hotel-backup run }
```

**`update`** → "run the default build, then `docker compose exec next npx prisma migrate deploy`"

```yaml
update:
  steps:
    - compose: { action: build }                 # "default build"
    - compose: { action: up, flags: ["-d", "--build"] }
    - exec: { service: next, command: npx prisma migrate deploy }
```

(If your "default build" is just `docker compose up -d --build`, drop the separate
`build` step and keep `up --build`.)

### Params (parameterized commands)

A command can declare params that are interpolated into steps:

```yaml
commands:
  restore:
    description: "Restore a backup by name"
    params:
      - name: backup_name
        required: true
        description: "Which backup to restore"
    steps:
      - exec:
          service: backup
          command: hotel-backup restore ${backup_name}
```

`${name}` is substituted from `stackman command run <stack> restore --param backup_name=…`.
The CLI and desktop UI both render the params as a small form.

## 3. Built-in commands

These ship in the agent and are always available; the `.stackman.yaml` ones are
repo-specific. Built-ins include:

- `backup` / `update` as generic *executors* that read their steps from
  `.stackman.yaml` (i.e. the YAML is the source; the Go command just runs it).
- `git-check`, `git-pull` thin wrappers (also exposed as `stackman git …`).
- `up` / `down` / `restart` / `build` as command-form aliases of the container
  methods, so they can be composed into `on_pull` chains.

## 4. How `command run` executes

```
parse .stackman.yaml
  → resolve command + params
  → for each step:
       translate to a StepExecutor (compose/exec/shell/git)
       run, streaming output to out
       on error: abort, report { step, exit_code, output }
  → success: report { ok: true }
```

The `StepExecutor` set is another small interface, so adding a new step type is
just `RegisterStep("name", executor)`.

```go
type StepExecutor interface {
    Execute(ctx context.Context, stack *Stack, params map[string]string, out io.Writer) error
}
```

## 5. Extending it yourself — the two recipes

**Add a repo command** (no code): edit `.stackman.yaml`, commit, done. The agent
reads it on every `command list`/`run`.

**Add a native agent command** (code): create a type implementing `Command`, call
`commands.Register` in its `init()`, rebuild. Available everywhere instantly.

**Add a new step type** (code): implement `StepExecutor`, `RegisterStep` it, and
the YAML DSL can use it.
