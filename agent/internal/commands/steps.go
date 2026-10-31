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
	RegisterStep(&ComposeExecutor{})
	RegisterStep(&ExecExecutor{})
	RegisterStep(&ShellExecutor{})
	RegisterStep(&GitExecutor{client: g})

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

// ComposeExecutor runs `docker compose <action>`.
type ComposeExecutor struct{}

func (s *ComposeExecutor) StepName() string { return "compose" }

func (s *ComposeExecutor) Execute(ctx context.Context, st *stack.Stack, _ map[string]string, step Step, out io.Writer) error {
	cfg, ok := step.(*ComposeStep)
	if !ok || cfg == nil {
		return errors.New("compose step: expected *ComposeStep")
	}
	action := cfg.Action
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
	services, err := targetServices(sc.managed, cfg.Services)
	if err != nil {
		return err
	}
	args := []string{action}
	args = append(args, cfg.Flags...)
	args = append(args, services...)
	return compose.Run(ctx, st.Path, sc.files, out, args...)
}

// ---- exec ----

// ExecExecutor runs `docker compose exec -T <service> <command>`.
type ExecExecutor struct{}

func (s *ExecExecutor) StepName() string { return "exec" }

func (s *ExecExecutor) Execute(ctx context.Context, st *stack.Stack, _ map[string]string, step Step, out io.Writer) error {
	cfg, ok := step.(*ExecStep)
	if !ok || cfg == nil {
		return errors.New("exec step: expected *ExecStep")
	}
	service := cfg.Service
	if service == "" {
		return errors.New("exec step: service is required")
	}
	command := cfg.Command
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
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	for _, key := range sortedKeys(cfg.Env) {
		args = append(args, "-e", key+"="+cfg.Env[key])
	}
	args = append(args, service)
	args = append(args, argv...)
	return compose.Run(ctx, st.Path, sc.files, out, args...)
}

// ---- shell ----

// ShellExecutor is the raw shell escape hatch.
type ShellExecutor struct{}

func (s *ShellExecutor) StepName() string { return "shell" }

func (s *ShellExecutor) Execute(ctx context.Context, st *stack.Stack, _ map[string]string, step Step, out io.Writer) error {
	cfg, ok := step.(*ShellStep)
	if !ok || cfg == nil {
		return errors.New("shell step: expected *ShellStep")
	}
	command := cfg.Command
	if strings.TrimSpace(command) == "" {
		return errors.New("shell step: command is required")
	}
	dir := st.Path
	if cfg.Cwd != "" {
		dir = filepath.Join(st.Path, cfg.Cwd)
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(st.Path)
		if err != nil {
			return err
		}
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return fmt.Errorf("shell step: cwd %q escapes the stack directory", cfg.Cwd)
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

// GitExecutor runs fetch/check/pull through the git.Client.
type GitExecutor struct {
	client git.Client
}

func (s *GitExecutor) StepName() string { return "git" }

func (s *GitExecutor) Execute(ctx context.Context, st *stack.Stack, _ map[string]string, step Step, out io.Writer) error {
	cfg, ok := step.(*GitStep)
	if !ok || cfg == nil {
		return errors.New("git step: expected *GitStep")
	}
	if s.client == nil {
		return errors.New("git step: no git client configured")
	}
	action := cfg.Action
	branch := cfg.Branch
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

func sortedKeys(m map[string]string) []string {
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
