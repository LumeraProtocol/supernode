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

tests-system-setup:
	cd tests/scripts && ./install-lumera.sh

gen-lumera-proto:
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action.proto && cd ../../../
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action_service.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode_service.proto && cd ../../../

gen-dupe-detection-proto:
	cd  ./proto/dupedetection && protoc --go_out=../../gen/dupedetection --go-grpc_out=../../gen/dupedetection --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative dd-server.proto && cd ../../

gen-raptor-q-proto:
	cd  ./proto/raptorq && protoc --go_out=../../gen/raptorq --go-grpc_out=../../gen/raptorq --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative raptorq.proto && cd ../../

