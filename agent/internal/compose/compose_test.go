package compose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/example/stack-man/agent/internal/stack"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docker-compose.yml", "services: {}\n")
	writeFile(t, dir, "docker-compose.override.yml", "services: {}\n")

	files, err := FindFiles(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	if filepath.Base(files[0]) != "docker-compose.yml" || filepath.Base(files[1]) != "docker-compose.override.yml" {
		t.Fatalf("files = %v", files)
	}
}

func TestFindFilesPrefersComposeYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yml", "services: {}\n")
	writeFile(t, dir, "docker-compose.yml", "services: {}\n")
	writeFile(t, dir, "compose.override.yml", "services: {}\n")

	files, err := FindFiles(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || filepath.Base(files[0]) != "compose.yml" {
		t.Fatalf("files = %v", files)
	}
}

func TestFindFilesMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindFiles(dir, ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := FindFiles(dir, "nope.yml"); err == nil {
		t.Fatal("expected error for explicit missing file")
	}
}

func TestLoadAndPolicies(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docker-compose.yml", `
services:
  next:
    image: example/next
  ws:
    image: example/ws
  mysql:
    image: mysql:8
`)
	writeFile(t, dir, "docker-compose.override.yml", `
services:
  nginx:
    image: nginx:alpine
`)
	ctx := context.Background()
	project, err := Load(ctx, dir, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"mysql", "next", "nginx", "ws"}
	if !reflect.DeepEqual(project.Services, want) {
		t.Fatalf("services = %v, want %v", project.Services, want)
	}

	st := &stack.Stack{Allow: []string{"next", "ws"}, Exclude: []string{"mysql"}}
	policies := Policies(project, st)
	if len(policies) != 4 {
		t.Fatalf("policies = %+v", policies)
	}
	byName := map[string]string{}
	for _, p := range policies {
		byName[p.Service] = p.Policy
	}
	if byName["next"] != stack.PolicyAllow || byName["mysql"] != stack.PolicyExclude || byName["nginx"] != "" {
		t.Fatalf("policies = %+v", byName)
	}
}

func TestUnknownNames(t *testing.T) {
	got := UnknownNames([]string{"a", "b"}, []string{"a", "x"}, []string{"y", "x"})
	want := []string{"x", "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSanitizeProjectName(t *testing.T) {
	if got := sanitizeProjectName("My Stack!"); got != "my-stack" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeProjectName("---"); got != "stack" {
		t.Fatalf("got %q", got)
	}
}
