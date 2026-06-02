#!/usr/bin/env bash
# Cross-compile roach-code for every target and package per-platform archives plus a
# SHA256SUMS manifest into dist/. CodeGraph is NOT bundled — roach-code fetches it
# into its per-version cache on first use (see internal/codegraph), which keeps
# these archives small. Driven by the Release workflow on a v* tag; runnable
# locally with VERSION set.
set -euo pipefail

VERSION="${VERSION:?set VERSION, e.g. v1.0.0}"
LDFLAGS="-s -w -X main.version=${VERSION}"
TARGETS="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

rm -rf dist stage
mkdir -p dist stage

for t in $TARGETS; do
  os=${t%/*}
  arch=${t#*/}
  name="roach-code-${os}-${arch}"
  stagedir="stage/${name}"
  ext=""
  [ "$os" = windows ] && ext=".exe"

  echo "==> build ${name}"
  mkdir -p "$stagedir"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -ldflags "$LDFLAGS" -o "${stagedir}/roach-code${ext}" ./cmd/roach-code

  echo "==> package ${name}"
  if [ "$os" = windows ]; then
    (cd stage && zip -qr "../dist/${name}.zip" "$name")
  else
    tar -czf "dist/${name}.tar.gz" -C stage "$name"
  fi
done

echo "==> checksums"
(cd dist && sha256sum roach-code-*.* >SHA256SUMS)
ls -la dist
