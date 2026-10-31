# Decisions (ADRs)

A record of the choices made up front and why, so later changes have context.

## ADR-001 — Agent language: Go

**Decision:** the agent is a single static Go binary.

**Why:** one artifact to deploy, no runtime to install on the VPS, trivial
systemd integration, and `golang.org/x/crypto/ssh` gives us the challenge-response
auth and `authorized_keys` parsing for free. (The "js or go" idea resolved to Go
once we needed a standalone binary + native SSH support.)

## ADR-002 — Client: Go + Wails

**Decision:** the desktop app is Go with Wails (webview UI), frontend in Svelte.

**Why:** same language as the agent, so we can share `pkg/` types and RPC code.
Webview UI is fast to iterate and gives full CSS/JS for the dashboard. (Rust +
Tauri was the runner-up; we dropped it to keep the whole stack in one language.)

## ADR-003 — Network: Cloudflare WARP / Magic WAN

**Decision:** agents bind to the WARP subnet IP (CGNAT `100.96.0.0/12`) and are
reached directly by the laptop, which is on the same overlay.

**Why:** no public exposure, no extra tunnel config, wire already encrypted by
the overlay. Consequence: see ADR-006 (discovery).

## ADR-004 — Auth: SSH key challenge-response

**Decision:** the client proves possession of a private key whose public half is
in the VPS's `authorized_keys`, via a nonce-signing handshake over the agent API
(not over SSH/sshd).

**Why:** no new credentials, no manual login, reuses keys already deployed
everywhere, and the trust boundary is identical to what SSH already grants.
Optional `ssh-agent` support keeps the key out of the desktop app entirely.

**Rejected:** TLS client certs (separate PKI to manage), Cloudflare Access
(only applicable if traffic traverses a CF Tunnel, which it doesn't here),
no auth (needless given the cost of the handshake is ~50 lines with x/crypto/ssh).

## ADR-005 — Discovery: UDP broadcast beacon (with static fallback)

**Decision:** primary discovery is a UDP broadcast/response beacon on a
well-known port; the API port is advertised, not hard-coded.

**Caveat:** WARP/Magic WAN is an L3 unicast overlay; broadcast/multicast may not
traverse it. Therefore the client also supports a **static peer list**
(auto-populated after first successful contact), and manual "add agent by IP".
See `discovery.md`.

**Rejected:** TCP subnet sweep (noisy/slow on /12), mDNS (needs multicast, which
the overlay likely drops), central registry (extra component for little gain at
this scale).

## ADR-006 — Storage: SQLite (pure-Go driver)

**Decision:** stack registration lives in a SQLite database
(`modernc.org/sqlite`, no CGO) under `/var/lib/stackman/stackman.db`.

**Why:** transactional, one file, easy to add columns/tables as the command set
grows. **Rejected:** BoltDB (hand-rolled schema/migrations), JSON files (no
concurrent-write safety).

## ADR-007 — Repo commands: two-level extensibility

**Decision:** repo-specific commands are **declarative** in a `.stackman.yaml`
checked into the repo (a small step DSL: `compose`, `exec`, `shell`, `git`). The
agent's *own* operations are implemented behind a Go `Command` interface and a
registry, which the DSL executor itself is built on.

**Why:** declarative commands can be added by editing YAML (no rebuild, no Go).
The Go interface stays available for anything the DSL can't express. See
`commands.md`.

## ADR-008 — Git: shell out to `git`, behind an interface

**Decision:** git operations shell out to the system `git` binary, wrapped in a
`GitClient` interface.

**Why:** remotes are SSH (`git@github.com:…`), which the system `git` + ssh
already handle with the same `authorized_keys`/agent. Wrapping behind an
interface means we can swap in `go-git` later without touching callers.

**Rejected:** `go-git` (SSH-remote + agent support is weaker and it's one more
thing to debug on a VPS).

## ADR-009 — RPC: JSON-RPC 2.0 over length-prefixed TCP

**Decision:** the agent API is JSON-RPC 2.0 over a single TCP connection, framed
as `[4-byte big-endian length][JSON]`. Server→client notifications carry stream
chunks (logs) and progress.

**Why:** simple, human-debuggable, supports streaming, and the Wails client can
reuse `pkg/api` types verbatim. **Rejected:** gRPC (overkill + TLS/HTTP2 on a
private subnet), HTTP+REST (clunkier for streaming commands and the custom auth
handshake).
