// Package config holds the desktop client configuration (docs/client.md §3).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Agent is a static peer entry.
type Agent struct {
	ID   string `toml:"id"`
	Addr string `toml:"addr"`
}

// Config is ~/.config/stackman/client.toml.
type Config struct {
	PrivateKeyPath   string  `toml:"private_key_path"`
	UseSSHAgent      bool    `toml:"use_ssh_agent"`
	DiscoveryPort    int     `toml:"discovery_port"`
	DiscoveryTimeout string  `toml:"discovery_timeout"`
	Agents           []Agent `toml:"agent"`
}

// Default returns sane defaults.
func Default() Config {
	return Config{
		PrivateKeyPath:   "~/.ssh/id_ed25519",
		UseSSHAgent:      true,
		DiscoveryPort:    42701,
		DiscoveryTimeout: "2s",
	}
}

// DefaultPath returns the platform config path.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "client.toml"
	}
	return filepath.Join(dir, "stackman", "client.toml")
}

// Load reads path, merging onto Default(). A missing file is not an error:
// the defaults are usable.
func Load(path string) (*Config, error) {
	c := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &c, nil
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("client config: read %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the config.
func (c *Config) Validate() error {
	if c.DiscoveryPort < 1 || c.DiscoveryPort > 65535 {
		return fmt.Errorf("client config: discovery_port %d out of range", c.DiscoveryPort)
	}
	if _, err := c.Timeout(); err != nil {
		return err
	}
	for i, agent := range c.Agents {
		if agent.Addr == "" {
			return fmt.Errorf("client config: agent %d has no addr", i)
		}
	}
	return nil
}

// Timeout returns the parsed discovery timeout.
func (c *Config) Timeout() (time.Duration, error) {
	raw := strings.TrimSpace(c.DiscoveryTimeout)
	if raw == "" {
		return 2 * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("client config: invalid discovery_timeout %q", c.DiscoveryTimeout)
	}
	return d, nil
}

// KeyPath returns PrivateKeyPath with ~ expanded.
func (c *Config) KeyPath() string {
	return ExpandHome(c.PrivateKeyPath)
}

// Save writes the config as TOML.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".client-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ExpandHome expands a leading ~.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
