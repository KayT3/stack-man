// Package agent implements the authenticated client connection to an agent.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/stack-man/pkg/api"
	pkgauth "github.com/example/stack-man/pkg/auth"
)

const maxFrame = 16 << 20

// APIError is an error object returned by the agent.
type APIError struct {
	Code    int
	Message string
	Data    any
}

func (e *APIError) Error() string { return fmt.Sprintf("agent: %s (code %d)", e.Message, e.Code) }

// Connection is one authenticated JSON-RPC session.
type Connection struct {
	conn   net.Conn
	signer ssh.Signer

	writeMu sync.Mutex
	nextID  uint64

	pendingMu sync.Mutex
	pending   map[uint64]chan api.Response

	notifications chan api.Notification

	closeOnce sync.Once
	closed    chan struct{}
}

// Dial connects, performs the challenge-response handshake, and starts the
// response/notification reader.
func Dial(ctx context.Context, addr string, signer ssh.Signer) (*Connection, error) {
	if signer == nil {
		return nil, errors.New("agent: nil signer")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("agent: dial %s: %w", addr, err)
	}
	c := &Connection{
		conn:          conn,
		signer:        signer,
		pending:       make(map[uint64]chan api.Response),
		notifications: make(chan api.Notification, 256),
		closed:        make(chan struct{}),
	}
	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	go c.readLoop()
	return c, nil
}

func (c *Connection) handshake() error {
	_ = c.conn.SetDeadline(time.Now().Add(15 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	payload, err := readFrame(c.conn)
	if err != nil {
		return fmt.Errorf("agent: read challenge: %w", err)
	}
	var challengeReq api.Request
	if err := json.Unmarshal(payload, &challengeReq); err != nil {
		return fmt.Errorf("agent: parse challenge: %w", err)
	}
	if challengeReq.Method != pkgauth.MethodChallenge {
		return fmt.Errorf("agent: expected %s, got %q", pkgauth.MethodChallenge, challengeReq.Method)
	}
	var challenge pkgauth.Challenge
	if err := json.Unmarshal(challengeReq.Params, &challenge); err != nil {
		return fmt.Errorf("agent: parse challenge params: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(challenge.Nonce)
	if err != nil {
		return fmt.Errorf("agent: decode nonce: %w", err)
	}
	signature, err := c.signer.Sign(rand.Reader, pkgauth.SignedData(challenge.SessionID, nonce))
	if err != nil {
		return fmt.Errorf("agent: sign challenge: %w", err)
	}
	params, _ := json.Marshal(pkgauth.Authenticate{
		SessionID: challenge.SessionID,
		PublicKey: base64.StdEncoding.EncodeToString(c.signer.PublicKey().Marshal()),
		Signature: base64.StdEncoding.EncodeToString(ssh.Marshal(signature)),
	})
	req := api.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: pkgauth.MethodAuthenticate, Params: params}
	if err := c.writeRequest(req); err != nil {
		return fmt.Errorf("agent: send authenticate: %w", err)
	}
	payload, err = readFrame(c.conn)
	if err != nil {
		return fmt.Errorf("agent: read auth result: %w", err)
	}
	var resp api.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return fmt.Errorf("agent: parse auth result: %w", err)
	}
	if resp.Error != nil {
		return &APIError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	var result pkgauth.Result
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("agent: parse auth payload: %w", err)
	}
	if !result.OK {
		return errors.New("agent: authentication rejected")
	}
	return nil
}

// Call invokes method and unmarshals the result into result (which may be nil).
func (c *Connection) Call(ctx context.Context, method string, params, result any) error {
	select {
	case <-c.closed:
		return errors.New("agent: connection closed")
	default:
	}

	id := c.allocateID()
	respCh := make(chan api.Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("agent: encode params: %w", err)
		}
		rawParams = data
	}
	req := api.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.FormatUint(id, 10)),
		Method:  method,
		Params:  rawParams,
	}
	if err := c.writeRequest(req); err != nil {
		return err
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return &APIError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		}
		if result == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("agent: decode %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("agent: connection closed")
	}
}

// Notifications returns the stream of server notifications.
func (c *Connection) Notifications() <-chan api.Notification { return c.notifications }

// Done is closed when the connection is closed.
func (c *Connection) Done() <-chan struct{} { return c.closed }

// Notify forwards notifications to ch until the connection closes.
func (c *Connection) Notify(ch chan<- api.Notification) {
	go func() {
		for {
			select {
			case n := <-c.notifications:
				select {
				case ch <- n:
				case <-c.closed:
					return
				}
			case <-c.closed:
				return
			}
		}
	}()
}

// Close closes the connection.
func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.conn.Close()
	})
	return err
}

// Addr returns the remote address.
func (c *Connection) Addr() string { return c.conn.RemoteAddr().String() }

func (c *Connection) allocateID() uint64 {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *Connection) writeRequest(req api.Request) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.conn, payload)
}

func (c *Connection) readLoop() {
	for {
		payload, err := readFrame(c.conn)
		if err != nil {
			c.Close()
			return
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id,omitempty"`
			Method  string          `json:"method,omitempty"`
			Params  json.RawMessage `json:"params,omitempty"`
			Result  json.RawMessage `json:"result,omitempty"`
			Error   *api.Error      `json:"error,omitempty"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			continue
		}
		if envelope.Method != "" && (len(envelope.ID) == 0 || string(envelope.ID) == "null") {
			notification := api.Notification{
				JSONRPC: "2.0", Method: envelope.Method, Params: envelope.Params,
			}
			select {
			case c.notifications <- notification:
			case <-c.closed:
				return
			}
			continue
		}
		id, err := strconv.ParseUint(string(envelope.ID), 10, 64)
		if err != nil {
			continue
		}
		c.pendingMu.Lock()
		ch := c.pending[id]
		c.pendingMu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case ch <- api.Response{JSONRPC: "2.0", ID: envelope.ID, Result: envelope.Result, Error: envelope.Error}:
		default:
		}
	}
}

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxFrame {
		return nil, fmt.Errorf("agent: invalid frame length %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("agent: frame too large (%d bytes)", len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
