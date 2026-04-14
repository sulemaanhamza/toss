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

**Download a prebuilt binary** from [Releases](https://github.com/sulemaanhamza/toss/releases).

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

---

## Quick start

**1. Start the server** on any one machine:

```bash
$ toss serve
toss running on :9090

  http://192.168.1.50:9090

auto-discovery enabled
or set manually: export TOSS_HOST=192.168.1.50:9090
```

**2. Send and receive** from any other machine on the same network:

```bash
$ toss "deploy is done, check staging"
sent

$ toss get
deploy is done, check staging
```

That's it. The client finds the server automatically — no IP to remember, no env vars to set.

---

## Commands

### `toss serve`

Starts the server. Run this on one machine — any machine. It listens for HTTP requests on port 9090 and broadcasts its presence via UDP so clients can find it automatically.

```bash
toss serve                        # default port 9090
TOSS_PORT=4444 toss serve         # custom port
```

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
```

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
sudo apt install xclip       # Debian/Ubuntu/Pop!_OS
sudo pacman -S xclip         # Arch
sudo dnf install xclip       # Fedora
```

---

## Environment variables

| Variable    | Used by  | Description                    | Default         |
|-------------|----------|--------------------------------|-----------------|
| `TOSS_HOST` | client   | Server address to connect to   | auto-discovered |
| `TOSS_PORT` | server   | Port to listen on              | `9090`          |

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

- No authentication
- No encryption
- No access control

Do not run it on untrusted or public networks.

---

## License

[MIT](LICENSE)
