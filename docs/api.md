# API — RPC protocol & auth

## 1. Transport

- **TCP**, bound to the subnet IP only, on the advertised API port (default `42702`).
- **Framing:** length-prefixed JSON. Each frame is:

```
┌─────────────────┬──────────────────────────┐
│ uint32 big-endian│  JSON payload (UTF-8)     │
│  (byte length)   │                           │
└─────────────────┴──────────────────────────┘
```

- **Protocol:** JSON-RPC 2.0. Requests carry an `id`; responses echo it.
  Server→client messages with **no** `id` (or `id: null`) are **notifications**
  used for streaming logs/progress.

## 2. Handshake & authentication (SSH challenge-response)

Every connection begins with an auth exchange before any RPC method is accepted.

### 2.1 Server → client: challenge

Sent immediately on connect.

```json
{
  "jsonrpc": "2.0",
  "method": "auth.challenge",
  "params": {
    "version": 1,
    "session_id": "1f3a9c…",       // hex, random, this connection only
    "nonce": "c2VydmVyLXJhbmRvbQ==" // base64, random 32 bytes
  }
}
```

### 2.2 Client → server: authenticate

The client signs a canonical message derived from the challenge and replies.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "auth.authenticate",
  "params": {
    "session_id": "1f3a9c…",
    "public_key": "AAAAC3NzaC1lZDI1NTE5AAAA…",  // SSH wire format, base64
    "signature":  "AAAAIF…"                       // SSH signature, base64
  }
}
```

**What gets signed** (must match byte-for-byte on both sides):

```
data = SHA256( "stackman-auth-v1" || 0x00 || session_id || nonce )
```

where `nonce` is the *decoded* bytes, not the base64 string.

Client signing (Go):

```go
signer, _ := ssh.ParsePrivateKey(keyBytes)          // or ssh/agent signer
sig, err := signer.Sign(rand.Reader, data)          // *ssh.Signature
sigWire := ssh.Marshal(sig)                         // for the wire
```

Server verification (Go):

```go
pub, _, _, _, err := ssh.ParseAuthorizedKey(line)   // one line of authorized_keys
if !bytes.Equal(ssh.Marshal(pub), pubWire) { /* membership check */ }
err = pub.Verify(data, &sigStruct)                  // *ssh.Signature
```

### 2.3 Result

```json
{ "jsonrpc": "2.0", "id": 1, "result": { "identity": "ssh-ed25519 AAAAC3…", "ok": true } }
```

On failure the server sends an `error` and **closes the connection**:

```json
{ "jsonrpc": "2.0", "id": 1,
  "error": { "code": -32001, "message": "unauthorized key" } }
```

Error codes:

| code | meaning |
|------|---------|
| `-32001` | key not in `authorized_keys` |
| `-32002` | signature verification failed |
| `-32003` | bad/missing handshake |

### 2.4 Key selection on the client

The client signs with its configured key (default `~/.ssh/id_ed25519`). If the
agent rejects it, the client may retry with other keys it holds. The common case
is a single key that's already in every VPS's `authorized_keys`, so one attempt
suffices. Multi-key auto-selection is a documented future enhancement.

### 2.5 ssh-agent

If `SSH_AUTH_SOCK` is set, the client can load a signer from the agent instead of
reading the private key file. Then the key material never enters the desktop app.

## 3. Method catalog

Names are namespaced. All take `params` as an object. Errors are JSON-RPC errors.

### System

| Method | Params | Result |
|--------|--------|--------|
| `agent.ping` | — | `{ "pong": true }` |
| `agent.info` | — | hostname, version, subnet, uptime, stack count |
| `agent.version` | — | `{ "version": "0.1.0", "api_version": 1 }` |

### Stacks

| Method | Params | Result |
|--------|--------|--------|
| `stack.list` | — | `[StackSummary]` |
| `stack.get` | `{ name }` | `Stack` (full detail) |
| `stack.add` | `{ path, name?, remote?, branch?, allow[], exclude[] }` | `Stack` |
| `stack.remove` | `{ name }` | `{ removed: true }` |
| `stack.services` | `{ name }` | `[{service, policy}]` |
| `stack.set_policy` | `{ name, service, policy }` | `Stack` |

### Git

| Method | Params | Result |
|--------|--------|--------|
| `stack.git.status` | `{ name }` | porcelain lines |
| `stack.git.fetch` | `{ name }` | `{ fetched: true }` |
| `stack.git.check` | `{ name }` | `GitCheck` (see below) |
| `stack.git.pull` | `{ name }` | `GitPullResult` (refuses if unsafe) |

`GitCheck`:

```json
{
  "safe_to_pull": true,
  "clean": true,
  "ahead": 0, "behind": 2,
  "diverged": false,
  "changed_files": ["next/src/app/page.tsx", "docker-compose.yml"],
  "branch": "main", "head": "abc123", "remote_head": "def456"
}
```

`GitPullResult`: `{ "pulled": true, "from": "abc123", "to": "def456" }` or an
error `{ "code": -32010, "message": "conflict; pull aborted" }`.

### Containers

| Method | Params | Result |
|--------|--------|--------|
| `stack.containers.up` | `{ name, build?, force_recreate?, remove_orphans?, services[] }` | `{ ok }` |
| `stack.containers.down` | `{ name, volumes?, services[] }` | `{ ok }` |
| `stack.containers.restart` | `{ name, services[] }` | `{ ok }` |
| `stack.containers.build` | `{ name, services[] }` | `{ ok }` |
| `stack.containers.ps` | `{ name }` | `[ContainerStatus]` |
| `stack.containers.exec` | `{ name, service, cmd[] }` | `{ exit_code, stdout, stderr }` |
| `stack.containers.logs` | `{ name, service?, follow }` | streamed notifications |
| `stack.containers.networks` | `{ name }` | `[Network]` (stack networks) |
| `stack.containers.network_connect` | `{ name, network, services[] }` | `{ ok }` |
| `stack.containers.network_disconnect` | `{ name, network, services[] }` | `{ ok }` |

### Repo commands

| Method | Params | Result |
|--------|--------|--------|
| `stack.command.list` | `{ name }` | `[CommandInfo]` |
| `stack.command.run` | `{ name, command, params? }` | streamed notifications + final `{ ok, exit_code }` |

## 4. Streaming (notifications)

Long-running methods stream progress to the client as notifications:

```json
{ "jsonrpc": "2.0", "method": "stream.log",
  "params": { "stack": "hotel", "command": "update", "stream": "stdout", "data": "…" } }
```

```json
{ "jsonrpc": "2.0", "method": "stream.step",
  "params": { "stack": "hotel", "command": "update", "step": 2, "total": 3,
              "label": "docker compose exec next npx prisma migrate deploy" } }
```

Cancellation: the client closes the TCP connection (or sends a `cancel` request);
the daemon's request context is cancelled and the running command is stopped.

## 5. Versioning

- `api_version` starts at `1` and is negotiated via `agent.info`.
- Additive changes (new methods, new optional fields) are backwards-compatible.
- Breaking changes bump `api_version`; the client refuses to talk to a newer
  major than it understands.

## 6. Example session

```
C -> connect
S <- auth.challenge { session_id, nonce }
C -> auth.authenticate { session_id, public_key, signature }
S <- result { ok: true }
C -> { id:2, method:"stack.list" }
S <- { id:2, result: [ { name:"hotel", path:"/srv/hotel", … } ] }
C -> { id:3, method:"stack.git.check", params:{ name:"hotel" } }
S <- { id:3, result: { safe_to_pull:true, behind:2, changed_files:[…] } }
```
