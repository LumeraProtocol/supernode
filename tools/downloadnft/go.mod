module github.com/LumeraProtocol/supernode/tools/downloadnft

go 1.22.0

replace (
	github.com/LumeraProtocol/supernode/common => ../../common
	github.com/LumeraProtocol/supernode/mixins => ../../mixins
	github.com/LumeraProtocol/supernode/p2p => ../../p2p
	github.com/LumeraProtocol/supernode/pastel => ../../pastel
	github.com/LumeraProtocol/supernode/proto => ../../proto
	github.com/LumeraProtocol/supernode/raptorq => ../../raptorq
	github.com/LumeraProtocol/supernode/walletnode => ../../walletnode
)

require (
	github.com/gorilla/websocket v1.5.1
	github.com/LumeraProtocol/supernode/walletnode v0.0.0-00010101000000-000000000000
	github.com/pkg/errors v0.9.1
	goa.design/goa/v3 v3.15.0
)
