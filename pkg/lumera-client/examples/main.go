package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumera-client/client"
)

func main() {
	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Initialize the client with custom options
	log.Println("Initializing Lumera client...")
	lumeraClient, err := client.NewClient(
		ctx,
		client.WithGRPCAddr("localhost:9090"),
		client.WithChainID("lumera-mainnet"),
		client.WithTimeout(30),
	)
	if err != nil {
		log.Fatalf("Failed to create Lumera client: %v", err)
	}
	defer func() {
		log.Println("Closing Lumera client...")
		if err := lumeraClient.Close(); err != nil {
			log.Printf("Error closing client: %v", err)
		}
	}()

	log.Println("Lumera client initialized successfully")

	// Example: Get node info
	log.Println("Getting node info...")
	nodeInfo, err := lumeraClient.Node().GetNodeInfo(ctx)
	if err != nil {
		log.Printf("Failed to get node info: %v", err)
	} else {
		log.Printf("Connected to node: %s, version: %s",
			nodeInfo.DefaultNodeInfo.Moniker,
			nodeInfo.ApplicationVersion.Version)
	}

	// Example: Get latest block
	log.Println("Getting latest block...")
	latestBlock, err := lumeraClient.Node().GetLatestBlock(ctx)
	if err != nil {
		log.Printf("Failed to get latest block: %v", err)
	} else {
		log.Printf("Latest block height: %d, time: %s",
			latestBlock.Block.Header.Height,
			latestBlock.Block.Header.Time)
	}

	// Example: Get action fee
	log.Println("Calculating action fee...")
	actionFee, err := lumeraClient.Action().GetActionFee(ctx, "1024")
	if err != nil {
		log.Printf("Failed to get action fee: %v", err)
	} else {
		log.Printf("Fee for 1024 bytes: %s", actionFee.Amount)
	}

	// Example: Get top supernodes
	log.Println("Getting top supernodes...")
	topNodes, err := lumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, uint64(latestBlock.Block.Header.Height))
	if err != nil {
		log.Printf("Failed to get top supernodes: %v", err)
	} else {
		log.Printf("Found %d top supernodes for block %d", len(topNodes.Supernodes), latestBlock.Block.Header.Height)
		for i, node := range topNodes.Supernodes {
			log.Printf("  Supernode #%d: %s", i+1, node.ValidatorAddress)
		}
	}

	// Run for a little while then exit
	select {
	case <-ctx.Done():
		log.Println("Context canceled, shutting down...")
	case <-time.After(10 * time.Second):
		log.Println("Example completed, shutting down...")
	}
}
