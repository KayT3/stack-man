// Package daemon serves the private RPC API (phase 2).
//
// Responsibilities, per docs/agent.md §5 and docs/api.md:
//   - bind TCP to the subnet IP only, and source-filter by the subnet CIDR
//   - length-prefixed JSON-RPC framing
//   - run the auth handshake, then dispatch methods to the service layer
package daemon
