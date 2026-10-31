# stack-man

A self-hosted control plane for managing Docker Compose **stacks** across multiple
VPSes connected over a private Cloudflare WARP / Magic WAN subnet.

You register a git repo + `docker-compose.yml` as a **stack**, then drive it from a
desktop client: check for git updates (before pulling), pull with conflict
detection, bring containers up/down, run migrations and backups — without ever
exposing the management API to the public internet.

## Why this exists

- The VPSes expose **only HTTPS (public ingress) and SSH**. No new public ports.
- The management API lives **only on the private subnet**, bound to the subnet IP.
- Auth reuses the SSH keys you **already** have in `~/.ssh/authorized_keys` on
  every VPS — no new credentials, no manual login.
- One Go binary per server acts as both the **agent daemon** and the **CLI**.

## Components

| Component | What it is | Where it runs |
|-----------|-----------|----------------|
| **`stackman` agent** | One Go binary. CLI (`config`, `install`, `stack add`, git/container/command ops) **and** daemon (`stackman daemon` — private RPC API). | Each VPS |
| **`stackman-desktop` client** | Go + Wails desktop app. Auto-discovers agents, authenticates with your SSH key, drives stacks. | Your laptop |
| **`pkg/`** | Shared Go module: auth, discovery, and RPC message types used by both agent and client. | Library |

## The mental model

```
                ┌─────────────────────────────────────────────┐
                │  Private subnet  (WARP / Magic WAN, 100.96/12)│
                │                                               │
  ┌──────────┐  │   ┌──────────────┐     ┌──────────────┐       │
  │ Desktop   │◄─┼──►│  stackman     │     │  stackman     │     │
  │ client    │  │   │  (VPS #1)     │     │  (VPS #2)     │     │
  └──────────┘  │   │  daemon :42702│     │  daemon :42702│     │
   SSH key     │   └──────┬───────┘     └──────┬───────┘     │
   (challenge) │          │ stacks             │ stacks       │
                └──────────┼────────────────────┼─────────────┘
                           ▼                    ▼
                    docker compose        docker compose
                    (git repos)           (git repos)

   Public side:  [Internet] ──► Cloudflare Tunnel (HTTPS) ──► nginx/etc
                [SSH] ──► sshd  (unchanged, still your only other door)
```

The agent never listens on a public interface. The public ingress and SSH stay
exactly as they are today.

## Quick start (agent)

```bash
# build
cd agent && go build -o stackman ./cmd/stackman

# interactive config: pick your WARP subnet, ports, authorized_keys path
./stackman config

# install as a systemd service (registers + enables + starts)
sudo ./stackman install

# register a stack (must be a git repo)
./stackman stack add /srv/hotel --allow next,ws,nginx --exclude mysql

# day-to-day
./stackman git check hotel
./stackman containers up hotel --build
./stackman command run hotel update
```

## Quick start (client)

```bash
# one-time: install the Wails CLI (https://wails.io/docs/gettingstarted/installation)
cd client/frontend && npm install && npm run build
cd .. && wails build -tags wails
```

The resulting `stackman-desktop` binary discovers agents on your WARP subnet
(or uses the static list in `~/.config/stackman/client.toml`), authenticates with
your SSH key, and shows the dashboard. See [`docs/client.md`](docs/client.md).

> `go build ./client/...` also works without the Wails toolchain: it produces a
> headless backend used for smoke tests (`-list-agents`).

## Documentation

Read in this order:

1. [`docs/architecture.md`](docs/architecture.md) — system overview, data flow, security model.
2. [`docs/decisions.md`](docs/decisions.md) — the decisions we made and why (ADRs).
3. [`docs/agent.md`](docs/agent.md) — the agent binary: CLI, config file, systemd.
4. [`docs/api.md`](docs/api.md) — the RPC protocol and SSH challenge-response auth.
5. [`docs/discovery.md`](docs/discovery.md) — UDP beacon + the Magic WAN caveat and fallback.
6. [`docs/stacks.md`](docs/stacks.md) — the stack model, allow/deny services, storage.
7. [`docs/commands.md`](docs/commands.md) — the command system (Go interface + compiled-in repo profiles).
8. [`docs/client.md`](docs/client.md) — the Wails desktop client.

## Repository layout

```
stack-man/
├── go.work                  # Go workspace linking the three modules
├── pkg/                     # shared types (imported by agent + client)
│   ├── auth/  discovery/  api/
├── agent/                   # the stackman binary
│   ├── cmd/stackman/
│   └── internal/ (cli, config, daemon, auth, discovery, stack, compose, commands, git, systemd)
├── client/                  # the Wails desktop app
│   ├── main.go  main_wails.go  app.go  wails.json
│   └── frontend/
└── docs/
```

> The module path `github.com/<owner>/stack-man` is a placeholder — replace it
> with your real repo path when you `git init` this.

## Status / roadmap

- [x] **Phase 1 — Agent core**: config, stack registry, compose parsing, git, command engine, systemd.
- [x] **Phase 2 — Daemon**: private RPC API, SSH auth, UDP discovery.
- [x] **Phase 3 — Client core**: discovery + auth + stack/git/container operations.
- [x] **Phase 4 — Client UI**: dashboard, service lists, command runner, logs. *(last, by design)*
