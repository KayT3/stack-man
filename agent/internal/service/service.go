// Package service implements the operations shared by the CLI and daemon.
// Each operation lives here exactly once; both faces are thin wrappers.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/example/stack-man/agent/internal/commands"
	"github.com/example/stack-man/agent/internal/compose"
	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/git"
	"github.com/example/stack-man/agent/internal/stack"
)

// Service is the agent service layer.
type Service struct {
	Config *config.Config
	Store  *stack.Store
	Git    git.Client
}

// New opens the stack registry and prepares the command registry.
func New(cfg *config.Config) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("service: nil config")
	}
	store, err := stack.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	s := &Service{
		Config: cfg,
		Store:  store,
		Git:    git.New(),
	}
	commands.RegisterBuiltins(s.Git)
	return s, nil
}

// Close releases the registry.
func (s *Service) Close() error { return s.Store.Close() }

// AddOptions are the inputs to AddStack.
type AddOptions struct {
	Path    string
	Name    string
	Remote  string
	Branch  string
	Allow   []string
	Exclude []string
}

// AddStack registers a git repo + Compose file. It returns non-fatal warnings
// (missing Compose file, unknown service names) on success.
func (s *Service) AddStack(ctx context.Context, opts AddOptions) (*stack.Stack, []string, error) {
	dir, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("stack add: resolve path: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("stack add: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("stack add: %s is not a directory", dir)
	}

	isRepo, err := s.isRepo(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	if !isRepo {
		return nil, nil, fmt.Errorf("stack add: %s is not a git repository", dir)
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	if strings.ContainsAny(name, `/\`) {
		return nil, nil, fmt.Errorf("stack add: invalid name %q", name)
	}

	var warnings []string
	composeFile := "docker-compose.yml"
	var services []string
	files, err := compose.FindFiles(dir, "")
	switch {
	case err == nil:
		composeFile = filepath.Base(files[0])
		project, loadErr := compose.Load(ctx, dir, composeFile)
		if loadErr != nil {
			warnings = append(warnings, loadErr.Error())
		} else {
			services = project.Services
		}
	case errors.Is(err, compose.ErrNotFound):
		warnings = append(warnings, err.Error()+"; registered anyway")
	default:
		return nil, nil, err
	}

	allow := normalizeList(opts.Allow)
	exclude := normalizeList(opts.Exclude)
	if len(services) > 0 {
		for _, unknown := range compose.UnknownNames(services, allow, exclude) {
			warnings = append(warnings, fmt.Sprintf("service %q is not in the Compose file (typo?), keeping policy anyway", unknown))
		}
	}
	// Exclude always wins over allow.
	allow = minus(allow, exclude)

	remote := strings.TrimSpace(opts.Remote)
	if remote == "" {
		if origin, err := s.origin(ctx, dir); err == nil {
			remote = origin
		}
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		if current, err := s.currentBranch(ctx, dir); err == nil {
			branch = current
		}
	}

	st := &stack.Stack{
		Name:        name,
		Path:        dir,
		Remote:      remote,
		Branch:      branch,
		ComposeFile: composeFile,
		Allow:       allow,
		Exclude:     exclude,
	}
	if err := s.Store.Save(ctx, st); err != nil {
		return nil, warnings, err
	}
	sort.Strings(warnings)
	return st, warnings, nil
}

// RemoveStack unregisters a stack.
func (s *Service) RemoveStack(ctx context.Context, name string) error {
	return s.Store.Remove(ctx, name)
}

// ListStacks returns all registered stacks.
func (s *Service) ListStacks(ctx context.Context) ([]*stack.Stack, error) {
	return s.Store.List(ctx)
}

// GetStack returns one stack.
func (s *Service) GetStack(ctx context.Context, name string) (*stack.Stack, error) {
	return s.Store.Get(ctx, name)
}

// StackServices is the service/policy view of one stack.
type StackServices struct {
	Stack    *stack.Stack
	Project  *compose.Project
	Policies []stack.ServicePolicy
	Unknown  []string
}

// Services enumerates the effective Compose services and their policies.
func (s *Service) Services(ctx context.Context, name string) (*StackServices, error) {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	project, err := compose.Load(ctx, st.Path, st.ComposeFile)
	if err != nil {
		return nil, err
	}
	return &StackServices{
		Stack:    st,
		Project:  project,
		Policies: compose.Policies(project, st),
		Unknown:  compose.UnknownNames(project.Services, st.Allow, st.Exclude),
	}, nil
}

// SetPolicy updates one service's policy ("allow", "exclude", or "").
func (s *Service) SetPolicy(ctx context.Context, name, service, policy string) (*stack.Stack, error) {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if policy != "" && policy != stack.PolicyAllow && policy != stack.PolicyExclude {
		return nil, fmt.Errorf("invalid policy %q", policy)
	}
	project, err := compose.Load(ctx, st.Path, st.ComposeFile)
	if err != nil {
		return nil, err
	}
	if !contains(project.Services, service) {
		return nil, fmt.Errorf("service %q is not in the Compose file", service)
	}
	if err := s.Store.SetPolicy(ctx, st.ID, service, policy); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, name)
}

// GitStatus returns porcelain status.
func (s *Service) GitStatus(ctx context.Context, name string) (string, error) {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	return s.Git.Status(ctx, st.Path)
}

// GitFetch fetches the tracked branch.
func (s *Service) GitFetch(ctx context.Context, name string) error {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return err
	}
	return s.Git.Fetch(ctx, st.Path, remoteFor(st), st.Branch)
}

// GitCheck reports whether a pull is safe.
func (s *Service) GitCheck(ctx context.Context, name string) (git.CheckResult, error) {
	var res git.CheckResult
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return res, err
	}
	return s.Git.Check(ctx, st.Path, remoteFor(st), st.Branch)
}

// GitPull fast-forwards and runs the repo's on_pull hook when configured.
func (s *Service) GitPull(ctx context.Context, name string, out io.Writer, progress commands.StepProgress) (git.PullResult, error) {
	var res git.PullResult
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return res, err
	}
	res, err = s.Git.Pull(ctx, st.Path, remoteFor(st), st.Branch)
	if err != nil {
		return res, err
	}
	if !res.Pulled {
		return res, nil
	}
	if out == nil {
		out = io.Discard
	}
	if err := s.runOnPull(ctx, st, out, progress); err != nil {
		return res, fmt.Errorf("pulled %s -> %s, but on_pull failed: %w", shortSHA(res.From), shortSHA(res.To), err)
	}
	return res, nil
}

func (s *Service) runOnPull(ctx context.Context, st *stack.Stack, out io.Writer, progress commands.StepProgress) error {
	f, err := commands.Load(st.Path)
	if err != nil || f == nil || f.OnPull == "" {
		return err
	}
	return commands.NewRunner().Run(ctx, st, f, f.OnPull, nil, out, progress)
}

// CommandInfo describes a command available on a stack.
type CommandInfo struct {
	Name        string
	Description string
	Params      []commands.Param
	Source      string // "repo" or "builtin"
}

// ListCommands merges .stackman.yaml commands with native built-ins.
func (s *Service) ListCommands(ctx context.Context, name string) ([]CommandInfo, error) {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	f, err := commands.Load(st.Path)
	if err != nil {
		return nil, err
	}
	var out []CommandInfo
	seen := map[string]struct{}{}
	for _, cmdName := range f.Names() {
		def, _ := f.Get(cmdName)
		out = append(out, CommandInfo{
			Name:        cmdName,
			Description: def.Description,
			Params:      def.Spec().Params,
			Source:      "repo",
		})
		seen[cmdName] = struct{}{}
	}
	builtins := make([]string, 0, len(commands.Names()))
	for _, cmdName := range commands.Names() {
		if _, dup := seen[cmdName]; !dup {
			builtins = append(builtins, cmdName)
		}
	}
	sort.Strings(builtins)
	for _, cmdName := range builtins {
		cmd, _ := commands.Get(cmdName)
		out = append(out, CommandInfo{
			Name:        cmdName,
			Description: cmd.Description(),
			Params:      cmd.Spec().Params,
			Source:      "builtin",
		})
	}
	return out, nil
}

// RunCommand runs either a repo command or a native command.
func (s *Service) RunCommand(ctx context.Context, name, command string, params map[string]string, out io.Writer, progress commands.StepProgress) error {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}
	f, err := commands.Load(st.Path)
	if err != nil {
		return err
	}
	if _, ok := f.Get(command); ok {
		return commands.NewRunner().Run(ctx, st, f, command, params, out, progress)
	}
	if cmd, ok := commands.Get(command); ok {
		return cmd.Run(ctx, st, params, out)
	}
	if f == nil {
		return fmt.Errorf("command %q not found (no %s in %s)", command, commands.FileName, st.Path)
	}
	return fmt.Errorf("command %q not found", command)
}

// ---- compose helpers ----

type stackCompose struct {
	Stack   *stack.Stack
	Project *compose.Project
	Files   []string
	Managed []string
}

func (s *Service) stackCompose(ctx context.Context, name string) (*stackCompose, error) {
	st, err := s.Store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	files, err := compose.FindFiles(st.Path, st.ComposeFile)
	if err != nil {
		return nil, err
	}
	project, err := compose.Load(ctx, st.Path, st.ComposeFile)
	if err != nil {
		return nil, err
	}
	return &stackCompose{
		Stack:   st,
		Project: project,
		Files:   files,
		Managed: st.Managed(project.Services),
	}, nil
}

// target resolves requested services against the managed set. Empty means all
// managed services.
func (c *stackCompose) target(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return c.Managed, nil
	}
	for _, service := range requested {
		if !contains(c.Managed, service) {
			return nil, fmt.Errorf("service %q is not managed by stack %q (check allow/exclude policy)", service, c.Stack.Name)
		}
	}
	return requested, nil
}

func (s *Service) isRepo(ctx context.Context, dir string) (bool, error) {
	if detector, ok := s.Git.(interface {
		IsRepo(context.Context, string) (bool, error)
	}); ok {
		return detector.IsRepo(ctx, dir)
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil, nil
}

func (s *Service) origin(ctx context.Context, dir string) (string, error) {
	if provider, ok := s.Git.(interface {
		Origin(context.Context, string) (string, error)
	}); ok {
		return provider.Origin(ctx, dir)
	}
	return "", errors.New("origin not available")
}

func (s *Service) currentBranch(ctx context.Context, dir string) (string, error) {
	if provider, ok := s.Git.(interface {
		CurrentBranch(context.Context, string) (string, error)
	}); ok {
		return provider.CurrentBranch(ctx, dir)
	}
	return "", errors.New("current branch not available")
}

func remoteFor(st *stack.Stack) string {
	if st.Remote != "" {
		return st.Remote
	}
	return "origin"
}

func normalizeList(items []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, dup := seen[part]; dup {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func minus(items, remove []string) []string {
	rm := make(map[string]struct{}, len(remove))
	for _, item := range remove {
		rm[item] = struct{}{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, skip := rm[item]; !skip {
			out = append(out, item)
		}
	}
	return out
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
