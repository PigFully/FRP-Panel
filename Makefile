# FRPanel build. `make build` produces web assets + both binaries.
SHELL := /usr/bin/env bash
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG := github.com/frpanel/frpanel/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)
GO := CGO_ENABLED=0 go
BIN := bin
ARCHES := amd64 arm64

.PHONY: all build web panel agent test lint vet tidy clean dist run-panel

all: build

## build: web SPA + panel + agent (native arch)
build: web panel agent

## web: compile the React SPA into web/dist (embedded by the panel)
web:
	cd web && corepack pnpm install --frozen-lockfile && corepack pnpm build

panel:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/frpanel-panel ./panel

agent:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/frpanel-agent ./agent

## test: unit tests (proc parsing, port validation, toml gen, receipt, seq dedup, ...)
test:
	go test ./internal/...

vet:
	go vet ./...

lint: vet
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "golangci-lint not installed, skipped"

tidy:
	go mod tidy

## dist: multi-arch binaries + frp + install scripts + sha256, into dist/
dist: web
	@mkdir -p dist
	@for a in $(ARCHES); do \
		echo ">> building $$a"; \
		$(GO) GOOS=linux GOARCH=$$a build -trimpath -ldflags "$(LDFLAGS)" -o dist/frpanel-panel-$$a ./panel; \
		$(GO) GOOS=linux GOARCH=$$a build -trimpath -ldflags "$(LDFLAGS)" -o dist/frpanel-agent-$$a ./agent; \
	done
	cp scripts/install-agent.sh scripts/install-panel.sh dist/
	echo "$(VERSION)" > dist/VERSION
	cd dist && sha256sum frpanel-* > sha256sums.txt
	@echo "dist/ ready (add frpc-<arch> / frps-<arch> from the pinned frp release)"

run-panel: panel
	sudo ./$(BIN)/frpanel-panel run -config /etc/frp-panel/config.yaml

clean:
	rm -rf $(BIN) dist web/dist web/node_modules
