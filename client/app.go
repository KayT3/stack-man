package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	clientagent "github.com/example/stack-man/client/internal/agent"
	clientconfig "github.com/example/stack-man/client/internal/config"
	clientdiscovery "github.com/example/stack-man/client/internal/discovery"
	clientkeys "github.com/example/stack-man/client/internal/keys"
	"github.com/example/stack-man/pkg/api"
)

// Emitter receives backend events for the UI. The Wails main wires this to
// runtime.EventsEmit; tests can use a no-op or recorder.
type Emitter interface {
	Emit(event string, data any)
}

type noopEmitter struct{}

func (noopEmitter) Emit(string, any) {}

// AgentInfo is the frontend-facing agent entry.
type AgentInfo struct {
	ID         string   `json:"id"`
	Hostname   string   `json:"hostname"`
	Addr       string   `json:"addr"`
	Version    string   `json:"version"`
	APIVersion int      `json:"api_version"`
	Subnet     string   `json:"subnet"`
	Stacks     []string `json:"stacks"`
	Source     string   `json:"source"`
	Reachable  bool     `json:"reachable"`
}

// UpOptions controls ContainersUp.
type UpOptions struct {
	Build         bool     `json:"build"`
	ForceRecreate bool     `json:"force_recreate"`
	RemoveOrphans bool     `json:"remove_orphans"`
	Services      []string `json:"services"`
}

// DownOptions controls ContainersDown.
type DownOptions struct {
	Volumes  bool     `json:"volumes"`
	Services []string `json:"services"`
}

// App is the Wails-bound backend. Its methods are called by the frontend and
// drive agent connections (discovery + SSH auth + JSON-RPC).
type App struct {
	mu         sync.Mutex
	configPath string
	cfg        *clientconfig.Config
	loadErr    error
	emitter    Emitter
	signer     *clientkeys.Result
	agents     map[string]clientdiscovery.Agent
	conns      map[string]*clientagent.Connection
}

// NewApp loads the default client config.
func NewApp() *App {
	return NewAppWithConfig(clientconfig.DefaultPath())
}

// NewAppWithConfig loads the client config at path.
func NewAppWithConfig(path string) *App {
	app := &App{
		configPath: path,
		emitter:    noopEmitter{},
		agents:     make(map[string]clientdiscovery.Agent),
		conns:      make(map[string]*clientagent.Connection),
	}
	app.cfg, app.loadErr = clientconfig.Load(path)
	return app
}

// SetEmitter installs the UI event sink.
func (a *App) SetEmitter(e Emitter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e == nil {
		e = noopEmitter{}
	}
	a.emitter = e
}

// ConfigPath returns the config file in use.
func (a *App) ConfigPath() string { return a.configPath }

// Close releases connections and ssh-agent.
func (a *App) Close() {
	a.mu.Lock()
	conns := a.conns
	signer := a.signer
	a.conns = make(map[string]*clientagent.Connection)
	a.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
	if signer != nil && signer.Closer != nil {
		signer.Closer.Close()
	}
}

func (a *App) config() (*clientconfig.Config, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadErr != nil {
		return nil, a.loadErr
	}
	if a.cfg == nil {
		return nil, errors.New("client config not loaded")
	}
	return a.cfg, nil
}

// ListAgents scans the subnet, merges the static list, and returns the result.
func (a *App) ListAgents() ([]AgentInfo, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	timeout, err := cfg.Timeout()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	found, err := clientdiscovery.Scan(ctx, cfg.DiscoveryPort, timeout, cfg.Agents)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	for _, agent := range found {
		a.agents[agent.ID] = agent
	}
	a.mu.Unlock()

	out := make([]AgentInfo, 0, len(found))
	for _, agent := range found {
		out = append(out, a.agentInfo(agent))
	}
	return out, nil
}

// AgentInfo returns live details for one agent.
func (a *App) AgentInfo(agentID string) (api.AgentInfo, error) {
	var info api.AgentInfo
	err := a.call(agentID, api.MethodInfo, nil, &info, 10*time.Second)
	if err != nil {
		return info, err
	}
	a.mu.Lock()
	if agent, ok := a.agents[agentID]; ok {
		agent.Hostname = info.Hostname
		agent.Version = info.Version
		agent.Subnet = info.Subnet
		a.agents[agentID] = agent
	}
	a.mu.Unlock()
	return info, nil
}

// ListStacks returns the stacks registered on an agent.
func (a *App) ListStacks(agentID string) ([]api.StackSummary, error) {
	var out []api.StackSummary
	err := a.call(agentID, api.MethodStackList, nil, &out, 15*time.Second)
	return out, err
}

// StackServices returns the Compose services and their policy.
func (a *App) StackServices(agentID, stack string) ([]api.ServicePolicy, error) {
	var out []api.ServicePolicy
	err := a.call(agentID, api.MethodStackServices, map[string]string{"name": stack}, &out, 15*time.Second)
	return out, err
}

// GitStatus returns porcelain status.
func (a *App) GitStatus(agentID, stack string) (string, error) {
	var out string
	err := a.call(agentID, api.MethodGitStatus, map[string]string{"name": stack}, &out, 15*time.Second)
	return out, err
}

// GitCheck reports whether a pull is safe.
func (a *App) GitCheck(agentID, stack string) (api.GitCheck, error) {
	var out api.GitCheck
	err := a.call(agentID, api.MethodGitCheck, map[string]string{"name": stack}, &out, 30*time.Second)
	return out, err
}

// GitPull fast-forwards the stack (streams output as events).
func (a *App) GitPull(agentID, stack string) (api.GitPullResult, error) {
	var out api.GitPullResult
	err := a.call(agentID, api.MethodGitPull, map[string]string{"name": stack}, &out, 2*time.Minute)
	return out, err
}

// ContainersUp starts the managed services.
func (a *App) ContainersUp(agentID, stack string, opts UpOptions) error {
	params := map[string]any{
		"name": stack, "build": opts.Build, "force_recreate": opts.ForceRecreate,
		"remove_orphans": opts.RemoveOrphans, "services": opts.Services,
	}
	return a.call(agentID, api.MethodContainersUp, params, nil, 10*time.Minute)
}

// ContainersDown stops the managed services.
func (a *App) ContainersDown(agentID, stack string, opts DownOptions) error {
	params := map[string]any{"name": stack, "volumes": opts.Volumes, "services": opts.Services}
	return a.call(agentID, api.MethodContainersDown, params, nil, 10*time.Minute)
}

// ContainersRestart restarts the managed services.
func (a *App) ContainersRestart(agentID, stack string, services []string) error {
	return a.call(agentID, api.MethodContainersRestart, map[string]any{"name": stack, "services": services}, nil, 5*time.Minute)
}

// ContainersBuild builds the managed services.
func (a *App) ContainersBuild(agentID, stack string, services []string) error {
	return a.call(agentID, api.MethodContainersBuild, map[string]any{"name": stack, "services": services}, nil, 30*time.Minute)
}

// ContainersPs lists containers for a stack.
func (a *App) ContainersPs(agentID, stack string) ([]api.ContainerStatus, error) {
	var out []api.ContainerStatus
	err := a.call(agentID, api.MethodContainersPs, map[string]string{"name": stack}, &out, 30*time.Second)
	return out, err
}

// ContainersLogs streams logs as events.
func (a *App) ContainersLogs(agentID, stack, service string, follow bool) error {
	params := map[string]any{"name": stack, "service": service, "follow": follow}
	return a.call(agentID, api.MethodContainersLogs, params, nil, 0)
}

// ListCommands lists profile and built-in commands.
func (a *App) ListCommands(agentID, stack string) ([]api.CommandInfo, error) {
	var out []api.CommandInfo
	err := a.call(agentID, api.MethodCommandList, map[string]string{"name": stack}, &out, 30*time.Second)
	return out, err
}

// RunCommand runs a command and streams output/step events.
func (a *App) RunCommand(agentID, stack, command string, params map[string]string) error {
	if params == nil {
		params = map[string]string{}
	}
	return a.call(agentID, api.MethodCommandRun, map[string]any{
		"name": stack, "command": command, "params": params,
	}, nil, 0)
}

// ---- connections ----

func (a *App) agentInfo(agent clientdiscovery.Agent) AgentInfo {
	a.mu.Lock()
	_, connected := a.conns[agent.ID]
	a.mu.Unlock()
	return AgentInfo{
		ID: agent.ID, Hostname: agent.Hostname, Addr: agent.Addr, Version: agent.Version,
		APIVersion: agent.APIVersion, Subnet: agent.Subnet, Stacks: agent.Stacks,
		Source: agent.Source, Reachable: connected || agent.Source == "beacon",
	}
}

func (a *App) call(agentID, method string, params, result any, timeout time.Duration) error {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	conn, err := a.connection(agentID)
	if err != nil {
		return err
	}
	err = conn.Call(ctx, method, params, result)
	if err != nil && strings.Contains(err.Error(), "connection closed") && timeout > 0 {
		if redialErr := a.reconnect(agentID); redialErr == nil {
			if retryConn, connErr := a.connection(agentID); connErr == nil {
				return retryConn.Call(ctx, method, params, result)
			}
		}
	}
	return err
}

func (a *App) reconnect(agentID string) error {
	a.mu.Lock()
	if conn, ok := a.conns[agentID]; ok {
		conn.Close()
		delete(a.conns, agentID)
	}
	a.mu.Unlock()
	_, err := a.connection(agentID)
	return err
}

func (a *App) connection(agentID string) (*clientagent.Connection, error) {
	a.mu.Lock()
	if conn, ok := a.conns[agentID]; ok {
		select {
		case <-conn.Done():
			delete(a.conns, agentID)
		default:
			a.mu.Unlock()
			return conn, nil
		}
	}
	agent, ok := a.agents[agentID]
	if !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("unknown agent %q; refresh discovery first", agentID)
	}
	a.mu.Unlock()

	signer, err := a.signerFor()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := clientagent.Dial(ctx, agent.Addr, signer)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	if existing, ok := a.conns[agentID]; ok {
		a.mu.Unlock()
		conn.Close()
		return existing, nil
	}
	a.conns[agentID] = conn
	emitter := a.emitter
	a.mu.Unlock()

	go a.pump(conn, emitter)
	return conn, nil
}

func (a *App) signerFor() (ssh.Signer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.signer != nil {
		return a.signer.Signer, nil
	}
	if a.loadErr != nil {
		return nil, a.loadErr
	}
	signer, err := clientkeys.Load(a.cfg)
	if err != nil {
		return nil, err
	}
	a.signer = signer
	return signer.Signer, nil
}

func (a *App) pump(conn *clientagent.Connection, emitter Emitter) {
	for {
		select {
		case notification := <-conn.Notifications():
			emitter.Emit("stream", notification)
		case <-conn.Done():
			return
		}
	}
}
