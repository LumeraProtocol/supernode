module github.com/LumeraProtocol/supernode/v2/sn-manager

go 1.26.2

replace (
	github.com/LumeraProtocol/supernode/v2 => ../
	github.com/envoyproxy/protoc-gen-validate => github.com/bufbuild/protoc-gen-validate v1.3.0
	github.com/lyft/protoc-gen-validate => github.com/envoyproxy/protoc-gen-validate v1.3.0
	nhooyr.io/websocket => github.com/coder/websocket v1.8.7
)

require (
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/LumeraProtocol/supernode/v2 v2.4.72
	github.com/spf13/cobra v1.10.2
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/term v0.43.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260427160629-7cedc36a6bc4 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260427160629-7cedc36a6bc4 // indirect
	google.golang.org/grpc v1.80.0 // indirect
)
