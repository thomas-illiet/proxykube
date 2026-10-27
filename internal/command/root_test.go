package command

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thomasilliet/proxykube/internal/config"
	"github.com/thomasilliet/proxykube/internal/telemetry"
)

// TestCommandConstructionAndValidationError verifies invalid CLI configuration handling.
func TestCommandConstructionAndValidationError(t *testing.T) {
	cmd := New()
	cmd.SetArgs([]string{"serve", "--listen-address", "bad"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid address error")
	}
}

// TestServeCommandRuns verifies successful Cobra-to-runtime delegation.
func TestServeCommandRuns(t *testing.T) {
	cmd := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"serve", "--listen-address", "127.0.0.1:0", "--admin-address", "localhost:0", "--log-format", "text"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

// TestAdminHandlerReadiness verifies readiness transitions and optional metrics routing.
func TestAdminHandlerReadiness(t *testing.T) {
	var ready atomic.Bool
	handler := newAdminHandler(&ready, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status=%d", response.Code)
	}
	ready.Store(true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("metrics status=%d", response.Code)
	}
}

// TestRunGracefulShutdown verifies cancellation-driven server shutdown.
func TestRunGracefulShutdown(t *testing.T) {
	cfg := commandConfig()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

// TestRunErrors covers allowlist and listener initialization failures.
func TestRunErrors(t *testing.T) {
	t.Run("allowlist", func(t *testing.T) {
		cfg := commandConfig()
		cfg.Proxy.Allow = []config.Rule{{Domain: "invalid", Ports: []int{443}}}
		if err := run(context.Background(), cfg); err == nil {
			t.Fatal("expected allowlist error")
		}
	})
	t.Run("proxy listener", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		cfg := commandConfig()
		cfg.Proxy.ListenAddress = listener.Addr().String()
		if err := run(context.Background(), cfg); err == nil {
			t.Fatal("expected listener error")
		}
	})
	t.Run("admin listener", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		cfg := commandConfig()
		cfg.Proxy.AdminAddress = listener.Addr().String()
		if err := run(context.Background(), cfg); err == nil {
			t.Fatal("expected listener error")
		}
	})
}

// TestServeIgnoresServerClosed verifies that expected HTTP shutdown is not reported.
func TestServeIgnoresServerClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{}
	errorsCh := make(chan error, 1)
	go serve(server, listener, errorsCh)
	_ = server.Close()
	select {
	case err := <-errorsCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestRunEndpoints exercises live health, readiness, and Prometheus endpoints.
func TestRunEndpoints(t *testing.T) {
	proxyAddress := freeAddress(t)
	adminAddress := freeAddress(t)
	cfg := commandConfig()
	cfg.Proxy.ListenAddress = proxyAddress
	cfg.Proxy.AdminAddress = adminAddress
	cfg.Prometheus.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	var response *http.Response
	var err error
	for i := 0; i < 50; i++ {
		response, err = http.Get("http://" + adminAddress + "/readyz")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ready status=%d", response.StatusCode)
	}
	for _, path := range []string{"/healthz", "/metrics"} {
		response, err = http.Get("http://" + adminAddress + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.StatusCode)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestInjectedRunErrors covers telemetry, shutdown, and serve failures.
func TestInjectedRunErrors(t *testing.T) {
	originalTelemetry := newTelemetry
	originalShutdownTelemetry := shutdownTelemetry
	originalShutdownServer := shutdownServer
	originalListen := listenTCP
	defer func() {
		newTelemetry = originalTelemetry
		shutdownTelemetry = originalShutdownTelemetry
		shutdownServer = originalShutdownServer
		listenTCP = originalListen
	}()
	want := errors.New("injected")

	newTelemetry = func(context.Context, config.Config) (*telemetry.Telemetry, error) { return nil, want }
	if err := run(context.Background(), commandConfig()); !errors.Is(err, want) {
		t.Fatalf("telemetry error=%v", err)
	}
	newTelemetry = originalTelemetry

	shutdownTelemetry = func(*telemetry.Telemetry, context.Context) error { return want }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, commandConfig()); err != nil {
		t.Fatal(err)
	}
	shutdownTelemetry = originalShutdownTelemetry

	shutdownServer = func(*http.Server, context.Context) error { return want }
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, commandConfig()); !errors.Is(err, want) {
		t.Fatalf("shutdown error=%v", err)
	}
	shutdownServer = originalShutdownServer

	listenTCP = func(string, string) (net.Listener, error) { return failingListener{}, nil }
	if err := run(context.Background(), commandConfig()); !errors.Is(err, wantAccept) {
		t.Fatalf("serve error=%v", err)
	}
}

var wantAccept = errors.New("accept")

type failingListener struct{}

// Accept implements net.Listener with a deterministic failure.
func (failingListener) Accept() (net.Conn, error) { return nil, wantAccept }

// Close implements net.Listener without owning external resources.
func (failingListener) Close() error { return nil }

// Addr returns the listener's synthetic address.
func (failingListener) Addr() net.Addr { return testAddress("test") }

type testAddress string

// Network returns the synthetic network name.
func (a testAddress) Network() string { return string(a) }

// String renders the synthetic listener address.
func (a testAddress) String() string { return string(a) }

// freeAddress reserves and releases an ephemeral address for a live-server test.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// commandConfig returns a minimal valid runtime configuration for command tests.
func commandConfig() config.Config {
	return config.Config{
		Proxy:   config.ProxyConfig{ListenAddress: "127.0.0.1:0", AdminAddress: "localhost:0", DialTimeout: time.Second, IdleTimeout: time.Second},
		OTel:    config.OTelConfig{Timeout: time.Second, ServiceName: "test"},
		Logging: config.LoggingConfig{Level: "error", Format: "json"},
	}
}
