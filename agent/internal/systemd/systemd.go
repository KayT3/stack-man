// Package systemd generates the unit file and drives service management.
//
// Responsibilities (phase 1), per docs/agent.md §4:
//   - write /etc/systemd/system/stackman.service
//   - wrap systemctl for start/stop/restart/enable/disable/status
package systemd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Paths used by install/uninstall.
const (
	UnitPath   = "/etc/systemd/system/stackman.service"
	BinaryPath = "/usr/local/bin/stackman"
	UnitName   = "stackman"
)

// Unit is the generated systemd unit.
type Unit struct {
	Description string
	ExecStart   string
	User        string
}

// DefaultUnit returns the unit documented in docs/agent.md §4.
func DefaultUnit() Unit {
	return Unit{
		Description: "stackman agent",
		ExecStart:   BinaryPath + " daemon",
		User:        "root",
	}
}

// Render returns the unit file contents.
func (u Unit) Render() string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", u.Description)
	b.WriteString("After=network-online.target docker.service\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", u.ExecStart)
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("# Run as root (needs docker socket + /root/.ssh/authorized_keys).\n")
	b.WriteString("# A dedicated user in the `docker` group is the stricter alternative.\n")
	if u.User != "" {
		fmt.Fprintf(&b, "User=%s\n", u.User)
	}
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

// WriteUnit writes the unit file at path.
func WriteUnit(path string, u Unit) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("systemd: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(u.Render()), 0o644); err != nil {
		return fmt.Errorf("systemd: write %s: %w", path, err)
	}
	return nil
}

// Manager runs systemctl for the stackman unit.
type Manager struct {
	// Run executes a command and returns its combined output. Tests replace it.
	Run func(ctx context.Context, name string, args ...string) (string, error)
}

// New returns a Manager that shells out to the system's systemctl.
func New() *Manager {
	return &Manager{Run: runCommand}
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// DaemonReload runs systemctl daemon-reload.
func (m *Manager) DaemonReload(ctx context.Context) error {
	_, err := m.Run(ctx, "systemctl", "daemon-reload")
	return err
}

// Start starts the unit.
func (m *Manager) Start(ctx context.Context) error { return m.simple(ctx, "start") }

// Stop stops the unit.
func (m *Manager) Stop(ctx context.Context) error { return m.simple(ctx, "stop") }

// Restart restarts the unit.
func (m *Manager) Restart(ctx context.Context) error { return m.simple(ctx, "restart") }

// Enable enables the unit at boot.
func (m *Manager) Enable(ctx context.Context) error { return m.simple(ctx, "enable") }

// Disable disables the unit at boot.
func (m *Manager) Disable(ctx context.Context) error { return m.simple(ctx, "disable") }

// Status prints the unit status.
func (m *Manager) Status(ctx context.Context) error {
	out, err := m.Run(ctx, "systemctl", "status", UnitName, "--no-pager")
	if out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	return err
}

func (m *Manager) simple(ctx context.Context, verb string) error {
	_, err := m.Run(ctx, "systemctl", verb, UnitName)
	return err
}

// Install copies the running binary to BinaryPath (unless it is already
// there), writes the unit, reloads systemd, and enables + starts the service.
func (m *Manager) Install(ctx context.Context, executable string) error {
	if err := installBinary(executable, BinaryPath); err != nil {
		return err
	}
	if err := WriteUnit(UnitPath, DefaultUnit()); err != nil {
		return err
	}
	if err := m.DaemonReload(ctx); err != nil {
		return err
	}
	_, err := m.Run(ctx, "systemctl", "enable", "--now", UnitName)
	return err
}

// Uninstall stops and disables the unit and removes the unit file. The data
// directory is deliberately kept.
func (m *Manager) Uninstall(ctx context.Context) error {
	if _, err := m.Run(ctx, "systemctl", "disable", "--now", UnitName); err != nil {
		return err
	}
	if err := os.Remove(UnitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("systemd: remove %s: %w", UnitPath, err)
	}
	return m.DaemonReload(ctx)
}

func installBinary(src, dst string) error {
	if src == "" {
		return errors.New("systemd: cannot determine current executable")
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if srcAbs == dstAbs {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("systemd: create %s: %w", filepath.Dir(dst), err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("systemd: open %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".stackman-*")
	if err != nil {
		return fmt.Errorf("systemd: create binary: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("systemd: copy binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("systemd: install %s: %w", dst, err)
	}
	return nil
}
