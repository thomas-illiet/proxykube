# Configuration reference

Configuration precedence, from highest to lowest, is:

1. Explicit Cobra flags.
2. `PROXYKUBE_*` environment variables.
3. The YAML file passed with `--config`.
4. Built-in defaults.

Nested keys become uppercase environment variables separated with underscores. Examples include `PROXYKUBE_PROXY_LISTENADDRESS`, `PROXYKUBE_OTEL_ENABLED`, and `PROXYKUBE_LOGGING_LEVEL`.

| Key | Default | Description |
| --- | --- | --- |
| `proxy.listenAddress` | `:3128` | Client-facing proxy listener. |
| `proxy.adminAddress` | `:9090` | Health and optional metrics listener. |
| `proxy.dialTimeout` | `10s` | Upstream connection and TLS handshake timeout. |
| `proxy.idleTimeout` | `90s` | Inbound HTTP idle timeout. |
| `proxy.allow` | `[]` | Allowed domain/port rules; empty denies all traffic. |
| `otel.enabled` | `false` | Enables OTLP traces and metrics. |
| `otel.endpoint` | `localhost:4317` | OTLP gRPC endpoint without a URL scheme. |
| `otel.insecure` | `false` | Disables TLS for OTLP transport. |
| `otel.headers` | `{}` | OTLP request headers, normally sourced from a Secret through environment variables. |
| `otel.timeout` | `5s` | Timeout applied to OTLP export operations. |
| `otel.serviceName` | `proxykube` | OpenTelemetry service name. |
| `prometheus.enabled` | `false` | Registers `/metrics` on the admin listener. |
| `logging.level` | `info` | `debug`, `info`, `warn`, or `error`. |
| `logging.format` | `json` | `json` or `text`. |

Durations use Go syntax, such as `250ms`, `10s`, or `2m`.

## CLI

```text
proxykube serve --config /etc/proxykube/config.yaml
  --listen-address :3128
  --admin-address :9090
  --otel-enabled
  --prometheus-enabled
  --log-level info
  --log-format json
```

Boolean flags accept an explicit value, so `--otel-enabled=false` can override a YAML value of `true`.
