package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/stack-man/pkg/api"
	pkgauth "github.com/example/stack-man/pkg/auth"
)

func testSigner(t *testing.T) ssh.Signer {
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

// fakeAgent runs a minimal handshake + echo server.
func fakeAgent(t *testing.T, authorized bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		challenge := pkgauth.Challenge{Version: 1, SessionID: "session", Nonce: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))}
		params, _ := json.Marshal(challenge)
		writeJSON(t, conn, api.Request{JSONRPC: "2.0", Method: pkgauth.MethodChallenge, Params: params})

		frame, err := readFrame(conn)
		if err != nil {
			return
		}
		var authReq api.Request
		json.Unmarshal(frame, &authReq)
		var authenticate pkgauth.Authenticate
		json.Unmarshal(authReq.Params, &authenticate)
		if !authorized {
			writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: authReq.ID, Error: &api.Error{Code: pkgauth.ErrUnauthorizedKey, Message: "unauthorized key"}})
			return
		}
		pubWire, _ := base64.StdEncoding.DecodeString(authenticate.PublicKey)
		pub, err := ssh.ParsePublicKey(pubWire)
		if err != nil {
			return
		}
		sigWire, _ := base64.StdEncoding.DecodeString(authenticate.Signature)
		var sig ssh.Signature
		if err := ssh.Unmarshal(sigWire, &sig); err != nil {
			return
		}
		nonce, _ := base64.StdEncoding.DecodeString(challenge.Nonce)
		if err := pub.Verify(pkgauth.SignedData(challenge.SessionID, nonce), &sig); err != nil {
			writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: authReq.ID, Error: &api.Error{Code: pkgauth.ErrBadSignature, Message: "bad signature"}})
			return
		}
		result, _ := json.Marshal(pkgauth.Result{Identity: "test", OK: true})
		writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: authReq.ID, Result: result})

		for {
			frame, err := readFrame(conn)
			if err != nil {
				return
			}
			var req api.Request
			if err := json.Unmarshal(frame, &req); err != nil {
				continue
			}
			switch req.Method {
			case "test.notify":
				notifParams, _ := json.Marshal(api.StreamLog{Stack: "s", Data: "hello"})
				writeJSON(t, conn, api.Notification{JSONRPC: "2.0", Method: api.NotifyStreamLog, Params: notifParams})
				writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"ok":true}`)})
			case "test.error":
				writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: req.ID, Error: &api.Error{Code: api.ErrConflict, Message: "conflict"}})
			default:
				result, _ := json.Marshal(map[string]string{"method": req.Method})
				writeJSON(t, conn, api.Response{JSONRPC: "2.0", ID: req.ID, Result: result})
			}
		}
	}()
	return ln.Addr().String()
}

func writeJSON(t *testing.T, conn net.Conn, v any) {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = writeFrame(conn, payload)
}

func TestDialAndCall(t *testing.T) {
	addr := fakeAgent(t, true)
	c, err := Dial(context.Background(), addr, testSigner(t))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	var result map[string]string
	if err := c.Call(context.Background(), api.MethodPing, nil, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result["method"] != api.MethodPing {
		t.Fatalf("result = %+v", result)
	}
}

func TestNotifications(t *testing.T) {
	addr := fakeAgent(t, true)
	c, err := Dial(context.Background(), addr, testSigner(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var result map[string]bool
	if err := c.Call(context.Background(), "test.notify", nil, &result); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-c.Notifications():
		if n.Method != api.NotifyStreamLog {
			t.Fatalf("notification = %+v", n)
		}
		var log api.StreamLog
		if err := json.Unmarshal(n.Params, &log); err != nil || log.Data != "hello" {
			t.Fatalf("payload = %s (%v)", n.Params, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification")
	}
}

func TestCallError(t *testing.T) {
	addr := fakeAgent(t, true)
	c, err := Dial(context.Background(), addr, testSigner(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.Call(context.Background(), "test.error", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != api.ErrConflict {
		t.Fatalf("error = %v", err)
	}
}

func TestDialRejected(t *testing.T) {
	addr := fakeAgent(t, false)
	_, err := Dial(context.Background(), addr, testSigner(t))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != pkgauth.ErrUnauthorizedKey {
		t.Fatalf("error = %v", err)
	}
}
