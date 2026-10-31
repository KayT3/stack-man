// Package commands defines the Command/StepExecutor extension points, the
// compiled-in repo profile registry, and the native command registry
// (docs/commands.md). Native commands implement Command and call Register in
// init(); repo profiles call RegisterProfile; new step types implement Step and
// StepExecutor.
package commands

import (
	"context"
	"io"

	"github.com/example/stack-man/agent/internal/stack"
)

// Param describes a single input a command accepts, so UIs can render a form.
// It mirrors pkg/api.Param on the wire; map between them at the RPC boundary.
type Param struct {
	Name        string
	Description string
	Required    bool
	Default     string
	Allowed     []string // empty = free-form
}

// CommandSpec describes a command's inputs and UI hints.
type CommandSpec struct {
	Params     []Param
	NeedsBuild bool
}

// Command is a named, composable action run against a Stack.
type Command interface {
	Name() string
	Description() string
	Spec() CommandSpec
	// Run executes the command, streaming output to out. A non-nil error marks
	// the command failed. ctx carries cancellation from the caller.
	Run(ctx context.Context, st *stack.Stack, params map[string]string, out io.Writer) error
}

// Step is one action in a compiled-in profile command (compose, exec, shell,
// git). Exactly one concrete step type describes the action.
type Step interface {
	// StepName returns the executor key for this step (e.g. "exec").
	StepName() string
	// Substitute returns a copy with ${param} placeholders expanded.
	Substitute(params map[string]string) Step
}

// StepExecutor runs one concrete Step (compose, exec, shell, git). See
// docs/commands.md §2.
type StepExecutor interface {
	// StepName returns the step type this executor handles (e.g. "exec").
	StepName() string
	// Execute runs step, streaming output to out.
	Execute(ctx context.Context, st *stack.Stack, params map[string]string, step Step, out io.Writer) error
}

// registry is the process-wide command registry.
var registry = struct {
	commands map[string]Command
	steps    map[string]StepExecutor
}{
	commands: make(map[string]Command),
	steps:    make(map[string]StepExecutor),
}

// Register adds a native command. Duplicate names panic (programmer error).
func Register(c Command) {
	if _, dup := registry.commands[c.Name()]; dup {
		panic("commands: duplicate command name " + c.Name())
	}
	registry.commands[c.Name()] = c
}

// RegisterStep adds a declarative step executor.
func RegisterStep(s StepExecutor) {
	registry.steps[s.StepName()] = s
}

// Get returns a registered command by name.
func Get(name string) (Command, bool) {
	c, ok := registry.commands[name]
	return c, ok
}

// Names returns registered command names.
func Names() []string {
	out := make([]string, 0, len(registry.commands))
	for n := range registry.commands {
		out = append(out, n)
	}
	return out
}

// GetStep returns a registered step executor by step name.
func GetStep(name string) (StepExecutor, bool) {
	s, ok := registry.steps[name]
	return s, ok
}
