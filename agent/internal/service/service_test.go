package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

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
