package config

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	want := Default()
	want.BindIP = "100.96.1.2"
	want.APIPort = 43000
	want.DataDir = filepath.Join(t.TempDir(), "data")
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", *got, want)
	}
}

func TestLoadMergesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("api_port = 5000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if got.APIPort != 5000 {
		t.Fatalf("api_port = %d, want 5000", got.APIPort)
	}
	if got.DiscoveryPort != def.DiscoveryPort || got.Subnet != def.Subnet {
		t.Fatalf("defaults not merged: %+v", got)
	}
}

func TestLoadMissingMentionsConfig(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidate(t *testing.T) {
	c := Default()
	c.Subnet = "nope"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid subnet error")
	}
	c = Default()
	c.APIPort = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestDeriveSubnet(t *testing.T) {
	_, network, _ := net.ParseCIDR("100.96.1.2/12")
	network.IP = net.ParseIP("100.96.1.2")
	got := DeriveSubnet(net.ParseIP("100.96.1.2"), network)
	if got != "100.96.0.0/12" {
		t.Fatalf("DeriveSubnet = %q", got)
	}
}

func TestDeriveSubnetWARPPointToPoint(t *testing.T) {
	// WARP presents a /32, which must not become the peer subnet.
	_, network, _ := net.ParseCIDR("100.96.0.2/32")
	network.IP = net.ParseIP("100.96.0.2")
	got := DeriveSubnet(net.ParseIP("100.96.0.2"), network)
	if got != Default().Subnet {
		t.Fatalf("DeriveSubnet = %q, want %q", got, Default().Subnet)
	}
	// A non-CGNAT /32 (e.g. a provider point-to-point link) is left alone.
	_, lan, _ := net.ParseCIDR("192.168.1.5/32")
	lan.IP = net.ParseIP("192.168.1.5")
	if got := DeriveSubnet(net.ParseIP("192.168.1.5"), lan); got != "192.168.1.5/32" {
		t.Fatalf("non-CGNAT DeriveSubnet = %q", got)
	}
}

func TestInCGNAT(t *testing.T) {
	if !inCGNAT(net.ParseIP("100.96.1.2")) {
		t.Fatal("100.96.1.2 should be CGNAT")
	}
	if inCGNAT(net.ParseIP("10.0.0.1")) {
		t.Fatal("10.0.0.1 is not CGNAT")
	}
}
