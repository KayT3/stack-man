package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DiscoveryPort != 42701 || !c.UseSSHAgent {
		t.Fatalf("defaults = %+v", c)
	}
	if _, err := c.Timeout(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.toml")
	if err := os.WriteFile(path, []byte(`
private_key_path = "~/.ssh/id_rsa"
use_ssh_agent = false
discovery_port = 43000
discovery_timeout = "500ms"
[[agent]]
  id = "vps-1"
  addr = "100.96.1.2:42702"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscoveryPort != 43000 || c.UseSSHAgent {
		t.Fatalf("config = %+v", c)
	}
	if timeout, _ := c.Timeout(); timeout != 500*time.Millisecond {
		t.Fatalf("timeout = %v", timeout)
	}
	if len(c.Agents) != 1 || c.Agents[0].Addr != "100.96.1.2:42702" {
		t.Fatalf("agents = %+v", c.Agents)
	}
	if c.KeyPath() == "~/.ssh/id_rsa" {
		t.Fatal("key path not expanded")
	}
}

func TestInvalidTimeout(t *testing.T) {
	c := Default()
	c.DiscoveryTimeout = "soon"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
}
