GO ?= go
BIN := jitenv
PREFIX ?= $(HOME)/.local

.PHONY: build install test fmt vet tidy clean

build:
	$(GO) build -trimpath -ldflags "-s -w" -o bin/$(BIN) ./cmd/jitenv

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
