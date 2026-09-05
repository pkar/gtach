#!/bin/sh
# Download a checksummed release, or build the same tag on other supported targets.
set -eu
REPO=pkar/gtach
fail() { echo "gtach install: $*" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0
trap 'exit 1' INT TERM
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in linux|darwin) ;; *) fail "unsupported OS: $os" ;; esac
case "$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
release=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || fail "could not resolve latest release"
prefix="https://github.com/$REPO/releases/tag/"
case "$release" in "$prefix"*) tag=${release#"$prefix"} ;; *) fail "invalid release URL" ;; esac
case "$tag" in v[0-9]*) ;; *) fail "invalid release tag" ;; esac
case "$tag" in *[!a-zA-Z0-9.-]*) fail "invalid release tag" ;; esac
asset="gtach-$os-$arch"
base="https://github.com/$REPO/releases/download/$tag"
case "$os/$arch" in
linux/amd64|linux/arm64|darwin/arm64)
 curl -fsSL -o "$tmp/gtach" "$base/$asset" || fail "could not download $asset"
 curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download checksums"
 expected=""
 while read -r digest name extra; do
  if [ "$name" = "$asset" ]; then
   [ -z "$expected" ] && [ -z "$extra" ] || fail "ambiguous checksum entry"
   [ "${#digest}" -eq 64 ] || fail "invalid checksum"
   case "$digest" in *[!0-9a-f]*) fail "invalid checksum" ;; esac
   expected=$digest
  fi
 done < "$tmp/checksums.txt"
 [ -n "$expected" ] || fail "missing checksum for $asset"
 if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/gtach")
 elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/gtach")
 else
  fail "checksum verification requires sha256sum or shasum"
 fi
 [ "${actual%% *}" = "$expected" ] || fail "checksum mismatch for $asset"
 ;;
*)
 command -v go >/dev/null 2>&1 || fail "no prebuilt binary for $os/$arch; install Go to build from source"
 curl -fsSL -o "$tmp/source.tar.gz" "https://github.com/$REPO/archive/refs/tags/$tag.tar.gz"
 mkdir "$tmp/source"
 tar -xzf "$tmp/source.tar.gz" -C "$tmp/source" --strip-components=1
 (cd "$tmp/source" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${tag#v}" -o "$tmp/gtach" ./cmd/gtach)
 ;;
esac
BINDIR=${GTACH_INSTALL_DIR:-}
if [ -z "$BINDIR" ]; then
 for d in /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin"; do
  if [ -w "$d" ]; then BINDIR=$d; break; fi
 done
fi
BINDIR=${BINDIR:-$HOME/.local/bin}
mkdir -p "$BINDIR"
install -m 0755 "$tmp/gtach" "$BINDIR/gtach"
echo "installed $BINDIR/gtach ($tag)"
case ":$PATH:" in *:"$BINDIR":*) ;; *) echo "note: $BINDIR is not on your PATH" >&2 ;; esac
