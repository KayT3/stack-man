# Discovery

## 1. Goal

The desktop client finds agents on the private subnet without any configuration,
and learns each agent's **API port** so that port does not need to be hard-coded
across servers.

## 2. Protocol (UDP beacon)

- **Well-known UDP port:** `42701` (configurable). This is the *only* fixed port.
- **Request** — client broadcasts:

```json
{ "magic": "stackman", "type": "discover", "version": 1, "request_id": "…" }
```

- **Response** — each agent unicasts back to the client's source address:

```json
{
  "magic": "stackman", "type": "announce", "version": 1,
  "request_id": "…",
  "agent_id": "…",                 // stable random id
  "hostname": "vps-1",
  "api_addr": "100.96.1.2:42702",  // <-- actual API port, may differ per agent
  "agent_version": "0.1.0",
  "api_version": 1,
  "subnet": "100.96.0.0/12",
  "stacks": ["hotel", "blog"]
}
```

`magic` lets the client ignore unrelated UDP traffic. `request_id` lets the
client correlate responses to a specific probe.

## 3. Client behavior

1. Send the `discover` datagram to:
   - the subnet's **directed broadcast address** (`subnet IP | ~netmask`), and
   - `255.255.255.255` (limited broadcast) on the WARP interface.
2. Collect `announce`s for a short window (e.g. 2s).
3. Merge with the **static peer list** from client config (see §5) and any
   previously-known agents.
4. Present agents to the UI; cache with a TTL plus a manual "rescan" button.

## 4. ⚠️ Magic WAN caveat

Cloudflare WARP / Magic WAN is an **L3 unicast overlay**. It typically does **not**
forward:

- IP **broadcast** (neither `255.255.255.255` nor subnet-directed broadcast),
- IP **multicast** (so mDNS/DNS-SD won't work either).

**Practical consequence:** the UDP beacon may find nothing on Magic WAN. That's
why discovery is layered:

1. Try the beacon (works if you ever run a flat L2 private net, e.g. a provider
   VPC, or if CF adds broadcast passthrough).
2. **Fall back to the static peer list** (below).
3. Manual "add agent by IP" is always available.

Do **not** build the product assuming broadcast works on Magic WAN — treat it as
a best-effort fast path, with the static list as the reliable default.

## 5. Static peer list (reliable fallback)

Client config (`~/.config/stackman/client.toml`):

```toml
# Agents to contact directly when discovery can't reach them.
[[agent]]
  id   = "vps-1"
  addr = "100.96.1.2:42702"

[[agent]]
  id   = "vps-2"
  addr = "100.96.1.3:42702"
```

Workflow that keeps this pleasant:

1. On first run, the client shows "add agent by IP".
2. Once an agent is reached (by any means), the client stores its
   `id → addr` mapping.
3. Because agent IPs on Magic WAN are stable (they're assigned per-device),
   this list rarely needs touching.

### Alternative future option: rendezvous

If the fleet grows, one designated agent (or a tiny Cloudflare Worker that can
list Magic WAN members) could act as a rendezvous point the client queries for
the current member list. Not needed now — documented as the growth path.

## 6. Hard-coded port question, answered

> "if auto discovery possible, hard-coded port is not needed, right?"

Partially. The **API port** can vary per agent because discovery advertises it.
But you still need **one well-known UDP port** for the beacon (otherwise the
client wouldn't know where to broadcast). So: one fixed *discovery* port, dynamic
*API* ports.
