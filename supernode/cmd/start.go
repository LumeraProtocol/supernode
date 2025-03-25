package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/spf13/cobra"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the supernode",
	Long: `Start the supernode service using the configuration defined in config.yaml.
The supernode will connect to the Lumera network and begin participating in the network.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Initialize logging
		logLevel := slog.LevelInfo
		logtrace.Setup("supernode", "dev", logLevel)

		// Create context with correlation ID for tracing
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-start")

		// Log configuration info
		logtrace.Info(ctx, "Starting supernode with configuration", logtrace.Fields{
			"config_file": cfgFile,
			"keyring_dir": appConfig.KeyringConfig.Dir,
			"key_name":    appConfig.SupernodeConfig.KeyName,
		})

		// Initialize keyring
		kr, err := keyring.InitKeyring(
			appConfig.KeyringConfig.Backend,
			appConfig.KeyringConfig.Dir,
		)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize keyring", logtrace.Fields{
				"error": err.Error(),
			})
			return err
		}

		// Initialize Lumera client
		lumeraClient, err := initLumeraClient(ctx, appConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize Lumera client: %w", err)
		}

		// Initialize RaptorQ store for Cascade processing
		rqStore, err := initRQStore(ctx, appConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize RaptorQ store: %w", err)
		}

		// Initialize the supernode (next step)
		supernode, err := NewSupernode(ctx, appConfig, kr, nil, rqStore, lumeraClient)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize supernode", logtrace.Fields{
				"error": err.Error(),
			})
			return err
		}

		// Start the supernode
		if err := supernode.Start(ctx); err != nil {
			logtrace.Error(ctx, "Failed to start supernode", logtrace.Fields{
				"error": err.Error(),
			})
			return err
		}

		// Set up signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Wait for termination signal
		sig := <-sigCh
		logtrace.Info(ctx, "Received signal, shutting down", logtrace.Fields{
			"signal": sig.String(),
		})

		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
