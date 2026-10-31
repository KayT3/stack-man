# Commands

Two extension points, both compiled into the agent binary:

1. **Go `Command` interface** — for logic that ships in the agent binary.
2. **Repo profiles** — per-repo command sets defined as Go structs under
   `agent/internal/profiles/`, registered at agent startup.

There is no runtime config file. Changing a repo profile means changing Go code
and rebuilding the agent.

## 1. The Go `Command` interface

Adding a native command = implementing one interface and registering it.

```go
package commands

// Param describes the inputs a command accepts, so the UI can render a form.
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

## 2. Repo profiles — compiled-in per-repo commands

A profile is a Go value in `agent/internal/profiles/`. It is keyed by the
**normalized git remote**, so the same repository matches whether it was cloned
over SSH or HTTPS:

```
git@github.com:Owner/Repo.git          -> github.com/owner/repo
https://github.com/Owner/Repo/         -> github.com/owner/repo
ssh://git@github.com/Owner/Repo.git    -> github.com/owner/repo
```

The profile package is blank-imported by the service layer, so its `init()`
functions run in both the daemon and the CLI.

```go
package profiles

import "github.com/example/stack-man/agent/internal/commands"

func init() {
    commands.RegisterProfile(commands.Profile{
        Repo:   "github.com/c-lentosc/star-class-hotel-system",
        OnPull: "update", // optional; must name a command below
        Commands: map[string]commands.Def{
            "backup": {
                Description: "Run the hotel backup job",
                Steps: []commands.Step{
                    &commands.ExecStep{Service: "backup", Command: "hotel-backup run"},
                },
            },
            "update": {
                Description: "Build images and apply DB migrations",
                Steps: []commands.Step{
                    &commands.ComposeStep{Action: "build"},
                    &commands.ComposeStep{Action: "up", Flags: []string{"-d", "--build"}},
                    &commands.ExecStep{Service: "next", Command: "npx prisma migrate deploy"},
                },
            },
        },
    })
}
```

Profiles may declare `Aliases: []string{...}` for renamed or mirrored remotes.
Duplicate remote keys panic at startup, so a copy/paste mistake fails loudly.

### Step types

Each step is a concrete Go type. Steps run in order; the first failing step
aborts the command with a clear report.

| Type | Go type | Fields | Runs |
|------|---------|--------|------|
| `compose` | `*commands.ComposeStep` | `Action` (`build`\|`up`\|`down`\|`restart`\|`pull`\|`ps`), `Flags`, `Services` | `docker compose <action> [flags] [services]` |
| `exec` | `*commands.ExecStep` | `Service`, `Command`, `Workdir`, `Env` | `docker compose exec -T <service> <command>` |
| `shell` | `*commands.ShellStep` | `Command`, `Cwd` | raw shell (escape hatch; avoid when possible) |
| `git` | `*commands.GitStep` | `Action` (`pull`\|`fetch`\|`check`), `Branch` | git op via the `GitClient` |

### Params (parameterized commands)

A command can declare params that are interpolated into step fields:

```go
"restore": {
    Description: "Restore a backup by name",
    Params: []commands.Param{
        {Name: "backup_name", Required: true, Description: "Which backup to restore"},
    },
    Steps: []commands.Step{
        &commands.ExecStep{
            Service: "backup",
            Command: "hotel-backup restore ${backup_name}",
        },
    },
},
```

`${name}` is substituted from `stackman command run <stack> restore --param backup_name=…`.
The CLI and desktop UI both render the params as a small form.

### `on_pull`

Set `Profile.OnPull` to a command name to run it automatically after a successful
`git pull`. There is no repo file lookup: the hook comes from the same compiled
profile.

## 3. Built-in commands

These ship in the agent and are always available:

- `git-check`, `git-pull` thin wrappers (also exposed as `stackman git …`).
- `up` / `down` / `restart` / `build` as command-form aliases of the container
  methods, so they can be composed into `on_pull` chains.

Profile commands take precedence over a built-in with the same name.

## 4. How `command run` executes

```
stack.Remote
  → NormalizeRemote
  → ProfileFor (compiled registry)
  → resolve command + params
  → for each step:
       GetStep(step.StepName())
       step.Substitute(params)
       Execute, streaming output to out
       on error: abort, report { step, exit_code, output }
  → success: report { ok: true }
```

No file is read. If no profile matches the stack's remote, only built-ins are
available.

The `StepExecutor` set is another small interface, so adding a new step type is
just implementing `Step` + `StepExecutor` and calling `RegisterStep`:

```go
type Step interface {
    StepName() string
    Substitute(params map[string]string) Step
}

type StepExecutor interface {
    StepName() string
    Execute(ctx context.Context, stack *Stack, params map[string]string, step Step, out io.Writer) error
}
```

## 5. Extending it yourself — the recipes

**Add/change a repo profile** (code + rebuild): add or edit a file under
`agent/internal/profiles/`, then `make agent` (or `go build`). The new profile is
compiled into every agent binary.

**Add a native agent command** (code): create a type implementing `Command`, call
`commands.Register` in its `init()`, rebuild. Available everywhere instantly.

**Add a new step type** (code): implement `Step` and `StepExecutor`, call
`RegisterStep`, and use it from profiles.
