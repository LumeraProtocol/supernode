package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/spf13/cobra"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the supernode",
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize logging
		logtrace.Setup("supernode", "dev", slog.LevelInfo)

		// Create context with correlation ID for tracing
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-start")

		// Create context that can be canceled on shutdown
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Load configuration
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			logtrace.Error(ctx, "Failed to load configuration", logtrace.Fields{
				"error":       err.Error(),
				"config_file": cfgFile,
			})
			os.Exit(1)
		}

		// Initialize and start the supernode
		supernode, err := NewSupernode(ctx, cfg)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize supernode", logtrace.Fields{
				"error": err.Error(),
			})
			os.Exit(1)
		}

		// Start the supernode
		if err := supernode.Start(ctx); err != nil {
			logtrace.Error(ctx, "Failed to start supernode", logtrace.Fields{
				"error": err.Error(),
			})
			os.Exit(1)
		}

		logtrace.Info(ctx, "Supernode started", logtrace.Fields{
			"supernode_id":   cfg.SupernodeID,
			"listen_address": cfg.P2P.ListenAddress,
			"port":           cfg.P2P.Port,
		})

		// Wait for shutdown signal
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logtrace.Info(ctx, "Shutdown signal received", logtrace.Fields{})

		// Stop the supernode
		if err := supernode.Stop(ctx); err != nil {
			logtrace.Error(ctx, "Error stopping supernode", logtrace.Fields{
				"error": err.Error(),
			})
		}

		logtrace.Info(ctx, "Supernode shutdown complete", logtrace.Fields{})
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
