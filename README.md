# toss

Share text and files between your devices over the local network.  
No accounts. No pairing. No config. Just toss it.

```
Device A                          Device B
┌────────────────────┐            ┌────────────────────┐
│                    │            │                    │
│  toss "hey there"  │───────────│  toss get          │
│  toss ./photo.png  │  your     │  toss watch        │
│  toss paste        │  local    │  toss copy         │
│                    │  network  │                    │
└────────────────────┘            └────────────────────┘
        any device can send or receive
```

One binary, ~9 MB, zero dependencies. CLI only.  
Works on **macOS**, **Linux**, and **Windows**.

---

## Install

**One command** (macOS / Linux):

```bash
curl -sSL https://raw.githubusercontent.com/sulemaanhamza/toss/main/install.sh | sh
```

Or install from source:

```bash
go install github.com/sulemaanhamza/toss@latest
```

Or build locally:

```bash
git clone https://github.com/sulemaanhamza/toss.git
cd toss
make build       # builds for your current platform
make all         # builds for all platforms → dist/
```

## Uninstall

```bash
toss uninstall
```

Removes the binary, config, and service. That's it.

---

## Quick start

Just send something — the server starts automatically if needed:

```bash
$ toss "deploy is done, check staging"
no server found — starting one in background
sent

$ toss get
deploy is done, check staging
```

That's it. No server to start manually, no IP to remember, no env vars to set.

For a permanent setup, install the server as a system service so it runs on login:

```bash
toss serve --install
```

---

## Commands

### `toss serve`

Starts the server. You usually don't need to run this manually — the server auto-starts in the background when you send or receive something.

```bash
toss serve                        # run in foreground
toss serve --install              # install as system service (starts on login)
toss serve --uninstall            # remove the service
TOSS_PORT=4444 toss serve         # custom port
```

On macOS, `--install` creates a launchd agent. On Linux, it creates a systemd user service.

### `toss <text>`

Sends text to the server. If the argument isn't a file path, it's treated as text.

```bash
toss "remember to update the DNS"
toss meeting at 3pm
```

### `toss <file>`

Sends a file. If the argument is a path to an existing file, it's uploaded.

```bash
toss ./screenshot.png
toss ~/Documents/report.pdf
```

### `echo ... | toss`

Reads from stdin and sends it as text. Useful for piping output from other commands.

```bash
echo "hello from the other side" | toss
cat ~/.ssh/id_rsa.pub | toss
git diff | toss
```

### `toss get`

Retrieves the latest item. Text is printed to stdout. Files are saved to the current directory.

```bash
$ toss get
remember to update the DNS

$ toss get
saved: screenshot.png (248109 bytes)
```

### `toss paste`

Reads your system clipboard and sends its contents as text. Select text anywhere, copy it, then run `toss paste`.

```bash
# copy something to your clipboard, then:
toss paste
```

### `toss copy`

Fetches the latest text from the server and writes it to your system clipboard. The reverse of `toss paste`.

```bash
$ toss copy
copied: remember to update the DNS
```

### `toss watch`

Streams new items in real time. Stays running and prints each new item as it arrives. Only shows items that arrive *after* you start watching.

```bash
$ toss watch
watching http://192.168.1.50:9090
deploy is done                        ← text goes to stdout
file: report.pdf (2.4 MB)            ← file notifications go to stderr
just pushed the fix
```

Pipe-friendly — text goes to stdout, file notifications to stderr:

```bash
toss watch >> received.txt            # log all incoming text
toss watch 2>/dev/null                # text only, suppress file notices
toss watch --notify                   # also show desktop notifications
```

Add `--notify` to get native OS notifications (macOS Notification Center / `notify-send` on Linux) alongside terminal output.

### `toss chat`

Interactive two-way chat. Send and receive in one terminal.

```bash
$ toss chat
connected to http://192.168.1.50:9090
type a message and press enter. ctrl+c to exit.

> hey, deploy is done
> ← nice, checking now
> ← file: report.pdf (2.4 MB)
```

Add `--notify` for desktop notifications on incoming messages:

```bash
toss chat --notify
```

### `toss update`

Updates toss to the latest version. Downloads the right binary for your platform and replaces the current one.

```bash
$ toss update
current: v0.3.0
found v0.4.0
downloading toss_0.4.0_darwin_arm64.tar.gz
updated to v0.4.0
```

### `toss uninstall`

Removes toss from your system — binary, config, and service.

```bash
$ toss uninstall
this will remove:
  binary:  /usr/local/bin/toss
  config:  /Users/you/.config/toss
  service: ~/Library/LaunchAgents/com.toss.server.plist

uninstall? [y/N] y
toss uninstalled
```

### `toss config`

Sets a shared key for authentication. Run this on every device that needs access.

```bash
$ toss config
current: no key
enter shared key (leave empty to remove): ********
saved to ~/.config/toss/config.json
use the same key on all your devices.
```

Once set, the key is stored locally and sent automatically with every request. The server rejects any request with a missing or wrong key.

To remove auth:

```bash
$ toss config
current: key is set
enter shared key (leave empty to remove):
key removed — auth disabled
```

The `TOSS_KEY` environment variable can also be used and takes priority over the config file.

---

## Authentication

```
Device A                             Device B
┌──────────────┐                     ┌──────────────┐
│ toss config  │                     │ toss config  │
│ key: s3cret  │                     │ key: s3cret  │
│              │                     │              │
│ config.json  │                     │ config.json  │
└──────┬───────┘                     └──────┬───────┘
       │                                    │
       │   Authorization: Bearer s3cret     │
       ├───────────────────────────────────→ │
       │                          200 OK ←──┤
       │                                    │
       │   Authorization: Bearer wrong      │
       ├ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ → │
       │                     401 Denied ←──┤
```

- **Optional** — if no key is set, toss works without auth (open access)
- **Shared key** — same key on all devices, stored in `~/.config/toss/config.json`
- **Constant-time comparison** — prevents timing attacks
- **`TOSS_KEY` env var** — overrides the config file, useful for scripts
- **No encryption** — the key protects access, not the data in transit. For a trusted LAN this is fine. Do not use on public networks.

Config file location:
| OS      | Path                                        |
|---------|---------------------------------------------|
| macOS   | `~/Library/Application Support/toss/config.json` |
| Linux   | `~/.config/toss/config.json`                |
| Windows | `%AppData%\toss\config.json`                |

---

## How auto-discovery works

```
CLIENT                              SERVER
──────                              ──────

1. UDP broadcast ──────────────────→ Listening on
   "TOSS?"                          UDP :9090
   to 255.255.255.255:9090

2.                 ←────────────────  UDP reply
                                     "TOSS:9090"

3. HTTP request ──────────────────→  HTTP server
   POST /api/text                    on TCP :9090
   "hello world"
```

When you run any client command (`toss "hi"`, `toss get`, etc.), the client sends a UDP broadcast on port 9090. The server responds with its HTTP port. The client then talks to the server over HTTP. This all happens in under a second, transparently.

If discovery doesn't work (different subnets, firewalls), set the server address manually:

```bash
export TOSS_HOST=192.168.1.50:9090
```

---

## How storage works

```
toss serve
    │
    ├── HTTP server (TCP :9090)
    │     POST /api/text  → text stored in memory
    │     POST /api/file  → file saved to temp dir
    │     GET  /api/items → list all items (JSON)
    │     GET  /api/latest → get most recent item
    │
    └── UDP discovery (UDP :9090)
          responds to "TOSS?" broadcasts

Storage:
    ├── text items → in-memory ([]item slice)
    └── file items → /tmp/toss-*/
                     cleaned up on Ctrl+C
```

- Last **64 items** are kept. Oldest items are evicted automatically.
- Text up to **1 MB** per item. Files up to **100 MB** per upload.
- Everything is ephemeral — nothing persists across restarts.

---

## Clipboard support

Clipboard commands use native OS tools:

| OS      | Read             | Write    |
|---------|------------------|----------|
| macOS   | `pbpaste`        | `pbcopy` |
| Linux   | `xclip` / `xsel` | `xclip` / `xsel` |
| Windows | `PowerShell`     | `clip`   |

Linux users need `xclip` or `xsel` installed:

```bash
sudo apt install xclip       # Debian/Ubuntu
sudo pacman -S xclip         # Arch
sudo dnf install xclip       # Fedora
```

---

## Environment variables

| Variable    | Used by  | Description                    | Default         |
|-------------|----------|--------------------------------|-----------------|
| `TOSS_HOST` | client   | Server address to connect to   | auto-discovered |
| `TOSS_PORT` | server   | Port to listen on              | `9090`          |
| `TOSS_KEY`  | both     | Shared auth key (overrides config) | none        |

---

## Build for all platforms

```bash
make all
```

```
dist/
├── toss-darwin-arm64         macOS (Apple Silicon)
├── toss-darwin-amd64         macOS (Intel)
├── toss-linux-amd64          Linux (x86_64)
├── toss-linux-arm64          Linux (ARM64)
├── toss-windows-amd64.exe    Windows (x86_64)
└── toss-windows-arm64.exe    Windows (ARM64)
```

Releases are automated — push a tag and GitHub Actions builds all binaries via [GoReleaser](https://goreleaser.com):

```bash
git tag v0.3.0
git push origin v0.3.0
# binaries appear at github.com/sulemaanhamza/toss/releases
```

---

## Security

Toss is designed for **trusted local networks only**.

- **Authentication** is available via shared key (`toss config`) but optional
- **No encryption** — traffic is plaintext HTTP
- Use a shared key on any network where others might be present
- Do not expose toss to the public internet

---

## License

[MIT](LICENSE)
