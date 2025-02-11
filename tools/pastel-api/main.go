package main

import (
	"os"

	"github.com/LumeraProtocol/supernode/common/errors"
	"github.com/LumeraProtocol/supernode/common/log"
	"github.com/LumeraProtocol/supernode/common/sys"
	"github.com/LumeraProtocol/supernode/tools/pastel-api/cmd"
)

const (
	debugModeEnvName = "PASTEL_API_DEBUG"
)

var (
	debugMode = sys.GetBoolEnv(debugModeEnvName, false)
)

func main() {
	defer errors.Recover(log.FatalAndExit)

	app := cmd.NewApp()
	err := app.Run(os.Args)

	log.FatalAndExit(err)
}

func init() {
	log.SetDebugMode(debugMode)
}
