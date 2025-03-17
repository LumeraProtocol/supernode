package cmd

import (
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
		// Initialize logging with custom correlation ID
		correlationID := "supernode-start-" + appConfig.SupernodeConfig.KeyName
		ctx := logtrace.CtxWithCorrelationID(cmd.Context(), correlationID)

		// Set log level based on environment or config (could be enhanced)
		logLevel := slog.LevelInfo
		logtrace.Setup("supernode", "dev", logLevel)

		// Log detailed configuration info
		logtrace.Info(ctx, "Starting supernode with configuration", logtrace.Fields{
			"config_file":    cfgFile,
			"keyring_dir":    appConfig.KeyringConfig.Dir,
			"key_name":       appConfig.SupernodeConfig.KeyName,
			"listen_address": appConfig.P2PConfig.ListenAddress,
			"p2p_port":       appConfig.P2PConfig.Port,
			"data_dir":       appConfig.P2PConfig.DataDir,
			"external_ip":    appConfig.P2PConfig.ExternalIP,
			"lumera_grpc":    appConfig.LumeraClientConfig.GRPCAddr,
			"chain_id":       appConfig.LumeraClientConfig.ChainID,
		})

		// Initialize keyring
		logtrace.Info(ctx, "Initializing keyring", logtrace.Fields{
			"backend": appConfig.KeyringConfig.Backend,
			"dir":     appConfig.KeyringConfig.Dir,
		})

		kr, err := keyring.InitKeyring(
			appConfig.KeyringConfig.Backend,
			appConfig.KeyringConfig.Dir,
		)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize keyring", logtrace.Fields{
				"error": err.Error(),
				"dir":   appConfig.KeyringConfig.Dir,
			})
			return err
		}

		// Initialize the supernode (next step)
		logtrace.Info(ctx, "Initializing supernode", logtrace.Fields{})
		supernode, err := NewSupernode(ctx, appConfig, kr)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize supernode", logtrace.Fields{
				"error": err.Error(),
			})
			return err
		}

		// Start the supernode
		logtrace.Info(ctx, "Starting supernode services", logtrace.Fields{})
		if err := supernode.Start(ctx); err != nil {
			logtrace.Error(ctx, "Failed to start supernode", logtrace.Fields{
				"error": err.Error(),
			})
			return err
		}

		// Set up signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		logtrace.Info(ctx, "Supernode started successfully, waiting for termination signal", logtrace.Fields{})

		// Wait for termination signal
		sig := <-sigCh
		logtrace.Info(ctx, "Received signal, initiating shutdown", logtrace.Fields{
			"signal": sig.String(),
		})

		// Attempt graceful shutdown
		logtrace.Info(ctx, "Stopping supernode services", logtrace.Fields{})
		if err := supernode.Stop(ctx); err != nil {
			logtrace.Error(ctx, "Error during supernode shutdown", logtrace.Fields{
				"error": err.Error(),
			})
		}

		logtrace.Info(ctx, "Supernode shutdown complete", logtrace.Fields{})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
