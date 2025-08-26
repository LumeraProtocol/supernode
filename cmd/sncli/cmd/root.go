package cmd

import (
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"github.com/LumeraProtocol/supernode/v2/cmd/sncli/cli"
)

var (
	opts	cli.CLIOptions
	app 	*cli.CLI
	initOnce sync.Once
)

var rootCmd = &cobra.Command{
	Use:           "sncli",
	Short:         "Supernode CLI for Lumera",
	Long:  		   "sncli is a command-line tool to interact with Lumera supernodes.",
	SilenceUsage:  true,
	SilenceErrors: false,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		initOnce.Do(func() {
			app = cli.NewCLI()
			app.SetOptions(opts)
			err = app.Initialize()
		})
		return err
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if app != nil {
			app.Finalize()
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&opts.ConfigPath, "config", "", "Path to config file")
	rootCmd.PersistentFlags().StringVar(&opts.GrpcEndpoint, "grpc_endpoint", "", "Supernode gRPC endpoint")
	rootCmd.PersistentFlags().StringVar(&opts.SupernodeAddr, "address", "", "Supernode Lumera address")
	rootCmd.PersistentFlags().StringVar(&opts.P2PEndpoint, "p2p_endpoint", "", "Supernode P2P endpoint")
}