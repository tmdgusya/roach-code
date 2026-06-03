#!/usr/bin/env sh
# run-local.sh — build roach-code from this checkout and (optionally) install it
# on top of $HOME/.local/bin/roach-code so `roach`/`roach-code` picks up the
# local fix immediately. Designed for the "검은 띠" fix verification flow:
#
#   ./run-local.sh              # build only -> bin/roach-code
#   ./run-local.sh install      # build + overwrite $HOME/.local/bin/roach-code
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
CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/roach-code ./cmd/roach-code

case "$mode" in
    build)
        printf 'done. run ./bin/roach-code or: ./run-local.sh run\n'
        ;;
    install)
        dest="${ROACH_INSTALL_DIR:-$HOME/.local/bin}/roach-code"
        mkdir -p "$(dirname "$dest")"
        cp bin/roach-code "$dest"
        chmod 0755 "$dest"
        printf 'installed -> %s\n' "$dest"
        printf 'launch:    %s\n' "$dest"
        ;;
    run)
        exec ./bin/roach-code
        ;;
esac
