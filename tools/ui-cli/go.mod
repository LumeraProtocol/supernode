module github.com/LumeraProtocol/supernode/tools/ui-cli

go 1.22.0

require (
	github.com/gorilla/websocket v1.5.1
	github.com/LumeraProtocol/supernode/walletnode v0.0.0-20210723172801-5d493665cdd7
	github.com/pkg/errors v0.9.1
	goa.design/goa/v3 v3.15.0
)

replace (
	github.com/LumeraProtocol/supernode/common => ../../common
	github.com/LumeraProtocol/supernode/mixins => ../../mixins
	github.com/LumeraProtocol/supernode/pastel => ../../pastel
	github.com/LumeraProtocol/supernode/proto => ../../proto
	github.com/LumeraProtocol/supernode/raptorq => ../../raptorq
	github.com/LumeraProtocol/supernode/walletnode => ../../walletnode
)
