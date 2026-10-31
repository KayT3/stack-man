package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/example/stack-man/agent/internal/stack"
)

type recordStep struct {
	Tag  string
	List []string
}

func (s *recordStep) StepName() string { return "record" }

func (s *recordStep) Substitute(params map[string]string) Step {
	if s == nil {
		return (*recordStep)(nil)
	}
	return &recordStep{
		Tag:  expand(s.Tag, params),
		List: expandSlice(s.List, params),
	}
}

type recordExecutor struct {
	got *recordStep
}

func (e *recordExecutor) StepName() string { return "record" }

func (e *recordExecutor) Execute(_ context.Context, _ *stack.Stack, _ map[string]string, step Step, out io.Writer) error {
	cfg, ok := step.(*recordStep)
	if !ok || cfg == nil {
		return fmt.Errorf("record: wrong step type %T", step)
	}
	e.got = cfg
	fmt.Fprint(out, "recorded")
	return nil
}

func TestProfileRun(t *testing.T) {
	p := &Profile{
		Repo:   "github.com/stackman-tests/demo",
		OnPull: "deploy",
		Commands: map[string]Def{
			"deploy": {
				Description: "Deploy",
				Params: []Param{
					{Name: "tag", Required: true},
					{Name: "mode", Default: "safe", Allowed: []string{"safe", "fast"}},
				},
				Steps: []Step{
					&recordStep{Tag: "${tag}", List: []string{"${mode}", "plain"}},
				},
			},
		},
	}
	rec := &recordExecutor{}
	RegisterStep(rec)

	var out bytes.Buffer
	var progress []string
	err := NewRunner().Run(context.Background(), &stack.Stack{Name: "x"}, p, "deploy",
		map[string]string{"tag": "v1"}, &out, func(step, total int, label string) {
			progress = append(progress, fmt.Sprintf("%d/%d %s", step, total, label))
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "recorded" {
		t.Fatalf("out = %q", out.String())
	}
	if rec.got.Tag != "v1" {
		t.Fatalf("tag = %q", rec.got.Tag)
	}
	wantList := []string{"safe", "plain"}
	if !reflect.DeepEqual(rec.got.List, wantList) {
		t.Fatalf("list = %#v, want %#v", rec.got.List, wantList)
	}
	if len(progress) != 1 || !strings.Contains(progress[0], "1/1") {
		t.Fatalf("progress = %v", progress)
	}
}

func TestRunParamErrors(t *testing.T) {
	p := &Profile{
		Repo: "github.com/stackman-tests/params",
		Commands: map[string]Def{
			"need":   {Steps: []Step{&recordStep{}}, Params: []Param{{Name: "name", Required: true}}},
			"choice": {Steps: []Step{&recordStep{}}, Params: []Param{{Name: "mode", Allowed: []string{"a", "b"}}}},
		},
	}
	RegisterStep(&recordExecutor{})
	st := &stack.Stack{Name: "x"}
	if err := NewRunner().Run(context.Background(), st, p, "need", nil, io.Discard, nil); err == nil {
		t.Fatal("expected required param error")
	}
	if err := NewRunner().Run(context.Background(), st, p, "choice", map[string]string{"mode": "c"}, io.Discard, nil); err == nil {
		t.Fatal("expected allowed-value error")
	}
	if err := NewRunner().Run(context.Background(), st, p, "missing", nil, io.Discard, nil); err == nil {
		t.Fatal("expected missing command error")
	}
	if err := NewRunner().Run(context.Background(), st, nil, "missing", nil, io.Discard, nil); err == nil {
		t.Fatal("expected missing profile error")
	}
}

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"":                                                "",
		"/srv/hotel":                                      "",
		`C:\repos\hotel`:                                  "",
		"git@github.com:Owner/Repo.git":                   "github.com/owner/repo",
		"https://github.com/Owner/Repo.git":               "github.com/owner/repo",
		"https://github.com/Owner/Repo/":                  "github.com/owner/repo",
		"ssh://git@github.com:22/Owner/Repo.git":          "github.com/owner/repo",
		"git://github.com/Owner/Repo.GIT":                 "github.com/owner/repo",
		"https://user:pass@github.com/Owner/Repo.git?x=1": "github.com/owner/repo",
		"https://github.com/":                             "",
		"github.com:Owner/Repo.git":                       "github.com/owner/repo",
	}
	for input, want := range cases {
		if got := NormalizeRemote(input); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProfileRegistry(t *testing.T) {
	p := Profile{
		Repo:    "git@github.com:Stackman-Tests/Widgets.git",
		Aliases: []string{"https://gitlab.com/stackman-tests/widgets"},
		OnPull:  "build",
		Commands: map[string]Def{
			"build": {
				Description: "Build widgets",
				Steps:       []Step{&ShellStep{Command: "echo build"}},
			},
		},
	}
	RegisterProfile(p)

	for _, remote := range []string{
		"git@github.com:Stackman-Tests/Widgets.git",
		"https://github.com/stackman-tests/widgets/",
		"ssh://git@github.com/stackman-tests/widgets.git",
		"https://gitlab.com/Stackman-Tests/Widgets",
	} {
		got := ProfileFor(remote)
		if got == nil || got.Repo != "github.com/stackman-tests/widgets" {
			t.Fatalf("ProfileFor(%q) = %+v", remote, got)
		}
	}
	if got := ProfileFor(""); got != nil {
		t.Fatalf("ProfileFor(empty) = %+v", got)
	}
	got := ProfileFor("git@github.com:Stackman-Tests/Widgets.git")
	if names := got.Names(); len(names) != 1 || names[0] != "build" {
		t.Fatalf("names = %v", names)
	}
	def, ok := got.Get("build")
	if !ok || def.Description != "Build widgets" {
		t.Fatalf("build = %+v, %v", def, ok)
	}
}

func TestRegisterProfileRejectsDuplicate(t *testing.T) {
	p := Profile{
		Repo: "github.com/stackman-tests/duplicate",
		Commands: map[string]Def{
			"x": {Steps: []Step{&ShellStep{Command: "true"}}},
		},
	}
	RegisterProfile(p)
	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate profile panic")
		}
	}()
	RegisterProfile(p)
}

func TestShellStep(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	step := &ShellStep{Command: "echo hello"}
	if err := (&ShellExecutor{}).Execute(context.Background(), &stack.Stack{Path: dir}, nil, step, &out); err != nil {
		t.Fatalf("shell: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("out = %q", out.String())
	}
	bad := &ShellStep{Command: "echo x", Cwd: "../../"}
	if err := (&ShellExecutor{}).Execute(context.Background(), &stack.Stack{Path: dir}, nil, bad, &out); err == nil {
		t.Fatal("expected cwd escape error")
	}
}

func TestTargetServices(t *testing.T) {
	managed := []string{"a", "b"}
	got, err := targetServices(managed, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	got, err = targetServices(managed, []string{"b"})
	if err != nil || len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := targetServices(managed, []string{"mysql"}); err == nil {
		t.Fatal("expected excluded service error")
	}
}

func TestDefServices(t *testing.T) {
	def := Def{Steps: []Step{
		&ExecStep{Service: "next"},
		&ComposeStep{Services: []string{"app", "next", ""}},
		&ShellStep{Command: "true"},
		&ExecStep{Service: "${dynamic}"},
	}}
	got := def.Services()
	want := []string{"app", "next"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Services = %#v, want %#v", got, want)
	}
}

func TestStepLabel(t *testing.T) {
	cases := []struct {
		step Step
		want string
	}{
		{&ComposeStep{Action: "up", Services: []string{"a", "b"}}, "docker compose up a b"},
		{&ExecStep{Service: "next", Command: "migrate"}, "docker compose exec next migrate"},
		{&ShellStep{Command: "echo hi"}, "shell: echo hi"},
		{&GitStep{Action: "pull"}, "git pull"},
	}
	for _, c := range cases {
		if got := StepLabel(c.step); got != c.want {
			t.Errorf("StepLabel(%T) = %q, want %q", c.step, got, c.want)
		}
	}
}
