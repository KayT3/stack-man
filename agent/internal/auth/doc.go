// Package auth verifies the SSH challenge-response handshake on the agent side.
//
// Responsibilities (phase 2), per docs/api.md §2:
//   - parse authorized_keys with golang.org/x/crypto/ssh
//   - verify a client signature over pkg/auth.SignedData(...)
//   - return the auth error codes from pkg/auth
package auth
