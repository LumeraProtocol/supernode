package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

func main() {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nReceived termination signal. Shutting down...")
		cancel()
	}()

	// Create a new Lumera client connected to localhost:9090
	// Note: No keyring is provided as requested
	client, err := lumera.NewClient(ctx, lumera.WithGRPCAddr("localhost:9090"))
	if err != nil {
		log.Fatalf("Failed to create Lumera client: %v", err)
	}

	// Ensure client is closed when program exits
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("Error closing client: %v", err)
		} else {
			fmt.Println("Client connection closed successfully")
		}
	}()

	fmt.Println("Successfully connected to Lumera node at localhost:9090")

	// Basic client info display
	fmt.Println("Available modules:")
	fmt.Println("- Auth Module (client.Auth())")
	fmt.Println("- Action Module (client.Action())")
	fmt.Println("- SuperNode Module (client.SuperNode())")
	fmt.Println("- Tx Module (client.Tx())")
	fmt.Println("- Node Module (client.Node()) - Note: Some functions may require a keyring")

	fmt.Println("\nClient is running. Press Ctrl+C to exit or wait for timeout.")

	// Wait for context cancellation (timeout or interrupt)
	<-ctx.Done()
}
