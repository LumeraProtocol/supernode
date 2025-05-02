.PHONY: test-unit test-integration test-system tests-system-setup

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
CONFIG_FILE=supernode/config.test-1.yml

# Consolidated target that runs all setup steps
setup-all: install-supernode install-rqservice install-lumera
	@echo "All setup steps completed successfully!"

# Setup the supernode test environment
install-supernode:
	@echo "Setting up supernode test environment..."
	@bash tests/scripts/install-sn.sh $(SUPERNODE_SRC) $(DATA_DIR) $(CONFIG_FILE)

install-rqservice:
	@echo "Installing and running rq-service in $(DATA_DIR)..."
	@bash tests/scripts/install-rq-service.sh $(DATA_DIR)

install-lumera:
	cd tests/scripts && ./install-lumera.sh


# Define the additional paths - add these to your Makefile
DATA_DIR2=tests/system/supernode-data2
DATA_DIR3=tests/system/supernode-data3
CONFIG_FILE2=supernode/config.test-2.yml
CONFIG_FILE3=supernode/config.test-3.yml

# Add this target to your Makefile
install-nodes:
	@echo "Setting up additional supernode environments..."
	@bash tests/scripts/multinode.sh $(DATA_DIR) $(DATA_DIR2) $(CONFIG_FILE2) $(DATA_DIR3) $(CONFIG_FILE3)
