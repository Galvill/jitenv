GO ?= go
BIN := jitenv
PREFIX ?= $(HOME)/.local

.PHONY: build install test fmt vet tidy lint release-snapshot clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/gv/jitenv/internal/version.Version=$(VERSION) \
	-X github.com/gv/jitenv/internal/version.Commit=$(COMMIT) \
	-X github.com/gv/jitenv/internal/version.Date=$(DATE)

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/jitenv

install: build
	install -Dm0755 bin/$(BIN) $(PREFIX)/bin/$(BIN)

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin

lint:
	golangci-lint run

release-snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign
