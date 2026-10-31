// Package config holds the agent configuration (docs/agent.md §3).
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/example/stack-man/pkg/api"
	"github.com/example/stack-man/pkg/discovery"
)

// ErrNotImplemented marks scaffold stubs awaiting implementation.
var ErrNotImplemented = errors.New("stackman: not implemented yet")

// Paths used when running as a system service.
const (
	DefaultConfigPath = "/etc/stackman/config.toml"
	DefaultDataDir    = "/var/lib/stackman"
	DefaultDBPath     = DefaultDataDir + "/stackman.db"
)

// Config is the on-disk agent configuration (TOML).
type Config struct {
	Subnet             string `toml:"subnet"`
	BindIP             string `toml:"bind_ip"`
	DiscoveryPort      int    `toml:"discovery_port"`
	APIPort            int    `toml:"api_port"`
	AuthorizedKeysPath string `toml:"authorized_keys_path"`
	DataDir            string `toml:"data_dir"`
}

// Default returns sane defaults. Shared constants come from pkg.
func Default() Config {
	return Config{
		Subnet:             "100.96.0.0/12",
		DiscoveryPort:      discovery.DefaultPort,
		APIPort:            api.DefaultAPIPort,
		AuthorizedKeysPath: "/root/.ssh/authorized_keys",
		DataDir:            DefaultDataDir,
	}
}

// Load reads a TOML config file, merging onto Default().
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("config: empty config path")
	}
	c := Default()
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: %s not found; run \"stackman config\" first", path)
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the values needed by the daemon.
func (c *Config) Validate() error {
	if c.Subnet == "" {
		return errors.New("config: subnet is required")
	}
	if _, _, err := net.ParseCIDR(c.Subnet); err != nil {
		return fmt.Errorf("config: invalid subnet %q: %w", c.Subnet, err)
	}
	if c.BindIP != "" && net.ParseIP(c.BindIP) == nil {
		return fmt.Errorf("config: invalid bind_ip %q", c.BindIP)
	}
	if c.DiscoveryPort < 1 || c.DiscoveryPort > 65535 {
		return fmt.Errorf("config: discovery_port %d out of range", c.DiscoveryPort)
	}
	if c.APIPort < 1 || c.APIPort > 65535 {
		return fmt.Errorf("config: api_port %d out of range", c.APIPort)
	}
	if c.AuthorizedKeysPath == "" {
		return errors.New("config: authorized_keys_path is required")
	}
	if c.DataDir == "" {
		return errors.New("config: data_dir is required")
	}
	return nil
}

// DBPath returns the SQLite database path derived from DataDir.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "stackman.db")
}

// Save writes c as TOML, creating the parent directory when needed.
func (c *Config) Save(path string) error {
	if path == "" {
		return errors.New("config: empty config path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("config: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	return nil
}
