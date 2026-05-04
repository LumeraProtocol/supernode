package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/cloud"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/sqlite"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	grpcserver "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/server"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	hostReporterService "github.com/LumeraProtocol/supernode/v2/supernode/host_reporter"
	recheckService "github.com/LumeraProtocol/supernode/v2/supernode/recheck"
	selfHealingService "github.com/LumeraProtocol/supernode/v2/supernode/self_healing"
	statusService "github.com/LumeraProtocol/supernode/v2/supernode/status"
	storageChallengeService "github.com/LumeraProtocol/supernode/v2/supernode/storage_challenge"
	// Legacy supernode metrics reporter (MsgReportSupernodeMetrics) has been superseded by
	// epoch-scoped audit reporting in `x/audit`.
	// supernodeMetrics "github.com/LumeraProtocol/supernode/v2/supernode/supernode_metrics"
	"github.com/LumeraProtocol/supernode/v2/supernode/transport/gateway"
	cascadeRPC "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/cascade"
	selfHealingRPC "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/self_healing"
	server "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/status"
	storageChallengeRPC "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/storage_challenge"
	"github.com/LumeraProtocol/supernode/v2/supernode/verifier"

	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/spf13/cobra"

	pbcascade "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"

	pbsupernode "github.com/LumeraProtocol/supernode/v2/gen/supernode"

	// Configure DHT advertised/minimum versions from build-time variables
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the supernode",
	Long: `Start the supernode service using the configuration defined in config.yaml.
The supernode will connect to the Lumera network and begin participating in the network.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Initialize logging
		logtrace.Setup("supernode")

		// Advertise our binary version to peers
		kademlia.SetLocalVersion(Version)
		// Optionally enforce a minimum peer version if provided at build time
		if strings.TrimSpace(MinVer) != "" {
			kademlia.SetMinVersion(MinVer)
		}

		// Create context with correlation ID for tracing
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-start")
		// Make the context cancelable for graceful shutdown
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Log configuration info
		cfgFile := filepath.Join(baseDir, DefaultConfigFile)
		logtrace.Debug(ctx, "Starting supernode with configuration", logtrace.Fields{"config_file": cfgFile, "keyring_dir": appConfig.GetKeyringDir(), "key_name": appConfig.SupernodeConfig.KeyName})

		// Initialize keyring
		kr, err := initKeyringFromConfig(appConfig)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize keyring", logtrace.Fields{"error": err.Error()})
		}

		// Initialize Lumera client
		lumeraClient, err := initLumeraClient(ctx, appConfig, kr)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to connect Lumera, please check your configuration", logtrace.Fields{"error": err.Error()})
		}

		// Reachability evidence store (used for open_ports inference).
		reachability.SetDefaultStore(reachability.NewStore())
		// Epoch tracker: mark per-service inbound evidence per chain epoch (best-effort).
		// If no component sets the current epoch ID, reachability evidence is still recorded
		// but is not bucketed by epoch.
		reachability.SetDefaultEpochTracker(reachability.NewEpochTracker(8)) // W+2 with W=6 default

		// Verify config matches chain registration before starting services
		logtrace.Debug(ctx, "Verifying configuration against chain registration", logtrace.Fields{})
		configVerifier := verifier.NewConfigVerifier(appConfig, lumeraClient, kr)
		verificationResult, err := configVerifier.VerifyConfig(ctx)
		if err != nil || (verificationResult != nil && !verificationResult.IsValid()) {
			logFields := logtrace.Fields{}
			if err != nil {
				logFields["error"] = err.Error()
			}
			if verificationResult != nil {
				if len(verificationResult.Errors) > 0 {
					logFields["errors"] = verificationResult.Errors
				}
				if len(verificationResult.Warnings) > 0 {
					logFields["warnings"] = verificationResult.Warnings
				}
				if !verificationResult.IsValid() {
					logFields["summary"] = verificationResult.Summary()
				}
			}
			logtrace.Fatal(ctx, "Config verification failed", logFields)
		}

		if verificationResult.HasWarnings() {
			logtrace.Warn(ctx, "Config verification warnings", logtrace.Fields{"summary": verificationResult.Summary()})
		}

		logtrace.Debug(ctx, "Configuration verification successful", logtrace.Fields{})

		// Set Datadog host to identity and service to latest IP address from chain
		logtrace.SetDatadogHost(appConfig.SupernodeConfig.Identity)
		if snInfo, err := lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, appConfig.SupernodeConfig.Identity); err == nil && snInfo != nil {
			if ip := strings.TrimSpace(snInfo.LatestAddress); ip != "" {
				logtrace.SetDatadogService(ip)
			}
		}

		// Initialize RaptorQ store for Cascade processing
		rqStore, err := initRQStore(ctx, appConfig)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize RaptorQ store", logtrace.Fields{"error": err.Error()})
		}

		// Initialize P2P service
		p2pService, err := initP2PService(ctx, appConfig, lumeraClient, kr, rqStore, nil, nil)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize P2P service", logtrace.Fields{"error": err.Error()})
		}

		// Supernode wrapper removed; components are managed directly

		// Configure cascade service
		cService := cascadeService.NewCascadeService(
			appConfig.SupernodeConfig.Identity,
			lumeraClient,
			p2pService,
			codec.NewRaptorQCodec(appConfig.GetRaptorQFilesDir()),
			rqStore,
		)

		// Create a task tracker and cascade action server with DI
		tr := task.New()
		cascadeActionServer := cascadeRPC.NewCascadeActionServer(cService, tr, 0, 0)

		// Set the version in the status service package
		statusService.Version = Version

		// Create supernode status service with injected tracker
		statusSvc := statusService.NewSupernodeStatusService(p2pService, lumeraClient, appConfig, tr)

		// Test/devnet affordance: when LUMERA_SUPERNODE_DISABLE_HOST_REPORTER=1 is set,
		// skip starting the on-chain host_reporter service. This frees the supernode's
		// reporter key for externally driven `MsgSubmitEpochReport` flows (e.g. the
		// everlight devnet test scenarios) that would otherwise lose the account-sequence
		// race against the SN's own ~5s auto-submit ticker. Production deployments must
		// leave this unset; gated behind an env var with no config-file surface so the
		// canonical path is unchanged.
		var hostReporter *hostReporterService.Service
		if v := strings.TrimSpace(os.Getenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER")); v == "1" || strings.EqualFold(v, "true") {
			logtrace.Info(ctx, "host_reporter disabled via LUMERA_SUPERNODE_DISABLE_HOST_REPORTER", logtrace.Fields{})
		} else {
			hr, err := hostReporterService.NewService(
				appConfig.SupernodeConfig.Identity,
				lumeraClient,
				kr,
				appConfig.SupernodeConfig.KeyName,
				appConfig.BaseDir,
				appConfig.GetP2PDataDir(),
			)
			if err != nil {
				logtrace.Fatal(ctx, "Failed to initialize host reporter", logtrace.Fields{"error": err.Error()})
			}
			hostReporter = hr
		}

		// Legacy on-chain supernode metrics reporting has been superseded by `x/audit`.
		// metricsCollector := supernodeMetrics.NewCollector(
		// 	statusSvc,
		// 	lumeraClient,
		// 	appConfig.SupernodeConfig.Identity,
		// 	Version,
		// 	kr,
		// 	appConfig.SupernodeConfig.Port,
		// 	appConfig.P2PConfig.Port,
		// 	appConfig.SupernodeConfig.GatewayPort,
		// )
		// logtrace.Info(ctx, "Metrics collection enabled", logtrace.Fields{})

		// Storage challenge history DB (shared by the gRPC handler and runner).
		historyStore, err := queries.OpenHistoryDB()
		if err != nil {
			logtrace.Fatal(ctx, "Failed to open history DB", logtrace.Fields{"error": err.Error()})
		}

		// LEP-6 result buffer: drained by host_reporter on each tick and
		// appended to by the LEP6Dispatcher.
		resultBuffer := storageChallengeService.NewBuffer()
		if hostReporter != nil {
			hostReporter.SetProofResultProvider(resultBuffer)
		}

		storageChallengeServer := storageChallengeRPC.NewServer(appConfig.SupernodeConfig.Identity, p2pService, historyStore).
			WithArtifactReader(newP2PArtifactReader(p2pService)).
			WithRecipientSigner(kr, appConfig.SupernodeConfig.KeyName)
		var storageChallengeRunner *storageChallengeService.Service
		var recheckRunner *recheckService.Service
		if appConfig.StorageChallengeConfig.Enabled {
			storageChallengeRunner, err = storageChallengeService.NewService(
				appConfig.SupernodeConfig.Identity,
				appConfig.SupernodeConfig.Port,
				lumeraClient,
				p2pService,
				kr,
				historyStore,
				storageChallengeService.Config{
					Enabled:        true,
					PollInterval:   time.Duration(appConfig.StorageChallengeConfig.PollIntervalMs) * time.Millisecond,
					SubmitEvidence: appConfig.StorageChallengeConfig.SubmitEvidence,
					KeyName:        appConfig.SupernodeConfig.KeyName,
				},
			)
			if err != nil {
				logtrace.Fatal(ctx, "Failed to initialize storage challenge runner", logtrace.Fields{"error": err.Error()})
			}

			// LEP-6 dispatcher (mode-gated internally; see DispatchEpoch).
			if appConfig.StorageChallengeConfig.LEP6.Enabled {
				dispatcher, derr := storageChallengeService.NewLEP6Dispatcher(
					lumeraClient,
					kr,
					appConfig.SupernodeConfig.KeyName,
					appConfig.SupernodeConfig.Identity,
					storageChallengeService.NewSecureSupernodeClientFactory(lumeraClient, kr, appConfig.SupernodeConfig.Identity, appConfig.SupernodeConfig.Port),
					storageChallengeService.NewChainTicketProvider(lumeraClient),
					newCascadeMetaProvider(lumeraClient),
					resultBuffer,
				)
				if derr != nil {
					logtrace.Fatal(ctx, "Failed to initialize LEP-6 dispatcher", logtrace.Fields{"error": derr.Error()})
				}
				storageChallengeRunner.SetLEP6Dispatcher(dispatcher)

				if appConfig.StorageChallengeConfig.LEP6.Recheck.Enabled {
					rc := appConfig.StorageChallengeConfig.LEP6.Recheck
					tickInterval := time.Duration(rc.TickIntervalMs) * time.Millisecond
					recheckCfg := recheckService.Config{Enabled: true, LookbackEpochs: rc.LookbackEpochs, MaxPerTick: rc.MaxPerTick, TickInterval: tickInterval}
					attestor := recheckService.NewAttestor(appConfig.SupernodeConfig.Identity, lumeraClient.AuditMsg(), historyStore)
					reporterSource := recheckService.NewSupernodeReporterSource(lumeraClient.SuperNode(), appConfig.SupernodeConfig.Identity)
					recheckRunner, err = recheckService.NewServiceWithReporters(recheckCfg, lumeraClient.Audit(), historyStore, dispatcher, attestor, appConfig.SupernodeConfig.Identity, reporterSource)
					if err != nil {
						logtrace.Fatal(ctx, "Failed to initialize LEP-6 recheck runner", logtrace.Fields{"error": err.Error()})
					}
				}
			}
		}

		// Create supernode server
		supernodeServer := server.NewSupernodeServer(statusSvc)

		// LEP-6 self-healing runtime (chain-driven heal-op dispatch).
		// The dispatcher polls audit heal-ops and runs healer/verifier/
		// finalizer roles based on chain assignment. The §19 transport
		// server lets verifiers fetch reconstructed bytes from the
		// assigned healer before chain VERIFIED quorum.
		var selfHealingRunner *selfHealingService.Service
		var selfHealingServer *selfHealingRPC.Server
		if appConfig.SelfHealingConfig.Enabled {
			pollInterval := time.Duration(appConfig.SelfHealingConfig.PollIntervalMs) * time.Millisecond
			fetchTimeout := time.Duration(appConfig.SelfHealingConfig.VerifierFetchTimeoutMs) * time.Millisecond
			shCfg := selfHealingService.Config{
				Enabled:                    true,
				PollInterval:               pollInterval,
				MaxConcurrentReconstructs:  appConfig.SelfHealingConfig.MaxConcurrentReconstructs,
				MaxConcurrentVerifications: appConfig.SelfHealingConfig.MaxConcurrentVerifications,
				MaxConcurrentPublishes:     appConfig.SelfHealingConfig.MaxConcurrentPublishes,
				StagingRoot:                appConfig.SelfHealingConfig.StagingDir,
				VerifierFetchTimeout:       fetchTimeout,
				VerifierFetchAttempts:      appConfig.SelfHealingConfig.VerifierFetchAttempts,
				KeyName:                    appConfig.SupernodeConfig.KeyName,
			}
			fetcher := selfHealingService.NewSecureVerifierFetcher(lumeraClient, kr, appConfig.SupernodeConfig.Identity, appConfig.SupernodeConfig.Port)
			selfHealingRunner, err = selfHealingService.New(
				appConfig.SupernodeConfig.Identity,
				shCfg,
				lumeraClient,
				historyStore,
				cService,
				fetcher,
			)
			if err != nil {
				logtrace.Fatal(ctx, "Failed to initialize self-healing runner", logtrace.Fields{"error": err.Error()})
			}
			selfHealingServer, err = selfHealingRPC.NewServer(
				appConfig.SupernodeConfig.Identity,
				shCfg.StagingRoot,
				lumeraClient,
				selfHealingRPC.DefaultCallerIdentityResolver(),
			)
			if err != nil {
				logtrace.Fatal(ctx, "Failed to initialize self-healing transport", logtrace.Fields{"error": err.Error()})
			}
		}

		// Create gRPC server (explicit args, no config struct)
		grpcServices := []grpcserver.ServiceDesc{
			{Desc: &pbcascade.CascadeService_ServiceDesc, Service: cascadeActionServer},
			{Desc: &pbsupernode.SupernodeService_ServiceDesc, Service: supernodeServer},
			{Desc: &pbsupernode.StorageChallengeService_ServiceDesc, Service: storageChallengeServer},
		}
		if selfHealingServer != nil {
			grpcServices = append(grpcServices, grpcserver.ServiceDesc{
				Desc:    &pbsupernode.SelfHealingService_ServiceDesc,
				Service: selfHealingServer,
			})
		}
		grpcServer, err := server.New(
			appConfig.SupernodeConfig.Identity,
			appConfig.SupernodeConfig.Host,
			int(appConfig.SupernodeConfig.Port),
			"service",
			kr,
			lumeraClient,
			grpcServices...,
		)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to create gRPC server", logtrace.Fields{"error": err.Error()})
		}

		// Create HTTP gateway server that directly calls the supernode server.
		// Recovery endpoints are always registered; access is token-gated at handler level.
		gatewayServer, err := gateway.NewServerWithConfigAndRecovery(
			appConfig.SupernodeConfig.Host,
			int(appConfig.SupernodeConfig.GatewayPort),
			supernodeServer,
			appConfig.LumeraClientConfig.ChainID,
			&gateway.RecoveryDeps{
				CascadeFactory:       cService,
				P2PClient:            p2pService,
				SelfSupernodeAddress: appConfig.SupernodeConfig.Identity,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create gateway server: %w", err)
		}

		// Start the services using the standard runner and capture exit
		servicesErr := make(chan error, 1)
		go func() {
			services := []service{grpcServer, cService, p2pService, gatewayServer}
			if hostReporter != nil {
				services = append(services, hostReporter)
			}
			if storageChallengeRunner != nil {
				services = append(services, storageChallengeRunner)
			}
			if selfHealingRunner != nil {
				services = append(services, selfHealingRunner)
			}
			if recheckRunner != nil {
				services = append(services, recheckRunner)
			}
			servicesErr <- RunServices(ctx, services...)
		}()

		// Set up signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		// Wait for either a termination signal or service exit
		var triggeredBySignal bool
		var runErr error
		select {
		case sig := <-sigCh:
			triggeredBySignal = true
			logtrace.Debug(ctx, "Received signal, shutting down", logtrace.Fields{"signal": sig.String()})
		case runErr = <-servicesErr:
			if runErr != nil {
				logtrace.Error(ctx, "Service error", logtrace.Fields{"error": runErr.Error()})
			} else {
				logtrace.Debug(ctx, "Services exited", logtrace.Fields{})
			}
		}

		// Cancel context to signal all services
		cancel()

		// Stop HTTP gateway and gRPC servers without blocking shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		go func() {
			if err := gatewayServer.Stop(shutdownCtx); err != nil {
				logtrace.Warn(ctx, "Gateway shutdown warning", logtrace.Fields{"error": err.Error()})
			}
		}()
		grpcServer.Close()
		historyStore.CloseHistoryDB(context.Background())

		// Close Lumera client without blocking shutdown
		logtrace.Debug(ctx, "Closing Lumera client", logtrace.Fields{})
		go func() {
			if err := lumeraClient.Close(); err != nil {
				logtrace.Error(ctx, "Error closing Lumera client", logtrace.Fields{"error": err.Error()})
			}
		}()

		// If we triggered shutdown by signal, wait for services to drain
		if triggeredBySignal {
			if err := <-servicesErr; err != nil {
				logtrace.Error(ctx, "Service error on shutdown", logtrace.Fields{"error": err.Error()})
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}

// initP2PService initializes the P2P service
func initP2PService(ctx context.Context, config *config.Config, lumeraClient lumera.Client, kr cKeyring.Keyring, rqStore rqstore.Store, cloud cloud.Storage, mst *sqlite.MigrationMetaStore) (p2p.P2P, error) {
	// Get the supernode address from the keyring
	keyInfo, err := kr.Key(config.SupernodeConfig.KeyName)
	if err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}
	address, err := keyInfo.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}

	// Create P2P config using helper function
	p2pConfig := createP2PConfig(config, address.String())

	logtrace.Debug(ctx, "Initializing P2P service", logtrace.Fields{"address": p2pConfig.ListenAddress, "port": p2pConfig.Port, "data_dir": p2pConfig.DataDir, "supernode_id": address.String()})

	p2pService, err := p2p.New(ctx, p2pConfig, lumeraClient, kr, rqStore, cloud, mst)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize p2p service: %w", err)
	}

	return p2pService, nil
}

// initLumeraClient initializes the Lumera client based on configuration
func initLumeraClient(ctx context.Context, config *config.Config, kr cKeyring.Keyring) (lumera.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	lumeraConfig, err := lumera.NewConfig(
		config.LumeraClientConfig.GRPCAddr,
		config.LumeraClientConfig.ChainID,
		config.SupernodeConfig.KeyName,
		kr,
		lumera.TxOptions{
			GasAdjustment:            config.LumeraClientConfig.GasAdjustment,
			GasAdjustmentMultiplier:  config.LumeraClientConfig.GasAdjustmentMultiplier,
			GasAdjustmentMaxAttempts: config.LumeraClientConfig.GasAdjustmentMaxAttempts,
			GasPadding:               config.LumeraClientConfig.GasPadding,
			GasPrice:                 config.LumeraClientConfig.GasPrice,
			FeeDenom:                 config.LumeraClientConfig.FeeDenom,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Lumera config: %w", err)
	}
	return lumera.NewClient(
		ctx,
		lumeraConfig,
	)
}

// initRQStore initializes the RaptorQ store for Cascade processing
func initRQStore(ctx context.Context, config *config.Config) (rqstore.Store, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Create RaptorQ store directory if it doesn't exist
	rqDir := config.GetRaptorQFilesDir() + "/rq"
	if err := os.MkdirAll(rqDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create RQ store directory: %w", err)
	}

	// Create the SQLite file path
	rqStoreFile := rqDir + "/rqstore.db"

	logtrace.Debug(ctx, "Initializing RaptorQ store", logtrace.Fields{
		"file_path": rqStoreFile,
	})

	// Initialize RaptorQ store with SQLite
	return rqstore.NewSQLiteRQStore(rqStoreFile)
}
