package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thomasilliet/proxykube/internal/config"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// TestPrometheusOptional verifies conditional handler registration and safe labels.
func TestPrometheusOptional(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			cfg := telemetryConfig(enabled)
			tel, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer tel.Shutdown(context.Background())
			if enabled != (tel.PromHandler != nil) {
				t.Fatalf("handler presence mismatch")
			}
			if !enabled {
				return
			}
			tel.Record(context.Background(), http.MethodGet, "allowed", 200, 80, time.Second)
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			response := httptest.NewRecorder()
			tel.PromHandler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if !strings.Contains(response.Body.String(), "proxykube_requests_total") {
				t.Fatalf("metric missing: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "server_address") {
				t.Fatal("domain must not be a Prometheus label")
			}
		})
	}
}

// TestExporterCombinations covers all independent OTLP and Prometheus states.
func TestExporterCombinations(t *testing.T) {
	for _, otelEnabled := range []bool{false, true} {
		for _, prometheusEnabled := range []bool{false, true} {
			name := fmt.Sprintf("otel=%t/prometheus=%t", otelEnabled, prometheusEnabled)
			t.Run(name, func(t *testing.T) {
				cfg := telemetryConfig(prometheusEnabled)
				cfg.OTel.Enabled = otelEnabled
				cfg.OTel.Endpoint = "127.0.0.1:1"
				cfg.OTel.Insecure = true
				cfg.OTel.Timeout = 10 * time.Millisecond
				tel, err := New(context.Background(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				tel.Record(context.Background(), http.MethodGet, "allowed", 200, 443, time.Millisecond)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = tel.Shutdown(shutdownCtx)
			})
		}
	}
}

// telemetryConfig returns a minimal telemetry configuration for tests.
func telemetryConfig(prometheus bool) config.Config {
	return config.Config{
		OTel:       config.OTelConfig{Enabled: false, Endpoint: "localhost:4317", Timeout: time.Second, ServiceName: "proxykube-test"},
		Prometheus: config.PrometheusConfig{Enabled: prometheus},
	}
}

// TestNewErrorPaths verifies initialization failures from every external constructor.
func TestNewErrorPaths(t *testing.T) {
	originalMerge := mergeResources
	originalProm := newPrometheusExporter
	originalMetric := newOTLPMetricExporter
	originalTrace := newOTLPTraceExporter
	defer func() {
		mergeResources = originalMerge
		newPrometheusExporter = originalProm
		newOTLPMetricExporter = originalMetric
		newOTLPTraceExporter = originalTrace
	}()
	want := errors.New("injected")

	mergeResources = func(*resource.Resource, *resource.Resource) (*resource.Resource, error) { return nil, want }
	if _, err := New(context.Background(), telemetryConfig(false)); err == nil {
		t.Fatal("expected resource error")
	}
	mergeResources = originalMerge

	newPrometheusExporter = func() (sdkmetric.Reader, http.Handler, error) { return nil, nil, want }
	if _, err := New(context.Background(), telemetryConfig(true)); err == nil {
		t.Fatal("expected Prometheus error")
	}
	newPrometheusExporter = originalProm

	cfg := telemetryConfig(false)
	cfg.OTel.Enabled = true
	cfg.OTel.Insecure = true
	newOTLPMetricExporter = func(context.Context, ...otlpmetricgrpc.Option) (*otlpmetricgrpc.Exporter, error) { return nil, want }
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("expected metric exporter error")
	}
	newOTLPMetricExporter = originalMetric

	newOTLPTraceExporter = func(context.Context, ...otlptracegrpc.Option) (*otlptrace.Exporter, error) { return nil, want }
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("expected trace exporter error")
	}
}

// TestMustInstrumentAndShutdownError verifies invalid instrument definitions panic.
func TestMustInstrumentAndShutdownError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = mustInstrument(0, errors.New("invalid instrument"))
}

// TestShutdownReturnsFirstError verifies that shutdown preserves the first failure.
func TestShutdownReturnsFirstError(t *testing.T) {
	want := errors.New("shutdown")
	tel := &Telemetry{shutdown: []func(context.Context) error{
		func(context.Context) error { return want },
		func(context.Context) error { return nil },
	}}
	if err := tel.Shutdown(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}
