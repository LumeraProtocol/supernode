package main

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/supernode/cmd"
)

func main() {
	// Create initial context with correlation ID
	ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-main")

	// Initialize Cosmos SDK configuration
	logtrace.Info(ctx, "Initializing Cosmos SDK configuration", logtrace.Fields{})
	keyring.InitSDKConfig()

	// Execute root command
	logtrace.Info(ctx, "Executing CLI command", logtrace.Fields{})
	cmd.Execute()
}
