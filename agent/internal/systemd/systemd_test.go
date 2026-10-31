package systemd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	got := DefaultUnit().Render()
	for _, want := range []string{
		"Description=stackman agent",
		"ExecStart=/usr/local/bin/stackman daemon",
		"Restart=on-failure",
		"User=root",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q:\n%s", want, got)
		}
	}
}

func TestWriteUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "stackman.service")
	if err := WriteUnit(path, DefaultUnit()); err != nil {
		t.Fatalf("WriteUnit: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[Service]") {
		t.Fatalf("unexpected unit: %s", data)
	}
}

func TestManagerCommands(t *testing.T) {
	var calls [][]string
	m := &Manager{Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", nil
	}}
	ctx := context.Background()
	if err := m.Install(ctx, filepath.Join(t.TempDir(), "stackman")); err == nil {
		// source does not exist -> installBinary fails before any command
		t.Fatal("expected missing binary error")
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "systemctl start stackman" {
		t.Fatalf("calls = %v", calls)
	}
	if err := m.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls[1], " ") != "systemctl restart stackman" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestInstallBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "bin", "stackman")
	if err := installBinary(src, dst); err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary" {
		t.Fatalf("content = %q", data)
	}
	if err := installBinary(dst, dst); err != nil {
		t.Fatalf("same path should be a no-op: %v", err)
	}
}
