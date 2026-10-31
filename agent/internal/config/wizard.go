package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
)

// InterfaceCandidate is one local address the wizard can bind to.
type InterfaceCandidate struct {
	Interface string
	IP        net.IP
	Network   *net.IPNet
}

// CIDR returns the network in CIDR notation.
func (c InterfaceCandidate) CIDR() string {
	if c.Network == nil {
		return ""
	}
	return c.Network.String()
}

// Label is the human-facing line shown by the wizard.
func (c InterfaceCandidate) Label() string {
	return fmt.Sprintf("%s — %s (%s)", c.Interface, c.IP, c.CIDR())
}

// Candidates enumerates usable local interface addresses: private, CGNAT
// (Cloudflare WARP), link-local and loopback addresses. It deliberately hides
// public addresses.
func Candidates() ([]InterfaceCandidate, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("config: enumerate interfaces: %w", err)
	}
	var out []InterfaceCandidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if !usableIP(ip) {
				continue
			}
			out = append(out, InterfaceCandidate{
				Interface: iface.Name,
				IP:        ip,
				Network:   ipNet,
			})
		}
	}
	return out, nil
}

func usableIP(ip net.IP) bool {
	return ip.IsPrivate() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		inCGNAT(ip)
}

// inCGNAT reports whether ip is in 100.64.0.0/10, the shared address space
// Cloudflare WARP uses.
func inCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// DeriveSubnet returns the network CIDR containing ip using the interface's
// netmask.
//
// Cloudflare WARP assigns each device a point-to-point /32, so the interface
// mask would yield a subnet that contains only the agent itself. Peers on the
// same WARP network then fail the daemon's source-subnet check, so WARP
// addresses fall back to the documented shared range.
func DeriveSubnet(ip net.IP, network *net.IPNet) string {
	if network == nil {
		return ""
	}
	if ones, bits := network.Mask.Size(); bits == 32 && ones == 32 && inCGNAT(ip) {
		return Default().Subnet
	}
	return (&net.IPNet{IP: ip.Mask(network.Mask), Mask: network.Mask}).String()
}

// Run walks the interactive wizard and writes the result to path.
func Run(path string) error {
	fmt.Fprintln(os.Stderr, "stackman config")
	fmt.Fprintln(os.Stderr)

	candidates, err := Candidates()
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("config: no usable private interface addresses found")
	}

	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = c.Label()
	}
	var choice int
	if err := survey.AskOne(&survey.Select{
		Message: "Private subnet interface:",
		Options: labels,
	}, &choice); err != nil {
		return err
	}
	selected := candidates[choice]

	c := Default()
	c.BindIP = selected.IP.String()
	c.Subnet = DeriveSubnet(selected.IP, selected.Network)
	if c.Subnet == "" {
		c.Subnet = Default().Subnet
	}

	if err := askInt("Discovery UDP port:", &c.DiscoveryPort); err != nil {
		return err
	}
	if err := askInt("RPC API TCP port:", &c.APIPort); err != nil {
		return err
	}
	if err := survey.AskOne(&survey.Input{
		Message: "authorized_keys path:",
		Default: c.AuthorizedKeysPath,
	}, &c.AuthorizedKeysPath); err != nil {
		return err
	}
	if err := survey.AskOne(&survey.Input{
		Message: "Data directory:",
		Default: c.DataDir,
	}, &c.DataDir); err != nil {
		return err
	}
	c.AuthorizedKeysPath = expandHome(c.AuthorizedKeysPath)
	c.DataDir = expandHome(c.DataDir)

	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.Save(path); err != nil {
		return err
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("config: create data dir %s: %w", c.DataDir, err)
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", path)
	return nil
}

func askInt(message string, target *int) error {
	for {
		var raw string
		if err := survey.AskOne(&survey.Input{
			Message: message,
			Default: strconv.Itoa(*target),
		}, &raw); err != nil {
			return err
		}
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && n >= 1 && n <= 65535 {
			*target = n
			return nil
		}
		fmt.Fprintln(os.Stderr, "enter a port between 1 and 65535")
	}
}

func expandHome(path string) string {
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
