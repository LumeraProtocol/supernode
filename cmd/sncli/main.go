package main

import (
	"github.com/LumeraProtocol/supernode/v2/cmd/sncli/cli"
)

func main() {
	c := cli.NewCLI()

	c.Initialize()
	defer c.Finalize()

	c.Run()
}
