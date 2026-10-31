package commands

import "strings"

// ComposeStep runs `docker compose <action>`.
type ComposeStep struct {
	Action   string
	Flags    []string
	Services []string
}

func (s *ComposeStep) StepName() string { return "compose" }

func (s *ComposeStep) Substitute(params map[string]string) Step {
	if s == nil {
		return (*ComposeStep)(nil)
	}
	return &ComposeStep{
		Action:   expand(s.Action, params),
		Flags:    expandSlice(s.Flags, params),
		Services: expandSlice(s.Services, params),
	}
}

// ExecStep runs `docker compose exec -T <service> <command>`.
type ExecStep struct {
	Service string
	Command string
	Workdir string
	Env     map[string]string
}

func (s *ExecStep) StepName() string { return "exec" }

func (s *ExecStep) Substitute(params map[string]string) Step {
	if s == nil {
		return (*ExecStep)(nil)
	}
	env := make(map[string]string, len(s.Env))
	for key, value := range s.Env {
		env[key] = expand(value, params)
	}
	return &ExecStep{
		Service: expand(s.Service, params),
		Command: expand(s.Command, params),
		Workdir: expand(s.Workdir, params),
		Env:     env,
	}
}

// ShellStep is the raw shell escape hatch. Prefer compose/exec when possible.
type ShellStep struct {
	Command string
	Cwd     string
}

func (s *ShellStep) StepName() string { return "shell" }

func (s *ShellStep) Substitute(params map[string]string) Step {
	if s == nil {
		return (*ShellStep)(nil)
	}
	return &ShellStep{
		Command: expand(s.Command, params),
		Cwd:     expand(s.Cwd, params),
	}
}

// GitStep runs fetch/check/pull through the git.Client.
type GitStep struct {
	Action string
	Branch string
}

func (s *GitStep) StepName() string { return "git" }

func (s *GitStep) Substitute(params map[string]string) Step {
	if s == nil {
		return (*GitStep)(nil)
	}
	return &GitStep{
		Action: expand(s.Action, params),
		Branch: expand(s.Branch, params),
	}
}

func expand(value string, params map[string]string) string {
	for name, replacement := range params {
		value = strings.ReplaceAll(value, "${"+name+"}", replacement)
	}
	return value
}

func expandSlice(values []string, params map[string]string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = expand(value, params)
	}
	return out
}
