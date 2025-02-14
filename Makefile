gen-lumera-proto:
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action.proto && cd ../../../
	cd  ./proto/lumera/action && protoc --go_out=../../../gen/lumera/action --go-grpc_out=../../../gen/lumera/action --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative action_service.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode.proto && cd ../../../
	cd  ./proto/lumera/supernode && protoc --go_out=../../../gen/lumera/supernode --go-grpc_out=../../../gen/lumera/supernode --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative supernode_service.proto && cd ../../../
