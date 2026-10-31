# Decisions (ADRs)

A record of the choices made up front and why, so later changes have context.

## ADR-001 — Agent language: Go

**Decision:** the agent is a single static Go binary.

**Why:** one artifact to deploy, no runtime to install on the VPS, trivial
systemd integration, and `golang.org/x/crypto/ssh` gives us the challenge-response
auth and `authorized_keys` parsing for free. (The "js or go" idea resolved to Go
once we needed a standalone binary + native SSH support.)

## ADR-002 — Client: Go + Wails

> **Partially superseded by ADR-011.** The Go backend and `pkg/` sharing
> stand; the Wails *webview UI* is replaced by a native WinUI 3 front end. The
> Wails skin is kept as a frozen fallback.

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

## ADR-007 — Repo commands: compiled-in Go profiles

**Decision:** repo-specific commands are **compiled into the agent** as Go
`Profile` values under `agent/internal/profiles/`, keyed by the normalized git
remote (a small step DSL of typed structs: `compose`, `exec`, `shell`, `git`).
The agent's *own* operations use the same Go `Command`/`StepExecutor` interfaces
and registry.

**Why:** profiles travel with the binary, are type-checked at build time, and
cannot drift from the deployed version or be changed by editing files on a VPS.
There is no parser and no runtime config read. **Consequence:** changing a
repo's commands requires rebuilding and redeploying the agent; there is no
per-repo self-service. See `commands.md`.

**Rejected:** a repo-local `.stackman.yaml` read at runtime (untracked config,
parse failures at runtime, extra trust surface), and embedded YAML (still a
parser and weaker type safety than Go structs).

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

## ADR-010 - Mutations run as daemon-owned jobs

**Decision:** `containers.{up,down,restart,build}`, network connect/disconnect,
`git.pull`, and `command.run` run on a context rooted at the daemon and are
tracked in a `stack.job.list` registry. A client disconnect only ends streaming
reads; it does not cancel a mutation. One mutation per stack may run at a time.

**Why:** `docker compose build` takes minutes; a laptop sleeping or WARP
reconnecting should not leave a half-built image or a killed deploy. Jobs are
visible after reconnect so the UI can show them and avoid retrying a mutation.

**Rejected:** tying mutations to the connection (the previous failure mode), and
outsourcing detachment to `systemd-run`/Docker retries (opaque, and progress
cannot be reported).

## ADR-011 — Desktop UI: native WinUI 3 in C#, Go backend as a c-shared DLL

**Decision:** the desktop client becomes one process: a C# / .NET / WinUI 3
host executable that loads the existing Go client code as
`stackman-backend.dll` (`go build -buildmode=c-shared`). The ABI is four
functions — `SM_Init`, `SM_Call`, `SM_PollEvents`, `SM_Free` — exchanging UTF-8
JSON envelopes. Notifications use a blocking long-poll instead of cgo
callbacks. Plan of record: `docs/client-native.md`.

**Why:** we wanted two things at once — no webview (native text, scroll, focus,
IME, sub-200 ms startup) and a genuinely stock Windows 11 look (Mica,
NavigationView, ThemeResource colours, system theme and accent). Only real
WinUI 3 delivers both, and only C# gives real XAML, XAML Hot Reload, and
Microsoft's samples verbatim — which is exactly the fast design loop that
polish work needs.

Keeping the backend in Go is the point: discovery, the SSH challenge-response,
and key custody stay in one audited implementation and never enter .NET. The
`App` surface in `client/app.go` was already Wails-free behind an `Emitter`
interface (ADR-002), so the process-internal bridge is a thin transport, not a
port.

**Rejected:**

- *WinUI 3 from Go (`go-bindings-windowsappsdk`).* No XAML compiler (UI built
  imperatively), no deriving from `Page`/`UserControl` (its M7 is open), amd64
  only, one maintainer, and every layout tweak is a recompile with no preview.
  Wrong tool for a pixel-polish phase.
- *Windigo (pure-Go Win32).* Fixes the webview, but exposes the classic
  comctl32 control set: no Mica, no ThemeResource, no dark mode in its `ui`
  package, WinForms-style anchor layout. Looks older, not native-newer.
- *Porting the client logic to C#.* Would duplicate the SSH handshake and key
  custody — the highest-risk code in the project — across two languages.
- *Two processes (Go sidecar + stdio JSON-RPC).* Viable and cheaper to build;
  rejected only because it adds readiness handshakes, orphan handling, and
  two-binary packaging for no crash-isolation benefit we care about. Kept as
  the documented fallback, and the transport is isolated so switching is cheap.
- *C# as a NativeAOT library called from a Go host.* WinUI 3 requires the .NET
  app to own the entry point; NativeAOT is unsupported for WinUI.

**Consequences:** cgo (and therefore a C toolchain) enters the client build;
the shipped artefact is a folder, not a single file, because WinUI 3 cannot be
`PublishSingleFile` (`resources.pri` and the WinAppSDK binaries must be on
disk); self-contained deployment is ~80–120 MB; a Go panic that escapes
`recover()` now takes down the UI process. The Wails/Svelte skin is frozen but
retained until the native client has seen real use.
