GO ?= go
BIN := jitenv
PREFIX ?= $(HOME)/.local

.PHONY: build install test fmt vet tidy lint release-snapshot clean \
	e2e-up e2e-down e2e-down-hard e2e-build e2e-run e2e-runner-build e2e-shell

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/gv/jitenv/internal/cli.Version=$(VERSION) \
	-X github.com/gv/jitenv/internal/cli.Commit=$(COMMIT) \
	-X github.com/gv/jitenv/internal/cli.Date=$(DATE)

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

# ---- e2e ---------------------------------------------------------------
# The harness lives under e2e/ as a separate Go module so it can pull in
# yaml deps without polluting the main module. See e2e/README.md.

E2E_COMPOSE := e2e/docker-compose.yml
E2E_PROJECT := jitenv-e2e
E2E_RUNNER  := e2e/harness/bin/runner

e2e-build:
	docker compose -f $(E2E_COMPOSE) -p $(E2E_PROJECT) build

e2e-up: e2e-build
	docker compose -f $(E2E_COMPOSE) -p $(E2E_PROJECT) up -d --wait

e2e-down:
	docker compose -f $(E2E_COMPOSE) -p $(E2E_PROJECT) down

e2e-down-hard:
	docker compose -f $(E2E_COMPOSE) -p $(E2E_PROJECT) down -v --remove-orphans

e2e-runner-build:
	cd e2e/harness && $(GO) build -o ../../$(E2E_RUNNER) ./cmd/runner

# Run a single scenario by filename in e2e/scenarios/.
#   make e2e-run SCENARIO=unlock-and-run-local.yaml
e2e-run: e2e-runner-build
	@test -n "$(SCENARIO)" || (echo "usage: make e2e-run SCENARIO=<file.yaml>"; exit 2)
	$(E2E_RUNNER) -scenario e2e/scenarios/$(SCENARIO) -compose-file $(E2E_COMPOSE) -project $(E2E_PROJECT)

# Drop into a shell in the named distro service (debian | alpine).
#   make e2e-shell DISTRO=alpine
e2e-shell:
	docker compose -f $(E2E_COMPOSE) -p $(E2E_PROJECT) exec --user jitenv $(or $(DISTRO),debian) bash
