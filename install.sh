#!/bin/sh
# Install mek from GitHub Releases, verifying the SHA-256 checksum.
#
#   curl -fsSL https://raw.githubusercontent.com/prateep-r/mek/main/install.sh | sh
#
# Options (environment variables):
#   MEK_VERSION=v0.2.0         install a specific version (default: latest)
#   MEK_INSTALL_DIR=/some/dir  install location (default: ~/.local/bin, no sudo)
#   MEK_DOWNLOAD_URL=https://… download from a mirror instead of GitHub Releases
set -eu

REPO="prateep-r/mek"
DIR="${MEK_INSTALL_DIR:-$HOME/.local/bin}"

if [ -n "${MEK_DOWNLOAD_URL:-}" ]; then
  BASE="$MEK_DOWNLOAD_URL"
elif [ -n "${MEK_VERSION:-}" ]; then
  BASE="https://github.com/$REPO/releases/download/$MEK_VERSION"
else
  BASE="https://github.com/$REPO/releases/latest/download"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin | linux) ;;
  *) echo "mek: unsupported OS: $OS" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *) echo "mek: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
FILE="mek_${OS}_${ARCH}.tar.gz"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "downloading $FILE ..."
curl -fsSL "$BASE/$FILE" -o "$TMP/$FILE"
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt"

cd "$TMP"
if command -v sha256sum >/dev/null 2>&1; then
  SHA="sha256sum"
else
  SHA="shasum -a 256"
fi
if ! grep " $FILE\$" checksums.txt | $SHA -c - >/dev/null 2>&1; then
  echo "mek: checksum verification FAILED for $FILE — not installing" >&2
  exit 1
fi
echo "checksum ok"

tar -xzf "$FILE" mek
mkdir -p "$DIR"
install -m 755 mek "$DIR/mek"
echo "installed: $DIR/mek ($("$DIR/mek" version))"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "note: $DIR is not in your PATH. Add this to your shell profile:"
     echo "  export PATH=\"$DIR:\$PATH\"" ;;
esac
echo "next: mek doctor && mek init"
