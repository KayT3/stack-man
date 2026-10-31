package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/example/stack-man/agent/internal/stack"
)

// StepProgress is called before each step runs.
type StepProgress func(step, total int, label string)

// Runner executes compiled-in profile commands.
type Runner struct{}

// NewRunner returns a profile command runner.
func NewRunner() *Runner { return &Runner{} }

// Run executes a named command from p, streaming output to out.
func (r *Runner) Run(ctx context.Context, st *stack.Stack, p *Profile, name string, given map[string]string, out io.Writer, progress StepProgress) error {
	if p == nil {
		remote := ""
		if st != nil {
			remote = st.Remote
		}
		return fmt.Errorf("commands: %q not found (no profile matches remote %q)", name, remote)
	}
	def, ok := p.Get(name)
	if !ok {
		return fmt.Errorf("commands: %q not defined in profile %s", name, p.Repo)
	}
	params, err := resolveParams(def, given)
	if err != nil {
		return err
	}
	total := len(def.Steps)
	for i, step := range def.Steps {
		if step == nil {
			return fmt.Errorf("commands: %s step %d: nil step", name, i+1)
		}
		exec, ok := GetStep(step.StepName())
		if !ok {
			return fmt.Errorf("commands: step %d: no executor registered for %q", i+1, step.StepName())
		}
		if progress != nil {
			progress(i+1, total, StepLabel(step))
		}
		step = step.Substitute(params)
		if err := exec.Execute(ctx, st, params, step, out); err != nil {
			return fmt.Errorf("commands: %s step %d (%s): %w", name, i+1, step.StepName(), err)
		}
	}
	return nil
}

// StepLabel is a human-readable summary for progress notifications.
func StepLabel(step Step) string {
	if step == nil {
		return ""
	}
	switch s := step.(type) {
	case *ComposeStep:
		if s == nil {
			return "compose"
		}
		if len(s.Services) > 0 {
			return fmt.Sprintf("docker compose %s %s", s.Action, strings.Join(s.Services, " "))
		}
		return "docker compose " + s.Action
	case *ExecStep:
		if s == nil {
			return "exec"
		}
		return fmt.Sprintf("docker compose exec %s %s", s.Service, s.Command)
	case *ShellStep:
		if s == nil {
			return "shell"
		}
		return "shell: " + s.Command
	case *GitStep:
		if s == nil {
			return "git"
		}
		return "git " + s.Action
	default:
		return step.StepName()
	}
}

func resolveParams(def Def, given map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(given)+len(def.Params))
	for k, v := range given {
		out[k] = v
	}
	for _, p := range def.Params {
		if p.Name == "" {
			return nil, fmt.Errorf("commands: parameter without a name")
		}
		value, set := out[p.Name]
		if (!set || value == "") && p.Default != "" {
			value = p.Default
			out[p.Name] = value
		}
		if p.Required && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("commands: parameter %q is required", p.Name)
		}
		if len(p.Allowed) > 0 && value != "" {
			if !contains(p.Allowed, value) {
				return nil, fmt.Errorf("commands: parameter %q must be one of %s", p.Name, strings.Join(p.Allowed, ", "))
			}
		}
	}
	return out, nil
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
