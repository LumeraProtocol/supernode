package main

import (
	"github.com/LumeraProtocol/supernode/cmd/sncli/cli"
)

func main() {
	c := cli.NewCLI()

	c.Initialize()
	defer c.Finalize()

	c.Run()
}
