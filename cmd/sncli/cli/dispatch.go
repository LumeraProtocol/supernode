package cli

import (
	"fmt"
	"log"
)

func showHelp() {
	helpText := `Supernode CLI Usage:
  ./sncli [options] <command> [args...]

Available Options:
  --config <path>          Path to config file (default: ./config.toml or SNCLI_CONFIG_PATH env)
  --grpc_endpoint <addr>   Override Supernode gRPC endpoint (e.g., localhost:9090)
  --address <addr>         Override Supernode Lumera address

Available Commands:
  help                         Show this help message
  list                         List available gRPC services on Supernode
  list <service>               List methods in a specific gRPC service
  health-check                 Check Supernode health status
  get-status                   Query Supernode's current status (CPU, memory)`
	fmt.Println(helpText)
}

func (c *CLI) Run() {
	// Dispatch command handler
	switch c.opts.Command {
	case "help", "":
		showHelp()
	case "list":
		if err := c.listGRPCMethods(); err != nil {
			log.Fatalf("List gRPC methods failed: %v", err)
		}
	case "health-check":
		if err := c.healthCheck(); err != nil {
			log.Fatalf("Health check failed: %v", err)
		}
	case "get-status":
		if err := c.getSupernodeStatus(); err != nil {
			log.Fatalf("Get supernode status failed: %v", err)
		}
	default:
		log.Fatalf("Unknown command: %s", c.opts.Command)
	}
}

