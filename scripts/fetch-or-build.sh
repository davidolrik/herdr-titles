#!/bin/sh
# fetch-or-build.sh — the herdr [[build]] step: produce bin/herdr-window-title.
#
# Downloads the prebuilt release binary matching this checkout's manifest
# version and the current platform, verifying its sha256 against the release
# checksums.txt, so installing the plugin needs only sh + curl. Falls back to
# `go build` when no matching asset is reachable (dev checkouts, unusual
# platforms, offline installs). A checksum mismatch never falls back — a
# corrupted or tampered download must fail loudly, not get papered over.
#
# Runs with cwd = plugin root (herdr guarantees this for build commands).
# Overrides for tests: HWT_UNAME_S, HWT_UNAME_M, HWT_BASE_URL.
set -eu

BASE_URL="${HWT_BASE_URL:-https://github.com/davidolrik/herdr-window-title/releases/download}"
UNAME_S="${HWT_UNAME_S:-$(uname -s)}"
UNAME_M="${HWT_UNAME_M:-$(uname -m)}"

version=$(sed -n 's/^version = "\([^"]*\)".*/\1/p' herdr-plugin.toml | head -1)
if [ -z "$version" ]; then
  echo "fetch-or-build: cannot read version from herdr-plugin.toml" >&2
  exit 1
fi

case "$UNAME_S" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *)      os="" ;;
esac
case "$UNAME_M" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *)             arch="" ;;
esac

asset="herdr-window-title_${version}_${os}_${arch}"

if [ "${1:-}" = "--print-asset" ]; then
  if [ -z "$os" ] || [ -z "$arch" ]; then
    echo "fetch-or-build: unsupported platform: $UNAME_S/$UNAME_M" >&2
    exit 1
  fi
  echo "$asset"
  exit 0
fi

build_from_source() {
  echo "fetch-or-build: $1" >&2
  if command -v go >/dev/null 2>&1; then
    echo "fetch-or-build: building from source with go" >&2
    exec go build -o bin/herdr-window-title .
  fi
  echo "fetch-or-build: no prebuilt binary and go is not installed" >&2
  echo "fetch-or-build: install Go (https://go.dev/dl/) or retry with network access" >&2
  exit 1
}

if [ -z "$os" ] || [ -z "$arch" ]; then
  build_from_source "no prebuilt binary for platform $UNAME_S/$UNAME_M"
fi
if ! command -v curl >/dev/null 2>&1; then
  build_from_source "curl not available"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

url="$BASE_URL/v$version/$asset"
if ! curl -fsSL -o "$tmp/$asset" "$url"; then
  build_from_source "download failed: $url"
fi
if ! curl -fsSL -o "$tmp/checksums.txt" "$BASE_URL/v$version/checksums.txt"; then
  build_from_source "checksums download failed: $BASE_URL/v$version/checksums.txt"
fi

expected=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/checksums.txt")
if [ -z "$expected" ]; then
  build_from_source "no checksum entry for $asset"
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
if [ "$actual" != "$expected" ]; then
  echo "fetch-or-build: checksum mismatch for $asset (expected $expected, got $actual)" >&2
  exit 1
fi

mkdir -p bin
mv "$tmp/$asset" bin/herdr-window-title
chmod +x bin/herdr-window-title
echo "fetch-or-build: installed prebuilt $asset" >&2
