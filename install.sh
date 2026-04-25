#!/bin/sh
set -e

REPO="sulemaanhamza/toss"
INSTALL_DIR="/usr/local/bin"
BINARY="toss"

main() {
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
    esac

    case "$os" in
        darwin|linux) ;;
        *) echo "unsupported OS: $os (use install.ps1 for Windows)" >&2; exit 1 ;;
    esac

    # get latest version
    version=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
    if [ -z "$version" ]; then
        echo "error: could not find latest release" >&2
        exit 1
    fi

    # strip leading v for archive name
    ver="${version#v}"
    archive="toss_${ver}_${os}_${arch}.tar.gz"
    url="https://github.com/${REPO}/releases/download/${version}/${archive}"

    echo "installing toss ${version} (${os}/${arch})"

    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    curl -sSfL "$url" -o "${tmpdir}/${archive}"
    tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"

    if [ -w "$INSTALL_DIR" ]; then
        mv "${tmpdir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    else
        echo "need sudo to install to ${INSTALL_DIR}"
        sudo mv "${tmpdir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    fi
    chmod +x "${INSTALL_DIR}/${BINARY}"

    echo "installed: $(${INSTALL_DIR}/${BINARY} --version 2>/dev/null || echo "${INSTALL_DIR}/${BINARY}")"
    echo ""
    echo "run 'toss serve' on one machine, then 'toss \"hello\"' from another."
    echo "uninstall anytime with: toss uninstall"
}

main
