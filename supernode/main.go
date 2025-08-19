package main

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/supernode/cmd"
)

func main() {

	keyring.InitSDKConfig()
	cmd.Execute()
}
