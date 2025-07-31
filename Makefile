.PHONY: build build-release build-sncli
.PHONY: install-lumera setup-supernodes system-test-setup 
.PHONY: gen-cascade gen-supernode
.PHONY: test-e2e test-unit test-integration test-system

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Linker flags for version information
LDFLAGS = -X github.com/LumeraProtocol/supernode/supernode/cmd.Version=$(VERSION) \
          -X github.com/LumeraProtocol/supernode/supernode/cmd.GitCommit=$(GIT_COMMIT) \
          -X github.com/LumeraProtocol/supernode/supernode/cmd.BuildTime=$(BUILD_TIME)

build:
	@mkdir -p release
	CGO_ENABLED=1 \
	GOOS=linux \
	GOARCH=amd64 \
	go build \
		-trimpath \
		-ldflags="-s -w $(LDFLAGS)" \
		-o release/supernode-linux-amd64 \
		./supernode
	@chmod +x release/supernode-linux-amd64

build-sncli: build/sncli

build/sncli: $(SNCLI_SRC) go.mod go.sum
	@mkdir -p release
	@echo "Building sncli..."
	@RELEASE_DIR=$(CURDIR)/release && \
	cd tests/client && \
	CGO_ENABLED=1 \
	GOOS=linux \
	GOARCH=amd64 \
	go build \
		-trimpath \
		-ldflags="-s -w $(LDFLAGS)" \
		-o $$RELEASE_DIR/sncli

SNCLI_SRC=$(shell find tests/client -name "*.go")

test-unit:
	go test -v ./...

test-integration:
	go test -v -p 1 -count=1 -tags=integration ./...

test-system:
	cd tests/system && go test -tags=system_test -v .

gen-cascade:
	protoc \
		--proto_path=proto \
		--go_out=gen \
		--go_opt=paths=source_relative \
		--go-grpc_out=gen \
		--go-grpc_opt=paths=source_relative \
		proto/supernode/action/cascade/service.proto

gen-supernode:
	protoc \
		--proto_path=proto \
		--go_out=gen \
		--go_opt=paths=source_relative \
		--go-grpc_out=gen \
		--go-grpc_opt=paths=source_relative \
		proto/supernode/supernode.proto

# Define the paths
SUPERNODE_SRC=supernode/main.go
DATA_DIR=tests/system/supernode-data1
DATA_DIR2=tests/system/supernode-data2
DATA_DIR3=tests/system/supernode-data3
CONFIG_FILE=tests/system/config.test-1.yml
CONFIG_FILE2=tests/system/config.test-2.yml
CONFIG_FILE3=tests/system/config.test-3.yml

# Setup script
SETUP_SCRIPT=tests/scripts/setup-supernodes.sh

# Install Lumera
# Optional: specify lumera binary path to skip download
LUMERAD_BINARY ?=
# Optional: specify installation mode (latest-release, latest-tag, or vX.Y.Z)
INSTALL_MODE ?=latest-tag

install-lumera:
	@echo "Installing Lumera..."
	@chmod +x tests/scripts/install-lumera.sh
	@sudo LUMERAD_BINARY="$(LUMERAD_BINARY)" tests/scripts/install-lumera.sh $(INSTALL_MODE)
	@echo "PtTDUHythfRfXHh63yzyiGDid4TZj2P76Zd,18749999981413" > ~/claims.csv
# Setup supernode environments
setup-supernodes:
	@echo "Setting up all supernode environments..."
	@chmod +x $(SETUP_SCRIPT)
	@bash $(SETUP_SCRIPT) all $(SUPERNODE_SRC) $(DATA_DIR) $(CONFIG_FILE) $(DATA_DIR2) $(CONFIG_FILE2) $(DATA_DIR3) $(CONFIG_FILE3)

# Complete system test setup (Lumera + Supernodes)
system-test-setup: install-lumera setup-supernodes
	@echo "System test environment setup complete."
	@if [ -f claims.csv ]; then cp claims.csv ~/; echo "Copied claims.csv to home directory."; fi

# Run system tests with complete setup
test-e2e:
	@echo "Running system tests..."
	@cd tests/system && go test -tags=system_test -v .