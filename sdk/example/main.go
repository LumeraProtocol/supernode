package main

import (
	"context"
	"crypto/sha3"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/supernode/config"
)

func main() {

	// STEP -1  Calculate the size of input data
	filePath := "/home/enxsys/Documents/Github/supernode/sdk/example/Excalidraw.zip"
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)

	}
	defer file.Close()

	// Get file info to determine size
	fileInfo, err := file.Stat()
	if err != nil {

	}

	// fileSize := fileInfo.Size()

	// Create a byte buffer of appropriate size
	data := make([]byte, fileInfo.Size())

	// Read the file contents into the buffer
	_, err = io.ReadFull(file, data)
	if err != nil {

	}

	// fileSize, data

	// STEP-2 Get Action fee for the calculated size from lumera

	// fee := 1000000 // 1M gas

	// STEP-3 Calculate the hash of input data
	hash := sha3.New256()
	hash.Write(data)
	// hashBytes := hash.Sum(nil)

	// STEP - 4  Cascade

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfgFile := "config.yml"
	appConfig, err := config.LoadConfig(cfgFile)
	if err != nil {
		log.Fatalf("Failed to load configuration file %s: %v", cfgFile, err)
	}

	config := raptorq.NewConfig()

	config.RqFilesDir = appConfig.RaptorQConfig.FilesDir
	client := raptorq.NewClient()

	// Server address using the config
	address := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// Connect to the server
	connection, err := client.Connect(ctx, address)
	if err != nil {
		log.Fatalf("Failed to connect to RaptorQ server at %s: %v", address, err)
	}
	defer connection.Close()

	// 4- Initialize the Lumera client
	lumeraClient, err := lumera.NewClient(
		ctx,
		lumera.WithGRPCAddr(appConfig.LumeraClientConfig.GRPCAddr),
		lumera.WithChainID(appConfig.LumeraClientConfig.ChainID),
		lumera.WithTimeout(appConfig.LumeraClientConfig.Timeout),
	)
	if err != nil {
		log.Fatalf("Failed to initialize Lumera client: %v", err)
	}

	rq := connection.RaptorQ(config, lumeraClient)

	if err != nil {
		fmt.Printf("Error loading file: %v\n", err)
		return
	}

	// Q- Block hash for which block ?

	encodeMetaResp, err := rq.GenRQIdentifiersFiles(ctx, raptorq.GenRQIdentifiersFilesRequest{
		BlockHash:        "112BC173FD838FB68EB43476816CD7B4C6661B6884A9E357B417EE957E1CF8F7",
		RqMax:            100,
		Data:             data,
		CreatorSNAddress: "lumera1gufjgrdvae704euyqtnhgchvxp3l6sagn2048h",
		// SignedData:       Si,
	})
	if err != nil {
		log.Fatalf("EncodeMetaData failed: %v", err)
	}

	fmt.Println("EncodeMetaData response:", encodeMetaResp)

}
