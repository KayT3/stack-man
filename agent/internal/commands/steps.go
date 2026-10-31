package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kballard/go-shellquote"

	"github.com/example/stack-man/agent/internal/compose"
	"github.com/example/stack-man/agent/internal/git"
	"github.com/example/stack-man/agent/internal/stack"
)

// RegisterBuiltins installs the compose/exec/shell/git step executors and the
// native commands that ship with the agent. It is safe to call more than once.
func RegisterBuiltins(g git.Client) {
	RegisterStep(&ComposeStep{})
	RegisterStep(&ExecStep{})
	RegisterStep(&ShellStep{})
	RegisterStep(&GitStep{client: g})

	for _, c := range []Command{
		&GitCheckCommand{client: g},
		&GitPullCommand{client: g},
		&ComposeCommand{name: "up", description: "Start the managed services", needsBuild: true},
		&ComposeCommand{name: "down", description: "Stop the managed services"},
		&ComposeCommand{name: "restart", description: "Restart the managed services"},
		&ComposeCommand{name: "build", description: "Build images for the managed services"},
	} {
		if _, exists := Get(c.Name()); !exists {
			Register(c)
		}
	}
}

// ---- compose ----

// ComposeStep runs `docker compose <action>`.
type ComposeStep struct{}

func (s *ComposeStep) StepName() string { return "compose" }

func (s *ComposeStep) Execute(context.Context, *stack.Stack, map[string]string, io.Writer) error {
	return errors.New("compose step requires configuration")
}

func (s *ComposeStep) ExecuteStep(ctx context.Context, st *stack.Stack, _ map[string]string, cfg map[string]any, out io.Writer) error {
	action := stringValue(cfg["action"])
	switch action {
	case "build", "up", "down", "restart", "pull", "ps":
	case "":
		return errors.New("compose step: action is required")
	default:
		return fmt.Errorf("compose step: unsupported action %q", action)
	}

	sc, err := loadStackCompose(ctx, st)
	if err != nil {
		return err
	}
	services, err := targetServices(sc.managed, stringSlice(cfg["services"]))
	if err != nil {
		return err
	}
	args := []string{action}
	args = append(args, stringSlice(cfg["flags"])...)
	args = append(args, services...)
	return compose.Run(ctx, st.Path, sc.files, out, args...)
}

// ---- exec ----

// ExecStep runs `docker compose exec -T <service> <command>`.
type ExecStep struct{}

func (s *ExecStep) StepName() string { return "exec" }

func (s *ExecStep) Execute(context.Context, *stack.Stack, map[string]string, io.Writer) error {
	return errors.New("exec step requires configuration")
}

func (s *ExecStep) ExecuteStep(ctx context.Context, st *stack.Stack, _ map[string]string, cfg map[string]any, out io.Writer) error {
	service := stringValue(cfg["service"])
	if service == "" {
		return errors.New("exec step: service is required")
	}
	command := stringValue(cfg["command"])
	if strings.TrimSpace(command) == "" {
		return errors.New("exec step: command is required")
	}
	sc, err := loadStackCompose(ctx, st)
	if err != nil {
		return err
	}
	if err := ensureManaged(sc.managed, service); err != nil {
		return err
	}
	argv, err := shellquote.Split(command)
	if err != nil {
		return fmt.Errorf("exec step: parse command: %w", err)
	}
	if len(argv) == 0 {
		return errors.New("exec step: command is empty")
	}
	args := []string{"exec", "-T"}
	if workdir := stringValue(cfg["workdir"]); workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	for _, key := range sortedKeys(cfg["env"]) {
		args = append(args, "-e", key+"="+stringValue(cfg["env"].(map[string]any)[key]))
	}
	args = append(args, service)
	args = append(args, argv...)
	return compose.Run(ctx, st.Path, sc.files, out, args...)
}

// ---- shell ----

// ShellStep is the raw shell escape hatch (docs/commands.md §2).
type ShellStep struct{}

func (s *ShellStep) StepName() string { return "shell" }

func (s *ShellStep) Execute(context.Context, *stack.Stack, map[string]string, io.Writer) error {
	return errors.New("shell step requires configuration")
}

func (s *ShellStep) ExecuteStep(ctx context.Context, st *stack.Stack, _ map[string]string, cfg map[string]any, out io.Writer) error {
	command := stringValue(cfg["command"])
	if strings.TrimSpace(command) == "" {
		return errors.New("shell step: command is required")
	}
	dir := st.Path
	if cwd := stringValue(cfg["cwd"]); cwd != "" {
		dir = filepath.Join(st.Path, cwd)
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(st.Path)
		if err != nil {
			return err
		}
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return fmt.Errorf("shell step: cwd %q escapes the stack directory", cwd)
		}
		dir = abs
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	return nil
}

// ---- git ----

// GitStep runs fetch/check/pull through the git.Client.
type GitStep struct {
	client git.Client
}

func (s *GitStep) StepName() string { return "git" }

func (s *GitStep) Execute(context.Context, *stack.Stack, map[string]string, io.Writer) error {
	return errors.New("git step requires configuration")
}

func (s *GitStep) ExecuteStep(ctx context.Context, st *stack.Stack, _ map[string]string, cfg map[string]any, out io.Writer) error {
	if s.client == nil {
		return errors.New("git step: no git client configured")
	}
	action := stringValue(cfg["action"])
	branch := stringValue(cfg["branch"])
	if branch == "" {
		branch = st.Branch
	}
	remote := st.Remote
	if remote == "" {
		remote = "origin"
	}
	switch action {
	case "fetch":
		if err := s.client.Fetch(ctx, st.Path, remote, branch); err != nil {
			return err
		}
		fmt.Fprintln(out, "fetched")
	case "check":
		res, err := s.client.Check(ctx, st.Path, remote, branch)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "branch %s: ahead %d, behind %d, clean %t, diverged %t, safe %t\n",
			res.Branch, res.Ahead, res.Behind, res.Clean, res.Diverged, res.Safe)
		for _, file := range res.ChangedFiles {
			fmt.Fprintf(out, "  %s\n", file)
		}
	case "pull":
		res, err := s.client.Pull(ctx, st.Path, remote, branch)
		if err != nil {
			return err
		}
		if !res.Pulled {
			fmt.Fprintln(out, "already up to date")
			return nil
		}
		fmt.Fprintf(out, "pulled %s -> %s\n", shortSHA(res.From), shortSHA(res.To))
	case "":
		return errors.New("git step: action is required")
	default:
		return fmt.Errorf("git step: unsupported action %q", action)
	}
	return nil
}

// ---- native commands ----

// GitCheckCommand wraps git.Check.
type GitCheckCommand struct{ client git.Client }

func (c *GitCheckCommand) Name() string        { return "git-check" }
func (c *GitCheckCommand) Description() string { return "Fetch and report whether a pull is safe" }
func (c *GitCheckCommand) Spec() CommandSpec   { return CommandSpec{} }

func (c *GitCheckCommand) Run(ctx context.Context, st *stack.Stack, _ map[string]string, out io.Writer) error {
	if c.client == nil {
		return errors.New("git-check: no git client configured")
	}
	remote := st.Remote
	if remote == "" {
		remote = "origin"
	}
	res, err := c.client.Check(ctx, st.Path, remote, st.Branch)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "branch %s\nhead %s\nremote %s\nahead %d behind %d\ndiverged %t clean %t safe_to_pull %t\n",
		res.Branch, shortSHA(res.Head), shortSHA(res.RemoteHead), res.Ahead, res.Behind, res.Diverged, res.Clean, res.Safe)
	for _, file := range res.ChangedFiles {
		fmt.Fprintf(out, "  %s\n", file)
	}
	return nil
}

// GitPullCommand wraps git.Pull.
type GitPullCommand struct{ client git.Client }

func (c *GitPullCommand) Name() string        { return "git-pull" }
func (c *GitPullCommand) Description() string { return "Fast-forward the stack, refusing unsafe pulls" }
func (c *GitPullCommand) Spec() CommandSpec   { return CommandSpec{} }

func (c *GitPullCommand) Run(ctx context.Context, st *stack.Stack, _ map[string]string, out io.Writer) error {
	if c.client == nil {
		return errors.New("git-pull: no git client configured")
	}
	remote := st.Remote
	if remote == "" {
		remote = "origin"
	}
	res, err := c.client.Pull(ctx, st.Path, remote, st.Branch)
	if err != nil {
		return err
	}
	if !res.Pulled {
		fmt.Fprintln(out, "already up to date")
		return nil
	}
	fmt.Fprintf(out, "pulled %s -> %s\n", shortSHA(res.From), shortSHA(res.To))
	return nil
}

// ComposeCommand is a command-form alias for a compose action over the managed
// services (up/down/restart/build).
type ComposeCommand struct {
	name        string
	description string
	needsBuild  bool
}

func (c *ComposeCommand) Name() string        { return c.name }
func (c *ComposeCommand) Description() string { return c.description }
func (c *ComposeCommand) Spec() CommandSpec {
	if c.needsBuild {
		return CommandSpec{NeedsBuild: true}
	}
	return CommandSpec{}
}

func (c *ComposeCommand) Run(ctx context.Context, st *stack.Stack, params map[string]string, out io.Writer) error {
	sc, err := loadStackCompose(ctx, st)
	if err != nil {
		return err
	}
	args := []string{c.name}
	switch c.name {
	case "up":
		args = append(args, "-d")
		if params["build"] == "true" || params["build"] == "1" {
			args = append(args, "--build")
		}
	case "down":
		// Keep excluded services running.
	}
	args = append(args, sc.managed...)
	return compose.Run(ctx, st.Path, sc.files, out, args...)
}

// ---- helpers ----

type stackCompose struct {
	files   []string
	managed []string
}

func loadStackCompose(ctx context.Context, st *stack.Stack) (stackCompose, error) {
	files, err := compose.FindFiles(st.Path, st.ComposeFile)
	if err != nil {
		return stackCompose{}, err
	}
	project, err := compose.Load(ctx, st.Path, st.ComposeFile)
	if err != nil {
		return stackCompose{}, err
	}
	return stackCompose{files: files, managed: st.Managed(project.Services)}, nil
}

func targetServices(managed, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return managed, nil
	}
	for _, service := range requested {
		if err := ensureManaged(managed, service); err != nil {
			return nil, err
		}
	}
	return requested, nil
}

func ensureManaged(managed []string, service string) error {
	for _, candidate := range managed {
		if candidate == service {
			return nil
		}
	}
	return fmt.Errorf("service %q is not managed by this stack (check allow/exclude policy)", service)
}

func sortedKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
