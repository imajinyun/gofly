package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
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
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/blob"
	appconfig "github.com/imajinyun/gofly/examples/gosky/internal/config"
	appdiscovery "github.com/imajinyun/gofly/examples/gosky/internal/discovery"
	"github.com/imajinyun/gofly/examples/gosky/internal/jobruntime"
	"github.com/imajinyun/gofly/examples/gosky/internal/jobsamples"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"
	appmq "github.com/imajinyun/gofly/examples/gosky/internal/mq"
	"github.com/imajinyun/gofly/examples/gosky/internal/navigation"
	"github.com/imajinyun/gofly/examples/gosky/internal/routes"
	"github.com/imajinyun/gofly/examples/gosky/internal/runtimecache"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/examples/gosky/internal/twofactor"
)

const muxWatchTransportIdentifier = "rpc:greeter/Watch"

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
	var policyStore *authz.MySQLPolicyStore
	var authorizationSQLStore *storage.SQLStore
	if c.Authorization.Enabled {
		jwtSecret := []byte(os.Getenv(c.Authorization.SecretEnv()))
		switch c.Authorization.PolicyBackendName() {
		case "file":
			authorizer, err = authz.NewFileAuthorizer(authz.DefaultModelPath, authz.DefaultPolicyPath, jwtSecret)
		case "mysql":
			policyStoreConfig, configErr := c.Authorization.MySQL.StorageConfig()
			if configErr != nil {
				slog.Error("resolve authorization policy store config", "error", configErr)
				return
			}
			policyTables, configErr := authz.NewMySQLPolicyTables(c.Authorization.MySQL.RulesTable, c.Authorization.MySQL.EventsTable)
			if configErr != nil {
				slog.Error("resolve authorization policy table names", "error", configErr)
				return
			}
			authorizationSQLStore, err = storage.Open(ctx, policyStoreConfig)
			if err == nil {
				authorizer, err = authz.NewMySQLAuthorizerWithTables(authz.DefaultModelPath, authorizationSQLStore.DB(), policyTables, jwtSecret)
			}
			if err == nil && c.Authorization.MySQL.WatcherEnabled {
				err = authorizer.StartMySQLPolicyWatcher(ctx, c.Authorization.MySQL.WatcherInterval)
			}
			if err == nil {
				policyStore, err = authz.NewMySQLPolicyStoreWithTables(authorizationSQLStore.DB(), policyTables)
			}
		}
		if err != nil {
			slog.Error("setup authorization", "error", err)
			return
		}
		defer authorizer.Close()
	}
	if authorizationSQLStore != nil {
		defer func() {
			if closeErr := authorizationSQLStore.Close(); closeErr != nil {
				slog.Warn("close authorization policy store", "error", closeErr)
			}
		}()
	}
	var authenticationService *authn.Service
	var identitySQLStore *storage.SQLStore
	var identityRepository *repo.MySQLIdentityRepository
	if c.IdentityStore.Enabled {
		identityStoreConfig, configErr := c.IdentityStore.StorageConfig()
		if configErr != nil {
			slog.Error("resolve identity store config", "error", configErr)
			return
		}
		identitySQLStore, err = storage.Open(ctx, identityStoreConfig)
		if err == nil {
			identityRepository = repo.NewMySQLIdentityRepository(identitySQLStore)
			authenticationService, err = authn.NewService(authn.ServiceOptions{
				Repository:   identityRepository,
				AccessSecret: []byte(os.Getenv(c.Authorization.SecretEnv())),
				Issuer:       c.IdentityStore.AccessTokenIssuer(),
				Audience:     c.IdentityStore.AccessTokenAudience(),
				AccessTTL:    c.IdentityStore.AccessTokenTTL(),
				RefreshTTL:   c.IdentityStore.RefreshTokenTTL(),
			})
		}
		if err != nil {
			slog.Error("setup authentication service", "error", err)
			return
		}
	}
	if identitySQLStore != nil {
		defer func() {
			if closeErr := identitySQLStore.Close(); closeErr != nil {
				slog.Warn("close identity store", "error", closeErr)
			}
		}()
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
		projectRepository = repo.NewMySQLProjectRepository(projectStore)
	}
	svcCtx := svc.NewServiceContext(c, mqBroker)
	svcCtx.SetAuthorizer(authorizer)
	var policyService *authz.PolicyService
	var rbacService *goskyapp.RBACService
	var rbacRepository *repo.MySQLRBACRepository
	if policyStore != nil && authorizationSQLStore != nil {
		policyService = authz.NewPolicyService(policyStore, authorizer)
		svcCtx.SetPolicyService(policyService)
		// The RBAC repository validates custom roles and shares the policy
		// database connection for the combined role_menus/Casbin transaction.
		rbacRepository = repo.NewMySQLRBACRepository(authorizationSQLStore, policyStore)
		policyStore.SetRoleDirectory(rbacRepository)
		rbacService = goskyapp.NewRBACService(rbacRepository, authorizer)
		svcCtx.SetRBACService(rbacService)
	}
	if authenticationService != nil {
		svcCtx.SetAuthenticationService(authenticationService)
		svcCtx.SetAdminSessionService(authenticationService)
		var roleManagers []smartweb.RoleManager
		if policyService != nil && identityRepository != nil {
			var roleDirectories []goskyapp.RoleCodeDirectory
			if rbacRepository != nil {
				roleDirectories = append(roleDirectories, rbacRepository)
			}
			roleManagers = append(roleManagers, goskyapp.NewRoleService(policyService, identityRepository, roleDirectories...))
		}
		if identitySQLStore != nil && rbacService != nil {
			userAdminRepository := repo.NewMySQLUserAdminRepository(identitySQLStore)
			svcCtx.SetUserAdminService(goskyapp.NewUserAdminService(userAdminRepository, rbacRepository, authorizer))
		}
		cacheRegistry := runtimecache.NewRegistry()
		captchaService := smartweb.NewCaptchaService(smartweb.CaptchaOptions{})
		_ = cacheRegistry.Register("captcha", runtimecache.Func{
			KeysFn:  func() ([]string, error) { return captchaService.CacheKeys(), nil },
			ClearFn: func() error { captchaService.CacheClear(); return nil },
		})
		smartWebService, smartWebErr := smartweb.NewService(authenticationService, authorizer, captchaService, roleManagers...)
		if smartWebErr != nil {
			slog.Error("setup smart-web compatibility", "error", smartWebErr)
			return
		}
		if rbacService != nil {
			smartWebService.SetDirectory(rbacService)
			if rbacRepository != nil && authorizationSQLStore != nil {
				orgRepository := repo.NewOrgRepository(authorizationSQLStore, rbacRepository)
				if err := orgRepository.EnsureAllTenants(context.Background()); err != nil {
					slog.Warn("ensure tenant organization", "error", err)
				}
				departmentService := goskyapp.NewDepartmentService(authorizer, orgRepository)
				positionService := goskyapp.NewPositionService(authorizer, orgRepository)
				employeeService := goskyapp.NewEmployeeService(authorizer, orgRepository, rbacRepository, policyService)
				_ = departmentService
				_ = positionService
				smartWebService.SetOrganization(goskyapp.NewOrganizationFacade(departmentService, positionService, employeeService))
			}
		}
		svcCtx.SetSmartWebService(smartWebService)
		if identityRepository != nil {
			svcCtx.SetOwnPasswordService(goskyapp.NewOwnPasswordChange(identityRepository, false))
		}
		if authorizationSQLStore != nil {
			svcCtx.SetDictService(goskyapp.NewDictService(repo.NewDictRepository(authorizationSQLStore), authorizer))
		}
		if authorizationSQLStore != nil {
			svcCtx.SetConfigService(goskyapp.NewConfigParamService(repo.NewConfigRepository(authorizationSQLStore), authorizer))
		}
		if authorizationSQLStore != nil {
			svcCtx.SetTableColumnService(goskyapp.NewTableColumnService(repo.NewTableColumnRepository(authorizationSQLStore)))
		}
		if authorizationSQLStore != nil {
			svcCtx.SetSerialNumberService(goskyapp.NewSerialNumberService(repo.NewSerialNumberRepository(authorizationSQLStore), authorizer))
		}
		if authorizationSQLStore != nil {
			svcCtx.SetMessageService(goskyapp.NewMessageService(repo.NewMessageRepository(authorizationSQLStore), authorizer))
		}
		if twoFactorService, twoFactorErr := newTwoFactorService(c.IdentityStore.TwoFactor, identityRepository, twofactor.NewMySQLCodeStore(authorizationSQLStore)); twoFactorErr != nil {
			slog.Warn("setup two-factor email code", "error", twoFactorErr)
		} else if twoFactorService != nil {
			svcCtx.SetTwoFactorService(twoFactorService)
			// Smart-web login verifies the email code through the same service.
			smartWebService.SetTwoFactor(twoFactorService)
			_ = cacheRegistry.Register("twofactor_rate_limit", runtimecache.Func{
				KeysFn:  func() ([]string, error) { return twoFactorService.RateLimitKeys(), nil },
				ClearFn: func() error { twoFactorService.ClearRateLimit(); return nil },
			})
		}
		if authorizationSQLStore != nil {
			fileRoot := strings.TrimSpace(os.Getenv("GOSKY_FILE_ROOT"))
			if fileRoot == "" {
				fileRoot = "var/files"
			}
			if blobStore, blobErr := blob.NewLocalStore(fileRoot); blobErr != nil {
				slog.Warn("setup local file storage", "error", blobErr)
			} else {
				fileRepository := repo.NewFileRepository(authorizationSQLStore)
				signingKey := []byte(os.Getenv(c.Authorization.SecretEnv()))
				svcCtx.SetFileService(goskyapp.NewFileService(fileRepository, blobStore, authorizer, signingKey))
			}
		}
		if authorizationSQLStore != nil {
			// The file service signs embedded attachment URLs; when blob
			// storage is unavailable it is nil and feedback/help-doc render
			// without URLs rather than failing the console.
			svcCtx.SetFeedbackService(goskyapp.NewFeedbackService(
				repo.NewFeedbackRepository(authorizationSQLStore), authorizer, svcCtx.CurrentFileService()))
			svcCtx.SetHelpDocService(goskyapp.NewHelpDocService(
				repo.NewHelpDocRepository(authorizationSQLStore), authorizer, svcCtx.CurrentFileService()))
			loginProtection := goskyapp.NewLoginProtectionService(
				repo.NewLoginSecurityRepository(authorizationSQLStore), authorizer)
			svcCtx.SetLoginProtectionService(loginProtection)
			smartWebService.SetLoginAuditor(smartweb.NewAppLoginAuditor(loginProtection))
			operateLog := goskyapp.NewOperateLogService(
				repo.NewOperateLogRepository(authorizationSQLStore), authorizer)
			operateLog.SetIdentityResolver(repo.NewLoginSecurityRepository(authorizationSQLStore))
			svcCtx.SetOperateLogService(operateLog)

			svcCtx.SetCacheConsoleService(goskyapp.NewCacheConsoleService(cacheRegistry, authorizer))
			reloadService := goskyapp.NewReloadService(
				repo.NewReloadRepository(authorizationSQLStore), cacheRegistry, authorizer)
			svcCtx.SetReloadService(reloadService)
			go reloadService.Run(ctx, 5*time.Second)

			jobRegistry := jobruntime.NewRegistry()
			jobRegistry.MustRegister(jobsamples.Sample1Key, jobsamples.SampleJob1{})
			jobRegistry.MustRegister(jobsamples.Sample2Key, jobsamples.SampleJob2{})
			jobService := goskyapp.NewJobService(
				repo.NewJobRepository(authorizationSQLStore), jobRegistry, authorizer)
			svcCtx.SetJobService(jobService)
			go goskyapp.NewJobScheduler(jobService).Run(ctx)

			svcCtx.SetChangeLogService(goskyapp.NewChangeLogService(
				repo.NewChangeLogRepository(authorizationSQLStore), authorizer))
			heartbeatService := goskyapp.NewHeartBeatService(
				repo.NewHeartBeatRepository(authorizationSQLStore), localIPv4s(), authorizer)
			svcCtx.SetHeartBeatService(heartbeatService)
			go heartbeatService.Run(ctx, 60*time.Second, 300*time.Second)
			svcCtx.SetDataTracerService(goskyapp.NewDataTracerService(
				repo.NewDataTracerRepository(authorizationSQLStore), authorizer))

			noticeRepo := repo.NewNoticeRepository(authorizationSQLStore)
			svcCtx.SetNoticeService(goskyapp.NewNoticeService(noticeRepo, authorizer))
			svcCtx.SetNoticeTypeService(goskyapp.NewNoticeTypeService(noticeRepo, authorizer))
		}
	}
	if authorizer != nil {
		svcCtx.SetNavigationService(navigation.NewAdminService(authorizer))
	}
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
			var receive = func(ctx context.Context, stream *rpc.ExperimentalMuxStream) (rpc.Message, error) {
				return stream.Receive(ctx)
			}
			var middlewares []rpc.ExperimentalMuxStreamMiddleware
			if authorizer != nil {
				registry := authz.DefaultCapabilityRegistry()
				receive = func(ctx context.Context, stream *rpc.ExperimentalMuxStream) (rpc.Message, error) {
					return authorizer.ReceiveRegisteredMuxMessage(ctx, stream, registry, muxWatchTransportIdentifier)
				}
				middlewares = append(middlewares, authorizer.RegisteredMuxStreamMiddleware(registry, muxWatchTransportIdentifier))
			}
			handler := func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := receive(ctx, stream)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("generated:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
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

// newTwoFactorService builds the email-code feature only when explicitly
// enabled. A disabled deployment returns (nil, nil), so login keeps its
// single-factor behavior; missing SMTP configuration is a startup warning.
func newTwoFactorService(config appconfig.TwoFactorConfig, identities twofactor.IdentityResolver, store twofactor.CodeStore) (*twofactor.Service, error) {
	if !config.Enabled {
		return nil, nil
	}
	feature := twofactor.DefaultFeature()
	feature.Enabled = true
	if config.CodeTTL > 0 {
		feature.CodeTTL = config.CodeTTL
	}
	if config.ResendGap > 0 {
		feature.ResendGap = config.ResendGap
	}
	if config.MaxAttempts > 0 {
		feature.MaxAttempts = config.MaxAttempts
	}
	if config.Issuer != "" {
		feature.Issuer = config.Issuer
	}
	mailer := twofactor.NewSMTPMailer(twofactor.SMTPConfig{
		Host:     config.SMTPHost,
		Port:     config.SMTPPort,
		Username: os.Getenv(config.SMTPUserEnv),
		Password: os.Getenv(config.SMTPPassEnv),
		From:     config.MailFrom,
	})
	return twofactor.NewService(feature, mailer, store, identities, nil), nil
}

// localIPv4s joins non-loopback IPv4 addresses for the heartbeat record.
func localIPv4s() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	parts := make([]string, 0, 2)
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok || network.IP.IsLoopback() {
			continue
		}
		if ip := network.IP.To4(); ip != nil {
			parts = append(parts, ip.String())
		}
	}
	return strings.Join(parts, ";")
}
