// Package commands defines the Command/StepExecutor extension points and the
// registry (docs/commands.md). New native commands implement Command and call
// Register in init(); new YAML step types implement StepExecutor and call
// RegisterStep.
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

// StepExecutor runs one step of a declarative .stackman.yaml command
// (compose, exec, shell, git). See docs/commands.md §2.
type StepExecutor interface {
	// StepName returns the YAML key this executor handles (e.g. "exec").
	StepName() string
	Execute(ctx context.Context, st *stack.Stack, params map[string]string, out io.Writer) error
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

// Step returns a registered step executor by YAML key.
func Step(name string) (StepExecutor, bool) {
	s, ok := registry.steps[name]
	return s, ok
}
