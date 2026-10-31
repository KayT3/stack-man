// Package discovery runs the UDP beacon responder (phase 2).
//
// Responsibilities, per docs/discovery.md:
//   - listen on the discovery port and reply to `discover` with `announce`
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"

	pkg "github.com/example/stack-man/pkg/discovery"
)

// Info is the identity advertised in an announce.
type Info struct {
	AgentID      string
	Hostname     string
	APIAddr      string
	AgentVersion string
	APIVersion   int
	Subnet       string
}

// Responder answers discovery probes.
type Responder struct {
	Info Info
	// Stacks returns the current stack names; called per probe.
	Stacks func() []string
}

// Listen opens the UDP socket on bindIP:port.
func Listen(bindIP net.IP, port int) (*net.UDPConn, error) {
	addr := &net.UDPAddr{IP: bindIP, Port: port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("discovery: listen %s: %w", addr, err)
	}
	return conn, nil
}

// Serve answers probes on conn until ctx is cancelled or the socket closes.
func (r *Responder) Serve(ctx context.Context, conn *net.UDPConn) error {
	buf := make([]byte, 64*1024)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("discovery: read: %w", err)
		}
		var probe pkg.Discover
		if err := json.Unmarshal(buf[:n], &probe); err != nil {
			continue
		}
		if probe.Magic != pkg.Magic || probe.Type != pkg.TypeDiscover {
			continue
		}
		announce := pkg.Announce{
			Magic:        pkg.Magic,
			Type:         pkg.TypeAnnounce,
			Version:      1,
			RequestID:    probe.RequestID,
			AgentID:      r.Info.AgentID,
			Hostname:     r.Info.Hostname,
			APIAddr:      r.Info.APIAddr,
			AgentVersion: r.Info.AgentVersion,
			APIVersion:   r.Info.APIVersion,
			Subnet:       r.Info.Subnet,
		}
		if r.Stacks != nil {
			announce.Stacks = r.Stacks()
		}
		payload, err := json.Marshal(announce)
		if err != nil {
			continue
		}
		if _, err := conn.WriteToUDP(payload, remote); err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
	}
}

// HostnameOr returns the machine hostname, falling back to fallback.
func HostnameOr(fallback string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return fallback
	}
	return host
}
