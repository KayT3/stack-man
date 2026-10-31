package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/example/stack-man/agent/internal/commands"
	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/stack"
)

func testService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	svc, err := New(&cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { svc.Close() })
	return svc
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "add", "."},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestAddStackAndServices(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(`
services:
  next: {image: next}
  ws: {image: ws}
  mysql: {image: mysql}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)

	svc := testService(t)
	ctx := context.Background()
	st, warnings, err := svc.AddStack(ctx, AddOptions{
		Path:    dir,
		Allow:   []string{"next", "ws", "typo"},
		Exclude: []string{"mysql"},
	})
	if err != nil {
		t.Fatalf("AddStack: %v", err)
	}
	if st.Name != filepath.Base(dir) {
		t.Fatalf("name = %q", st.Name)
	}
	if st.ComposeFile != "docker-compose.yml" {
		t.Fatalf("compose file = %q", st.ComposeFile)
	}
	if st.Branch != "main" {
		t.Fatalf("branch = %q", st.Branch)
	}
	if !reflect.DeepEqual(st.Allow, []string{"next", "typo", "ws"}) {
		t.Fatalf("allow = %v", st.Allow)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}

	view, err := svc.Services(ctx, st.Name)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(view.Policies) != 3 {
		t.Fatalf("policies = %+v", view.Policies)
	}
	want := map[string]string{"next": stack.PolicyAllow, "ws": stack.PolicyAllow, "mysql": stack.PolicyExclude}
	for _, p := range view.Policies {
		if want[p.Service] != p.Policy {
			t.Fatalf("policy for %s = %q, want %q", p.Service, p.Policy, want[p.Service])
		}
	}

	st2, err := svc.SetPolicy(ctx, st.Name, "mysql", "")
	if err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if len(st2.Exclude) != 0 {
		t.Fatalf("exclude = %v", st2.Exclude)
	}
}

func TestAddStackRejectsNonRepo(t *testing.T) {
	svc := testService(t)
	dir := t.TempDir()
	if _, _, err := svc.AddStack(context.Background(), AddOptions{Path: dir}); err == nil {
		t.Fatal("expected error for non-repo")
	}
}

func TestPackagingListAndConfig(t *testing.T) {
	svc := testService(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(`
services:
  app: {image: app}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	if _, _, err := svc.AddStack(context.Background(), AddOptions{Path: dir, Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListStacks(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "demo" {
		t.Fatalf("ListStacks = %v, %v", list, err)
	}
}

func TestProfileCommands(t *testing.T) {
	commands.RegisterProfile(commands.Profile{
		Repo: "github.com/stackman-tests/service-profile",
		Commands: map[string]commands.Def{
			"hello": {
				Description: "Say hello",
				Steps:       []commands.Step{&commands.ShellStep{Command: "echo hello"}},
			},
		},
	})

	svc := testService(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(`
services:
  app: {image: app}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	st, _, err := svc.AddStack(context.Background(), AddOptions{
		Path:   dir,
		Name:   "profile-demo",
		Remote: "git@github.com:Stackman-Tests/Service-Profile.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	infos, err := svc.ListCommands(context.Background(), st.Name)
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	var found bool
	for _, info := range infos {
		if info.Name != "hello" {
			continue
		}
		found = true
		if info.Source != "profile" || info.Description != "Say hello" {
			t.Fatalf("hello = %+v", info)
		}
	}
	if !found {
		t.Fatalf("profile command missing from %+v", infos)
	}

	var out bytes.Buffer
	if err := svc.RunCommand(context.Background(), st.Name, "hello", nil, &out, nil); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestProfileCommandsHideMissingServices(t *testing.T) {
	commands.RegisterProfile(commands.Profile{
		Repo: "github.com/stackman-tests/conditional-profile",
		Commands: map[string]commands.Def{
			"always": {
				Description: "Targets no service",
				Steps:       []commands.Step{&commands.ShellStep{Command: "true"}},
			},
			"backup": {
				Description: "Needs the backup service",
				Steps:       []commands.Step{&commands.ExecStep{Service: "backup", Command: "backup run"}},
			},
			"build-app": {
				Description: "Needs the app service",
				Steps:       []commands.Step{&commands.ComposeStep{Action: "build", Services: []string{"app"}}},
			},
		},
	})

	svc := testService(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(`
services:
  app: {image: app}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	st, _, err := svc.AddStack(context.Background(), AddOptions{
		Path:   dir,
		Name:   "conditional-demo",
		Remote: "git@github.com:Stackman-Tests/Conditional-Profile.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	infos, err := svc.ListCommands(context.Background(), st.Name)
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	visible := map[string]bool{}
	for _, info := range infos {
		visible[info.Name] = true
	}
	if !visible["always"] || !visible["build-app"] {
		t.Fatalf("commands targeting existing services missing: %+v", infos)
	}
	if visible["backup"] {
		t.Fatalf("backup should be hidden without a backup service: %+v", infos)
	}

	if err := svc.RunCommand(context.Background(), st.Name, "backup", nil, io.Discard, nil); err == nil {
		t.Fatal("expected unavailable command error")
	}
}

func TestNormalizeAndMinus(t *testing.T) {
	got := normalizeList([]string{" b, a ", "a", ""})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("normalize = %v", got)
	}
	if got := minus([]string{"a", "b", "c"}, []string{"b"}); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("minus = %v", got)
	}
}

func TestParsePS(t *testing.T) {
	cases := [][]byte{
		[]byte(`[{"Name":"p-next-1","Service":"next","State":"running","Status":"Up 2 hours"}]`),
		[]byte("{\"Name\":\"p-next-1\",\"Service\":\"next\",\"State\":\"running\",\"Status\":\"Up 2 hours\"}\n"),
	}
	for _, data := range cases {
		rows, err := parsePS(data)
		if err != nil {
			t.Fatalf("parsePS: %v", err)
		}
		if len(rows) != 1 || rows[0].Name != "p-next-1" || rows[0].Service != "next" {
			t.Fatalf("rows = %+v", rows)
		}
	}
	if rows, err := parsePS(nil); err != nil || len(rows) != 0 {
		t.Fatalf("empty = %v, %v", rows, err)
	}
}
