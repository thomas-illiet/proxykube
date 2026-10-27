package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thomasilliet/proxykube/internal/allowlist"
	"github.com/thomasilliet/proxykube/internal/config"
	"github.com/thomasilliet/proxykube/internal/telemetry"
)

// TestHTTPForwardAndDeny verifies forwarding, header stripping, and deny-by-default behavior.
func TestHTTPForwardAndDeny(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy authorization header leaked upstream")
		}
		_, _ = w.Write([]byte("upstream"))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	_, portText, _ := strings.Cut(upstreamURL.Host, ":")
	var port int
	_, _ = fmt.Sscan(portText, &port)

	tel, err := telemetry.New(context.Background(), config.Config{OTel: config.OTelConfig{Timeout: time.Second, ServiceName: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.Shutdown(context.Background())
	list, err := allowlist.New([]config.Rule{{Domain: upstreamURL.Hostname(), Ports: []int{port}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(list, slog.New(slog.NewTextHandler(io.Discard, nil)), tel, time.Second))
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	request, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	request.Header.Set("Proxy-Authorization", "secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "upstream" {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}

	denied, err := client.Get("http://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("denied status=%d", denied.StatusCode)
	}
}

// TestHTTPSConnectTunnel verifies end-to-end opaque TLS tunneling through CONNECT.
func TestHTTPSConnectTunnel(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	_, portText, _ := strings.Cut(upstreamURL.Host, ":")
	var port int
	_, _ = fmt.Sscan(portText, &port)

	tel, err := telemetry.New(context.Background(), config.Config{OTel: config.OTelConfig{Timeout: time.Second, ServiceName: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.Shutdown(context.Background())
	list, err := allowlist.New([]config.Rule{{Domain: upstreamURL.Hostname(), Ports: []int{port}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(list, slog.New(slog.NewTextHandler(io.Discard, nil)), tel, time.Second))
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test server certificate only.
	}}

	response, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "secure" {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
}

// TestErrorPathsAndHelpers covers malformed requests and unavailable upstreams.
func TestErrorPathsAndHelpers(t *testing.T) {
	tel, err := telemetry.New(context.Background(), config.Config{OTel: config.OTelConfig{Timeout: time.Second, ServiceName: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.Shutdown(context.Background())
	list, _ := allowlist.New([]config.Rule{{Domain: "example.com", Ports: []int{1, 443}}})
	handler := New(list, slog.New(slog.NewTextHandler(io.Discard, nil)), tel, 10*time.Millisecond)
	handler.CloseIdleConnections()

	tests := []struct {
		name, method, target, host string
		status                     int
	}{
		{"invalid destination", http.MethodGet, "/relative", "", http.StatusBadRequest},
		{"unsupported scheme", http.MethodGet, "ftp://example.com:1/file", "", http.StatusBadRequest},
		{"upstream failure", http.MethodGet, "http://example.com:1/", "", http.StatusBadGateway},
		{"connect failure", http.MethodConnect, "http://unused", "example.com:1", http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.name == "bad port" || tt.name == "unbracketed colon" {
				host := "example.com:bad"
				if tt.name == "unbracketed colon" {
					host = "bad:host:name"
				}
				req = &http.Request{Method: tt.method, URL: &url.URL{Scheme: "http", Host: host}}
			} else {
				req = httptest.NewRequest(tt.method, tt.target, nil)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.status {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	if got := requestMethod(context.Background()); got != "UNKNOWN" {
		t.Fatalf("method=%q", got)
	}
}

// TestDestination verifies host and effective-port extraction for all request forms.
func TestDestination(t *testing.T) {
	tests := []struct {
		name, method, target, host string
		wantPort                   int
		wantErr                    bool
	}{
		{"http default", http.MethodGet, "http://example.com/a", "", 80, false},
		{"https default", http.MethodGet, "https://example.com/a", "", 443, false},
		{"explicit", http.MethodGet, "http://example.com:8080/a", "", 8080, false},
		{"connect", http.MethodConnect, "http://unused", "example.com:8443", 8443, false},
		{"missing", http.MethodGet, "/relative", "", 0, true},
		{"bad port", http.MethodGet, "http://example.com:bad/a", "", 0, true},
		{"unbracketed colon", http.MethodGet, "http://bad:host:name/a", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.name == "bad port" || tt.name == "unbracketed colon" {
				host := "example.com:bad"
				if tt.name == "unbracketed colon" {
					host = "bad:host:name"
				}
				req = &http.Request{Method: tt.method, URL: &url.URL{Scheme: "http", Host: host}}
			} else {
				req = httptest.NewRequest(tt.method, tt.target, nil)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			_, port, err := destination(req)
			if (err != nil) != tt.wantErr || port != tt.wantPort {
				t.Fatalf("port=%d err=%v", port, err)
			}
		})
	}
}

// TestConnectWriterErrors covers unsupported and failing HTTP connection hijacking.
func TestConnectWriterErrors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	tel, _ := telemetry.New(context.Background(), config.Config{OTel: config.OTelConfig{Timeout: time.Second, ServiceName: "test"}})
	defer tel.Shutdown(context.Background())
	list, _ := allowlist.New(nil)
	handler := New(list, slog.New(slog.NewTextHandler(io.Discard, nil)), tel, time.Second)
	req := httptest.NewRequest(http.MethodConnect, "http://unused", nil)
	req.Host = listener.Addr().String()
	req = req.WithContext(context.WithValue(req.Context(), methodKey{}, http.MethodConnect))
	handler.connect(httptest.NewRecorder(), req, "127.0.0.1", port, time.Now())

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener2.Close()
	go func() {
		conn, acceptErr := listener2.Accept()
		if acceptErr == nil {
			defer conn.Close()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	_, portText, _ = net.SplitHostPort(listener2.Addr().String())
	port, _ = strconv.Atoi(portText)
	handler.connect(hijackFailure{ResponseWriter: httptest.NewRecorder()}, req, "127.0.0.1", port, time.Now())
}

// TestTunnelReturnsCopyError verifies propagation of bidirectional copy failures.
func TestTunnelReturnsCopyError(t *testing.T) {
	one := &errorConn{}
	two := &errorConn{}
	buffered := bufio.NewReadWriter(bufio.NewReader(one), bufio.NewWriter(one))
	if err := tunnel(one, buffered, two); err == nil {
		t.Fatal("expected copy error")
	}
}

type hijackFailure struct{ http.ResponseWriter }

// Hijack implements http.Hijacker and always returns a controlled failure.
func (h hijackFailure) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack")
}

type errorConn struct{}

// Read implements net.Conn with a deterministic read failure.
func (*errorConn) Read([]byte) (int, error) { return 0, errors.New("read") }

// Write implements net.Conn and accepts the complete payload.
func (*errorConn) Write(p []byte) (int, error) { return len(p), nil }

// Close implements net.Conn without owning external resources.
func (*errorConn) Close() error { return nil }

// LocalAddr returns the synthetic local test address.
func (*errorConn) LocalAddr() net.Addr { return testAddr("local") }

// RemoteAddr returns the synthetic remote test address.
func (*errorConn) RemoteAddr() net.Addr { return testAddr("remote") }

// SetDeadline implements net.Conn without applying a deadline.
func (*errorConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline implements net.Conn without applying a deadline.
func (*errorConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline implements net.Conn without applying a deadline.
func (*errorConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

// Network returns the synthetic network name.
func (a testAddr) Network() string { return string(a) }

// String renders the synthetic address.
func (a testAddr) String() string { return string(a) }
