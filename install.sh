#!/usr/bin/env sh
# roach-code installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.sh | sh
#
# Downloads the prebuilt binary for your OS/arch from GitHub Releases, verifies
# its SHA-256 against the release's SHA256SUMS, and installs it. No Go required.
#
# Environment overrides:
#   ROACH_REPO         GitHub "owner/repo"   (default: tmdgusya/roach-code)
#   ROACH_VERSION      tag to install        (default: latest release)
#   ROACH_INSTALL_DIR  install directory     (default: $HOME/.local/bin)
set -eu

REPO="${ROACH_REPO:-tmdgusya/roach-code}"
INSTALL_DIR="${ROACH_INSTALL_DIR:-$HOME/.local/bin}"

err() { printf 'install: %s\n' "$1" >&2; exit 1; }

# --- detect platform --------------------------------------------------------
os=$(uname -s)
case "$os" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *)      err "unsupported OS: $os (use install.ps1 on Windows)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)             err "unsupported arch: $arch" ;;
esac

# --- pick a downloader ------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO - "$1"; }
else
  err "need curl or wget"
fi

# --- resolve version --------------------------------------------------------
version="${ROACH_VERSION:-}"
if [ -z "$version" ]; then
  version=$(fetch "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || err "could not resolve latest release for $REPO (set ROACH_VERSION)"
fi

asset="roach-code-${os}-${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"
printf 'install: roach-code %s (%s/%s)\n' "$version" "$os" "$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

dl "$base/$asset" "$tmp/$asset" || err "download failed: $base/$asset"

# --- verify checksum (best effort: only if SHA256SUMS is published) ---------
if sums=$(fetch "$base/SHA256SUMS" 2>/dev/null) && [ -n "$sums" ]; then
  want=$(printf '%s\n' "$sums" | awk -v a="$asset" '$2==a {print $1}')
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else
      got=""
    fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      err "checksum mismatch for $asset (expected $want, got $got)"
    fi
    [ -n "$got" ] && printf 'install: checksum ok\n'
  fi
fi

# --- extract & install ------------------------------------------------------
tar -xzf "$tmp/$asset" -C "$tmp"
bin="$tmp/roach-code-${os}-${arch}/roach-code"
[ -f "$bin" ] || bin=$(find "$tmp" -type f -name roach-code | head -n1)
[ -f "$bin" ] || err "binary not found in archive"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$bin" "$INSTALL_DIR/roach-code" 2>/dev/null \
  || { cp "$bin" "$INSTALL_DIR/roach-code" || err "could not write $INSTALL_DIR/roach-code"; chmod 0755 "$INSTALL_DIR/roach-code" || err "could not chmod $INSTALL_DIR/roach-code"; }

# short alias: `roach` -> roach-code (symlink so `roach update` only touches one binary)
rm -f "$INSTALL_DIR/roach" || err "could not replace $INSTALL_DIR/roach"
ln -s roach-code "$INSTALL_DIR/roach" 2>/dev/null \
  || cp "$INSTALL_DIR/roach-code" "$INSTALL_DIR/roach" \
  || err "could not create $INSTALL_DIR/roach"

if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$INSTALL_DIR" "$INSTALL_DIR/roach-code" "$INSTALL_DIR/roach" 2>/dev/null || true
  xattr -d com.apple.provenance "$INSTALL_DIR" "$INSTALL_DIR/roach-code" "$INSTALL_DIR/roach" 2>/dev/null || true
fi

"$INSTALL_DIR/roach" version >/dev/null || err "installed roach failed to run"

printf 'install: roach-code -> %s/roach-code  (short alias: roach)\n' "$INSTALL_DIR"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'install: add it to PATH:  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac
"$INSTALL_DIR/roach" version
