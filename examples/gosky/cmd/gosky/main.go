package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/config"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/proc"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"

	_ "github.com/go-sql-driver/mysql"

	appadmin "github.com/imajinyun/gofly/examples/gosky/internal/admin"
	apprpc "github.com/imajinyun/gofly/examples/gosky/internal/api/grpc/v1"
	goskyapp "github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	appconfig "github.com/imajinyun/gofly/examples/gosky/internal/config"
	appdiscovery "github.com/imajinyun/gofly/examples/gosky/internal/discovery"
	appmq "github.com/imajinyun/gofly/examples/gosky/internal/mq"
	"github.com/imajinyun/gofly/examples/gosky/internal/routes"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
)

const muxWatchAuthorizationResource = "rpc:greeter/Watch"

func main() {
	var c appconfig.Config
	configPath := appconfig.ResolveConfigPath("gosky")
	if err := config.Load(configPath, &c, config.WithEnvExpansion(), config.WithStrictFields(), config.WithLoadValidator(appconfig.Validate)); err != nil {
		slog.Error("load config", "error", err)
		return
	}
	ctx, stop := proc.SignalContext(context.Background())
	defer stop()
	serviceConf := c.ServiceConf()
	bootstrapConf := serviceConf.BootstrapConfig("gosky")
	shutdown, runtimeState, err := app.BootstrapWithRuntime(ctx, bootstrapConf)
	if err != nil {
		slog.Error("bootstrap", "error", err)
		return
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	governanceManager, err := governance.NewManager(c.Governance, governance.WithPlugin(serviceConf.ProductionGovernancePlugin()))
	if err != nil {
		slog.Error("setup governance", "error", err)
		return
	}
	mqBroker, err := appmq.NewBroker(c.MQ, governanceManager)
	if err != nil {
		slog.Error("setup mq", "error", err)
		return
	}
	defer func() { _ = mqBroker.Close(context.Background()) }()
	var authorizer *authz.Authorizer
	if c.Authorization.Enabled {
		jwtSecret := []byte(os.Getenv(c.Authorization.SecretEnv()))
		authorizer, err = authz.NewFileAuthorizer(authz.DefaultModelPath, authz.DefaultPolicyPath, jwtSecret)
		if err != nil {
			slog.Error("setup authorization", "error", err)
			return
		}
	}
	var projectRepository goskyapp.ProjectRepository
	if c.ProjectStore.Enabled {
		projectStoreConfig, err := c.ProjectStore.StorageConfig()
		if err != nil {
			slog.Error("resolve project store config", "error", err)
			return
		}
		projectStore, err := storage.Open(ctx, projectStoreConfig)
		if err != nil {
			slog.Error("setup project store", "error", err)
			return
		}
		defer func() {
			if closeErr := projectStore.Close(); closeErr != nil {
				slog.Warn("close project store", "error", closeErr)
			}
		}()
		projectRepository = goskyapp.NewMySQLProjectRepository(projectStore)
	}
	svcCtx := svc.NewServiceContext(c, mqBroker)
	svcCtx.SetAuthorizer(authorizer)
	svcCtx.SetProjectService(goskyapp.NewProjectService(projectRepository, authorizer))
	restConf := serviceConf.RESTConfig(c.Rest)
	httpServer := rest.MustNewServer(
		restConf,
		rest.WithGovernanceManager(governanceManager),
	)
	routes.RegisterRoutes(httpServer, svcCtx)
	if c.OpenAPIEnabled() {
		httpServer.AddOpenAPIRoutes(c.OpenAPIInfo())
	}
	registry, closeRegistry, err := appdiscovery.NewRegistry(ctx, c.Discovery)
	if err != nil {
		slog.Error("setup discovery", "error", err)
		return
	}
	defer func() { _ = closeRegistry(context.Background()) }()
	registrar := rpc.NewDiscoveryRegistrar(registry, c.Discovery.RegisterOptions()...)
	rpcOptions := append(serviceConf.RPCServerOptions(),
		rpc.WithAddress(c.RPC.Addr),
		rpc.WithRegistry(registrar, "greeter", c.RPC.Advertise),
		rpc.WithRegistryTTL(c.Discovery.RegistryTTL()),
		rpc.WithServerGovernanceManager(governanceManager),
	)
	muxSinkSet, err := c.RPC.Mux.Log.OTelCompatible.NewSinkSet()
	if err != nil {
		slog.Error("setup mux diagnosis sink set", "error", err)
		return
	}
	defer func() { _ = muxSinkSet.Close() }()
	rpcOptions = append(rpcOptions, c.RPC.Mux.ServerOptionsWithSinkSet(muxSinkSet)...)
	var muxServer *rpc.ExperimentalMuxServer
	if c.RPC.Mux.Enabled {
		configureMux := func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			if !c.RPC.Mux.Probe {
				return nil
			}
			handler := func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("generated:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			}
			var middlewares []rpc.ExperimentalMuxStreamMiddleware
			if authorizer != nil {
				middlewares = append(middlewares, authorizer.MuxStreamMiddleware(muxWatchAuthorizationResource))
			}
			return adapter.RegisterStreamWithMiddlewares("greeter/Watch", handler, middlewares...)
		}
		if c.RPC.Mux.Candidate.Enabled {
			muxServer, err = rpc.NewExperimentalMuxCandidateServer(c.RPC.Mux.Addr, configureMux, c.RPC.Mux.CandidateServerConfig())
		} else {
			muxServer, err = rpc.NewExperimentalMuxServer(c.RPC.Mux.Addr, configureMux)
		}
		if err != nil {
			slog.Error("setup mux rpc", "error", err)
			return
		}
		rpcOptions = append(rpcOptions, rpc.WithExperimentalMuxServerAdapter(muxServer))
	}
	rpcServer := rpc.NewServer(rpcOptions...)
	if err := apprpc.RegisterServices(rpcServer, svcCtx); err != nil {
		slog.Error("register rpc", "error", err)
		return
	}
	servers := []app.Server{httpServer, rpcServer}
	if muxServer != nil {
		servers = append(servers, muxServer)
	}
	if c.Admin.Enabled {
		servers = append(servers, appadmin.NewServer(c.Admin.Addr, c.Admin.PathPrefix, rpcServer, appadmin.WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
			snapshot, err := c.ControlPlaneSnapshotWithDiscovery(ctx, registry)
			if err != nil {
				return snapshot, err
			}
			historyStore, verifyErr := rpcServer.MuxDiagnosisOperatorHistoryIntegritySnapshot(ctx)
			if verifyErr != nil {
				historyStore.Store.LastError = verifyErr.Error()
			}
			data, err := json.Marshal(historyStore)
			if err != nil {
				return snapshot, err
			}
			if snapshot.Configs == nil {
				snapshot.Configs = make(map[string]json.RawMessage)
			}
			snapshot.Configs["generated.rpcMuxOperatorHistoryStore"] = data
			return snapshot.WithChecksum(), nil
		})))
	}
	governanceManager.StartAsync(ctx, func(err error) { slog.Warn("governance manager stopped", "error", err) })
	go func() {
		if err := config.Watch[appconfig.Config](ctx, configPath, 2*time.Second, func(next appconfig.Config) {
			diffPlan, err := next.RPC.Mux.Log.OTelCompatible.DiffSinkSet(ctx, muxSinkSet)
			if err != nil {
				slog.Warn("plan mux diagnosis sink set reload", "error", err)
				return
			}
			if err := next.RPC.Mux.Log.OTelCompatible.ReloadSinkSet(ctx, muxSinkSet); err != nil {
				slog.Warn("reload mux diagnosis sink set", "error", err)
				return
			}
			if next.RPC.Mux.Log.EventExportEnabled() {
				rpcServer.UpdateMuxDiagnosisEventExporter(muxSinkSet, next.RPC.Mux.Log.Filter())
				svcCtx.UpdateRPCMuxDiagnosisExporters(muxSinkSet, next.RPC.Mux.Log.Filter())
			} else {
				rpcServer.UpdateMuxDiagnosisEventExporter(nil, rpc.RPCMuxDiagnosisFilter{})
				svcCtx.UpdateRPCMuxDiagnosisExporters(nil, rpc.RPCMuxDiagnosisFilter{})
			}
			svcCtx.UpdateConfig(next)
			slog.Info("config reloaded", "rest_host", next.Rest.Host, "rest_port", next.Rest.Port, "rpc_addr", next.RPC.Addr, "mux_sink_diff_plan", diffPlan)
		}); err != nil && ctx.Err() == nil {
			slog.Warn("watch config", "error", err)
		}
	}()
	slog.Info("gosky starting", "rest_host", restConf.Host, "rest_port", restConf.Port, "rpc_addr", c.RPC.Addr, "runtime", runtimeState.Snapshot(ctx))
	if err := app.Run(ctx, servers, serviceConf.RunOptions()...); err != nil {
		slog.Error("gosky stopped", "error", err)
	}
}
