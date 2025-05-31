.PHONY: test-unit test-integration test-system install-lumera setup-supernodes system-test-setup

# Run unit tests (regular tests with code)
test-unit:
	go test -v ./...

# Run integration tests
test-integration:
	go test -v -p 1 -count=1 -tags=integration ./...

# Run system tests
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
install-lumera:
	@echo "Installing Lumera..."
	@chmod +x tests/scripts/install-lumera.sh
	@sudo tests/scripts/install-lumera.sh latest-tag

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