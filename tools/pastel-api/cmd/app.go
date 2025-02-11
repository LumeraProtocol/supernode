package cmd

import (
	"context"
	"io/ioutil"

	"github.com/LumeraProtocol/supernode/common/cli"
	"github.com/LumeraProtocol/supernode/common/configurer"
	"github.com/LumeraProtocol/supernode/common/errors"
	"github.com/LumeraProtocol/supernode/common/log"
	"github.com/LumeraProtocol/supernode/common/log/hooks"
	"github.com/LumeraProtocol/supernode/common/sys"
	"github.com/LumeraProtocol/supernode/common/version"
	"github.com/LumeraProtocol/supernode/tools/pastel-api/api"
	"github.com/LumeraProtocol/supernode/tools/pastel-api/api/services/fake"
	"github.com/LumeraProtocol/supernode/tools/pastel-api/configs"
)

const (
	appName  = "pastel-api"
	appUsage = "Pastel API"

	defaultConfigFile = ""
)

// NewApp inits a new command line interface.
func NewApp() *cli.App {
	configFile := defaultConfigFile
	config := configs.New()

	app := cli.NewApp(appName)
	app.SetUsage(appUsage)
	app.SetVersion(version.Version())

	app.AddFlags(
		// Main
		cli.NewFlag("config-file", &configFile).SetUsage("Set `path` to the config file.").SetValue(configFile).SetAliases("c"),
		cli.NewFlag("log-level", &config.LogLevel).SetUsage("Set the log `level`.").SetValue(config.LogLevel),
		cli.NewFlag("log-file", &config.LogFile).SetUsage("The log `file` to write to."),
		cli.NewFlag("quiet", &config.Quiet).SetUsage("Disallows log output to stdout.").SetAliases("q"),
	)

	app.SetActionFunc(func(ctx context.Context, args []string) error {
		if configFile != "" {
			if err := configurer.ParseFile(configFile, config); err != nil {
				return err
			}
		}

		if config.Quiet {
			log.SetOutput(ioutil.Discard)
		} else {
			log.SetOutput(app.Writer)
		}

		if config.LogFile != "" {
			fileHook := hooks.NewFileHook(config.LogFile)
			log.AddHook(fileHook)
		}

		if err := log.SetLevelName(config.LogLevel); err != nil {
			return errors.Errorf("--log-level %q, %w", config.LogLevel, err)
		}

		return runApp(ctx, config)
	})

	return app
}

func runApp(ctx context.Context, config *configs.Config) error {
	ctx = log.ContextWithPrefix(ctx, "app")

	log.WithContext(ctx).Info("Start")
	defer log.WithContext(ctx).Info("End")

	log.WithContext(ctx).Infof("Config: %s", config)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sys.RegisterInterruptHandler(func() {
		cancel()
		log.WithContext(ctx).Info("Interrupt signal received. Gracefully shutting down...")
	})

	fakeService, err := fake.New()
	if err != nil {
		return err
	}

	server := api.NewServer(
		fakeService,
	)
	return server.Run(ctx, config.Server)
}
