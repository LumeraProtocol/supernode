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

gen-lumera-proto:
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action.proto && cd ../../../
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action_service.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode_service.proto && cd ../../../

gen-dupe-detection-proto:
	cd  ./proto/dupedetection && protoc --go_out=../../gen/dupedetection --go-grpc_out=../../gen/dupedetection --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative dd-server.proto && cd ../../

gen-raptor-q-proto:
	cd  ./proto/raptorq && protoc --go_out=../../gen/raptorq --go-grpc_out=../../gen/raptorq --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative raptorq.proto && cd ../../

# Define the paths
SUPERNODE_SRC=supernode/main.go
DATA_DIR=tests/system/supernode-data
CONFIG_FILE=supernode/config.test.yml

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