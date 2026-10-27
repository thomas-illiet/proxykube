package logging

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/thomasilliet/proxykube/internal/config"
	"go.opentelemetry.io/otel/trace"
)

// TestJSONLevelFiltering verifies structured output and severity filtering.
func TestJSONLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	logger := New(config.LoggingConfig{Level: "info", Format: "json"}, &output)
	logger.Debug("hidden")
	logger.InfoContext(context.Background(), "request", "domain", "example.com")
	text := output.String()
	if strings.Contains(text, "hidden") {
		t.Fatal("debug log should be filtered")
	}
	if !strings.Contains(text, `"domain":"example.com"`) {
		t.Fatalf("expected structured JSON log, got %s", text)
	}
}

// TestTextLoggerAndTraceContext verifies text output and trace correlation fields.
func TestTextLoggerAndTraceContext(t *testing.T) {
	var output bytes.Buffer
	logger := New(config.LoggingConfig{Level: "debug", Format: "text"}, &output)
	if WithTrace(context.Background(), logger) != logger {
		t.Fatal("context without span should return original logger")
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	WithTrace(ctx, logger).Debug("traced")
	text := output.String()
	if !strings.Contains(text, "trace_id=") || !strings.Contains(text, "span_id=") {
		t.Fatalf("missing trace correlation: %s", text)
	}
}
