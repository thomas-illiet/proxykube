package config

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	Proxy      ProxyConfig      `mapstructure:"proxy"`
	OTel       OTelConfig       `mapstructure:"otel"`
	Prometheus PrometheusConfig `mapstructure:"prometheus"`
	Logging    LoggingConfig    `mapstructure:"logging"`
}

type ProxyConfig struct {
	ListenAddress string        `mapstructure:"listenAddress"`
	AdminAddress  string        `mapstructure:"adminAddress"`
	DialTimeout   time.Duration `mapstructure:"dialTimeout"`
	IdleTimeout   time.Duration `mapstructure:"idleTimeout"`
	Allow         []Rule        `mapstructure:"allow"`
}

type Rule struct {
	Domain string `mapstructure:"domain"`
	Ports  []int  `mapstructure:"ports"`
}

type OTelConfig struct {
	Enabled     bool              `mapstructure:"enabled"`
	Endpoint    string            `mapstructure:"endpoint"`
	Insecure    bool              `mapstructure:"insecure"`
	Headers     map[string]string `mapstructure:"headers"`
	Timeout     time.Duration     `mapstructure:"timeout"`
	ServiceName string            `mapstructure:"serviceName"`
}

type PrometheusConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// NewViper creates an isolated Viper instance with proxykube defaults and environment mapping.
func NewViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("PROXYKUBE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("proxy.listenAddress", ":3128")
	v.SetDefault("proxy.adminAddress", ":9090")
	v.SetDefault("proxy.dialTimeout", "10s")
	v.SetDefault("proxy.idleTimeout", "90s")
	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.endpoint", "localhost:4317")
	v.SetDefault("otel.insecure", false)
	v.SetDefault("otel.timeout", "5s")
	v.SetDefault("otel.serviceName", "proxykube")
	v.SetDefault("prometheus.enabled", false)
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	return v
}

// Load merges flags, environment variables, a YAML file, and defaults into a validated Config.
func Load(v *viper.Viper, flags *pflag.FlagSet, path string) (Config, error) {
	bindings := map[string]string{
		"listen-address":     "proxy.listenAddress",
		"admin-address":      "proxy.adminAddress",
		"otel-enabled":       "otel.enabled",
		"prometheus-enabled": "prometheus.enabled",
		"log-level":          "logging.level",
		"log-format":         "logging.format",
	}
	for flag, key := range bindings {
		if err := v.BindPFlag(key, flags.Lookup(flag)); err != nil {
			return Config{}, fmt.Errorf("bind flag %s: %w", flag, err)
		}
	}
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate verifies addresses, timeouts, allowlist rules, telemetry, and logging settings.
func (c Config) Validate() error {
	if err := validateAddress("proxy.listenAddress", c.Proxy.ListenAddress); err != nil {
		return err
	}
	if err := validateAddress("proxy.adminAddress", c.Proxy.AdminAddress); err != nil {
		return err
	}
	if c.Proxy.ListenAddress == c.Proxy.AdminAddress {
		return fmt.Errorf("proxy listen and admin addresses must differ")
	}
	if c.Proxy.DialTimeout <= 0 || c.Proxy.IdleTimeout <= 0 {
		return fmt.Errorf("proxy timeouts must be positive")
	}
	for i, rule := range c.Proxy.Allow {
		if strings.TrimSpace(rule.Domain) == "" {
			return fmt.Errorf("proxy.allow[%d].domain is required", i)
		}
		if len(rule.Ports) == 0 {
			return fmt.Errorf("proxy.allow[%d].ports must not be empty", i)
		}
		for _, port := range rule.Ports {
			if port < 1 || port > 65535 {
				return fmt.Errorf("proxy.allow[%d] contains invalid port %d", i, port)
			}
		}
	}
	if c.OTel.Enabled && strings.TrimSpace(c.OTel.Endpoint) == "" {
		return fmt.Errorf("otel.endpoint is required when OpenTelemetry is enabled")
	}
	if c.OTel.Timeout <= 0 {
		return fmt.Errorf("otel.timeout must be positive")
	}
	if strings.TrimSpace(c.OTel.ServiceName) == "" {
		return fmt.Errorf("otel.serviceName is required")
	}
	if c.Logging.Format != "json" && c.Logging.Format != "text" {
		return fmt.Errorf("logging.format must be json or text")
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error")
	}
	return nil
}

// validateAddress verifies that an address uses the host:port form accepted by net.Listen.
func validateAddress(name, address string) error {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	return nil
}
