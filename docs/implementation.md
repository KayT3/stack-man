# Implementation guide (handoff)

This is the entry point for whichever model implements the code. The scaffold
compiles as-is; every stub returns `config.ErrNotImplemented` or exits with a
"not implemented" message. Fill in the TODOs in the order below.

## Build & verify

```bash
# From the repo root (the workspace is a go.work of three modules):
go build ./agent/... ./pkg/... ./client/...
go vet   ./agent/... ./pkg/... ./client/...
go test  ./agent/... ./pkg/... ./client/...
```

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

## Phase 3 — Client core

11. Scaffold Wails (`wails init` in `client/`), keep the `pkg` dependency.
    → docs/client.md §2.
12. Discovery client + static list; `AgentConnection` (dial + handshake +
    `Call`/`Notify`). → docs/client.md §4.
13. Bind the `App` methods (listed in docs/client.md §4) to agent connections.

## Phase 4 — UI

14. Svelte dashboard (agents → stack → services → command runner).
    → docs/client.md §5.

## Where to look when stuck

| Question | Look at |
|----------|---------|
| Wire message shapes | `pkg/{auth,discovery,api}` |
| Extensibility contracts | `agent/internal/{commands,git}` |
| Domain model + policy | `agent/internal/stack` |
| Config schema | `agent/internal/config` + docs/agent.md §3 |
| Why a decision was made | `docs/decisions.md` |
