#!/usr/bin/env sh
# run-local.sh — build roach-code from this checkout and (optionally) install it
# on top of $HOME/.local/bin/roach-code so `roach`/`roach-code` picks up the
# local fix immediately. Designed for the "검은 띠" fix verification flow:
#
#   ./run-local.sh              # build only -> bin/roach-code (+ bin/roach alias)
#   ./run-local.sh install      # build + overwrite $HOME/.local/bin/roach-code (+ roach alias)
#   ./run-local.sh run          # build + exec the resulting binary
#
# This is a dev helper, not a release. It does not touch the system PATH; if
# $HOME/.local/bin isn't on your PATH, call the binary by absolute path.
set -eu

usage() {
    sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

mode="${1:-build}"
case "$mode" in
    -h|--help|help) usage 0 ;;
    build|install|run) ;;
    *) printf 'unknown mode: %s\n\n' "$mode" >&2; usage 2 ;;
esac

# --- build ----------------------------------------------------------------
# Mirror the Makefile flags (ldflags strips + injects version) so the local
# binary looks like a release build to the version probe.
version="$(git describe --tags --always 2>/dev/null || echo dev)"
ldflags="-s -w -X main.version=$version"

printf '==> building bin/roach-code (version=%s)\n' "$version"
mkdir -p bin
CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/roach-code ./cmd/roach-code
ln -sf roach-code bin/roach 2>/dev/null || cp bin/roach-code bin/roach

case "$mode" in
    build)
        printf 'done. run ./bin/roach or ./bin/roach-code; install with: ./run-local.sh install\n'
        ;;
    install)
        dest="${ROACH_INSTALL_DIR:-$HOME/.local/bin}/roach-code"
        mkdir -p "$(dirname "$dest")"
        if ! install -m 0755 bin/roach-code "$dest" 2>/dev/null; then
            cp bin/roach-code "$dest" || exit 1
            chmod 0755 "$dest" || exit 1
        fi
        alias="$(dirname "$dest")/roach"
        rm -f "$alias" || exit 1
        ln -s roach-code "$alias" 2>/dev/null || cp "$dest" "$alias" || exit 1
        if [ "$(uname -s)" = Darwin ] && command -v xattr >/dev/null 2>&1; then
            xattr -d com.apple.quarantine "$(dirname "$dest")" "$dest" "$alias" 2>/dev/null || true
            xattr -d com.apple.provenance "$(dirname "$dest")" "$dest" "$alias" 2>/dev/null || true
        fi
        "$alias" version >/dev/null || exit 1
        printf 'installed -> %s\n' "$dest"
        printf 'alias     -> %s\n' "$alias"
        printf 'launch:    %s\n' "$alias"
        ;;
    run)
        exec ./bin/roach
        ;;
esac
