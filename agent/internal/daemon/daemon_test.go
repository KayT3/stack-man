package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/service"
	"github.com/example/stack-man/pkg/api"
	pkgauth "github.com/example/stack-man/pkg/auth"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"jsonrpc":"2.0","method":"agent.ping"}`)
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %s", got)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	header := []byte{0x7f, 0xff, 0xff, 0xff}
	if _, err := readFrame(bytes.NewReader(header)); err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestLoadOrCreateAgentID(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateAgentID(dir)
	if err != nil || id == "" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	again, err := LoadOrCreateAgentID(dir)
	if err != nil || again != id {
		t.Fatalf("id changed: %q -> %q", id, again)
	}
}

func freePort(t *testing.T, network string) int {
	t.Helper()
	if network == "tcp" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

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

type harness struct {
	cfg    *config.Config
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func startHarness(t *testing.T, signer ssh.Signer) *harness {
	t.Helper()
	dir := t.TempDir()
	keysPath := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(keysPath, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Subnet = "127.0.0.0/8"
	cfg.BindIP = "127.0.0.1"
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.AuthorizedKeysPath = keysPath
	cfg.APIPort = freePort(t, "tcp")
	cfg.DiscoveryPort = freePort(t, "udp")

	svc, err := service.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })

	srv, err := New(svc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.APIPort))
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	h := &harness{cfg: &cfg, addr: addr, cancel: cancel, done: done}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return h
}

func handshake(t *testing.T, conn net.Conn, signer ssh.Signer) {
	t.Helper()
	frame, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	var req api.Request
	if err := json.Unmarshal(frame, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != pkgauth.MethodChallenge {
		t.Fatalf("method = %q", req.Method)
	}
	var challenge pkgauth.Challenge
	if err := json.Unmarshal(req.Params, &challenge); err != nil {
		t.Fatal(err)
	}
	nonce, err := decodeB64(challenge.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.Sign(rand.Reader, pkgauth.SignedData(challenge.SessionID, nonce))
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(pkgauth.Authenticate{
		SessionID: challenge.SessionID,
		PublicKey: encodeB64(signer.PublicKey().Marshal()),
		Signature: encodeB64(ssh.Marshal(sig)),
	})
	writeRequest(t, conn, 1, pkgauth.MethodAuthenticate, params)
	resp := readResponse(t, conn, 1)
	if resp.Error != nil {
		t.Fatalf("handshake error: %+v", resp.Error)
	}
	var result pkgauth.Result
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("auth result = %+v", result)
	}
}

func decodeB64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func encodeB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func writeRequest(t *testing.T, conn net.Conn, id int, method string, params []byte) {
	t.Helper()
	raw, _ := json.Marshal(id)
	payload, _ := json.Marshal(api.Request{JSONRPC: "2.0", ID: raw, Method: method, Params: params})
	if err := writeFrame(conn, payload); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, conn net.Conn, id int) api.Response {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	for {
		frame, err := readFrame(conn)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		var resp api.Response
		if err := json.Unmarshal(frame, &resp); err != nil {
			continue
		}
		if string(resp.ID) == strconv.Itoa(id) {
			return resp
		}
	}
}

func call(t *testing.T, conn net.Conn, id int, method string, params any, result any) {
	t.Helper()
	var raw []byte
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	writeRequest(t, conn, id, method, raw)
	resp := readResponse(t, conn, id)
	if resp.Error != nil {
		t.Fatalf("%s error: %+v", method, resp.Error)
	}
	if result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			t.Fatalf("%s result: %v (%s)", method, err, resp.Result)
		}
	}
}

func TestDaemonHandshakeAndCalls(t *testing.T) {
	signer := testSigner(t)
	h := startHarness(t, signer)

	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshake(t, conn, signer)

	var pong map[string]bool
	call(t, conn, 2, api.MethodPing, nil, &pong)
	if !pong["pong"] {
		t.Fatalf("pong = %+v", pong)
	}

	var info api.AgentInfo
	call(t, conn, 3, api.MethodInfo, nil, &info)
	if info.APIVersion != api.APIVersion || info.AgentID == "" || info.Subnet != "127.0.0.0/8" {
		t.Fatalf("info = %+v", info)
	}

	var stacks []api.StackSummary
	call(t, conn, 4, api.MethodStackList, nil, &stacks)
	if len(stacks) != 0 {
		t.Fatalf("stacks = %+v", stacks)
	}

	var ver map[string]any
	call(t, conn, 5, api.MethodVersion, nil, &ver)
	if ver["version"] == "" {
		t.Fatalf("version = %+v", ver)
	}
}

func TestDaemonRejectsUnauthorizedKey(t *testing.T) {
	trusted := testSigner(t)
	h := startHarness(t, trusted)

	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frame, err := readFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var req api.Request
	json.Unmarshal(frame, &req)
	var challenge pkgauth.Challenge
	json.Unmarshal(req.Params, &challenge)
	nonce, _ := decodeB64(challenge.Nonce)
	other := testSigner(t)
	sig, _ := other.Sign(rand.Reader, pkgauth.SignedData(challenge.SessionID, nonce))
	params, _ := json.Marshal(pkgauth.Authenticate{
		SessionID: challenge.SessionID,
		PublicKey: encodeB64(other.PublicKey().Marshal()),
		Signature: encodeB64(ssh.Marshal(sig)),
	})
	writeRequest(t, conn, 1, pkgauth.MethodAuthenticate, params)
	resp := readResponse(t, conn, 1)
	if resp.Error == nil || resp.Error.Code != pkgauth.ErrUnauthorizedKey {
		t.Fatalf("error = %+v", resp.Error)
	}
}

func TestDaemonStackAddOverRPC(t *testing.T) {
	signer := testSigner(t)
	h := startHarness(t, signer)

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("services:\n  web: {image: nginx}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "add", "."},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}

	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshake(t, conn, signer)

	var st api.Stack
	call(t, conn, 2, api.MethodStackAdd, map[string]any{
		"path": repo, "name": "demo", "allow": []string{"web"},
	}, &st)
	if st.Name != "demo" || st.ComposeFile != "docker-compose.yml" || len(st.Allow) != 1 {
		t.Fatalf("stack = %+v", st)
	}

	var list []api.StackSummary
	call(t, conn, 3, api.MethodStackList, nil, &list)
	if len(list) != 1 || list[0].Name != "demo" {
		t.Fatalf("list = %+v", list)
	}

	var services []api.ServicePolicy
	call(t, conn, 4, api.MethodStackServices, map[string]string{"name": "demo"}, &services)
	if len(services) != 1 || services[0].Service != "web" || services[0].Policy != "allow" {
		t.Fatalf("services = %+v", services)
	}
}
