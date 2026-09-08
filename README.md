# AI API Key Self-Service Portal

A self-service web portal that lets users generate, manage, and renew their own [LiteLLM](https://www.litellm.ai/) API keys after authenticating with an OIDC provider. Administrators can define usage profiles (model access, rate limits, budgets) and assign them to individual users.

## How it works

1. Users log in via OIDC (e.g. Keycloak, Dex, Authentik).
2. After login, each user lands on a dashboard where they can generate a personal LiteLLM API key.
3. The key is created directly in LiteLLM with the parameters of the user's assigned profile.
4. The key expires after a configurable number of days; users can extend or regenerate it at any time.
5. Admins can manage profiles and assign users to them via `/admin`.

## Features

- **Self-service key management** — generate, extend, regenerate, and delete LiteLLM API keys
- **Profile system** — per-user key validity, fair-use spend quotas, model restrictions and TPM/RPM limits
- **Usage reporting** — users see what their key has consumed, per day and against their quota
- **Model discovery** — the dashboard lists the models a key may use, click to copy the exact name
- **Expiry notifications** — users are warned before their key expires, in the
  dashboard and (when SMTP is configured) by email, in German and English
- **OIDC authentication** — login, logout, and back-channel logout support
- **SQLite storage** — single file, no separate database server
- **Admin panel** — manage profiles and assign them to users
- **Local development** — Keycloak or a faster OIDC mock, both in `dev/`

## Configuration

Copy `.env.example` to `.env` and fill in the values:

| Variable             | Required | Default     | Description                                                        |
| -------------------- | -------- | ----------- | ------------------------------------------------------------------ |
| `LITELLM_BASE_URL`   | yes      | —           | Base URL of the LiteLLM proxy                                      |
| `LITELLM_MASTER_KEY` | yes      | —           | LiteLLM master key for key management                              |
| `OIDC_ISSUER_URL`    | yes      | —           | OIDC provider issuer URL                                           |
| `OIDC_CLIENT_ID`     | yes      | —           | OIDC client ID                                                     |
| `OIDC_CLIENT_SECRET` | yes      | —           | OIDC client secret                                                 |
| `OIDC_REDIRECT_URL`  | yes      | —           | Callback URL (must match OIDC client config)                       |
| `FRONTEND_URL`       | yes      | —           | Public base URL of this app (shown to users as the API base URL)   |
| `ADMIN_ROLE`         | no       | —           | IdP role that grants the admin panel; supersedes `ADMIN_IDS`       |
| `ADMIN_IDS`          | no       | —           | Comma-separated admins, each an OIDC subject **or** an email       |
| `ADMIN_EMAILS`       | no       | —           | Deprecated alias for `ADMIN_IDS`; still read, email entries only   |
| `DB_PATH`            | no       | `./data.db` | Path to the SQLite database file                                   |
| `LISTEN_ADDR`        | no       | `:8080`     | Address and port to listen on                                      |
| `COOKIE_SECURE`      | no       | `false`     | Set `true` when serving over HTTPS                                 |
| `SESSION_DURATION`   | no       | `24h`       | How long a login session lasts                                     |
| `KEY_DURATION_DAYS`  | no       | `90`        | Default key validity; profiles may override it                     |
| `BUDGET_UNIT`        | no       | `$`         | Unit label for quota amounts; use a word such as `credits` when model prices are nominal |
| `USAGE_HISTORY_DAYS` | no       | `30`        | How far back the usage chart and per-model table reach; the gateway's spend-log retention must cover it |
| `SUCCESSOR_URL`      | no       |             | Set on a portal being retired: the dashboard shows a banner sending users to this address and warning that keys issued here will be revoked. Keys can no longer be created or extended here, only deleted |
| `SUCCESSOR_KEYS_REVOKED_ON` | no |            | Date named in that banner, shown as given (e.g. `2026-10-01`); empty leaves the date out |
| `SMTP_HOST`          | no       | —           | `host:port` of a mail relay; unset disables expiry emails          |
| `SMTP_FROM`          | no       | `noreply@uni-osnabrueck.de` | Sender address for expiry emails                   |
| `SMTP_USERNAME`      | no       | —           | Only if the relay requires authentication                          |
| `SMTP_PASSWORD`      | no       | —           | Only if the relay requires authentication                          |
| `LOG_LEVEL`          | no       | `info`      | `debug`, `info`, `warn` or `error`                                 |

## Running

```bash
go run ./cmd/server
```

The server runs database migrations and seeds a default profile on startup.

### Local development

The app needs an OIDC provider before it will serve traffic — it fetches the
discovery document at startup. `dev/` provides two; see `dev/README.md` for
which to use when.

```bash
docker compose -f dev/docker-compose.yml up -d                 # Keycloak, ~20s
docker compose -f dev/docker-compose.yml --profile mock up -d  # OIDC mock, ~8s
```

Keycloak is the software production runs, so it is what to use when touching
anything auth-shaped. The mock starts faster and needs no realm import, but
serves no back-channel logout, so that path cannot be exercised against it.

The auth-path tests need neither: `internal/oidc/mockprovider_test.go` runs an
in-process issuer, so `go test ./...` requires nothing external.

## Admin panel

**Prefer `ADMIN_ROLE`.** Set it to a realm role and admin membership is managed
in the IdP, where staff changes are already handled — the portal stops being a
second list to keep in step, and nothing has to be redeployed when someone
joins or leaves. It needs the IdP team to create the role and add a mapper that
puts it in the **ID token**: Keycloak sends realm roles only in the access
token by default. `dev/realm-export.json` contains a working example of both.

A realm that emits no role claim falls back to `ADMIN_IDS`, so configuring a
role before the IdP is ready locks nobody out. With `ADMIN_ROLE` unset, roles
are ignored entirely.

Users listed in `ADMIN_IDS` see an **Admin** link in the header. An entry is
either an **OIDC subject** or an email address, and the subject is the form to
prefer: an address is assigned by the IdP and can be reassigned, so an
allowlist keyed on it grants admin to whoever holds that address today rather
than to a person. Each user's subject is shown in the admin panel's user table,
click to copy. Granting by email still works and is logged as such, so an
existing `ADMIN_EMAILS` deployment keeps running while it is migrated. The admin panel at `/admin` provides:

- **Profiles** — create and edit profiles with model restrictions, TPM/RPM limits, and budget caps. Mark one profile as default; it applies to users with no explicit profile assignment.
- **Users** — view everyone who has logged in, see their key prefix and expiry,
  assign a profile, and revoke a key.
- **Audit log** — the 50 most recent key and profile changes, recording who did
  what to whom. Rows outlive the key and user they describe, so revoking does
  not erase the history.

Profile fields:

| Field            | Description                                                              |
| ---------------- | ------------------------------------------------------------------------ |
| Models           | Comma-separated list of allowed model names (empty = all models)          |
| Key validity     | How long a generated key lasts, in days (blank = `KEY_DURATION_DAYS`)     |
| Usage limit      | Spend allowance per period, in `BUDGET_UNIT` (blank = unlimited); several windows may apply at once |
| Limit resets     | `hourly`, `daily`, `weekly` or `monthly`                                  |
| TPM limit        | Maximum tokens per minute — burst control, complements the usage limit    |
| RPM limit        | Maximum requests per minute                                               |

Different cohorts get different profiles: students might get 30-day keys with a
$1.00 daily allowance, lecturers 365-day keys with no quota.

### How extending works

Extend sets the expiry to **now + the profile's key validity**. It does not add
to the existing expiry, so clicking twice does not stockpile time, and it does
not use the duration the key was originally created with — that is never
stored.

The consequence is that policy changes apply on the next extend: move a user
from a 30-day profile to a 365-day one and their existing key extends by 365,
without needing to be regenerated.

### How usage limits work

Admins configure quotas as **spend budgets** per period, in the unit LiteLLM
prices its models in (`BUDGET_UNIT` labels them on the page; default `$`).
LiteLLM enforces spend directly, so the figure an admin enters is the figure
the gateway enforces, whatever mix of models a key uses. A model priced at `0`
or `null` accrues no spend, so a budget never binds on it; the server warns
about such models at startup.

Quotas used to be stored in tokens and converted at one nominal price. That
was exact only while every model cost the same per token. Migration `20240007`
converts existing token quotas at that nominal rate (0.0000001 per token) so
enforced caps keep their size; deployments that priced models differently
should review profile budgets after upgrading.

Requests fail with HTTP 429 once the allowance is spent and resume when the
period resets.

**Several windows per profile.** A profile can hold one allowance per period
(hourly, daily, weekly, monthly) and LiteLLM enforces each independently — a
key takes `budget_limits` as a list of `{budget_duration, max_budget}` objects
and rejects with `ExceededBudget: Key over 1h budget` when any one is spent.
The tightest window binds, so a shorter period may not carry a larger budget
than a longer one; the admin form rejects that. The widest window is held
against the user rather than the key so regenerating a key does not reset it
(issue #26). This needs LiteLLM v1.97.0 or later; v1.90.0 accepted the field
and ignored it.

### How profile changes reach existing keys

Limits are pushed to the gateway when a key is issued, and re-applied every
time the owner loads the dashboard. Editing a profile's quota, or moving a user
to a different profile, therefore takes effect on their next page load rather
than requiring them to regenerate.

This matters because the two can disagree: the portal reads limits from its own
database to render the page, while the gateway enforces whatever was last
pushed to the key. Without the re-apply, the dashboard would advertise a quota
that nothing enforced.

Clearing a quota sends an explicit `null`. LiteLLM leaves an omitted field
untouched, so a profile that loses its allowance would otherwise keep enforcing
the previous one.

## Usage reporting

The dashboard shows what a key has consumed, from two sources with different
granularity:

- **Over the history window** (`USAGE_HISTORY_DAYS`, default 30) — read from
  LiteLLM's per-request spend log, filtered by the key's SHA-256 and
  aggregated by the portal. The chart spans the whole window, empty days
  included, so a bar's position says when the key was used. Windows up to
  two months draw a bar per day; up to two years, per ISO week (Monday
  first); beyond that, per calendar month. Labels are thinned on wide
  windows; every bar names its date on hover.
- **Per model, over the same window** — the same log summed by model, with
  the prompt/completion split.
- **Against the quota** — read from the key's own spend counter, which is what
  the gateway enforces against. Shown as a percentage of the budget with the
  amounts beside it. It resets on the budget period, so it need not agree with
  the 30-day chart above it.

Usage belongs to a key, not a person: regenerating a key starts the history
over, and the card says so.

Two gateway-side settings affect this:

- `disable_spend_logs: true` switches off the per-request log. The portal then
  falls back to the key's cumulative spend and hides the chart and the
  per-model table.
- `maximum_spend_logs_retention_period` must be at least as long as
  `USAGE_HISTORY_DAYS`, or users silently see less history than the page
  offers. A long window also means a larger log download per dashboard
  load, since the route cannot be narrowed server-side.

Passing `start_date`/`end_date` to `/spend/logs` returns a **different shape** —
daily aggregates carrying spend but no token counts. Since local models are
priced so that spend is near zero, that response looks valid and carries no
usable signal. The portal reads the raw per-request rows instead.

## Expiry notifications

Keys expire, so users are warned before they do — otherwise a key dies silently
in someone's pipeline.

- The dashboard shows a warning once a key is within 14 days of expiring, and
  an error once it has expired.
- With `SMTP_HOST` set, an email goes out at 14, 3 and 1 days before expiry.
  Each notice is sent at most once per key and threshold; a delivery failure
  leaves it pending so the next run retries.
- The notice is bilingual, German first: nothing records a per-user language,
  so it cannot pick one. Both halves carry the expiry date and the portal link.

The thresholds are a code-level default (`notify.DefaultThresholds`), not an
admin setting.

Without `SMTP_HOST` the portal logs what it would have sent. It does not
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
per-user path does not create a time series per user. In the deployment Caddy
restricts the endpoint to the monitoring host.

| Metric | Meaning |
| ------ | ------- |
| `aiselfservice_http_requests_total` | requests by route, method, status |
| `aiselfservice_http_request_duration_seconds` | latency by route |
| `aiselfservice_key_operations_total` | key issue/extend/revoke by outcome |
| `aiselfservice_active_keys` | keys currently issued |
| `aiselfservice_keys_expiring_7d` | keys expiring within a week |

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
| `GET`  | `/metrics`                    | Prometheus metrics                                        |

## OIDC client registration

Register the application with your OIDC provider:

- **Redirect URI**: `{FRONTEND_URL}/callback`
- **Post-logout redirect URI**: `{FRONTEND_URL}/` (optional)
- **Back-channel logout URI**: `{FRONTEND_URL}/backchannel-logout` (optional)
- Required scopes: `openid`, `email`, `profile`

## Technology stack

- **Go** with [chi](https://github.com/go-chi/chi) router
- Key issuance behind a `keyprovider.Provider` interface; LiteLLM is one adapter
- **bun** ORM over SQLite
- **coreos/go-oidc** for OIDC/OAuth2
- Server-rendered HTML templates (no JavaScript framework)

## License

[MIT](LICENSE) — Copyright (c) 2026 virtUOS, Osnabrück University
