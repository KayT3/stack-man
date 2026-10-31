// Package auth verifies the SSH challenge-response handshake on the agent side.
//
// Responsibilities (phase 2), per docs/api.md §2:
//   - parse authorized_keys with golang.org/x/crypto/ssh
//   - verify a client signature over pkg/auth.SignedData(...)
//   - return the auth error codes from pkg/auth
package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	stackauth "github.com/example/stack-man/pkg/auth"
)

// Error is a handshake failure carrying the JSON-RPC error code.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Key is one trusted public key from authorized_keys.
type Key struct {
	PublicKey ssh.PublicKey
	Comment   string
	Options   []string
}

// Identity returns the canonical "type base64" key identity.
func (k Key) Identity() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.PublicKey)))
}

// LoadKeys parses an authorized_keys file.
func LoadKeys(path string) ([]Key, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read %s: %w", path, err)
	}
	return ParseAuthorizedKeys(data)
}

// ParseAuthorizedKeys parses authorized_keys content.
func ParseAuthorizedKeys(data []byte) ([]Key, error) {
	var keys []Key
	rest := data
	for len(rest) > 0 {
		var line []byte
		if idx := bytes.IndexByte(rest, '\n'); idx >= 0 {
			line, rest = rest[:idx], rest[idx+1:]
		} else {
			line, rest = rest, nil
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		pub, comment, options, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			return nil, fmt.Errorf("auth: parse authorized_keys line %q: %w", trimmed, err)
		}
		keys = append(keys, Key{PublicKey: pub, Comment: comment, Options: options})
	}
	if len(keys) == 0 {
		return nil, errors.New("auth: no usable keys in authorized_keys")
	}
	return keys, nil
}

// Verifier checks client signatures against a live authorized_keys file.
type Verifier struct {
	Path string
}

// NewVerifier returns a verifier for path.
func NewVerifier(path string) *Verifier { return &Verifier{Path: path} }

// Verify checks that publicKeyWire is trusted and signatureWire is a valid
// signature over SignedData(sessionID, nonce).
func (v *Verifier) Verify(sessionID string, nonce, publicKeyWire, signatureWire []byte) (stackauth.Result, error) {
	var result stackauth.Result

	pub, err := ssh.ParsePublicKey(publicKeyWire)
	if err != nil {
		return result, &Error{Code: stackauth.ErrBadHandshake, Message: "invalid public key"}
	}
	keys, err := LoadKeys(v.Path)
	if err != nil {
		return result, &Error{Code: stackauth.ErrBadHandshake, Message: err.Error()}
	}

	var trusted *Key
	for i := range keys {
		if bytes.Equal(keys[i].PublicKey.Marshal(), publicKeyWire) {
			trusted = &keys[i]
			break
		}
	}
	if trusted == nil {
		return result, &Error{Code: stackauth.ErrUnauthorizedKey, Message: "unauthorized key"}
	}

	var sig ssh.Signature
	if err := ssh.Unmarshal(signatureWire, &sig); err != nil {
		return result, &Error{Code: stackauth.ErrBadSignature, Message: "malformed signature"}
	}
	if err := pub.Verify(stackauth.SignedData(sessionID, nonce), &sig); err != nil {
		return result, &Error{Code: stackauth.ErrBadSignature, Message: "signature verification failed"}
	}

	result.Identity = trusted.Identity()
	result.OK = true
	return result, nil
}

// PublicKeyWire decodes a base64 SSH public key as sent on the wire.
func PublicKeyWire(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}

// SignatureWire decodes a base64 SSH signature as sent on the wire.
func SignatureWire(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}
