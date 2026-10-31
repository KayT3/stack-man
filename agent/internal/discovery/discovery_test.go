package discovery

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	pkg "github.com/example/stack-man/pkg/discovery"
)

func TestResponderAnnounces(t *testing.T) {
	conn, err := Listen(net.ParseIP("127.0.0.1"), 0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Responder{
		Info: Info{
			AgentID:      "agent-1",
			Hostname:     "vps-1",
			APIAddr:      "127.0.0.1:42702",
			AgentVersion: "0.1.0",
			APIVersion:   1,
			Subnet:       "100.96.0.0/12",
		},
		Stacks: func() []string { return []string{"hotel"} },
	}
	done := make(chan error, 1)
	go func() { done <- r.Serve(ctx, conn) }()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.WriteToUDP([]byte("garbage"), conn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	probe, _ := json.Marshal(pkg.Discover{Magic: pkg.Magic, Type: pkg.TypeDiscover, Version: 1, RequestID: "req-1"})
	if _, err := client.WriteToUDP(probe, conn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read announce: %v", err)
	}
	var announce pkg.Announce
	if err := json.Unmarshal(buf[:n], &announce); err != nil {
		t.Fatalf("announce JSON: %v", err)
	}
	if announce.Magic != pkg.Magic || announce.Type != pkg.TypeAnnounce {
		t.Fatalf("announce = %+v", announce)
	}
	if announce.RequestID != "req-1" || announce.AgentID != "agent-1" || announce.APIAddr != "127.0.0.1:42702" {
		t.Fatalf("announce = %+v", announce)
	}
	if len(announce.Stacks) != 1 || announce.Stacks[0] != "hotel" {
		t.Fatalf("stacks = %v", announce.Stacks)
	}

	cancel()
	conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

func TestResponderIgnoresForeignTraffic(t *testing.T) {
	conn, err := Listen(net.ParseIP("127.0.0.1"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Responder{Info: Info{AgentID: "a"}}
	go r.Serve(ctx, conn)

	client, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer client.Close()
	other, _ := json.Marshal(map[string]string{"magic": "other", "type": "discover"})
	client.WriteToUDP(other, conn.LocalAddr().(*net.UDPAddr))
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1024)
	if _, _, err := client.ReadFromUDP(buf); err == nil {
		t.Fatal("expected no reply to foreign traffic")
	}
}
