# Architecture

## 1. Goals

- Manage Docker Compose stacks on many VPSes from one desktop app.
- **Zero new public surface.** Management traffic rides the existing private
  WARP/Magic WAN subnet.
- **Zero new credentials.** Authentication reuses the SSH public keys already
  deployed as `authorized_keys` on each VPS.
- **Single binary per server** that is both the agent daemon and the CLI, so the
  same operations work from a terminal and from the desktop app.
- **Extensible** — new repo command profiles are compiled into the agent and
  keyed by the normalized git remote.

## 2. Components

```
pkg/  (shared Go module)
 ├─ auth       message types for the SSH challenge-response handshake
 ├─ discovery  UDP beacon message types
 └─ api        JSON-RPC method/param/result types + protocol version

agent/  (Go module -> binary: stackman)
 ├─ cmd/stackman     entry point; dispatches to CLI or daemon
 └─ internal/
     ├─ cli          cobra commands (thin shells over the service layer)
     ├─ config       config file load/save + interactive wizard
     ├─ daemon       RPC server: bind, framing, request dispatch
     ├─ auth         server-side SSH challenge-response verification
     ├─ discovery    UDP beacon responder
     ├─ stack        stack registry (SQLite) + domain logic
     ├─ compose      compose file parsing + allow/deny service policy
     ├─ commands     command registry (Go interface) + typed step executors
     ├─ profiles     repo-specific command sets compiled into the binary
     ├─ git          git operations (fetch/status/pull/check) behind an interface
     └─ systemd      unit generation + install/start/stop/enable/disable

client/  (Go module, Wails app -> binary: stackman-desktop)
 ├─ main.go / app.go Wails bindings + backend services
 └─ frontend/        web UI (Svelte)
```

### The key layering rule

The **CLI and the daemon share one service layer**. Commands like `stack add`,
`git check`, `containers up`, and `command run` are implemented once, in
`internal/`, and exposed two ways:

- the daemon wraps them as RPC methods (`api.md`),
- the CLI wraps them as cobra subcommands.

This keeps terminal and desktop behavior identical by construction.

## 3. The two faces of the agent

`stackman` is one binary with two entry paths:

```
stackman daemon                 # long-running RPC server (systemd runs this)
stackman <anything-else>        # one-shot CLI command
```

Mode is chosen in `main()` by inspecting `os.Args[1]`.

## 4. Data flow

### 4.1 Discovery

1. Agent binds a UDP listener on the **discovery port** (default `42701`) on the
   subnet interface.
2. Client broadcasts a `discover` datagram to the subnet (and `255.255.255.255`).
3. Each agent replies **unicast** with an `announce` containing its hostname,
   subnet IP, **API port** (may vary per agent), version, and a stack summary.

Because the API port is advertised, it does **not** need to be identical across
servers — only the discovery port is well-known. See `discovery.md` for the
Magic WAN caveat and the static-list fallback.

### 4.2 Authenticated RPC

1. Client opens TCP to `agent_ip:api_port`.
2. Agent sends an `auth.challenge` (random nonce + session id).
3. Client signs the challenge with its SSH private key and replies with the
   public key + signature.
4. Agent verifies the public key is in its `authorized_keys` **and** the
   signature is valid. On success the connection becomes an authenticated
   JSON-RPC session; on failure it is closed.

Details and wire format: `api.md`.

### 4.3 Operations

Every operation is a JSON-RPC request (`stack.git.check`, `stack.containers.up`,
`stack.command.run`, …). The daemon dispatches to the same service layer the CLI
uses. Long-running operations (builds, logs) stream progress back as
notifications.

## 5. Security model

| Layer | Mechanism |
|-------|-----------|
| **Network reachability** | Agent binds only to the subnet IP (never `0.0.0.0`, never the public IP). |
| **Source filtering** | Agent rejects connections whose source IP is outside the configured subnet CIDR (defense in depth). |
| **Transport** | WARP/Magic WAN already encrypts the wire; no app-level TLS on the private subnet. (TLS can be layered on later without changing the auth model.) |
| **Authentication** | Per-connection SSH challenge-response. Trust store = `~/.ssh/authorized_keys` on the VPS. |
| **Replay protection** | A fresh random nonce per connection, bound to a session id. |
| **Key custody** | The client's private key never leaves the laptop. Optional `ssh-agent` support means the key isn't even loaded into the desktop app. |
| **Least surprise** | Public ingress (HTTPS) and sshd are untouched. No new listeners on public interfaces. |

### Threat model

- **Attacker with a subnet IP but no key** → can connect, cannot authenticate.
  They learn only the agent version (from the announce beacon, which is
  intentionally low-value).
- **Attacker with the private key** → full control; but that is exactly who
  `authorized_keys` already grants SSH shell access, so the trust boundary is
  unchanged.
- **Public internet** → cannot reach the API at all (not bound, not routed,
  source-filtered).

## 6. Ports

| Port | Proto | Purpose | Fixed? |
|------|-------|---------|--------|
| `42701` | UDP | discovery beacon | **yes** (well-known, configurable) |
| `42702` | TCP | agent RPC API | default only; **advertised** by discovery, may vary per agent |

Nothing else is added. HTTPS and SSH ports are not touched.

## 7. Concurrency & consistency

- Each agent is authoritative for its own stacks; there is no cross-agent
  coordination or shared state. The client is a pure orchestrator/UI.
- The SQLite registry is the agent's source of truth for stack registration.
  Repo-specific command sets are compiled into the agent and keyed by the
  normalized `stacks.remote` value.
- Git operations take a file lock (or rely on `git`'s own locking) so the daemon
  and CLI can't run `pull` concurrently against the same stack.

## 8. Failure handling

- Daemon runs under systemd with `Restart=on-failure`; crashes self-heal.
- Long operations support cancellation via the RPC context (client disconnect →
  context cancelled).
- A failed `pull` (conflict/divergence) is a **no-op with a report**, never a
  half-merged tree. See `stacks.md` §git.
