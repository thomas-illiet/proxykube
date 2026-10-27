package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thomasilliet/proxykube/internal/allowlist"
	"github.com/thomasilliet/proxykube/internal/config"
	"github.com/thomasilliet/proxykube/internal/logging"
	proxyhandler "github.com/thomasilliet/proxykube/internal/proxy"
	"github.com/thomasilliet/proxykube/internal/telemetry"
)

var (
	listenTCP         = net.Listen
	newTelemetry      = telemetry.New
	shutdownTelemetry = func(t *telemetry.Telemetry, ctx context.Context) error { return t.Shutdown(ctx) }
	shutdownServer    = func(server *http.Server, ctx context.Context) error { return server.Shutdown(ctx) }
)

// New builds the root Cobra command and registers all proxykube subcommands.
func New() *cobra.Command {
	root := &cobra.Command{
		Use:           "proxykube",
		Short:         "Allowlist-based HTTP and HTTPS forward proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newServeCommand())
	return root
}

// newServeCommand builds the serve command and binds its configuration overrides.
func newServeCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the proxy and administration servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := config.NewViper()
			cfg, err := config.Load(v, cmd.Flags(), configPath)
			if err != nil {
				return err
			}
			return run(cmd.Context(), cfg)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&configPath, "config", "", "path to the YAML configuration file")
	flags.String("listen-address", "", "proxy listen address (overrides configuration)")
	flags.String("admin-address", "", "administration listen address (overrides configuration)")
	flags.Bool("otel-enabled", false, "enable the OTLP trace and metric exporters")
	flags.Bool("prometheus-enabled", false, "enable the Prometheus /metrics endpoint")
	flags.String("log-level", "", "log level: debug, info, warn, or error")
	flags.String("log-format", "", "log format: json or text")
	return cmd
}

// run initializes dependencies, starts both listeners, and performs graceful shutdown.
func run(parent context.Context, cfg config.Config) error {
	logger := logging.New(cfg.Logging, os.Stdout)
	slog.SetDefault(logger)
	list, err := allowlist.New(cfg.Proxy.Allow)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tel, err := newTelemetry(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := shutdownTelemetry(tel, shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", "error", err)
		}
	}()

	handler := proxyhandler.New(list, logger, tel, cfg.Proxy.DialTimeout)
	defer handler.CloseIdleConnections()
	proxyServer := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: cfg.Proxy.IdleTimeout,
	}
	var ready atomic.Bool
	adminMux := newAdminHandler(&ready, tel.PromHandler)
	adminServer := &http.Server{Handler: adminMux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}

	proxyListener, err := listenTCP("tcp", cfg.Proxy.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on proxy address: %w", err)
	}
	defer proxyListener.Close()
	adminListener, err := listenTCP("tcp", cfg.Proxy.AdminAddress)
	if err != nil {
		return fmt.Errorf("listen on admin address: %w", err)
	}
	defer adminListener.Close()

	errorsCh := make(chan error, 2)
	go serve(proxyServer, proxyListener, errorsCh)
	go serve(adminServer, adminListener, errorsCh)
	ready.Store(true)
	logger.Info("proxykube started", "proxy_address", cfg.Proxy.ListenAddress, "admin_address", cfg.Proxy.AdminAddress, "otel_enabled", cfg.OTel.Enabled, "prometheus_enabled", cfg.Prometheus.Enabled)

	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		cancel()
		return err
	}
	ready.Store(false)
	shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	proxyErr := shutdownServer(proxyServer, shutdownCtx)
	adminErr := shutdownServer(adminServer, shutdownCtx)
	if proxyErr != nil {
		return proxyErr
	}
	return adminErr
}

// newAdminHandler creates the health, readiness, and optional Prometheus routes.
func newAdminHandler(ready *atomic.Bool, prometheusHandler http.Handler) http.Handler {
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	adminMux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	if prometheusHandler != nil {
		adminMux.Handle("/metrics", prometheusHandler)
	}
	return adminMux
}

// serve runs one HTTP server and reports unexpected termination.
func serve(server *http.Server, listener net.Listener, errorsCh chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errorsCh <- err
	}
}
