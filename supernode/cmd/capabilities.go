package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/capabilities"
	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var capabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Manage and query supernode capabilities",
	Long:  `Commands for managing and querying supernode capabilities including version information and supported actions.`,
}

var capabilitiesShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display local capabilities",
	Long:  `Display the local supernode's capabilities including version, supported actions, and action versions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Try to load capabilities from config
		capConfigPath := filepath.Join(baseDir, "capabilities.yaml")
		caps, err := capabilities.LoadCapabilitiesFromConfig(capConfigPath)
		if err != nil {
			// Fall back to default capabilities
			caps = capabilities.CreateDefaultCapabilities()
			fmt.Printf("Note: Using default capabilities (config not found at %s)\n\n", capConfigPath)
		}

		// Display capabilities
		fmt.Printf("Supernode Capabilities:\n")
		fmt.Printf("  Version: %s\n", caps.Version)
		fmt.Printf("  Supported Actions: %s\n", strings.Join(caps.SupportedActions, ", "))
		
		if len(caps.ActionVersions) > 0 {
			fmt.Printf("  Action Versions:\n")
			for action, versions := range caps.ActionVersions {
				fmt.Printf("    %s: %s\n", action, strings.Join(versions, ", "))
			}
		}

		if len(caps.Metadata) > 0 {
			fmt.Printf("  Metadata:\n")
			for key, value := range caps.Metadata {
				fmt.Printf("    %s: %s\n", key, value)
			}
		}

		fmt.Printf("  Timestamp: %s\n", caps.Timestamp.Format(time.RFC3339))

		return nil
	},
}

var capabilitiesQueryCmd = &cobra.Command{
	Use:   "query [peer-address]",
	Short: "Query peer capabilities",
	Long:  `Query the capabilities of a remote supernode peer including version details and supported actions.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		peerAddr := args[0]

		// Connect to peer
		conn, err := grpc.Dial(peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect to peer %s: %w", peerAddr, err)
		}
		defer conn.Close()

		// Create client and query capabilities
		client := pb.NewSupernodeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := client.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
		if err != nil {
			return fmt.Errorf("failed to query capabilities from peer %s: %w", peerAddr, err)
		}

		caps := resp.GetCapabilities()
		if caps == nil {
			return fmt.Errorf("peer returned empty capabilities")
		}

		// Display peer capabilities
		fmt.Printf("Peer Capabilities (%s):\n", peerAddr)
		fmt.Printf("  Version: %s\n", caps.Version)
		fmt.Printf("  Supported Actions: %s\n", strings.Join(caps.SupportedActions, ", "))
		
		if len(caps.ActionVersions) > 0 {
			fmt.Printf("  Action Versions:\n")
			for action, actionVersions := range caps.ActionVersions {
				fmt.Printf("    %s: %s\n", action, strings.Join(actionVersions.Versions, ", "))
			}
		}

		if len(caps.Metadata) > 0 {
			fmt.Printf("  Metadata:\n")
			for key, value := range caps.Metadata {
				fmt.Printf("    %s: %s\n", key, value)
			}
		}

		fmt.Printf("  Timestamp: %s\n", time.Unix(caps.Timestamp, 0).Format(time.RFC3339))

		return nil
	},
}

func init() {
	capabilitiesCmd.AddCommand(capabilitiesShowCmd)
	capabilitiesCmd.AddCommand(capabilitiesQueryCmd)
	rootCmd.AddCommand(capabilitiesCmd)
}