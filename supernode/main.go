package main

import (
	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/supernode/cmd"
)

func main() {

	keyring.InitSDKConfig()
	cmd.Execute()
}
