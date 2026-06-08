VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# CodeGraph release pinned for the bundled MCP server / e2e test. Bump together
# with any change to the integration in internal/codegraph.
CODEGRAPH_VERSION := v0.9.7

.PHONY: build install vet fmt test hooks cross clean e2e-codegraph

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/roach-code ./cmd/roach-code
	@ln -sf roach-code bin/roach 2>/dev/null || cp bin/roach-code bin/roach
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/roach-code-plugin-example ./cmd/roach-code-plugin-example

install: build
	@dir="$${ROACH_INSTALL_DIR:-$$HOME/.local/bin}"; \
	mkdir -p "$$dir"; \
	if ! install -m 0755 bin/roach-code "$$dir/roach-code" 2>/dev/null; then \
		cp bin/roach-code "$$dir/roach-code" || exit 1; \
		chmod 0755 "$$dir/roach-code" || exit 1; \
	fi; \
	rm -f "$$dir/roach" || exit 1; \
	ln -s roach-code "$$dir/roach" 2>/dev/null || cp "$$dir/roach-code" "$$dir/roach" || exit 1; \
	if [ "$$(uname -s)" = Darwin ] && command -v xattr >/dev/null 2>&1; then \
		xattr -d com.apple.quarantine "$$dir" "$$dir/roach-code" "$$dir/roach" 2>/dev/null || true; \
		xattr -d com.apple.provenance "$$dir" "$$dir/roach-code" "$$dir/roach" 2>/dev/null || true; \
	fi; \
	"$$dir/roach" version >/dev/null || exit 1; \
	printf 'installed: %s/roach-code (alias: %s/roach)\n' "$$dir" "$$dir"

vet:
	go vet ./...

fmt:
	gofmt -w .

test:
	go test ./...

hooks:
	@git config core.hooksPath .githooks
	@echo "installed: core.hooksPath -> .githooks (pre-push runs go vet)"

cross:
	@mkdir -p dist
	@for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/roach-code-$$os-$$arch$$ext ./cmd/roach-code; \
	done

clean:
	rm -rf bin dist

# Fetch the matching CodeGraph bundle into bin/codegraph/ (the distribution
# layout: launcher at bin/codegraph/bin/codegraph beside bin/roach-code) and run the
# gated MCP end-to-end test against it. Requires `gh`. Windows: install via the
# upstream install.ps1 and run the test with ROACH_CODEGRAPH_BIN set.
e2e-codegraph:
	@os=$$(uname -s | tr 'A-Z' 'a-z'); arch=$$(uname -m); \
	case $$arch in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=x64;; *) echo "unsupported arch $$arch"; exit 1;; esac; \
	asset=codegraph-$$os-$$arch.tar.gz; dest=bin/codegraph; \
	echo "fetching $$asset ($(CODEGRAPH_VERSION)) -> $$dest"; \
	rm -rf $$dest && mkdir -p $$dest; \
	gh release download $(CODEGRAPH_VERSION) -R colbymchenry/codegraph -p $$asset -O /tmp/$$asset; \
	tar -xzf /tmp/$$asset -C $$dest --strip-components=1; \
	ROACH_CODEGRAPH_E2E=1 ROACH_CODEGRAPH_BIN=$$PWD/$$dest/bin/codegraph \
		go test ./internal/codegraph/ -run E2E -v -count=1
