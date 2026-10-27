package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/thomasilliet/proxykube/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type Telemetry struct {
	Tracer         trace.Tracer
	Requests       metric.Int64Counter
	UpstreamErrors metric.Int64Counter
	Active         metric.Int64UpDownCounter
	Duration       metric.Float64Histogram
	PromHandler    http.Handler
	shutdown       []func(context.Context) error
}

var (
	mergeResources        = resource.Merge
	newOTLPMetricExporter = otlpmetricgrpc.New
	newOTLPTraceExporter  = otlptracegrpc.New
	newPrometheusExporter = func() (sdkmetric.Reader, http.Handler, error) {
		registry := prometheus.NewRegistry()
		exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
		return exporter, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), err
	}
)

// New initializes the optional OTLP and Prometheus exporters and the shared instruments.
func New(ctx context.Context, cfg config.Config) (*Telemetry, error) {
	res, err := mergeResources(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.OTel.ServiceName),
	))
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}

	result := &Telemetry{}
	var readers []sdkmetric.Reader
	if cfg.Prometheus.Enabled {
		exporter, handler, err := newPrometheusExporter()
		if err != nil {
			return nil, fmt.Errorf("create Prometheus exporter: %w", err)
		}
		readers = append(readers, exporter)
		result.PromHandler = handler
	}
	if cfg.OTel.Enabled {
		metricOptions := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.OTel.Endpoint),
			otlpmetricgrpc.WithHeaders(cfg.OTel.Headers),
			otlpmetricgrpc.WithTimeout(cfg.OTel.Timeout),
		}
		traceOptions := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.OTel.Endpoint),
			otlptracegrpc.WithHeaders(cfg.OTel.Headers),
			otlptracegrpc.WithTimeout(cfg.OTel.Timeout),
		}
		if cfg.OTel.Insecure {
			metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
			traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		}
		metricExporter, err := newOTLPMetricExporter(ctx, metricOptions...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		readers = append(readers, sdkmetric.NewPeriodicReader(metricExporter))

		traceExporter, err := newOTLPTraceExporter(ctx, traceOptions...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tracerProvider)
		result.shutdown = append(result.shutdown, tracerProvider.Shutdown)
	}

	meterOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, reader := range readers {
		meterOptions = append(meterOptions, sdkmetric.WithReader(reader))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)
	otel.SetMeterProvider(meterProvider)
	result.shutdown = append(result.shutdown, meterProvider.Shutdown)
	result.Tracer = otel.Tracer("proxykube/proxy")

	meter := otel.Meter("proxykube/proxy")
	result.Requests = mustInstrument(meter.Int64Counter("proxykube.requests", metric.WithDescription("Proxy requests by outcome")))
	result.UpstreamErrors = mustInstrument(meter.Int64Counter("proxykube.upstream.errors", metric.WithDescription("Upstream connection and request errors")))
	result.Active = mustInstrument(meter.Int64UpDownCounter("proxykube.connections.active", metric.WithDescription("Active proxy requests and tunnels")))
	result.Duration = mustInstrument(meter.Float64Histogram("proxykube.request.duration", metric.WithUnit("s"), metric.WithDescription("Proxy request and tunnel duration")))
	return result, nil
}

// mustInstrument returns a valid metric instrument and panics on a programming-time definition error.
func mustInstrument[T any](instrument T, err error) T {
	if err != nil {
		panic(err)
	}
	return instrument
}

// Record records the outcome and duration of a completed proxy operation.
func (t *Telemetry) Record(ctx context.Context, method, verdict string, status, port int, duration time.Duration) {
	attrs := metric.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("proxy.verdict", verdict),
		attribute.Int("http.response.status_code", status),
		attribute.Int("server.port", port),
	)
	t.Requests.Add(ctx, 1, attrs)
	t.Duration.Record(ctx, duration.Seconds(), attrs)
}

// Shutdown flushes and closes telemetry providers in reverse creation order.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var first error
	for i := len(t.shutdown) - 1; i >= 0; i-- {
		if err := t.shutdown[i](ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
