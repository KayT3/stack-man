// Package discovery defines the UDP beacon message types shared by the agent
// and client (docs/discovery.md).
package discovery

// Magic tags stackman UDP datagrams so peers can ignore unrelated traffic.
const Magic = "stackman"

// DefaultPort is the well-known UDP discovery port.
const DefaultPort = 42701

// Beacon message type values.
const (
	TypeDiscover = "discover"
	TypeAnnounce = "announce"
)

// Discover is broadcast by the client to find agents.
type Discover struct {
	Magic     string `json:"magic"`
	Type      string `json:"type"`
	Version   int    `json:"version"`
	RequestID string `json:"request_id"`
}

// Announce is unicast back by each agent in response to a Discover.
type Announce struct {
	Magic        string   `json:"magic"`
	Type         string   `json:"type"`
	Version      int      `json:"version"`
	RequestID    string   `json:"request_id"`
	AgentID      string   `json:"agent_id"`
	Hostname     string   `json:"hostname"`
	APIAddr      string   `json:"api_addr"` // e.g. "100.96.1.2:42702"
	AgentVersion string   `json:"agent_version"`
	APIVersion   int      `json:"api_version"`
	Subnet       string   `json:"subnet"`
	Stacks       []string `json:"stacks"`
}
