package cmd

import (
	"context"
	"fmt"
	"os"

	"log/slog"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile   string
	appConfig *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "supernode",
	Short: "Lumera CLI tool for key management",
	Long: `A command line tool for managing Lumera blockchain keys.
This application allows you to create and recover keys using mnemonics.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Create a base context with correlation ID for this command execution
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "root-command")
		cmd.SetContext(ctx)

		// Skip config loading for help command
		if cmd.Name() == "help" {
			logtrace.Info(ctx, "Help command - skipping config loading", logtrace.Fields{})
			return nil
		}

		// If config file path is not specified, use the default in current directory
		if cfgFile == "" {
			cfgFile = "config.yml"
		}

		logtrace.Info(ctx, "Loading configuration", logtrace.Fields{
			"config_file": cfgFile,
		})

		// Load configuration
		var err error
		appConfig, err = config.LoadConfig(cfgFile)
		if err != nil {
			logtrace.Error(ctx, "Failed to load configuration", logtrace.Fields{
				"config_file": cfgFile,
				"error":       err.Error(),
			})
			return fmt.Errorf("failed to load config file %s: %w", cfgFile, err)
		}

		logtrace.Info(ctx, "Configuration loaded successfully", logtrace.Fields{
			"config_file": cfgFile,
		})

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	// Setup basic logging first - will be enhanced when we have config
	logtrace.Setup("supernode", "dev", slog.LevelInfo)

	ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-cli")
	logtrace.Info(ctx, "Starting Lumera supernode CLI", logtrace.Fields{})

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		logtrace.Error(ctx, "Command execution failed", logtrace.Fields{
			"error": err.Error(),
		})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Allow user to override config file location with --config flag
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file path (default is ./config.yaml)")
}
