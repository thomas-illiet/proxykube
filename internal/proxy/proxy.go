package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thomasilliet/proxykube/internal/allowlist"
	"github.com/thomasilliet/proxykube/internal/logging"
	"github.com/thomasilliet/proxykube/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	allow     *allowlist.Allowlist
	logger    *slog.Logger
	telemetry *telemetry.Telemetry
	transport *http.Transport
	dialer    net.Dialer
}

// New creates a forward-proxy handler using an immutable allowlist and shared telemetry.
func New(allow *allowlist.Allowlist, logger *slog.Logger, tel *telemetry.Telemetry, dialTimeout time.Duration) *Handler {
	dialer := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &Handler{
		allow: allow, logger: logger, telemetry: tel, dialer: dialer,
		transport: &http.Transport{
			Proxy: nil, DialContext: dialer.DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 100,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: dialTimeout,
		},
	}
}

// CloseIdleConnections closes pooled outbound HTTP connections.
func (h *Handler) CloseIdleConnections() { h.transport.CloseIdleConnections() }

// ServeHTTP validates a destination and dispatches an HTTP request or CONNECT tunnel.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host, port, err := destination(r)
	if err != nil {
		http.Error(w, "invalid proxy destination", http.StatusBadRequest)
		h.telemetry.Record(r.Context(), r.Method, "invalid", http.StatusBadRequest, 0, time.Since(start))
		return
	}
	ctx, span := h.telemetry.Tracer.Start(r.Context(), "proxy "+r.Method)
	defer span.End()
	span.SetAttributes(attribute.String("server.address", host), attribute.Int("server.port", port), attribute.String("http.request.method", r.Method))
	ctx = context.WithValue(ctx, methodKey{}, r.Method)
	r = r.WithContext(ctx)

	if !h.allow.Allowed(host, port) {
		span.SetAttributes(attribute.String("proxy.verdict", "denied"))
		http.Error(w, "destination is not allowed", http.StatusForbidden)
		h.finish(r.Context(), host, port, "denied", http.StatusForbidden, start, nil)
		return
	}
	span.SetAttributes(attribute.String("proxy.verdict", "allowed"))
	h.telemetry.Active.Add(ctx, 1, metric.WithAttributes(attribute.String("http.request.method", r.Method)))
	defer h.telemetry.Active.Add(ctx, -1, metric.WithAttributes(attribute.String("http.request.method", r.Method)))
	if r.Method == http.MethodConnect {
		h.connect(w, r, host, port, start)
		return
	}
	h.forward(w, r, host, port, start)
}

// forward sends an allowed plain HTTP request to its upstream destination.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, host string, port int, start time.Time) {
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		http.Error(w, "unsupported URL scheme", http.StatusBadRequest)
		h.finish(r.Context(), host, port, "invalid", http.StatusBadRequest, start, nil)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header = r.Header.Clone()
	stripHopHeaders(out.Header)
	response, err := h.transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		h.finish(r.Context(), host, port, "allowed", http.StatusBadGateway, start, err)
		return
	}
	defer response.Body.Close()
	stripHopHeaders(response.Header)
	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, copyErr := io.Copy(w, response.Body)
	h.finish(r.Context(), host, port, "allowed", response.StatusCode, start, copyErr)
}

// connect establishes and relays an allowed opaque TCP tunnel for HTTPS.
func (h *Handler) connect(w http.ResponseWriter, r *http.Request, host string, port int, start time.Time) {
	upstream, err := h.dialer.DialContext(r.Context(), "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		http.Error(w, "upstream connection failed", http.StatusBadGateway)
		h.finish(r.Context(), host, port, "allowed", http.StatusBadGateway, start, err)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunneling is not supported", http.StatusInternalServerError)
		h.finish(r.Context(), host, port, "allowed", http.StatusInternalServerError, start, nil)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		h.finish(r.Context(), host, port, "allowed", http.StatusInternalServerError, start, err)
		return
	}
	defer client.Close()
	if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
		err = buffer.Flush()
	}
	if err == nil {
		err = tunnel(client, buffer, upstream)
	}
	h.finish(r.Context(), host, port, "allowed", http.StatusOK, start, err)
}

// tunnel copies bytes bidirectionally until both sides close.
func tunnel(client net.Conn, buffered *bufio.ReadWriter, upstream net.Conn) error {
	errors := make(chan error, 2)
	go func() { _, err := io.Copy(upstream, buffered); closeWrite(upstream); errors <- err }()
	go func() { _, err := io.Copy(client, upstream); closeWrite(client); errors <- err }()
	first := <-errors
	second := <-errors
	if first != nil {
		return first
	}
	return second
}

// closeWrite half-closes TCP connections and safely ignores other connection types.
func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

// finish emits bounded metrics and a structured completion log.
func (h *Handler) finish(ctx context.Context, host string, port int, verdict string, status int, start time.Time, err error) {
	duration := time.Since(start)
	h.telemetry.Record(ctx, requestMethod(ctx), verdict, status, port, duration)
	logger := logging.WithTrace(ctx, h.logger)
	args := []any{"domain", host, "port", port, "method", requestMethod(ctx), "verdict", verdict, "status", status, "duration_ms", duration.Milliseconds()}
	if err != nil {
		args = append(args, "error", err)
		h.telemetry.UpstreamErrors.Add(ctx, 1)
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, "proxy request failed")
		logger.ErrorContext(ctx, "proxy request completed", args...)
		return
	}
	logger.InfoContext(ctx, "proxy request completed", args...)
}

type methodKey struct{}

// requestMethod retrieves the method attached to a request context.
func requestMethod(ctx context.Context) string {
	if method, ok := ctx.Value(methodKey{}).(string); ok {
		return method
	}
	return "UNKNOWN"
}

// destination extracts and validates the target host and effective port from a proxy request.
func destination(r *http.Request) (string, int, error) {
	address := r.URL.Host
	defaultPort := 80
	if r.Method == http.MethodConnect {
		address = r.Host
		defaultPort = 443
	} else if r.URL.Scheme == "https" {
		defaultPort = 443
	}
	if address == "" {
		return "", 0, fmt.Errorf("missing host")
	}
	host := address
	port := defaultPort
	if parsedHost, parsedPort, err := net.SplitHostPort(address); err == nil {
		host = parsedHost
		value, err := strconv.Atoi(parsedPort)
		if err != nil || value < 1 || value > 65535 {
			return "", 0, fmt.Errorf("invalid port")
		}
		port = value
	} else if strings.Contains(address, ":") {
		return "", 0, fmt.Errorf("invalid address")
	}
	return strings.Trim(host, "[]"), port, nil
}

// stripHopHeaders removes headers that must not be forwarded by an HTTP intermediary.
func stripHopHeaders(headers http.Header) {
	for _, name := range strings.Split(headers.Get("Connection"), ",") {
		headers.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}

// copyHeaders appends all source header values to a destination header map.
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
