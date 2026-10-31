// Package discovery finds agents on the private subnet and merges the static
// peer list (docs/discovery.md).
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/example/stack-man/client/internal/config"
	pkg "github.com/example/stack-man/pkg/discovery"
)

// Agent is one discovered or statically configured agent.
type Agent struct {
	ID           string   `json:"id"`
	Addr         string   `json:"addr"`
	Hostname     string   `json:"hostname"`
	Version      string   `json:"version"`
	APIVersion   int      `json:"api_version"`
	Subnet       string   `json:"subnet"`
	Stacks       []string `json:"stacks"`
	Source       string   `json:"source"` // "beacon" or "static"
	LastAnnounce string   `json:"last_announce,omitempty"`
}

// Scan broadcasts a probe and merges responses with the static list.
func Scan(ctx context.Context, port int, timeout time.Duration, static []config.Agent) ([]Agent, error) {
	return scan(ctx, broadcastTargets(port), timeout, static)
}

func scan(ctx context.Context, targets []string, timeout time.Duration, static []config.Agent) ([]Agent, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("discovery: listen: %w", err)
	}
	defer conn.Close()
	_ = enableBroadcast(conn)

	var reqBytes [8]byte
	_, _ = rand.Read(reqBytes[:])
	requestID := hex.EncodeToString(reqBytes[:])
	probe, _ := json.Marshal(pkg.Discover{
		Magic: pkg.Magic, Type: pkg.TypeDiscover, Version: 1, RequestID: requestID,
	})
	for _, target := range targets {
		addr, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(probe, addr)
	}

	deadline := time.Now().Add(timeout)
	agents := map[string]Agent{}
	buf := make([]byte, 64*1024)
	for {
		if ctx.Err() != nil {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(remaining))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			break
		}
		var announce pkg.Announce
		if err := json.Unmarshal(buf[:n], &announce); err != nil {
			continue
		}
		if announce.Magic != pkg.Magic || announce.Type != pkg.TypeAnnounce || announce.RequestID != requestID {
			continue
		}
		if announce.APIAddr == "" {
			continue
		}
		agents[announce.APIAddr] = Agent{
			ID: announce.AgentID, Addr: announce.APIAddr, Hostname: announce.Hostname,
			Version: announce.AgentVersion, APIVersion: announce.APIVersion,
			Subnet: announce.Subnet, Stacks: announce.Stacks, Source: "beacon",
			LastAnnounce: time.Now().UTC().Format(time.RFC3339),
		}
	}

	for _, staticAgent := range static {
		if _, exists := agents[staticAgent.Addr]; exists {
			continue
		}
		agents[staticAgent.Addr] = Agent{
			ID: staticAgent.ID, Addr: staticAgent.Addr, Source: "static",
		}
	}

	out := make([]Agent, 0, len(agents))
	for _, agent := range agents {
		out = append(out, agent)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Addr < out[j].Addr
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// broadcastTargets returns every directed broadcast address plus the limited
// broadcast address.
func broadcastTargets(port int) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(ip net.IP) {
		if ip == nil || ip.IsUnspecified() {
			return
		}
		target := net.JoinHostPort(ip.String(), fmt.Sprint(port))
		if _, dup := seen[target]; dup {
			return
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok || ipNet.IP.To4() == nil {
					continue
				}
				ip := ipNet.IP.To4()
				mask := ipNet.Mask
				if len(mask) != 4 {
					continue
				}
				broadcast := make(net.IP, 4)
				for i := 0; i < 4; i++ {
					broadcast[i] = ip[i] | ^mask[i]
				}
				add(broadcast)
			}
		}
	}
	add(net.IPv4bcast)
	return out
}
