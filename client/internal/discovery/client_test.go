package discovery

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/example/stack-man/client/internal/config"
	pkg "github.com/example/stack-man/pkg/discovery"
)

func TestScanMergesBeaconsAndStatic(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	go func() {
		buf := make([]byte, 4096)
		n, remote, err := server.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var probe pkg.Discover
		if err := json.Unmarshal(buf[:n], &probe); err != nil {
			return
		}
		announce, _ := json.Marshal(pkg.Announce{
			Magic: pkg.Magic, Type: pkg.TypeAnnounce, Version: 1,
			RequestID: probe.RequestID, AgentID: "vps-1", Hostname: "vps-1",
			APIAddr: "127.0.0.1:42702", AgentVersion: "0.1.0", APIVersion: 1,
			Subnet: "100.96.0.0/12", Stacks: []string{"hotel"},
		})
		_, _ = server.WriteToUDP(announce, remote)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	agents, err := scan(ctx, []string{server.LocalAddr().String()}, 500*time.Millisecond, []config.Agent{
		{ID: "vps-2", Addr: "127.0.0.1:42703"},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	byID := map[string]Agent{}
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	if byID["vps-1"].Source != "beacon" || byID["vps-1"].Hostname != "vps-1" {
		t.Fatalf("beacon agent = %+v", byID["vps-1"])
	}
	if byID["vps-2"].Source != "static" {
		t.Fatalf("static agent = %+v", byID["vps-2"])
	}
}

func TestScanStaticOnly(t *testing.T) {
	ctx := context.Background()
	agents, err := scan(ctx, []string{"127.0.0.1:1"}, 100*time.Millisecond, []config.Agent{
		{ID: "vps-1", Addr: "100.96.1.2:42702"},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(agents) != 1 || agents[0].Source != "static" {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestBroadcastTargetsIncludesLimitedBroadcast(t *testing.T) {
	targets := broadcastTargets(42701)
	if len(targets) == 0 {
		t.Fatal("no targets")
	}
	found := false
	for _, target := range targets {
		if target == "255.255.255.255:42701" {
			found = true
		}
	}
	if !found {
		t.Fatalf("targets = %v", targets)
	}
}
