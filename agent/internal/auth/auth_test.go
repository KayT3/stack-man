package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	stackauth "github.com/example/stack-man/pkg/auth"
)

func testKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func writeKeys(t *testing.T, signers ...ssh.Signer) string {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("# trusted keys\n\n")
	for _, signer := range signers {
		buf.Write(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	}
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerify(t *testing.T) {
	signer := testKey(t)
	path := writeKeys(t, signer)
	v := NewVerifier(path)

	sessionID := "abc123"
	nonce := []byte("0123456789abcdef0123456789abcdef")
	sig, err := signer.Sign(rand.Reader, stackauth.SignedData(sessionID, nonce))
	if err != nil {
		t.Fatal(err)
	}

	res, err := v.Verify(sessionID, nonce, signer.PublicKey().Marshal(), ssh.Marshal(sig))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.OK || res.Identity == "" {
		t.Fatalf("result = %+v", res)
	}
}

func TestVerifyRejectsUnknownKey(t *testing.T) {
	trusted := testKey(t)
	other := testKey(t)
	path := writeKeys(t, trusted)
	v := NewVerifier(path)

	nonce := []byte("nonce")
	sig, _ := other.Sign(rand.Reader, stackauth.SignedData("s", nonce))
	_, err := v.Verify("s", nonce, other.PublicKey().Marshal(), ssh.Marshal(sig))
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != stackauth.ErrUnauthorizedKey {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyRejectsWrongSignature(t *testing.T) {
	signer := testKey(t)
	path := writeKeys(t, signer)
	v := NewVerifier(path)

	nonce := []byte("nonce")
	sig, _ := signer.Sign(rand.Reader, stackauth.SignedData("other-session", nonce))
	_, err := v.Verify("session", nonce, signer.PublicKey().Marshal(), ssh.Marshal(sig))
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != stackauth.ErrBadSignature {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyRejectsMalformedSignature(t *testing.T) {
	signer := testKey(t)
	path := writeKeys(t, signer)
	v := NewVerifier(path)
	_, err := v.Verify("s", []byte("n"), signer.PublicKey().Marshal(), []byte("garbage"))
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != stackauth.ErrBadSignature {
		t.Fatalf("error = %v", err)
	}
}

func TestParseAuthorizedKeysWithOptions(t *testing.T) {
	signer := testKey(t)
	line := `from="100.96.0.0/12",no-pty ` + string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	keys, err := ParseAuthorizedKeys([]byte(line + "\n"))
	if err != nil {
		t.Fatalf("ParseAuthorizedKeys: %v", err)
	}
	if len(keys) != 1 || len(keys[0].Options) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseAuthorizedKeysRejectsBadLine(t *testing.T) {
	if _, err := ParseAuthorizedKeys([]byte("not a key\n")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ParseAuthorizedKeys([]byte("# only comments\n")); err == nil {
		t.Fatal("expected no-keys error")
	}
}

func TestWireHelpers(t *testing.T) {
	signer := testKey(t)
	encoded := base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal())
	decoded, err := PublicKeyWire(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, signer.PublicKey().Marshal()) {
		t.Fatal("public key round trip mismatch")
	}
	if _, err := PublicKeyWire("%%%"); err == nil {
		t.Fatal("expected decode error")
	}
}
