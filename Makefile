.PHONY: test-unit test-integration test-system tests-system-setup setup-supernodes install-lumera

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
DATA_DIR=tests/system/supernode-data
DATA_DIR2=tests/system/supernode-data2
DATA_DIR3=tests/system/supernode-data3
CONFIG_FILE=tests/system/config.test-1.yml
CONFIG_FILE2=tests/system/config.test-2.yml
CONFIG_FILE3=tests/system/config.test-3.yml

# Setup script
SETUP_SCRIPT=tests/scripts/setup-supernodes.sh

install-lumera:
	cd tests/scripts && ./install-lumera.sh

# Single target to setup all supernode environments
install-supernodes:
	@echo "Setting up all supernode environments..."
	@bash $(SETUP_SCRIPT) all $(SUPERNODE_SRC) $(DATA_DIR) $(CONFIG_FILE) $(DATA_DIR2) $(CONFIG_FILE2) $(DATA_DIR3) $(CONFIG_FILE3)