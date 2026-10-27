# Kubernetes deployment

Install with an explicit allowlist:

```sh
helm upgrade --install proxykube ./charts/proxykube \
  --namespace proxykube --create-namespace \
  --set-json 'proxy.allow=[{"domain":"example.com","ports":[80,443]}]'
```

Configure workloads with `HTTP_PROXY` and `HTTPS_PROXY` pointing to `http://proxykube.proxykube.svc:3128`. Add Kubernetes service domains to `NO_PROXY` when they must bypass the proxy.

## Security guidance

- Keep the Service as `ClusterIP`; the proxy has no application authentication.
- Set `networkPolicy.proxyIngress` to the namespaces or pods allowed to use it.
- Set `networkPolicy.metricsIngress` to the Prometheus scraper identities.
- Treat changes to the allowlist as security-sensitive configuration.
- A permitted hostname may resolve to private or public IP addresses by design.
- Avoid secrets in the ConfigMap. Supply sensitive OTLP headers through environment variables from a Secret.

The pod runs as non-root with a read-only root filesystem, RuntimeDefault seccomp profile, no service-account token, and all Linux capabilities dropped.

## Prometheus Operator

```yaml
prometheus:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s
```

Without Prometheus Operator, enable `scrapeAnnotations` or configure the Service endpoint manually. `/metrics` returns `404` while Prometheus support is disabled.

## Verification

```sh
kubectl -n proxykube port-forward svc/proxykube 3128:3128 9090:9090
curl --proxy http://127.0.0.1:3128 https://example.com/
curl http://127.0.0.1:9090/healthz
curl http://127.0.0.1:9090/readyz
```
