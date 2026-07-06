#!/bin/sh
# install.sh — download the latest edc (event-driven-claude) release binary onto your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/event-driven-claude/main/install.sh | sh
#
# Override the install dir with EDC_INSTALL_DIR (default ~/.local/bin). Windows: grab the
# .zip from the releases page instead — this script targets macOS and Linux.
set -eu

REPO="jjuanrivvera/event-driven-claude"
BIN="edc"
INSTALL_DIR="${EDC_INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s)"
case "$os" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "unsupported OS: $os (Windows: download the .zip from https://github.com/$REPO/releases)" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | cut -d '"' -f4)"
[ -n "$tag" ] || { echo "could not resolve the latest release" >&2; exit 1; }
version="${tag#v}"
asset="event-driven-claude_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

echo "installing $BIN $tag ($os/$arch)..."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

# Verify the download against the release checksums before trusting the binary.
(
  cd "$tmp"
  line="$(grep " ${asset}\$" checksums.txt || true)"
  if [ -z "$line" ]; then
    echo "checksum for $asset not found in checksums.txt" >&2; exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    echo "$line" | sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then
    echo "$line" | shasum -a 256 -c -
  else
    echo "warning: no sha256 tool found, skipping checksum verification" >&2
  fi
)

mkdir -p "$INSTALL_DIR"
tar -xzf "$tmp/$asset" -C "$INSTALL_DIR" "$BIN"
chmod +x "$INSTALL_DIR/$BIN"
echo "installed: $INSTALL_DIR/$BIN"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) : ;;
  *) echo "note: $INSTALL_DIR is not on your PATH — add it so Claude Code can find '$BIN'" >&2 ;;
esac
