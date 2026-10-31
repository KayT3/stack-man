package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/example/stack-man/client/internal/config"
)

func writeKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return path, sshPub
}

func TestLoadFromFile(t *testing.T) {
	path, pub := writeKey(t)
	cfg := config.Default()
	cfg.UseSSHAgent = false
	cfg.PrivateKeyPath = path
	res, err := Load(&cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Closer != nil {
		t.Fatal("file signer should have no closer")
	}
	if string(res.Signer.PublicKey().Marshal()) != string(pub.Marshal()) {
		t.Fatal("wrong key loaded")
	}
}

func TestLoadMissingKey(t *testing.T) {
	cfg := config.Default()
	cfg.UseSSHAgent = false
	cfg.PrivateKeyPath = filepath.Join(t.TempDir(), "missing")
	if _, err := Load(&cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadNoPath(t *testing.T) {
	cfg := config.Default()
	cfg.UseSSHAgent = false
	cfg.PrivateKeyPath = ""
	if _, err := Load(&cfg); err == nil {
		t.Fatal("expected error")
	}
}
