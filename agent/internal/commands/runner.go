package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/example/stack-man/agent/internal/stack"
)

// StepConfigExecutor is implemented by step executors that need the step's
// YAML config. The base StepExecutor interface is kept small for executors
// that are fully configured at construction time.
type StepConfigExecutor interface {
	StepExecutor
	ExecuteStep(ctx context.Context, st *stack.Stack, params map[string]string, config map[string]any, out io.Writer) error
}

// StepProgress is called before each step runs.
type StepProgress func(step, total int, label string)

// Runner executes .stackman.yaml commands.
type Runner struct{}

// NewRunner returns a DSL runner.
func NewRunner() *Runner { return &Runner{} }

// Run executes a named command from f, streaming output to out.
func (r *Runner) Run(ctx context.Context, st *stack.Stack, f *File, name string, given map[string]string, out io.Writer, progress StepProgress) error {
	def, ok := f.Get(name)
	if !ok {
		if f == nil {
			return fmt.Errorf("commands: %s not found (no %s in %s)", name, FileName, st.Path)
		}
		return fmt.Errorf("commands: %q not defined in %s", name, FileName)
	}
	params, err := resolveParams(def, given)
	if err != nil {
		return err
	}
	total := len(def.Steps)
	for i, step := range def.Steps {
		exec, ok := Step(step.Type)
		if !ok {
			return fmt.Errorf("commands: step %d: no executor registered for %q", i+1, step.Type)
		}
		label := StepLabel(step)
		if progress != nil {
			progress(i+1, total, label)
		}
		if cfgExec, ok := exec.(StepConfigExecutor); ok {
			cfg := substituteMap(step.Config, params)
			if err := cfgExec.ExecuteStep(ctx, st, params, cfg, out); err != nil {
				return fmt.Errorf("commands: %s step %d (%s): %w", name, i+1, step.Type, err)
			}
			continue
		}
		if err := exec.Execute(ctx, st, params, out); err != nil {
			return fmt.Errorf("commands: %s step %d (%s): %w", name, i+1, step.Type, err)
		}
	}
	return nil
}

// StepLabel is a human-readable summary for progress notifications.
func StepLabel(step YAMLStep) string {
	switch step.Type {
	case "compose":
		action := stringValue(step.Config["action"])
		services := stringSlice(step.Config["services"])
		if len(services) > 0 {
			return fmt.Sprintf("docker compose %s %s", action, strings.Join(services, " "))
		}
		return "docker compose " + action
	case "exec":
		return fmt.Sprintf("docker compose exec %s %s", stringValue(step.Config["service"]), stringValue(step.Config["command"]))
	case "shell":
		return "shell: " + stringValue(step.Config["command"])
	case "git":
		return "git " + stringValue(step.Config["action"])
	default:
		return step.Type
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

func substituteMap(cfg map[string]any, params map[string]string) map[string]any {
	if cfg == nil {
		return nil
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = substituteValue(v, params)
	}
	return out
}

func substituteValue(v any, params map[string]string) any {
	switch value := v.(type) {
	case string:
		out := value
		for name, replacement := range params {
			out = strings.ReplaceAll(out, "${"+name+"}", replacement)
		}
		return out
	case map[string]any:
		return substituteMap(value, params)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = substituteValue(item, params)
		}
		return out
	case []string:
		out := make([]string, len(value))
		for i, item := range value {
			out[i] = item
			for name, replacement := range params {
				out[i] = strings.ReplaceAll(out[i], "${"+name+"}", replacement)
			}
		}
		return out
	default:
		return v
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func stringSlice(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	default:
		return nil
	}
}
