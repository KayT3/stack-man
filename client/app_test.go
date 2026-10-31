package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListAgentsStaticFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.toml")
	content := `
private_key_path = "unused"
use_ssh_agent = false
discovery_port = 42701
discovery_timeout = "50ms"
[[agent]]
  id = "vps-1"
  addr = "127.0.0.1:1"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewAppWithConfig(path)
	defer app.Close()
	agents, err := app.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "vps-1" || agents[0].Source != "static" {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestUnknownAgent(t *testing.T) {
	app := NewAppWithConfig(filepath.Join(t.TempDir(), "missing.toml"))
	defer app.Close()
	if _, err := app.ListStacks("nope"); err == nil {
		t.Fatal("expected unknown agent error")
	}
}
