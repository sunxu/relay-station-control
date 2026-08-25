package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	controlapi "github.com/sunxu/relay-station-control/internal/api"
	controlauth "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/webui"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	address := envOrDefault("CONTROL_HTTP_ADDR", "127.0.0.1:8080")
	cookieSecure, err := envBool("CONTROL_COOKIE_SECURE", false)
	if err != nil {
		logger.Error("invalid control configuration", "component", "auth")
		os.Exit(1)
	}
	mfaRequired, err := envBool("CONTROL_MFA_REQUIRED", false)
	if err != nil {
		logger.Error("invalid control configuration", "component", "auth")
		os.Exit(1)
	}
	config, err := (controlauth.Config{
		Environment:         controlauth.Environment(envOrDefault("CONTROL_ENVIRONMENT", "dev")),
		BindAddress:         address,
		BootstrapSecretFile: os.Getenv("CONTROL_BOOTSTRAP_SECRET_FILE"),
		AuthKeyringFile:     os.Getenv("CONTROL_AUTH_KEYRING_FILE"),
		TrustedProxyCIDRs:   splitCSV(os.Getenv("CONTROL_TRUSTED_PROXY_CIDRS")),
		CookieSecure:        cookieSecure,
		MFARequired:         mfaRequired,
	}).Validate()
	if err != nil {
		logger.Error("invalid control configuration", "component", "auth")
		os.Exit(1)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("database configuration is required", "component", "auth")
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		logger.Error("database initialization failed", "component", "auth")
		os.Exit(1)
	}
	defer pool.Close()
	if err = pool.Ping(context.Background()); err != nil {
		logger.Error("database unavailable", "component", "auth")
		os.Exit(1)
	}
	authService, err := controlauth.NewService(pool, config)
	if err != nil {
		logger.Error("authentication initialization failed", "component", "auth")
		os.Exit(1)
	}

	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))
	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(controlauth.NewPrometheusCollector(authService.Metrics()))
	router.Handle("/metrics", promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}))

	apiServer := controlapi.NewAuthenticatedServer(version, authService)
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

	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			logger.Error("http shutdown failed", "error", err)
		}
	}()

	logger.Info("control starting", "address", httpServer.Addr, "version", version)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("control stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseBool(value)
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
