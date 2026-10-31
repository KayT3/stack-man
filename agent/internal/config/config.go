// Package config holds the agent configuration (docs/agent.md §3).
package config

import (
	"errors"

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
// TODO(phase 1): github.com/BurntSushi/toml.
func Load(path string) (*Config, error) {
	return nil, ErrNotImplemented
}

// Save writes c as TOML.
// TODO(phase 1): github.com/BurntSushi/toml.
func (c *Config) Save(path string) error {
	return ErrNotImplemented
}
