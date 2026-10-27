package logging

import (
	"context"
	"io"
	"log/slog"

	"github.com/thomasilliet/proxykube/internal/config"
	"go.opentelemetry.io/otel/trace"
)

// New constructs a slog logger using the configured level and JSON or text handler.
func New(cfg config.LoggingConfig, output io.Writer) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	options := &slog.HandlerOptions{Level: levels[cfg.Level]}
	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(output, options))
	}
	return slog.New(slog.NewJSONHandler(output, options))
}

// WithTrace enriches a logger with trace and span identifiers when the context contains them.
func WithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return logger
	}
	return logger.With("trace_id", span.TraceID().String(), "span_id", span.SpanID().String())
}
