# proxykube

`proxykube` is a fail-closed HTTP/HTTPS forward proxy for Kubernetes. It forwards plain HTTP requests and HTTPS `CONNECT` tunnels only when the requested hostname and port match an explicit allowlist. TLS traffic is never decrypted.

![ProxyKube architecture](docs/architecture.svg)

Detailed documentation:

- [Architecture and request flow](docs/architecture.md)
- [Configuration reference](docs/configuration.md)
- [Kubernetes deployment and operations](docs/deployment.md)

## Configuration

```yaml
proxy:
  listenAddress: ":3128"
  adminAddress: ":9090"
  dialTimeout: 10s
  idleTimeout: 90s
  allow:
    - domain: example.com
      ports: [80, 443]
    - domain: "*.example.org"
      ports: [443]

otel:
  enabled: false
  endpoint: otel-collector.observability.svc:4317
  insecure: false
  headers: {}
  timeout: 5s
  serviceName: proxykube

prometheus:
  enabled: false

logging:
  level: info
  format: json
```

An exact rule matches only that hostname. A wildcard matches subdomains but not its root: `*.example.org` matches `api.example.org`, not `example.org`. Ports are mandatory. Literal IP addresses are rejected unless listed explicitly. Allowed hostnames may resolve to public or private addresses.

Configuration precedence is: explicitly supplied Cobra flags, `PROXYKUBE_*` environment variables, YAML, then defaults. Nested environment keys use underscores, such as `PROXYKUBE_OTEL_ENABLED` and `PROXYKUBE_PROXY_LISTENADDRESS`.

Run locally:

```sh
go run ./cmd/proxykube serve --config ./config.yaml
```

Available overrides include `--listen-address`, `--admin-address`, `--otel-enabled`, `--prometheus-enabled`, `--log-level`, and `--log-format`.

Configure a client with:

```sh
export HTTP_PROXY=http://proxykube.default.svc:3128
export HTTPS_PROXY=http://proxykube.default.svc:3128
export NO_PROXY=.svc,.cluster.local
```

## Observability

Health endpoints are always available on the administration listener:

- `/healthz` indicates that the process is alive.
- `/readyz` indicates that both listeners started successfully.

OTLP traces and metrics are enabled with `otel.enabled`. Prometheus is independent and enabled with `prometheus.enabled`; only then is `/metrics` registered. Both are disabled by default and may be enabled together. Logs use `log/slog`, JSON by default, and include trace/span IDs when tracing is active. URLs, request bodies, credentials, and request headers are not recorded.

## Helm

```sh
helm upgrade --install proxykube ./charts/proxykube \
  --set-json 'proxy.allow=[{"domain":"example.com","ports":[80,443]}]'
```

The chart creates a two-replica Deployment, ClusterIP Service, ConfigMap, ServiceAccount, PodDisruptionBudget, and NetworkPolicy. Configuration changes alter a pod-template checksum and trigger a rolling update.

Prometheus annotations and the Prometheus Operator `ServiceMonitor` are independently selectable after metrics are enabled:

```yaml
prometheus:
  enabled: true
  scrapeAnnotations: false
  serviceMonitor:
    enabled: true
```

Narrow `networkPolicy.proxyIngress` and `networkPolicy.metricsIngress` with standard NetworkPolicy peer objects in production. Empty peer lists leave the corresponding in-cluster port reachable.

## Development

```sh
make test
make vet
make fmt-check
make docs-check
make helm-lint
docker build -t proxykube:dev .
```
