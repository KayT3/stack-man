// Package auth defines the SSH challenge-response message types shared by the
// agent and client. It carries only wire shapes and the exact bytes to sign;
// the actual signing/verification uses golang.org/x/crypto/ssh on both sides.
//
// Protocol (docs/api.md §2):
//
//	agent  -> client:  auth.challenge   { session_id, nonce }
//	client -> agent:   auth.authenticate { session_id, public_key, signature }
//	signature is over SignedData(sessionID, nonceBytes).
package auth

import "crypto/sha256"

// ProtocolVersion is the version of the auth handshake.
const ProtocolVersion = 1

// DomainPrefix is prepended to the signed input to prevent cross-protocol
// signature reuse.
const DomainPrefix = "stackman-auth-v1"

// Handshake method names.
const (
	MethodChallenge    = "auth.challenge"
	MethodAuthenticate = "auth.authenticate"
)

// Auth error codes (JSON-RPC error codes), per docs/api.md §2.3.
const (
	ErrUnauthorizedKey = -32001
	ErrBadSignature    = -32002
	ErrBadHandshake    = -32003
)

// Challenge is sent by the agent immediately after a client connects.
type Challenge struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"` // hex-encoded, random, per connection
	Nonce     string `json:"nonce"`      // base64 of random 32 bytes
}

// Authenticate is the client's response carrying its key and signature.
type Authenticate struct {
	SessionID string `json:"session_id"`
	PublicKey string `json:"public_key"` // SSH wire format, base64
	Signature string `json:"signature"`  // SSH signature, base64
}

// Result is a successful auth result.
type Result struct {
	Identity string `json:"identity"` // e.g. "ssh-ed25519 AAAAC3…"
	OK       bool   `json:"ok"`
}

// SignedData builds the exact byte sequence the client signs and the agent
// verifies. Both sides MUST use this function so the bytes match:
//
//	SHA256(DomainPrefix || 0x00 || sessionID || nonce)
//
// nonce is the decoded bytes (not the base64 string).
func SignedData(sessionID string, nonce []byte) []byte {
	h := sha256.New()
	h.Write([]byte(DomainPrefix))
	h.Write([]byte{0x00})
	h.Write([]byte(sessionID))
	h.Write(nonce)
	return h.Sum(nil)
}
