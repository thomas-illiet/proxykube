package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

// TestLoadPrecedence verifies flag, environment, file, and default precedence.
func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
proxy:
  listenAddress: ":4000"
  adminAddress: ":4001"
  allow:
    - domain: example.com
      ports: [80]
otel:
  enabled: false
prometheus:
  enabled: false
logging:
  level: warn
  format: json
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXYKUBE_LOGGING_LEVEL", "debug")

	flags := testFlags()
	if err := flags.Set("listen-address", ":5000"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(NewViper(), flags, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.ListenAddress != ":5000" {
		t.Fatalf("flag did not override file: %q", cfg.Proxy.ListenAddress)
	}
	if cfg.Proxy.AdminAddress != ":4001" {
		t.Fatalf("file value not loaded: %q", cfg.Proxy.AdminAddress)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("environment did not override file: %q", cfg.Logging.Level)
	}
}

// TestValidateRejectsBadRule verifies rejection of an out-of-range port.
func TestValidateRejectsBadRule(t *testing.T) {
	cfg := validConfig()
	cfg.Proxy.Allow = []Rule{{Domain: "example.com", Ports: []int{0}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid port error")
	}
}

// TestLoadErrors covers flag binding, file reading, decoding, and validation failures.
func TestLoadErrors(t *testing.T) {
	t.Run("missing flag", func(t *testing.T) {
		_, err := Load(NewViper(), pflag.NewFlagSet("empty", pflag.ContinueOnError), "")
		if err == nil {
			t.Fatal("expected bind error")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := Load(NewViper(), testFlags(), filepath.Join(t.TempDir(), "missing.yaml"))
		if err == nil {
			t.Fatal("expected read error")
		}
	})
	t.Run("decode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte("proxy: invalid\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(NewViper(), testFlags(), path)
		if err == nil {
			t.Fatal("expected decode error")
		}
	})
	t.Run("validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.yaml")
		if err := os.WriteFile(path, []byte("proxy:\n  listenAddress: bad\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(NewViper(), testFlags(), path)
		if err == nil {
			t.Fatal("expected validation error")
		}
	})
}

// TestValidateAllFailures exercises every configuration validation decision.
func TestValidateAllFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"listen address", func(c *Config) { c.Proxy.ListenAddress = "bad" }},
		{"admin address", func(c *Config) { c.Proxy.AdminAddress = "bad" }},
		{"same addresses", func(c *Config) { c.Proxy.AdminAddress = c.Proxy.ListenAddress }},
		{"dial timeout", func(c *Config) { c.Proxy.DialTimeout = 0 }},
		{"idle timeout", func(c *Config) { c.Proxy.IdleTimeout = 0 }},
		{"missing domain", func(c *Config) { c.Proxy.Allow = []Rule{{Ports: []int{443}}} }},
		{"missing ports", func(c *Config) { c.Proxy.Allow = []Rule{{Domain: "example.com"}} }},
		{"large port", func(c *Config) { c.Proxy.Allow = []Rule{{Domain: "example.com", Ports: []int{65536}}} }},
		{"otel endpoint", func(c *Config) { c.OTel.Enabled = true; c.OTel.Endpoint = "" }},
		{"otel timeout", func(c *Config) { c.OTel.Timeout = 0 }},
		{"service name", func(c *Config) { c.OTel.ServiceName = "" }},
		{"log format", func(c *Config) { c.Logging.Format = "xml" }},
		{"log level", func(c *Config) { c.Logging.Level = "trace" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	for _, level := range []string{"debug", "info", "warn", "error"} {
		cfg := validConfig()
		cfg.Logging.Level = level
		if err := cfg.Validate(); err != nil {
			t.Fatalf("level %s: %v", level, err)
		}
	}
}

// testFlags constructs the same override flags used by the serve command.
func testFlags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("listen-address", "", "")
	flags.String("admin-address", "", "")
	flags.Bool("otel-enabled", false, "")
	flags.Bool("prometheus-enabled", false, "")
	flags.String("log-level", "", "")
	flags.String("log-format", "", "")
	return flags
}

// validConfig returns the minimal valid configuration used as a test baseline.
func validConfig() Config {
	return Config{
		Proxy:   ProxyConfig{ListenAddress: ":3128", AdminAddress: ":9090", DialTimeout: 1, IdleTimeout: 1},
		OTel:    OTelConfig{Endpoint: "localhost:4317", Timeout: 1, ServiceName: "proxykube"},
		Logging: LoggingConfig{Level: "info", Format: "json"},
	}
}
