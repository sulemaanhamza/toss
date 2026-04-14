# toss

Share text and files across devices on your local network. No setup, no accounts, no pairing.

One binary. Works from terminal or browser. macOS, Linux, Windows.

## Install

### Download

Grab the latest binary from [Releases](https://github.com/sulemaanhamza/toss/releases) for your platform.

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
# toss running on :9090
#
#   http://192.168.1.50:9090
#
# on other machines:
#   export TOSS_HOST=192.168.1.50:9090
```

From any other machine on the same network:

```bash
export TOSS_HOST=192.168.1.50:9090

# send text
toss "here are the meeting notes"

# send a file
toss ./report.pdf

# pipe into it
cat config.yaml | toss

# get the latest item
toss get
```

Or open `http://192.168.1.50:9090` in any browser for the web UI.

## Web UI

The built-in web interface supports:

- Paste and send text
- Drag-and-drop file upload
- Copy text / download files
- Auto-refreshes every 2 seconds
- Dark mode (follows system preference)

## How it works

- `toss serve` starts an HTTP server on port 9090
- Other devices send/receive via CLI or browser
- Text is stored in memory, files in a temp directory
- Last 64 items are kept, cleaned up on exit
- Zero dependencies beyond Go's standard library

## Environment variables

| Variable    | Description              | Default          |
|-------------|--------------------------|------------------|
| `TOSS_HOST` | Server address (client)  | `localhost:9090` |
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
