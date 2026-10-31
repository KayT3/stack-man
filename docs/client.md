# The client (`stackman-desktop`)

Go + Wails desktop app. UI is intentionally last — this doc fixes the backend
shape now, and the frontend is a thin skin over it.

## 1. Responsibilities

- Discover agents (UDP beacon + static list).
- Authenticate to each agent with your SSH key (challenge-response).
- List stacks, inspect services/policy.
- Run git checks (show changed files before pulling), pulls, container ops, and
  profile commands.
- Stream logs/progress from long-running commands.

## 2. Layout

```
client/
├── main.go              # Wails entry
├── app.go               # App struct with bound methods (the "backend")
├── wails.json           # Wails project config
├── internal/
│   ├── config/          # client config (agent list, key path, agent use)
│   ├── discovery/       # beacon client + static list merge
│   ├── agent/           # AgentConnection: TCP + handshake + JSON-RPC
│   └── state/           # in-memory model exposed to the frontend
└── frontend/            # Svelte app
```

The shared wire types come from `pkg/` (`auth`, `discovery`, `api`), so the
client and agent can't drift on message shapes.

## 3. Client config

`~/.config/stackman/client.toml`:

```toml
# SSH identity used to authenticate to agents.
private_key_path = "~/.ssh/id_ed25519"   # or omit to use ssh-agent
use_ssh_agent     = true                  # prefer SSH_AUTH_SOCK when set

# Discovery.
discovery_port    = 42701
discovery_timeout = "2s"

# Static agents (reliable fallback for Magic WAN; see discovery.md).
[[agent]]
  id   = "vps-1"
  addr = "100.96.1.2:42702"
```

## 4. Backend services (Go)

### `AgentConnection`

Wraps one authenticated JSON-RPC session:

```go
type AgentConnection struct { /* … */ }

func Dial(ctx context.Context, addr string, signer ssh.Signer) (*AgentConnection, error)
func (c *AgentConnection) Call(ctx context.Context, method string, params, result any) error
func (c *AgentConnection) Notify(ch chan<- Notification)      // stream/log events
func (c *AgentConnection) Close() error
```

`Dial` performs the challenge-response handshake (see `api.md` §2). The signer
comes from either the key file or `ssh-agent`.

### `Discovery`

Broadcasts `discover`, collects `announce`s, merges with static agents, and emits
a sorted list of `Agent` entries (with reachability marked after a handshake).

### `App` (Wails-bound methods)

These are the methods the frontend calls. They're intentionally coarse-grained so
the frontend stays dumb:

```go
func (a *App) ListAgents() ([]AgentInfo, error)
func (a *App) AgentInfo(agentID string) (AgentInfo, error)

func (a *App) ListStacks(agentID string) ([]StackSummary, error)
func (a *App) StackServices(agentID, stack string) ([]ServicePolicy, error)

func (a *App) GitCheck(agentID, stack string) (GitCheck, error)
func (a *App) GitPull(agentID, stack string) (GitPullResult, error)

func (a *App) ContainersUp(agentID, stack string, opts UpOptions) error
func (a *App) ContainersDown(agentID, stack string, opts DownOptions) error
func (a *App) ContainersPs(agentID, stack string) ([]ContainerStatus, error)

func (a *App) ListCommands(agentID, stack string) ([]CommandInfo, error)
func (a *App) RunCommand(agentID, stack, command string, params map[string]string) error
```

Long-running calls emit `EventsOn("stream")` events that the frontend subscribes
to for live output.

## 5. Frontend (Svelte)

A single dashboard, kept minimal for now:

```
┌ Agents ──────────────────────────────────────────────┐
│ ● vps-1 (100.96.1.2)  2 stacks                       │
│ ● vps-2 (100.96.1.3)  1 stack                        │
└──────────────────────────────────────────────────────┘
  [hotel]  branch main  ▲2 ▼0  [Check] [Pull] [Up ▸] [Commands ▾]
    services: next ●  ws ●  nginx ●  mysql ⊘(excluded)
    ── output ────────────────────────────────────────
    docker compose build … done
```

Views: **Agents** → **Stack detail** (services + policy) → **command runner**
(with a live output pane). `Check` renders the changed-files list and a
`safe_to_pull` badge before `Pull` is enabled.

## 6. Key custody

- Default: read the key from `private_key_path`.
- If `use_ssh_agent` and `SSH_AUTH_SOCK` is set: use the agent, never touch the
  key file. This is the recommended mode.
- The private key never leaves the laptop and is never sent over the wire (only
  signatures are).

## 7. Platform notes

- Wails targets Windows/macOS/Linux; the client is most useful on the laptop
  running the WARP client (so it has a subnet IP).
- Build: `wails build` inside `client/`. The webview needs the Wails runtime on
  Windows (bundled automatically).

## 8. What's deferred to the UI phase (by design)

Exact visual layout, theming, command history, saved credentials UX, and any
"app-level" niceties. The backend surface above is enough to build the UI later
without rework.
