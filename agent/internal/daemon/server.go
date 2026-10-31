// Package daemon serves the private RPC API (phase 2).
//
// Responsibilities, per docs/agent.md §5 and docs/api.md:
//   - bind TCP to the subnet IP only, and source-filter by the subnet CIDR
//   - length-prefixed JSON-RPC framing
//   - run the auth handshake, then dispatch methods to the service layer
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/stack-man/agent/internal/auth"
	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/discovery"
	"github.com/example/stack-man/agent/internal/service"
	"github.com/example/stack-man/agent/internal/version"
	"github.com/example/stack-man/pkg/api"
	pkgauth "github.com/example/stack-man/pkg/auth"
)

// Server is the authenticated JSON-RPC daemon.
type Server struct {
	Service  *service.Service
	Config   *config.Config
	AgentID  string
	Started  time.Time
	Verifier *auth.Verifier

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
}

// New prepares a server. The agent ID is persisted under the data dir so
// discovery entries stay stable across restarts.
func New(svc *service.Service, cfg *config.Config) (*Server, error) {
	agentID, err := LoadOrCreateAgentID(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		Service:  svc,
		Config:   cfg,
		AgentID:  agentID,
		Started:  time.Now(),
		Verifier: auth.NewVerifier(cfg.AuthorizedKeysPath),
		conns:    make(map[net.Conn]struct{}),
	}, nil
}

// LoadOrCreateAgentID returns the stable agent id stored in dataDir.
func LoadOrCreateAgentID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "agent-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("daemon: read agent id: %w", err)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("daemon: generate agent id: %w", err)
	}
	id := hex.EncodeToString(b[:])
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("daemon: write agent id: %w", err)
	}
	return id, nil
}

// ResolveBindIP returns cfg.BindIP, or the first address inside cfg.Subnet.
func ResolveBindIP(cfg *config.Config) (net.IP, error) {
	if cfg.BindIP != "" {
		ip := net.ParseIP(cfg.BindIP)
		if ip == nil {
			return nil, fmt.Errorf("daemon: invalid bind_ip %q", cfg.BindIP)
		}
		return ip, nil
	}
	_, subnet, err := net.ParseCIDR(cfg.Subnet)
	if err != nil {
		return nil, fmt.Errorf("daemon: invalid subnet: %w", err)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || !subnet.Contains(ipNet.IP) {
				continue
			}
			return ipNet.IP, nil
		}
	}
	return nil, fmt.Errorf("daemon: no local address inside %s; set bind_ip in the config", cfg.Subnet)
}

// Run binds the API and discovery sockets and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	bindIP, err := ResolveBindIP(s.Config)
	if err != nil {
		return err
	}
	_, subnet, err := net.ParseCIDR(s.Config.Subnet)
	if err != nil {
		return err
	}

	apiAddr := net.JoinHostPort(bindIP.String(), strconv.Itoa(s.Config.APIPort))
	listener, err := net.Listen("tcp", apiAddr)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", apiAddr, err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	udp, err := discovery.Listen(bindIP, s.Config.DiscoveryPort)
	if err != nil {
		listener.Close()
		return err
	}

	responder := &discovery.Responder{
		Info: discovery.Info{
			AgentID:      s.AgentID,
			Hostname:     discovery.HostnameOr("unknown"),
			APIAddr:      listener.Addr().String(),
			AgentVersion: version.Version,
			APIVersion:   api.APIVersion,
			Subnet:       s.Config.Subnet,
		},
		Stacks: func() []string {
			stacks, err := s.Service.ListStacks(context.Background())
			if err != nil {
				return nil
			}
			names := make([]string, 0, len(stacks))
			for _, st := range stacks {
				names = append(names, st.Name)
			}
			return names
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = responder.Serve(ctx, udp)
	}()

	go func() {
		<-ctx.Done()
		listener.Close()
		udp.Close()
		s.closeConns()
	}()

	var acceptWG sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				acceptWG.Wait()
				wg.Wait()
				return nil
			}
			s.closeConns()
			acceptWG.Wait()
			return fmt.Errorf("daemon: accept: %w", err)
		}
		if !subnet.Contains(remoteIP(conn)) {
			conn.Close()
			continue
		}
		s.track(conn)
		acceptWG.Add(1)
		go func() {
			defer acceptWG.Done()
			defer s.untrack(conn)
			s.serveConn(ctx, conn)
		}()
	}
}

func remoteIP(conn net.Conn) net.IP {
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return addr.IP
	}
	return nil
}

func (s *Server) track(conn net.Conn) {
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

func (s *Server) closeConns() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

// serveConn performs the handshake then serves JSON-RPC requests.
func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	c := &serverConn{
		server:  s,
		conn:    conn,
		writer:  newFrameWriter(conn),
		cancels: make(map[string]context.CancelFunc),
	}
	if err := c.authenticate(ctx); err != nil {
		return
	}

	for {
		frame, err := readFrame(conn)
		if err != nil {
			return
		}
		var req api.Request
		if err := json.Unmarshal(frame, &req); err != nil {
			_ = c.writer.writeJSON(api.Response{
				JSONRPC: "2.0",
				Error:   &api.Error{Code: api.ErrInvalidParams, Message: "invalid JSON-RPC request"},
			})
			continue
		}
		if req.Method == api.MethodCancel {
			c.handleCancel(req)
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			// Client-originated notifications are not part of the protocol.
			continue
		}
		reqCtx, reqCancel := context.WithCancel(ctx)
		key := string(req.ID)
		c.mu.Lock()
		c.cancels[key] = reqCancel
		c.mu.Unlock()

		wg.Add(1)
		go func(req api.Request, key string) {
			defer wg.Done()
			defer reqCancel()
			defer func() {
				c.mu.Lock()
				delete(c.cancels, key)
				c.mu.Unlock()
			}()
			result, rpcErr := s.dispatch(reqCtx, req.Method, req.Params, &stream{
				conn:   c,
				method: req.Method,
			})
			resp := api.Response{JSONRPC: "2.0", ID: req.ID}
			if rpcErr != nil {
				resp.Error = &api.Error{Code: rpcErr.Code, Message: rpcErr.Message, Data: rpcErr.Data}
			} else {
				payload, err := json.Marshal(result)
				if err != nil {
					resp.Error = &api.Error{Code: api.ErrInternal, Message: err.Error()}
				} else {
					resp.Result = payload
				}
			}
			_ = c.writer.writeJSON(resp)
		}(req, key)
	}
}

type serverConn struct {
	server  *Server
	conn    net.Conn
	writer  *frameWriter
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func (c *serverConn) handleCancel(req api.Request) {
	var params struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(req.Params, &params)
	c.mu.Lock()
	cancel := c.cancels[string(params.ID)]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = c.writer.writeJSON(api.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"cancelled":true}`)})
}

func (c *serverConn) authenticate(ctx context.Context) error {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	var session [16]byte
	if _, err := rand.Read(session[:]); err != nil {
		return err
	}
	challenge := pkgauth.Challenge{
		Version:   pkgauth.ProtocolVersion,
		SessionID: hex.EncodeToString(session[:]),
		Nonce:     base64.StdEncoding.EncodeToString(nonce[:]),
	}
	payload, _ := json.Marshal(challenge)
	if err := c.writer.writeJSON(api.Request{
		JSONRPC: "2.0",
		Method:  pkgauth.MethodChallenge,
		Params:  payload,
	}); err != nil {
		return err
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	frame, err := readFrame(c.conn)
	_ = c.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	var req api.Request
	if err := json.Unmarshal(frame, &req); err != nil || req.Method != pkgauth.MethodAuthenticate {
		c.writeAuthError(nil, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: "expected auth.authenticate"})
		return errors.New("daemon: bad handshake")
	}
	var reqParams pkgauth.Authenticate
	if err := json.Unmarshal(req.Params, &reqParams); err != nil {
		c.writeAuthError(req.ID, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: "invalid auth params"})
		return err
	}
	if reqParams.SessionID != challenge.SessionID {
		c.writeAuthError(req.ID, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: "session mismatch"})
		return errors.New("daemon: session mismatch")
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(challenge.Nonce)
	if err != nil {
		return err
	}
	pubWire, err := auth.PublicKeyWire(reqParams.PublicKey)
	if err != nil {
		c.writeAuthError(req.ID, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: "invalid public key encoding"})
		return err
	}
	sigWire, err := auth.SignatureWire(reqParams.Signature)
	if err != nil {
		c.writeAuthError(req.ID, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: "invalid signature encoding"})
		return err
	}
	result, err := c.server.Verifier.Verify(challenge.SessionID, nonceBytes, pubWire, sigWire)
	if err != nil {
		var authErr *auth.Error
		if errors.As(err, &authErr) {
			c.writeAuthError(req.ID, authErr)
		} else {
			c.writeAuthError(req.ID, &auth.Error{Code: pkgauth.ErrBadHandshake, Message: err.Error()})
		}
		return err
	}
	payload, _ = json.Marshal(result)
	_ = c.writer.writeJSON(api.Response{JSONRPC: "2.0", ID: req.ID, Result: payload})
	return nil
}

func (c *serverConn) writeAuthError(id json.RawMessage, err *auth.Error) {
	_ = c.writer.writeJSON(api.Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &api.Error{Code: err.Code, Message: err.Message},
	})
}
