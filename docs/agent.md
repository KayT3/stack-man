# The agent (`stackman`)

One Go binary per VPS. Two faces: a **CLI** and a **daemon**.

```
stackman daemon                      # run the RPC API (foreground; systemd)
stackman <command> [flags] [args]    # one-shot CLI
```

## 1. CLI surface

Built with `github.com/spf13/cobra`. Every command is a thin shell over the same
service layer the daemon uses.

### 1.1 System / config

```
stackman config             # interactive wizard (see §2)
stackman install            # write systemd unit, copy self to /usr/local/bin, enable+start
stackman uninstall          # disable+stop, remove unit (keeps data dir by default)
stackman start|stop|restart|enable|disable|status
stackman version
stackman daemon             # run server in foreground
```

### 1.2 Stacks

```
stackman stack add <dir> [--name N] [--remote URL] [--branch B]
                          [--allow svc,svc] [--exclude svc,svc]
stackman stack remove <name>
stackman stack list
stackman stack show <name>
stackman stack services <name>      # enumerate compose services + current policy
```

### 1.3 Git

```
stackman git status <name>          # working tree status (porcelain)
stackman git check  <name>          # fetch + report what would change (safe? see stacks.md)
stackman git fetch  <name>
stackman git pull   <name>          # refuses on conflict/divergence
```

### 1.4 Containers

```
stackman containers up    <name> [--build] [--force-recreate] [--remove-orphans]
stackman containers down  <name> [--volumes]
stackman containers restart <name> [service...]
stackman containers build <name> [service...]
stackman containers ps    <name>
stackman containers logs  <name> [service] [-f]
stackman containers exec  <name> <service> -- <cmd...>
stackman containers network ls [--external]
stackman containers network connect    <name> <network> [service...]
stackman containers network disconnect <name> <network> [service...]
```

### 1.5 Repo commands

```
stackman command list <name>
stackman command run  <name> <command> [--param k=v]...
```

## 2. `config` — the interactive wizard

`stackman config` walks through:

1. **Subnet selection.** It enumerates interfaces (`net.Interfaces` + addresses),
   filters to private/loopback ranges, and lists each with its name, IP, and
   CIDR — including the WARP interface (`CloudflareWARP`, `wgcf`, `zt*`, etc.).
   You pick the one whose IP is your private subnet. It derives the **bind IP**
   and **subnet CIDR** from this.
2. **Discovery port** (default `42701`).
3. **API port** (default `42702`). Note this is a default only; it's advertised.
4. **`authorized_keys` path** (default `/root/.ssh/authorized_keys`).
5. **Data dir** (default `/var/lib/stackman`).
6. Writes `/etc/stackman/config.toml`.

Recommendation for the wizard implementation: `github.com/AlecAivazis/survey/v2`
(simple prompt loop) or `charmbracelet/bubbletea` for a fancier TUI. Start with
survey; the selection UI can be upgraded later without touching the config schema.

## 3. Config file

`/etc/stackman/config.toml`:

```toml
# The private subnet the agent is part of. Used for bind + source filtering.
subnet = "100.96.0.0/12"

# Which local address to bind. Empty = auto-detect the interface in `subnet`.
bind_ip = ""          # e.g. "100.96.1.2"

# Discovery beacon (UDP). Well-known so clients know where to broadcast.
discovery_port = 42701

# RPC API (TCP). This is a default; the actual port is advertised by discovery.
api_port = 42702

# Trust store for SSH challenge-response auth.
authorized_keys_path = "/root/.ssh/authorized_keys"

# State.
data_dir = "/var/lib/stackman"   # stackman.db lives here
```

## 4. `install` and service management

`stackman install`:

1. Copies the current binary to `/usr/local/bin/stackman` (if not already there).
2. Writes `/etc/systemd/system/stackman.service`.
3. `systemctl daemon-reload && systemctl enable --now stackman`.

Generated unit:

```ini
[Unit]
Description=stackman agent
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/stackman daemon
Restart=on-failure
RestartSec=5
# Run as root (needs docker socket + /root/.ssh/authorized_keys).
# A dedicated user in the `docker` group is the stricter alternative.
User=root

[Install]
WantedBy=multi-user.target
```

`start`/`stop`/`enable`/`disable`/`restart`/`status` shell out to `systemctl`.
They exist so the same operations are scriptable and available over the API.

## 5. Daemon lifecycle

`stackman daemon`:

1. Loads config; errors clearly if `config` hasn't been run yet.
2. Resolves the bind IP from `bind_ip` or the interface matching `subnet`.
3. Opens the TCP listener **bound to that IP only** and the UDP discovery socket.
4. Registers all RPC handlers and serves (see `api.md`).

On SIGTERM (systemd stop): graceful drain of connections, then exit.

## 6. Dependencies

| Need | Library |
|------|---------|
| CLI | `github.com/spf13/cobra` |
| Interactive prompts | `github.com/AlecAivazis/survey/v2` |
| SQLite | `modernc.org/sqlite` (pure Go, no CGO → static binary) |
| SSH auth | `golang.org/x/crypto/ssh` (+ `/ssh/agent`) |
| Compose parsing | `github.com/compose-spec/compose-go/v2` |
| Config file | `github.com/BurntSushi/toml` |

## 7. Cross-cutting notes

- **Idempotency.** `install` and `stack add` are safe to re-run.
- **Same behavior everywhere.** The CLI and the RPC API call the *same* service
  functions, so anything you can do in a terminal you can do from the desktop app.
- **No shell injection.** Compose service names, command args, and remote URLs are
  passed as argv (not concatenated into a shell string). The `shell` step type in
  `.stackman.yaml` is the only place raw strings run, and it's explicitly marked
  as an escape hatch.
