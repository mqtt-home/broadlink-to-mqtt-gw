GO = go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

MODULE = broadlink-to-mqtt-gw
LDFLAGS = -ldflags "-s -w -X '$(MODULE)/version.Version=$(VERSION)' -X '$(MODULE)/version.GitCommit=$(GIT_COMMIT)' -X '$(MODULE)/version.BuildTime=$(BUILD_TIME)'"

BINARY_NAME = bl2mqtt
CONFIG ?= $(PWD)/../production/config/config-example.json

.PHONY: ui build test vet lint run dev docker clean

# Build the React UI into web/dist (embedded into the binary).
ui:
	@cd ../ui && npm ci && npm run build

build: ui
	@$(GO) build $(LDFLAGS) -trimpath -o $(BINARY_NAME) .

test:
	@$(GO) test ./... -race -cover

vet:
	@$(GO) vet ./...

lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed, skipping (go vet already ran)."; fi

run:
	@$(GO) run . $(CONFIG)

# UI + API without MQTT, simulated remote: http://127.0.0.1:8081
# (for hot reload run `npm run dev` in ../ui, which proxies /api here)
dev:
	@$(GO) run ./cmd/uidev -addr 127.0.0.1:8081

docker:
	@docker build -f ../Dockerfile -t broadlink-to-mqtt-gw:dev ..

clean:
	@rm -f $(BINARY_NAME)
