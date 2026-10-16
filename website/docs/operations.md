# Operating Keyosk

## Usage reporting

The dashboard shows what a person has consumed, from two sources with
different granularity:

- **Over the history window** (`USAGE_HISTORY_DAYS`, default 30) — read from
  the daily totals LiteLLM keeps per user, key and model
  (`/user/daily/activity`), filtered by the person's LiteLLM user. The chart
  spans the whole window, empty days included, so a bar's position says when
  the person used the gateway. Windows up to two months draw a bar per day; up
  to two years, per ISO week (Monday first); beyond that, per calendar month.
  Labels are thinned on wide windows; every bar names its date on hover. The
  days are UTC days, as LiteLLM buckets them.
- **Per model, over the same window** — the same daily totals by the public
  model name, with the requests sent and the prompt/completion split.
- **Against the quota** — read from the spend counter the gateway keeps for
  each window, which is what it enforces against. Shown as a percentage of the
  budget with the amounts beside it. It resets on the budget period, so it
  need not agree with the 30-day chart above it.

The history belongs to the person, across every key they have held, so
regenerating a key does not start it over. Usage from before a key was bound
to its owner is not included.

LiteLLM writes the daily totals whether or not `disable_spend_logs` is set,
and `maximum_spend_logs_retention_period` does not remove them, so neither
setting affects the dashboard. It never reads the per-request spend log: that
response grows with every request, and LiteLLM builds it on the worker that
serves everyone else ([#116](https://github.com/virtUOS/keyosk/issues/116)).

## Expiry notifications

Keys expire, so users are warned before they do — otherwise a key dies silently
in someone's pipeline.

- The dashboard shows a warning once a key is within 14 days of expiring, and
  an error once it has expired.
- With `SMTP_HOST` set, an email goes out at 14, 3 and 1 days before expiry.
  Each notice is sent at most once per key and threshold; a delivery failure
  leaves it pending so the next run retries.
- The notice is bilingual, German first: nothing records a per-user language,
  so it cannot pick one. Both halves carry the expiry date and the Keyosk link.

The thresholds are a code-level default (`notify.DefaultThresholds`), not an
admin setting.

Without `SMTP_HOST` Keyosk logs what it would have sent. It does not
silently pretend mail was delivered.

## Languages

The interface is German by default and English on request. Resolution order:

1. an explicit choice, stored in a `lang` cookie by the switcher in the header
2. the browser's `Accept-Language` (so a browser set to English gets English)
3. German

Messages live in `internal/i18n/messages.go`. A test asserts every key exists
in both languages, so a partial translation cannot ship; a missing one falls
back to English rather than rendering the key.

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

## Routes

| Method | Path                          | Description                                               |
| ------ | ----------------------------- | --------------------------------------------------------- |
| `GET`  | `/`                           | User dashboard                                            |
| `GET`  | `/login`                      | Redirect to OIDC provider                                 |
| `GET`  | `/callback`                   | OIDC authorization code callback                          |
| `POST` | `/logout`                     | Clear session and redirect to OIDC logout                 |
| `POST` | `/backchannel-logout`         | OIDC back-channel logout endpoint                         |
| `GET`  | `/session/status`             | Returns 200 / 401 (used by client-side polling)           |
| `POST` | `/key/generate`               | Generate (or replace) the user's API key                  |
| `POST` | `/key/extend`                 | Move the key expiry to a full period from now             |
| `POST` | `/key/delete`                 | Delete the user's API key                                 |
| `GET`  | `/admin`                      | Admin panel                                               |
| `POST` | `/admin/profiles`             | Create a profile                                          |
| `POST` | `/admin/profiles/{id}`        | Update a profile                                          |
| `POST` | `/admin/profiles/{id}/delete` | Delete a profile                                          |
| `POST` | `/admin/users/{id}/profile`   | Assign a profile to a user                                |
| `POST` | `/admin/users/{id}/key/revoke`| Revoke another user's API key                             |
| `POST` | `/lang`                       | Record an explicit language choice                        |
| `GET`  | `/healthz`                    | Liveness probe                                            |
| `GET`  | `/readyz`                     | Readiness probe (checks the database)                     |
| `GET`  | `/metrics`                    | Prometheus metrics (bearer token with `METRICS_TOKEN`)    |
