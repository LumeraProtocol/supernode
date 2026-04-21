module github.com/LumeraProtocol/supernode/v2/sn-manager

go 1.26.1

replace (
	github.com/LumeraProtocol/lumera => ../../lumera
	github.com/LumeraProtocol/supernode/v2 => ../
	github.com/envoyproxy/protoc-gen-validate => github.com/bufbuild/protoc-gen-validate v1.3.0
	github.com/lyft/protoc-gen-validate => github.com/envoyproxy/protoc-gen-validate v1.3.0
	nhooyr.io/websocket => github.com/coder/websocket v1.8.7
)

require (
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/LumeraProtocol/supernode/v2 v2.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.10.2
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.3 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mgutz/ansi v0.0.0-20170206155736-9520e82c474b // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/term v0.40.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.3 // indirect
)
