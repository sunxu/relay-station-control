package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	controlapi "github.com/sunxu/relay-station-control/internal/api"
	controlauth "github.com/sunxu/relay-station-control/internal/auth"
	controlenv "github.com/sunxu/relay-station-control/internal/environment"
	controlhistory "github.com/sunxu/relay-station-control/internal/historyruntime"
	controljobs "github.com/sunxu/relay-station-control/internal/jobs"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
	generatedstore "github.com/sunxu/relay-station-control/internal/store/sqlc"
	"github.com/sunxu/relay-station-control/internal/webui"
)

var version = "dev"

const (
	databaseMaxConnsEnvironment = "CONTROL_DATABASE_MAX_CONNS"
	databaseMaxConnsMinimum     = int32(1)
	databaseMaxConnsMaximum     = int32(100)
)

type jobRuntimeConfig struct {
	workerConcurrency     int
	reconcilerConcurrency int
	pollInterval          time.Duration
	reconcileInterval     time.Duration
	databaseBackoff       time.Duration
	shutdownGrace         time.Duration
}

type jobSlogLogger struct{ logger *slog.Logger }

func (adapter jobSlogLogger) Log(ctx context.Context, record controljobs.LogRecord) {
	if adapter.logger == nil {
		return
	}
	level := slog.LevelInfo
	if record.Result == controljobs.ResultFailure {
		level = slog.LevelError
	}
	adapter.logger.LogAttrs(ctx, level, "durable job lifecycle",
		slog.String("component", string(record.Component)),
		slog.String("action", string(record.Action)),
		slog.String("result", string(record.Result)),
		slog.String("job_kind", record.JobKind),
		slog.String("error_code", record.ErrorCode),
	)
}

func jobCatalogMatches(database []assetstore.JobKindPolicy, runtime []controljobs.CatalogEntry) bool {
	if len(database) != len(runtime) {
		return false
	}
	database = append([]assetstore.JobKindPolicy(nil), database...)
	sort.Slice(database, func(left, right int) bool {
		if database[left].JobKind != database[right].JobKind {
			return database[left].JobKind < database[right].JobKind
		}
		return database[left].PayloadSchemaVersion < database[right].PayloadSchemaVersion
	})
	for index, policy := range database {
		entry := runtime[index]
		if policy.JobKind != entry.Kind || policy.PayloadSchemaVersion != entry.SchemaVersion ||
			policy.Timeout != entry.Timeout || policy.LeaseDuration != entry.LeaseDuration ||
			policy.HeartbeatInterval != entry.HeartbeatInterval || policy.MaxAttempts != entry.MaxAttempts ||
			policy.MaxVerificationAttempts != entry.MaxVerifyAttempts || policy.ReplaySafe != entry.ReplaySafe ||
			policy.RollbackAllowed != entry.AllowRollback ||
			policy.AllowUnknownEffectReplay != entry.AllowUnknownEffectReplay ||
			policy.AllowDirectSuccess != entry.AllowDirectSuccess ||
			(policy.AllowUnknownEffectReplay && !policy.ReplaySafe) ||
			(entry.AllowUnknownEffectReplay && !entry.ReplaySafe) {
			return false
		}
	}
	return true
}

func loadJobRuntimeConfig() (jobRuntimeConfig, error) {
	workerConcurrency, err := envIntBounded("CONTROL_JOB_WORKER_CONCURRENCY", 4, 1, controljobs.MaxConcurrency)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	reconcilerConcurrency, err := envIntBounded("CONTROL_JOB_RECONCILER_CONCURRENCY", 2, 1, controljobs.MaxConcurrency)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	pollInterval, err := envDurationBounded("CONTROL_JOB_POLL_INTERVAL", time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	reconcileInterval, err := envDurationBounded("CONTROL_JOB_RECONCILE_INTERVAL", 5*time.Second, time.Second, time.Minute)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	databaseBackoff, err := envDurationBounded("CONTROL_JOB_DATABASE_BACKOFF", 5*time.Second, time.Second, time.Minute)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	shutdownGrace, err := envDurationBounded("CONTROL_JOB_SHUTDOWN_GRACE", 8*time.Second, time.Second, 30*time.Second)
	if err != nil {
		return jobRuntimeConfig{}, err
	}
	return jobRuntimeConfig{
		workerConcurrency: workerConcurrency, reconcilerConcurrency: reconcilerConcurrency,
		pollInterval: pollInterval, reconcileInterval: reconcileInterval,
		databaseBackoff: databaseBackoff, shutdownGrace: shutdownGrace,
	}, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	address := envOrDefault("CONTROL_HTTP_ADDR", "127.0.0.1:8080")
	environmentIdentity, err := controlenv.Validate(
		os.Getenv("CONTROL_ENVIRONMENT_ID"),
		envOrDefault("CONTROL_ENVIRONMENT", "dev"),
	)
	if err != nil {
		logger.Error("invalid control configuration", "component", "environment", "reason", controlenv.ReasonOf(err))
		os.Exit(1)
	}
	mfaRequired, err := envBool("CONTROL_MFA_REQUIRED", false)
	if err != nil {
		logger.Error("invalid control configuration", "component", "auth")
		os.Exit(1)
	}
	jobConfig, err := loadJobRuntimeConfig()
	if err != nil {
		logger.Error("invalid control configuration", "component", "jobs", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	dingtalkConfig, err := loadDingTalkConfig()
	if err != nil {
		logger.Error("invalid control configuration", "component", "dingtalk", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	inventoryPollConfig, err := loadAccountInventoryPollRuntimeConfig()
	if err != nil {
		logger.Error("invalid control configuration", "component", "account_inventory_poll", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	gatewayDirectoryConfig, err := loadGatewayDirectoryRuntimeConfig()
	if err != nil {
		logger.Error("invalid control configuration", "component", "gateway_directory", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	historyConfig, err := loadAccountInventoryHistoryRuntimeConfig()
	if err != nil {
		logger.Error("invalid control configuration", "component", "account_inventory_history", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	config, err := (controlauth.Config{
		Environment:         controlauth.Environment(environmentIdentity.Type),
		BindAddress:         address,
		BootstrapSecretFile: os.Getenv("CONTROL_BOOTSTRAP_SECRET_FILE"),
		AuthKeyringFile:     os.Getenv("CONTROL_AUTH_KEYRING_FILE"),
		TrustedProxyCIDRs:   splitCSV(os.Getenv("CONTROL_TRUSTED_PROXY_CIDRS")),
		MFARequired:         mfaRequired,
	}).Validate()
	if err != nil {
		logger.Error("invalid control configuration", "component", "auth")
		os.Exit(1)
	}
	nodeDrivers, err := loadNodeDriverRuntime(logger)
	if err != nil {
		logger.Error("invalid control configuration", "component", "node_driver", "reason", "invalid_runtime_config")
		os.Exit(1)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("database configuration is required", "component", "auth")
		os.Exit(1)
	}
	pool, err := newDatabasePool(context.Background(), databaseURL)
	if err != nil {
		logger.Error("database initialization failed", "component", "auth")
		os.Exit(1)
	}
	defer pool.Close()
	if err = pool.Ping(context.Background()); err != nil {
		logger.Error("database unavailable", "component", "auth")
		os.Exit(1)
	}
	if err = controlenv.Verify(context.Background(), generatedstore.New(pool), environmentIdentity); err != nil {
		logger.Error("environment identity verification failed", "component", "environment", "reason", controlenv.ReasonOf(err))
		os.Exit(1)
	}
	requestQualityRuntime, err := newAccountRequestQualityRuntime(pool, nodeDrivers, logger)
	if err != nil {
		logger.Error("request quality initialization failed", "component", "account_request_quality")
		os.Exit(1)
	}

	authService, err := controlauth.NewService(pool, config)
	if err != nil {
		logger.Error("authentication initialization failed", "component", "auth")
		os.Exit(1)
	}
	assetRepository, err := assetstore.NewAssetRepository(pool)
	if err != nil {
		logger.Error("asset registry initialization failed", "component", "assets")
		os.Exit(1)
	}
	intentKey, err := loadAssetIntentKey(os.Getenv("CONTROL_ASSET_INTENT_KEY_FILE"))
	if err != nil {
		logger.Warn("asset command intent key unavailable", "component", "assets", "reason", "invalid_key_file")
		intentKey = nil
	}
	gatewayLifecycleRepository, err := assetstore.NewGatewayLifecycleRepository(pool, intentKey)
	if err != nil {
		logger.Error("gateway lifecycle initialization failed", "component", "assets")
		os.Exit(1)
	}
	assetMetrics := controlapi.NewAssetMetrics()
	crossNodeDuplicateReader, err := assetstore.NewCrossNodeDuplicateOwnershipRepository(pool)
	if err != nil {
		logger.Error("cross-node duplicate ownership query initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	crossNodeDuplicateLifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
	if err != nil {
		logger.Error("cross-node duplicate ownership lifecycle initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	crossNodeDuplicateLifecycle.SetAlertObserver(crossNodeDuplicateOwnershipSlogAlertObserver{logger: logger})
	crossNodeDuplicateReconciler, err := assetstore.NewCrossNodeDuplicateOwnershipReconciler(pool, crossNodeDuplicateReader, crossNodeDuplicateLifecycle)
	if err != nil {
		logger.Error("cross-node duplicate ownership reconciler initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	crossNodeDuplicateTrigger := newCrossNodeDuplicateOwnershipReconciliationTrigger(crossNodeDuplicateReconciler, environmentIdentity.ID, logger)
	accountAvailability, err := assetstore.NewAccountAvailabilityRepository(pool)
	if err != nil {
		logger.Error("account availability initialization failed", "component", "account_availability")
		os.Exit(1)
	}
	availabilityTrigger := newAccountAvailabilityReconciliationTrigger(accountAvailability, logger)
	lifecycleTrigger := func(ctx context.Context) {
		availabilityTrigger(ctx)
		crossNodeDuplicateTrigger(ctx)
	}
	inventoryPollRuntime, err := newAccountInventoryPollRuntime(pool, nodeDrivers, inventoryPollConfig, logger, lifecycleTrigger)
	if err != nil {
		logger.Error("account inventory poll initialization failed", "component", "account_inventory_poll", "reason", "initialization_failed")
		os.Exit(1)
	}
	historyRuntime, err := newAccountInventoryHistoryRuntime(pool, historyConfig)
	if err != nil {
		logger.Error("account inventory history initialization failed", "component", "account_inventory_history", "reason", "initialization_failed")
		os.Exit(1)
	}
	jobRepository, err := assetstore.NewJobRepository(pool)
	if err != nil {
		logger.Error("durable job initialization failed", "component", "jobs")
		os.Exit(1)
	}
	accountInventoryRepository, err := assetstore.NewAccountInventoryRepository(pool)
	if err != nil {
		logger.Error("account inventory query initialization failed", "component", "account_inventory")
		os.Exit(1)
	}
	if err = accountInventoryRepository.CheckCompatibility(context.Background()); err != nil {
		logger.Warn("account inventory query compatibility check failed", "component", "account_inventory", "reason", "schema_incompatible")
	}
	relayBindingRepository, err := assetstore.NewRelayBindingRepository(pool)
	if err != nil {
		logger.Error("relay binding repository initialization failed", "component", "relay_binding")
		os.Exit(1)
	}
	crossNodeDuplicateOccurrences, err := assetstore.NewCrossNodeDuplicateOwnershipOccurrenceRepository(pool)
	if err != nil {
		logger.Error("cross-node duplicate occurrence repository initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	crossNodeDuplicateMetricsRepository, err := assetstore.NewCrossNodeDuplicateOwnershipMetricsRepository(pool)
	if err != nil {
		logger.Error("cross-node duplicate ownership metrics repository initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	jobKinds, err := jobRepository.JobKinds(context.Background())
	if err != nil {
		logger.Error("durable job catalog unavailable", "component", "jobs")
		os.Exit(1)
	}
	jobRegistry, err := newProductionJobRegistry(dingtalkConfig)
	if err != nil {
		logger.Error("durable job registry initialization failed", "component", "jobs")
		os.Exit(1)
	}
	if !jobCatalogMatches(jobKinds, jobRegistry.Catalog()) {
		logger.Error("durable job catalog mismatch", "component", "jobs", "reason", "registry_mismatch")
		os.Exit(1)
	}
	if dingtalkConfig.Enabled() {
		// No Redis publisher is configured; the existing Worker polls PostgreSQL.
		// This only suppresses wake publication, not the durable notification job.
		accountAvailability.SetNotificationDelivery(jobRegistry, false)
		crossNodeDuplicateLifecycle.SetNotificationDelivery(jobRegistry, false)
	}
	bootID := uuid.NewString()
	jobLogger := jobSlogLogger{logger: logger}
	worker, err := controljobs.NewWorker(jobRepository, jobRegistry, controljobs.WorkerConfig{
		Owner: "control-worker-" + bootID, Concurrency: jobConfig.workerConcurrency,
		PollInterval: jobConfig.pollInterval, DatabaseBackoff: jobConfig.databaseBackoff,
		ShutdownGrace: jobConfig.shutdownGrace,
		Retry:         controljobs.NewBackoffPolicy(time.Second, time.Minute),
		Logger:        jobLogger,
	})
	if err != nil {
		logger.Error("durable job worker initialization failed", "component", "jobs")
		os.Exit(1)
	}
	reconciler, err := controljobs.NewReconciler(jobRepository, jobRegistry, controljobs.ReconcilerConfig{
		Owner: "control-reconciler-" + bootID, Concurrency: jobConfig.reconcilerConcurrency,
		PollInterval: jobConfig.reconcileInterval, DatabaseBackoff: jobConfig.databaseBackoff,
		ShutdownGrace: jobConfig.shutdownGrace,
		Retry:         controljobs.NewBackoffPolicy(time.Second, time.Minute),
		Logger:        jobLogger,
	})
	if err != nil {
		logger.Error("durable job reconciler initialization failed", "component", "jobs")
		os.Exit(1)
	}

	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))
	metricsRegistry := prometheus.NewRegistry()
	gatewayDirectoryRuntime, err := newGatewayDirectoryRuntime(metricsRegistry, pool, gatewayDirectoryConfig)
	if err != nil {
		logger.Error("gateway directory runtime initialization failed", "component", "gateway_directory", "reason", "initialization_failed")
		os.Exit(1)
	}
	metricsRegistry.MustRegister(controlauth.NewPrometheusCollector(authService.Metrics()))
	metricsRegistry.MustRegister(controlapi.NewAssetPrometheusCollector(assetMetrics))
	metricsRegistry.MustRegister(nodeDrivers.metrics)
	metricsRegistry.MustRegister(inventoryPollRuntime.collector)
	historyCollector, err := controlhistory.NewCollector(historyRuntime.repository)
	if err != nil {
		logger.Error("account inventory history metrics initialization failed", "component", "account_inventory_history")
		os.Exit(1)
	}
	metricsRegistry.MustRegister(historyCollector)
	jobCollector, err := controljobs.NewCollector(jobRepository)
	if err != nil {
		logger.Error("durable job metrics initialization failed", "component", "jobs")
		os.Exit(1)
	}
	metricsRegistry.MustRegister(jobCollector)
	crossNodeDuplicateMetricsCollector, err := assetstore.NewCrossNodeDuplicateOwnershipMetricsCollector(crossNodeDuplicateMetricsRepository)
	if err != nil {
		logger.Error("cross-node duplicate ownership metrics collector initialization failed", "component", "cross_node_duplicate_ownership")
		os.Exit(1)
	}
	metricsRegistry.MustRegister(crossNodeDuplicateMetricsCollector)
	router.Handle("/metrics", promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}))

	apiServer, err := controlapi.NewAuthenticatedServerWithAssetsJobsAndAccountInventory(
		version, authService, assetRepository, assetMetrics, jobRepository, accountInventoryRepository,
	)
	if err != nil {
		logger.Error("asset API initialization failed", "component", "assets")
		os.Exit(1)
	}
	apiServer.SetRelayBindingRepository(relayBindingRepository)
	if err := apiServer.SetGatewayLifecycleManager(gatewayLifecycleRepository); err != nil {
		logger.Error("gateway lifecycle API initialization failed", "component", "assets")
		os.Exit(1)
	}
	apiServer.SetCrossNodeDuplicateOwnershipOccurrenceReader(crossNodeDuplicateOccurrences)
	apiServer.SetNodeDuplicateHistoryReader(crossNodeDuplicateOccurrences)
	providerStates, err := assetstore.NewAccountInventoryProviderStateRepository(pool)
	if err != nil {
		logger.Error("provider state reader initialization failed", "component", "account_inventory")
		os.Exit(1)
	}
	apiServer.SetAccountInventoryProviderStateReader(providerStates)
	accountQuality, err := assetstore.NewAccountRequestQualityRepository(pool)
	if err != nil {
		logger.Error("account request quality reader initialization failed", "component", "account_request_quality")
		os.Exit(1)
	}
	apiServer.SetAccountAvailabilityReader(accountAvailability)
	apiServer.SetAccountQualityReader(accountQuality)
	apiServer.SetAccountQualityIncidentsReader(accountQuality)
	apiServer.SetAccountRequestHistoryReader(accountQuality)
	problemAccounts, err := assetstore.NewProblemAccountRepository(pool)
	if err != nil {
		logger.Error("problem account reader initialization failed", "component", "problem_accounts")
		os.Exit(1)
	}
	if err := apiServer.SetProblemAccountReader(problemAccounts); err != nil {
		logger.Error("problem account API initialization failed", "component", "problem_accounts")
		os.Exit(1)
	}
	pollCapacity, err := assetstore.NewInventoryPollCapacityRepository(pool)
	if err != nil {
		logger.Error("poll capacity reader initialization failed", "component", "account_inventory")
		os.Exit(1)
	}
	pollCapacityConfig, err := inventoryPollConfig.poll.Validate()
	if err != nil {
		logger.Error("poll capacity configuration is invalid", "component", "account_inventory")
		os.Exit(1)
	}
	apiServer.SetInventoryPollCapacityReader(pollCapacity, inventoryPollConfig.enabled, pollCapacityConfig)

	metricsRegistry.MustRegister(apiServer.AccountInventoryMetrics())
	controlapi.HandlerWithOptions(apiServer, controlapi.ChiServerOptions{BaseRouter: router, ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
		apiServer.PrepareGeneratedError(w, r, err)
	}})
	webHandler := webui.Handler()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		webHandler.ServeHTTP(w, r)
	})

	httpServer := &http.Server{
		Addr:              address,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var controlLoops sync.WaitGroup
	controlLoops.Add(2)
	go func() {
		defer controlLoops.Done()
		if runErr := worker.Run(shutdownContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
			logger.Error("durable job worker stopped", "component", "jobs", "reason", "worker_stopped")
		}
	}()
	go func() {
		defer controlLoops.Done()
		if runErr := reconciler.Run(shutdownContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
			logger.Error("durable job reconciler stopped", "component", "jobs", "reason", "reconciler_stopped")
		}
	}()
	controlLoops.Add(1)
	go func() {
		defer controlLoops.Done()
		_ = runAccountInventoryHistoryRuntime(shutdownContext, historyRuntime, logger)
	}()
	if requestQualityRuntime != nil {
		controlLoops.Add(1)
		go func() {
			defer controlLoops.Done()
			if runErr := requestQualityRuntime.Run(shutdownContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
				logger.Error("request quality collector stopped", "component", "account_request_quality")
			}
		}()
	}

	if inventoryPollRuntime.enabled {
		controlLoops.Add(1)
		go func() {
			defer controlLoops.Done()
			if runErr := inventoryPollRuntime.service.Run(shutdownContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
				logger.Error("account inventory poll stopped", "component", "account_inventory_poll", "reason", "runtime_stopped")
			}
		}()
	}
	if gatewayDirectoryRuntime.enabled {
		controlLoops.Add(1)
		go func() {
			defer controlLoops.Done()
			gatewayDirectoryRuntime.run(shutdownContext, logger)
		}()
	}

	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			logger.Error("http shutdown failed", "error", err)
		}
	}()

	logger.Info("control starting", "address", httpServer.Addr, "version", version)
	serveErr := httpServer.ListenAndServe()
	stop()
	controlLoops.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("control stopped unexpectedly", "error", serveErr)
		os.Exit(1)
	}
}

func newDatabasePool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := newDatabasePoolConfig(databaseURL, os.Getenv(databaseMaxConnsEnvironment))
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, config)
}

func newDatabasePoolConfig(databaseURL, maximumConnections string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("database configuration is invalid")
	}
	if maximumConnections != "" {
		parsed, parseErr := strconv.ParseInt(maximumConnections, 10, 32)
		if parseErr != nil || parsed < int64(databaseMaxConnsMinimum) || parsed > int64(databaseMaxConnsMaximum) {
			return nil, errors.New("database configuration is invalid")
		}
		config.MaxConns = int32(parsed)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET TIME ZONE 'UTC'")
		return err
	}
	return config, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseBool(value)
}

func loadAssetIntentKey(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("asset intent key file is unsafe")
	}
	value, err := os.ReadFile(path)
	if err != nil || len(value) != 32 {
		return nil, errors.New("asset intent key must contain exactly 32 bytes")
	}
	return value, nil
}

func envIntBounded(name string, fallback, minimum, maximum int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("configuration integer is out of range")
	}
	return parsed, nil
}

func envDurationBounded(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("configuration duration is out of range")
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
