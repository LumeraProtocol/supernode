package main

import (
	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/supernode/cmd"
)

func main() {
	// Initialize Cosmos SDK configuration
	keyring.InitSDKConfig()

	// Execute root command
	cmd.Execute()
}
