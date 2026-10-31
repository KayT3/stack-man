// Package api defines the JSON-RPC 2.0 protocol constants and wire DTOs shared
// by the agent and client (docs/api.md). It has zero dependencies so both
// modules stay lightweight.
package api

import "encoding/json"

// APIVersion is the protocol version, negotiated via agent.info.
const APIVersion = 1

// DefaultAPIPort is the agent RPC API port. It's a default only: each agent
// advertises its actual port via discovery (docs/discovery.md).
const DefaultAPIPort = 42702

// JSON-RPC method names.
const (
	MethodPing    = "agent.ping"
	MethodInfo    = "agent.info"
	MethodVersion = "agent.version"

	MethodStackList      = "stack.list"
	MethodStackGet       = "stack.get"
	MethodStackAdd       = "stack.add"
	MethodStackRemove    = "stack.remove"
	MethodStackServices  = "stack.services"
	MethodStackSetPolicy = "stack.set_policy"

	MethodGitStatus = "stack.git.status"
	MethodGitFetch  = "stack.git.fetch"
	MethodGitCheck  = "stack.git.check"
	MethodGitPull   = "stack.git.pull"

	MethodContainersUp                = "stack.containers.up"
	MethodContainersDown              = "stack.containers.down"
	MethodContainersRestart           = "stack.containers.restart"
	MethodContainersBuild             = "stack.containers.build"
	MethodContainersPs                = "stack.containers.ps"
	MethodContainersExec              = "stack.containers.exec"
	MethodContainersLogs              = "stack.containers.logs"
	MethodContainersNetworks          = "stack.containers.networks"
	MethodContainersNetworkConnect    = "stack.containers.network_connect"
	MethodContainersNetworkDisconnect = "stack.containers.network_disconnect"

	MethodCommandList = "stack.command.list"
	MethodCommandRun  = "stack.command.run"

	// MethodCancel cancels an in-flight request by its JSON-RPC id.
	MethodCancel = "cancel"

	// Notifications (server -> client), docs/api.md §4.
	NotifyStreamLog  = "stream.log"
	NotifyStreamStep = "stream.step"
)

// JSON-RPC error codes used by the agent.
const (
	ErrNotFound      = -32004
	ErrConflict      = -32010
	ErrInvalidParams = -32602
	ErrInternal      = -32603
)

// Request is a JSON-RPC request/notification frame.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Notification is a server -> client event (streams/progress).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// ---- wire DTOs (add more as methods are implemented; see docs/api.md §3) ----

// AgentInfo is returned by agent.info.
type AgentInfo struct {
	AgentID    string `json:"agent_id"`
	Hostname   string `json:"hostname"`
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
	Subnet     string `json:"subnet"`
	Uptime     string `json:"uptime"`
	StackCount int    `json:"stack_count"`
}

// StackSummary is the light per-stack listing entry.
type StackSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

// Stack is the full stack detail (stack.get / stack.add result).
type Stack struct {
	StackSummary
	ComposeFile string   `json:"compose_file"`
	Allow       []string `json:"allow"`
	Exclude     []string `json:"exclude"`
}

// ServicePolicy is one compose service and its policy.
type ServicePolicy struct {
	Service string `json:"service"`
	Policy  string `json:"policy"` // "allow" | "exclude" | "" (unspecified)
}

// Param describes a command input for UI rendering (docs/commands.md §1).
type Param struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Default     string   `json:"default,omitempty"`
	Allowed     []string `json:"allowed,omitempty"`
}

// CommandInfo describes a registered command (stack.command.list).
type CommandInfo struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params,omitempty"`
}

// GitCheck is the result of stack.git.check (docs/stacks.md §6).
type GitCheck struct {
	SafeToPull   bool     `json:"safe_to_pull"`
	Clean        bool     `json:"clean"`
	Ahead        int      `json:"ahead"`
	Behind       int      `json:"behind"`
	Diverged     bool     `json:"diverged"`
	ChangedFiles []string `json:"changed_files"`
	Branch       string   `json:"branch"`
	Head         string   `json:"head"`
	RemoteHead   string   `json:"remote_head"`
}

// GitPullResult is the result of stack.git.pull.
type GitPullResult struct {
	Pulled bool   `json:"pulled"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// ContainerStatus is one row of stack.containers.ps.
type ContainerStatus struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
}

// ExecResult is the result of stack.containers.exec.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// Network is one Docker network (stack.containers.networks).
type Network struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

// OKResult is the generic success result.
type OKResult struct {
	OK bool `json:"ok"`
}

// CommandRunResult is the final result of stack.command.run.
type CommandRunResult struct {
	OK       bool `json:"ok"`
	ExitCode int  `json:"exit_code"`
}

// StreamLog is the params payload of a stream.log notification.
type StreamLog struct {
	Stack   string `json:"stack"`
	Command string `json:"command,omitempty"`
	Stream  string `json:"stream"`
	Data    string `json:"data"`
}

// StreamStep is the params payload of a stream.step notification.
type StreamStep struct {
	Stack   string `json:"stack"`
	Command string `json:"command,omitempty"`
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Label   string `json:"label"`
}
