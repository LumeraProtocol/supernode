package main

import (
	"fmt"
	"os"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/cmd"
)

var (
	// Build-time variables set by go build -ldflags
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func main() {
	if err := cmd.Execute(Version, GitCommit, BuildTime); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
