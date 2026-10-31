package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/example/stack-man/agent/internal/stack"
)

type recordingStep struct {
	config map[string]any
}

func (s *recordingStep) StepName() string { return "record" }
func (s *recordingStep) Execute(context.Context, *stack.Stack, map[string]string, io.Writer) error {
	return errors.New("not configured")
}
func (s *recordingStep) ExecuteStep(_ context.Context, _ *stack.Stack, _ map[string]string, cfg map[string]any, out io.Writer) error {
	s.config = cfg
	fmt.Fprint(out, "recorded")
	return nil
}

func TestLoadAndRun(t *testing.T) {
	dir := t.TempDir()
	content := `
version: 1
on_pull: update
commands:
  deploy:
    description: "Deploy"
    params:
      - name: tag
        required: true
      - name: mode
        default: safe
        allowed: [safe, fast]
    steps:
      - record:
          tag: ${tag}
          list: ["${mode}", plain]
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.OnPull != "update" {
		t.Fatalf("on_pull = %q", f.OnPull)
	}
	if got := f.Names(); len(got) != 1 || got[0] != "deploy" {
		t.Fatalf("names = %v", got)
	}
	if spec := f.Commands["deploy"].Spec(); len(spec.Params) != 2 || !spec.Params[0].Required {
		t.Fatalf("spec = %+v", spec)
	}

	rec := &recordingStep{}
	RegisterStep(rec)
	var out bytes.Buffer
	var progress []string
	err = NewRunner().Run(context.Background(), &stack.Stack{Name: "x", Path: dir}, f, "deploy",
		map[string]string{"tag": "v1"}, &out, func(step, total int, label string) {
			progress = append(progress, fmt.Sprintf("%d/%d %s", step, total, label))
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "recorded" {
		t.Fatalf("out = %q", out.String())
	}
	if rec.config["tag"] != "v1" {
		t.Fatalf("cfg = %+v", rec.config)
	}
	list, ok := rec.config["list"].([]any)
	if !ok || len(list) != 2 || list[0] != "safe" || list[1] != "plain" {
		t.Fatalf("list = %#v", rec.config["list"])
	}
	if len(progress) != 1 || !strings.Contains(progress[0], "1/1") {
		t.Fatalf("progress = %v", progress)
	}
}

func TestRunParamErrors(t *testing.T) {
	dir := t.TempDir()
	f := &File{Commands: map[string]Def{
		"need":   {Steps: []YAMLStep{{Type: "record"}}, Params: []ParamDef{{Name: "name", Required: true}}},
		"choice": {Steps: []YAMLStep{{Type: "record"}}, Params: []ParamDef{{Name: "mode", Allowed: []string{"a", "b"}}}},
	}}
	RegisterStep(&recordingStep{})
	st := &stack.Stack{Name: "x", Path: dir}
	if err := NewRunner().Run(context.Background(), st, f, "need", nil, io.Discard, nil); err == nil {
		t.Fatal("expected required param error")
	}
	if err := NewRunner().Run(context.Background(), st, f, "choice", map[string]string{"mode": "c"}, io.Discard, nil); err == nil {
		t.Fatal("expected allowed-value error")
	}
	if err := NewRunner().Run(context.Background(), st, f, "missing", nil, io.Discard, nil); err == nil {
		t.Fatal("expected missing command error")
	}
}

func TestLoadMissingAndInvalid(t *testing.T) {
	f, err := Load(t.TempDir())
	if err != nil || f != nil {
		t.Fatalf("missing file = %v, %v", f, err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("commands:\n  x:\n    steps: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for command without steps")
	}

	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("version: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unsupported version error")
	}
}

func TestShellStep(t *testing.T) {
	dir := t.TempDir()
	step := &ShellStep{}
	var out bytes.Buffer
	cmd := "echo hello"
	if err := step.ExecuteStep(context.Background(), &stack.Stack{Path: dir}, nil, map[string]any{"command": cmd}, &out); err != nil {
		t.Fatalf("shell: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("out = %q", out.String())
	}
	if err := step.ExecuteStep(context.Background(), &stack.Stack{Path: dir}, nil, map[string]any{"command": "echo x", "cwd": "../../"}, &out); err == nil {
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

func TestStepUnmarshalRejectsMultipleKeys(t *testing.T) {
	var s YAMLStep
	err := yaml.Unmarshal([]byte("compose:\n  action: up\nexec:\n  service: x\n"), &s)
	if err == nil {
		t.Fatal("expected error")
	}
}
