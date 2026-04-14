# toss

Share text and files across devices on your local network. No setup, no accounts, no pairing.

One binary. CLI only. macOS, Linux, Windows.

## Install

### Download

Grab the latest binary from [Releases](https://github.com/sulemaanhamza/toss/releases).

### From source

```bash
go install github.com/sulemaanhamza/toss@latest
```

### Build locally

```bash
git clone https://github.com/sulemaanhamza/toss.git
cd toss
make build
```

## Usage

Start the server on any machine:

```bash
toss serve
```

Other devices find the server automatically via LAN discovery — no config needed:

```bash
toss "meeting notes: check the API docs"     # send text
toss ./report.pdf                             # send a file
cat config.yaml | toss                        # pipe text
toss get                                      # get latest item
toss paste                                    # send clipboard
toss copy                                     # copy latest to clipboard
toss watch                                    # live stream incoming items
```

### Clipboard

```bash
# Copy some text on your macbook, then:
toss paste            # sends clipboard contents to server

# On another machine:
toss copy             # copies latest text to your clipboard
```

Uses native tools: `pbcopy`/`pbpaste` (macOS), `xclip`/`xsel` (Linux), `clip`/PowerShell (Windows).

Linux users: install `xclip` or `xsel` (`sudo apt install xclip`).

### Watch mode

```bash
toss watch
```

Streams incoming items in real time. Text prints to stdout, file notifications to stderr.

Pipe-friendly:

```bash
toss watch >> received.txt     # log all received text
```

### Auto-discovery

The server broadcasts its presence via UDP on the local network. Clients find it automatically — no need to set IPs or environment variables.

To override: `export TOSS_HOST=192.168.1.50:9090`

## How it works

- `toss serve` starts an HTTP server and a UDP discovery beacon
- Clients auto-discover the server on the local network
- Text is stored in memory, files in a temp directory
- Last 64 items are kept, cleaned up on exit
- Zero external dependencies

## Environment variables

| Variable    | Description              | Default          |
|-------------|--------------------------|------------------|
| `TOSS_HOST` | Server address (client)  | auto-discovered  |
| `TOSS_PORT` | Server port (server)     | `9090`           |

## Build for all platforms

```bash
make all
```

Produces binaries in `dist/` for:
- macOS (arm64, amd64)
- Linux (arm64, amd64)
- Windows (arm64, amd64)

## Security

Toss is designed for **trusted local networks only**. There is no authentication or encryption. Do not expose it to the public internet.

## License

[MIT](LICENSE)
