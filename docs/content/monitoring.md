# Monitoring

## Metrics

Prometheus metrics are served on `/metrics`, labelled by route template so a
per-user path does not create a time series per user. Scrapers that send an
OpenMetrics `Accept` header (Prometheus does by default) get the OpenMetrics
format; other clients get the classic Prometheus text format.

Set `METRICS_TOKEN` to require a bearer token, for example one made with
`openssl rand -hex 32`. Requests without it get `401`. Without the setting the
endpoint answers anyone who can reach it, and Keyosk warns about that at
startup. In Prometheus, put the token in a file and point the scrape job at it:

```yaml
scrape_configs:
  - job_name: keyosk
    scheme: https
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/keyosk-metrics-token
```

For a quick look, `curl -H "Authorization: Bearer $METRICS_TOKEN" https://…/metrics`.

| Metric | Meaning |
| ------ | ------- |
| `keyosk_http_requests_total` | requests by route, method, status |
| `keyosk_http_request_duration_seconds` | latency by route |
| `keyosk_key_operations_total` | key issue/extend/delete/revoke by outcome |
| `keyosk_keys_marked_missing_total` | keys marked as missing because the gateway no longer had them, by where Keyosk noticed (`dashboard`, `extend`, `sync`) |
| `keyosk_keys_missing` | keys marked as missing right now, waiting out the grace period; counted from the database on every scrape |
| `keyosk_keys_missing_removed_total` | marked keys removed after the grace period ran out |
| `keyosk_active_keys` | keys currently issued |
| `keyosk_keys_expiring_7d` | keys expiring within a week |

The missing-key metrics are the alarm for a Keyosk instance pointed at the
wrong gateway, or at one restored from a backup. Suggested alerts: a warning
when `keyosk_keys_missing` stays above zero for an hour, or when
`keyosk_keys_marked_missing_total` rises; and a critical alert when
`keyosk_keys_missing_removed_total` rises, since a record was lost. The first
fires while the grace period (`KEY_MISSING_GRACE_PERIOD`) still lets you
correct the cause without losing anything.

## Health checks

Two endpoints answer without a login, for load balancers and orchestrators:

- `GET /healthz` answers `200 ok` while the server is running. Use it as the
  liveness probe.
- `GET /readyz` also checks the database, and answers `503` when it cannot
  reach it. Use it as the readiness probe.

The container image needs no shell or `curl` for this: its `HEALTHCHECK` runs
`keyosk -healthcheck`, which probes `/healthz` on `LISTEN_ADDR` and exits
non-zero when that fails. Kubernetes ignores the image's health check and uses
its own probes on the two endpoints.
