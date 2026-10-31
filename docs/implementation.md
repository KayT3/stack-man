# Implementation guide (handoff)

This is the entry point for whichever model implements the code. The scaffold
compiles as-is; every stub returns `config.ErrNotImplemented` or exits with a
"not implemented" message. Fill in the TODOs in the order below.

## Current status and next action

| Phase | State |
|---|---|
| 1 — Agent core | in progress |
| 2 — Daemon | in progress |
| 3 — Client core (Go) | **done** — `App`, discovery, `AgentConnection`, keys, config all exist |
| 4 — Native Windows UI | **specified, not started** → `docs/client-native.md` |
| 5 — Wails/Svelte skin | exists; legacy fallback, do not extend |

**Next action: implement M0 of `docs/client-native.md`.** That document is the
plan of record for the UI: a WinUI 3 (C#) app hosting the Go client as a
`-buildmode=c-shared` DLL, one process, JSON over four exported C functions.
It contains the ABI, the method table, the threading rules, the file layout,
the acceptance test per milestone, and a copy-paste prompt you can hand
directly to a model.

Do not start Phase 4 by writing XAML. Start with `client/ipc.go` (pure Go, no
cgo, unit-tested) — see M0.

## Build & verify

```bash
# From the repo root (the workspace is a go.work of three modules):
go build ./agent/... ./pkg/... ./client/...
go vet   ./agent/... ./pkg/... ./client/...
go test  ./agent/... ./pkg/... ./client/...
```

That is the **cgo-free** gate and must always pass. The WinUI backend DLL is a
separate, opt-in build that needs a C compiler:

```bash
cd client && CGO_ENABLED=1 go build -buildmode=c-shared -tags cshared \
  -o ../desktop/StackMan/Native/stackman-backend.dll .
```

See `docs/client-native.md` §1.9 before running it the first time.

(You can also `cd agent && go build ./...` — the `replace` directives make
each module build independently of the workspace.)

## Before you start

Rename the module path. The scaffold uses `github.com/example/stack-man` as a
placeholder. Search-and-replace it with your real repo path across **all**
`go.mod` files and Go imports **before** pushing.

## Conventions (do not break these)

- **`pkg/` stays dependency-free.** Shared wire types have no imports beyond the
  stdlib, so both agent and client stay light.
- **Domain vs DTO.** Agent domain types live in `agent/internal/*`; wire DTOs
  live in `pkg/api`. Map between them at the RPC boundary (e.g.
  `internal/stack.Stack` ↔ `pkg/api.Stack`).
- **Interfaces are the contract.** `commands.Command`, `commands.StepExecutor`,
  and `git.Client` are already defined. Implement *behind* them; don't change
  their signatures casually.
- **No shell string concatenation for args.** Pass argv slices to compose/git;
  the only raw-string escape hatch is the `shell` step in a compiled repo
  profile.
- **CLI and daemon share one service layer.** Implement each operation once in
  `internal/`, then expose it from both `cli` and `daemon`.

## Phase 1 — Agent core (no daemon yet)

1. **config** — implement `Load`/`Save` (`github.com/BurntSushi/toml`) and the
   interactive `config` wizard (`github.com/AlecAivazis/survey/v2`).
   → docs/agent.md §2–3.
2. **systemd** — unit generation + `systemctl` wrappers.
   → docs/agent.md §4.
3. **stack** — SQLite storage (`modernc.org/sqlite`) for `stacks` and
   `stack_services` + migrations on startup. → docs/stacks.md §4.
4. **compose** — load compose/override files (`github.com/compose-spec/compose-go/v2`),
   enumerate services, apply policy. → docs/stacks.md §3.
5. **git** — implement `git.Client` via the system `git` binary; the
   check-before-pull + conflict-refusal logic. → docs/stacks.md §6.
6. **commands** — typed repo-profile registry keyed by normalized remote +
   `StepExecutor`s (compose/exec/shell/git) + native builtins. No YAML and no
   runtime config read. → docs/commands.md.
7. **cli** — wire cobra subcommands to the above; make `runCLI` dispatch here.
   → docs/agent.md §1.

**Acceptance:** from a shell on a VPS —
`stackman stack add /srv/hotel --allow next,ws,nginx --exclude mysql`,
`stackman git check hotel`, `stackman containers up hotel --build`, and
`stackman command run hotel update` all work.

## Phase 2 — Daemon

8. **auth** (agent side) — parse `authorized_keys`, verify `pkg/auth.SignedData`.
   → docs/api.md §2.
9. **discovery** — UDP beacon responder. → docs/discovery.md.
10. **daemon** — TCP bind to the subnet IP, source-filter by subnet, length-prefixed
    JSON-RPC framing, method dispatch to the Phase-1 service layer.
    → docs/api.md §1, §3–4.

**Acceptance:** a test client (or the CLI speaking RPC) authenticates with an
SSH key and drives a stack over the private subnet.

## Phase 3 — Client core (done)

11. ~~Scaffold Wails~~ — done; `client/` is a Wails app with `pkg` wired in.
    → docs/client.md §2.
12. ~~Discovery client + static list; `AgentConnection` (dial + handshake +
    `Call`/`Notify`)~~ — done. → docs/client.md §4.
13. ~~Bind the `App` methods to agent connections~~ — done; the full method
    surface is in `client/app.go`. This is the surface Phase 4 exposes over the
    DLL, so do not change its signatures without updating
    `docs/client-native.md` §1.5.

## Phase 4 — Native Windows UI (WinUI 3 + Go DLL)

All detail lives in **`docs/client-native.md`**. Summary of the order:

14. **M0 — dispatcher, no cgo.** `client/ipc.go`: `Dispatcher.Call([]byte) []byte`,
    `PollEvents`, bounded event queue, `Emitter` impl. Unit-tested.
    → client-native.md §1.6–1.7, §1.10.
15. **M1 — the DLL.** `client/backend_cshared.go` behind
    `//go:build windows && cshared`, exporting `SM_Init`, `SM_Call`,
    `SM_PollEvents`, `SM_Free`. Also change `client/main.go` to
    `//go:build !wails && !cshared`. Prove it with a throwaway C# console app
    and `dumpbin /dependents`.
16. **M2 — WinUI shell.** `desktop/StackMan/`, unpackaged, Mica backdrop,
    `NavigationView` + `Frame`, empty pages.
17. **M3–M4 — wire the UI.** `BackendClient` (async, `LongRunning`),
    `EventPump` (long-poll → `DispatcherQueue`), Agents → Stack list → Stack
    detail → output pane.
18. **M5 — polish.** Job pill, `ContentDialog`, `InfoBar`, ThemeResource-only
    colours, light/dark/high-contrast, 150% DPI.
19. **M6 — package.** `make desktop`, folder output, self-contained WinAppSDK.

**Acceptance:** a `stackman-desktop.exe` that opens with no webview, follows
the system theme, lists agents found over WARP, runs a build with live streamed
output, and shows a job started in a previous window session.

**Rules for this phase:** only `char*` JSON across the ABI; `recover()` in
every export; no `log.Fatal`/`os.Exit` in `client/`; never call the DLL from
the UI thread; never touch WinUI from Go.

## Phase 5 — Wails/Svelte skin (legacy, frozen)

20. Keep `main_wails.go`, `frontend/` and the `wails` build tag working so the
    old UI stays runnable (`make client`) as a fallback. Fix bugs, add no
    features. Delete only after the native client has had real use.
    → docs/client.md §5.

## Where to look when stuck

| Question | Look at |
|----------|---------|
| Wire message shapes | `pkg/{auth,discovery,api}` |
| Extensibility contracts | `agent/internal/{commands,git}` |
| Domain model + policy | `agent/internal/stack` |
| Config schema | `agent/internal/config` + docs/agent.md §3 |
| Client backend surface | `docs/client.md` §4 + `client/app.go` |
| UI plan, Go DLL ABI, WinUI layout | **`docs/client-native.md`** |
| Why a decision was made | `docs/decisions.md` (ADR-011 for the native UI) |
