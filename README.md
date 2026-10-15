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
- **Time-limited assignments** — any profile can be assigned to a user until a
  date, after which it reverts on its own to the default profile, to a profile
  the admin chose, or by deleting the key
- **Usage reporting** — users see what their key has consumed, per day and against their quota
- **Model discovery** — the dashboard lists the models a key may use, click to copy the exact name
- **Expiry notifications** — users are warned before their key expires, in the
  dashboard and (when SMTP is configured) by email, in German and English
- **OIDC authentication** — login, logout, and back-channel logout support
- **SQLite storage** — single file, no separate database server
- **Admin panel** — manage profiles and assign them to users
- **Limit sync** — a change to a profile or an assignment reaches every
  affected key in the background, with its progress shown to all admins
- **Admin rights from the panel** — admins can grant and withdraw the admin
  panel for other users without a redeploy; `ADMIN_IDS` still always grants and
  cannot be removed there, so a lockout is always recoverable
- **Local development** — one `docker compose up` in `dev/` starts an OIDC
  mock, a LiteLLM gateway with mock models, and a mail catcher; Keycloak is
  opt-in

## Configuration

Copy `.env.example` to `.env` and fill in the values. The example is preset
for the local development stack in `dev/`, so for that nothing needs filling
in:

| Variable             | Required | Default     | Description                                                        |
| -------------------- | -------- | ----------- | ------------------------------------------------------------------ |
| `LITELLM_BASE_URL`   | yes      | —           | Base URL of the LiteLLM proxy                                      |
| `LITELLM_MASTER_KEY` | yes      | —           | LiteLLM master key for key management                              |
| `OIDC_ISSUER_URL`    | yes      | —           | OIDC provider issuer URL                                           |
| `OIDC_CLIENT_ID`     | yes      | —           | OIDC client ID                                                     |
| `OIDC_CLIENT_SECRET` | yes      | —           | OIDC client secret                                                 |
| `OIDC_REDIRECT_URL`  | yes      | —           | Callback URL (must match OIDC client config)                       |
| `FRONTEND_URL`       | yes      | —           | Public base URL of this app (shown to users as the API base URL)   |
| `LITELLM_USER_ID_PREFIX` | no   |             | Only for several portals on one gateway; see [Several portals on one gateway](#several-portals-on-one-gateway) |
| `LITELLM_USER_EMAIL` | no       | `false`     | Record people's email addresses on their LiteLLM users, so LiteLLM's user search finds them. Only where addresses are unique across everything connected to the gateway; see [People in LiteLLM](#people-in-litellm) |
| `ADMIN_ROLE`         | no       | —           | IdP role that grants the admin panel; supersedes `ADMIN_IDS` and grants made in the panel |
| `ADMIN_IDS`          | no       | —           | Comma-separated admins, each an OIDC subject **or** an email. Always grant, and cannot be removed from the panel — this is the recovery path if the last admin is removed |
| `ADMIN_EMAILS`       | no       | —           | Deprecated alias for `ADMIN_IDS`; still read, email entries only   |
| `DB_PATH`            | no       | `./data.db` | Path to the SQLite database file. It runs in WAL mode, so `-wal` and `-shm` files sit next to it; keep the directory on a local filesystem (not NFS), and back up with `sqlite3 data.db .backup` rather than copying the file |
| `LISTEN_ADDR`        | no       | `:8080`     | Address and port to listen on                                      |
| `TRUSTED_PROXIES`    | no       | private ranges | Comma-separated CIDRs or IPs of reverse proxies whose `X-Forwarded-For` is believed for the client address in the request log. The default is loopback and the private ranges (`127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`); `none` trusts nobody and always logs the connecting address |
| `COOKIE_SECURE`      | no       | `false`     | Set `true` when serving over HTTPS. Cookies then get the `__Host-` prefix, which no other host under the same domain can set, so the app must be served at the root of its own host name |
| `SESSION_DURATION`   | no       | `24h`       | How long a login session lasts                                     |
| `KEY_DURATION_DAYS`  | no       | `90`        | Default key validity; profiles may override it                     |
| `BUDGET_UNIT`        | no       | `$`         | Unit label for quota amounts; use a word such as `credits` when model prices are nominal |
| `USAGE_HISTORY_DAYS` | no       | `30`        | How far back the usage chart and per-model table reach; the gateway's spend-log retention must cover it |
| `SUCCESSOR_URL`      | no       |             | Set on a portal being retired: the dashboard shows a banner sending users to this address. Keys can no longer be created or extended here, only deleted; existing keys expire on their own |
| `PRIVACY_NOTICE_FILE_DE` | no   | —           | Path to an HTML fragment shown at `/privacy` (no login needed) as this deployment's privacy notice, and linked from the dashboard. A named file that is missing or empty stops startup |
| `PRIVACY_NOTICE_FILE_EN` | no   | —           | The same in English. With only one language set, readers of the other get that one |
| `SMTP_HOST`          | no       | —           | `host:port` of a mail relay; unset disables expiry emails          |
| `SMTP_FROM`          | no       | `noreply@example.com` | Sender address for expiry emails                   |
| `SMTP_USERNAME`      | no       | —           | Only if the relay requires authentication                          |
| `SMTP_PASSWORD`      | no       | —           | Only if the relay requires authentication                          |
| `LOG_LEVEL`          | no       | `info`      | `debug`, `info`, `warn` or `error`                                 |
| `LIMIT_SYNC_WORKERS` | no       | `1`         | How many limit updates the portal sends to the gateway at once. One is gentle on the gateway but slow for large changes: about 25 minutes for 5,000 keys |
| `LIMIT_SYNC_INTERVAL` | no      | `5m`        | How often the limit sync retries keys whose update failed. Admin changes start a run straight away and do not wait for it |

## Running

```bash
go run ./cmd/server
```

The server runs database migrations and seeds a default profile on startup.

The database holds no usable API keys: a key is shown to its user once, and
only its SHA-256 hash is stored, which LiteLLM accepts for revoking, updating
and reporting on it. Releases before migration `20240011` stored the keys
themselves. That migration replaces them with their hashes and vacuums the file
afterwards. It cannot be rolled back. **Backups taken before the upgrade still
contain every key in plaintext**; destroy them, or rotate the keys.

### Local development

```bash
cp .env.example .env
docker compose -f dev/docker-compose.yml up -d --wait   # or podman compose
go run ./cmd/server
```

Open <http://localhost:8080> and log in by entering `admin` (admin panel) or
`student` at the mock's prompt. Keys are issued on a local LiteLLM gateway at
<http://localhost:4000>, whose mock models answer without reaching any
provider, and mail lands in Mailpit at <http://localhost:8025>. Stop it all
with `docker compose -f dev/docker-compose.yml down`.

The app needs an OIDC provider before it will serve traffic — it fetches the
discovery document at startup. The mock starts in seconds and needs no
configuration, but serves no back-channel logout, so that path cannot be
exercised against it. When touching anything auth-shaped, switch to Keycloak,
the software production runs: start `--profile keycloak`, which runs next to
the mock, and set `OIDC_ISSUER_URL=http://localhost:8082/realms/virtuos`. See
`dev/README.md` for details.

The auth-path tests need neither: `internal/oidc/mockprovider_test.go` runs an
in-process issuer, so `go test ./...` requires nothing external. The LiteLLM
end-to-end tests run against the local gateway with
`go test -tags e2e ./internal/litellm/`.

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
existing `ADMIN_EMAILS` deployment keeps running while it is migrated.

Admin rights can also be granted from the panel itself, on the **Admins** tab,
without a redeploy. The three sources are checked in order, first match wins:
`ADMIN_ROLE` first, then `ADMIN_IDS`, then the grants made from the panel.
`ADMIN_IDS` always wins over a panel grant, and a panel grant can never remove
an `ADMIN_IDS` entry, which makes the env allowlist the recovery path if the
last panel-granted admin is ever removed by mistake — there is always a way
back in that does not depend on the database. A grant can be made by email
address before the person has ever logged in; their **OIDC subject** is
recorded the first time they authenticate, and from that point the grant no
longer depends on the address, for the same reason `ADMIN_IDS` prefers subjects
over emails above. The admin panel at `/admin` provides:

- **Profiles** — create and edit profiles with model restrictions, TPM/RPM limits, and budget caps. Mark one profile as default; it applies to users with no explicit profile assignment.
- **Users** — view everyone who has logged in, see their key prefix and expiry,
  assign a profile, and revoke a key. An assignment can carry a deadline: set a
  date and the user reverts to the default profile when it passes, or to a
  profile you choose, or their key is deleted if you tick that instead. Leave
  the date empty for a permanent assignment.
- **Admins** — grant and revoke admin rights, and see where each admin's
  rights come from. Entries from `ADMIN_ROLE` or `ADMIN_IDS` show as "from
  configuration" with no remove button, since removing them here would not
  actually take effect. `ADMIN_ROLE` holders cannot be listed individually,
  because role membership lives in the IdP rather than this app, so the tab
  says as much instead of implying its list is complete. An admin can neither
  revoke their own rights nor revoke an `ADMIN_IDS` entry, and every grant and
  revocation is written to the audit log.
- **Audit log** — the 50 most recent key and profile changes, recording who did
  what to whom. Rows outlive the key and user they describe, so revoking does
  not erase the history. Changes nobody in the portal made, such as a profile
  deadline passing or a key found deleted in the gateway (`key.missing`), are
  recorded with `system` as the actor.
- **Limit sync status** — a card above the tabs says whether all keys
  carry their profile's current limits yet. If not, it shows how many are still
  waiting, whether an update is running, which keys failed and why, and each
  profile shows how many of its keys are waiting. See
  [How profile changes reach existing keys](#how-profile-changes-reach-existing-keys).

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
(issue #26). This needs LiteLLM v1.105.0 or later: the dashboard reads what has
been spent in each window from the `budget_limits_usage` that `/key/info`
reports since then (issue #116).

### How profile changes reach existing keys

Limits are pushed to the gateway when a key is issued. After that, a background
**limit sync** keeps every key in line with its owner's profile. It reacts to
everything that can change what a key should enforce:

- an admin edits a profile's models, TPM/RPM limits or quota windows,
- an admin moves a user to another profile,
- another profile becomes the default (this affects every user without a
  profile of their own),
- a time-limited assignment runs out,
- a person logs in with a new username or email address, which their LiteLLM
  user is labelled with (see [People in LiteLLM](#people-in-litellm)).

Renaming a profile, rewording its description or changing its key validity
does not touch any key, because none of that changes what the gateway
enforces.

**How it works.** Each profile has a revision number that goes up whenever its
limits change, and each key records the profile and revision it was last
updated with. A key whose owner's profile differs from that record is out of
date. The sync works through the out-of-date keys one at a time (more with
`LIMIT_SYNC_WORKERS`). An admin change starts it immediately; the admin's page
does not wait for it. One update takes three calls to the gateway, so a change
to a profile with 5,000 users takes about 25 minutes with the default single
worker.

Expired keys are left alone: they enforce nothing, and updating one would
still rewrite its owner's allowance in the gateway. A key that is extended
later is updated then, if it is out of date.

There is no queue of changes. A key always receives what its owner's profile
says at the moment it is updated, so if a profile is saved twice in quick
succession, keys not reached yet only ever see the second version, and keys
that already got the first are updated again.

**Failures.** An update that fails, for example because the gateway is down,
is retried every `LIMIT_SYNC_INTERVAL`. The admin panel lists failed keys with
the gateway's error. A key whose owner has no profile at all, because no
default profile exists, is never updated with empty limits, which the gateway
would read as "unlimited"; it is reported as failed instead.

**Keys deleted in the gateway.** A key deleted in LiteLLM directly, outside
the portal, cannot be restored: the portal does not have what it would need to
recreate it. When the sync, the dashboard or an extension finds that the
gateway no longer has a key, the portal removes its record, logs a warning and
writes a `key.missing` entry to the audit log. The owner's dashboard says the
key was deleted and offers a new one. Deleting or revoking a key the gateway
no longer has succeeds, since the key is gone either way.

Only LiteLLM's own "not found" answer counts. A 404 from anything in front of
it, such as a reverse proxy whose route changed or a maintenance page, is an
ordinary failure: the record is kept and the call retried.

This trusts the gateway's answer. A portal pointed at the wrong LiteLLM, or at
one whose database was reset, sees every key as missing and removes every
record; check `LITELLM_BASE_URL` before starting a portal against a new
gateway.

**After upgrading** to the version that introduced the sync, every existing
key counts as out of date once, so the first run updates all keys. With many
users this takes a while; the admin panel shows the progress.

**Changes made directly in the gateway** are invisible to the portal. To undo
them, mark every key as out of date:

```bash
ai-self-service -resync-limits
```

The command needs only `DB_PATH`, changes nothing in the gateway itself, and
exits at once; the running server then updates every key on its next run,
within `LIMIT_SYNC_INTERVAL`. It must run where the database is, so against a
container use `docker exec <container> ai-self-service -resync-limits`, or
`kubectl exec` into the pod. Run it from cron to do this nightly.

The dashboard never pushes limits. It only reads, and when it cannot load the
user's profile, key or usage, it says so rather than showing defaults.

Clearing a quota sends an explicit `null`. LiteLLM leaves an omitted field
untouched, so a profile that loses its allowance would otherwise keep enforcing
the previous one.

### People in LiteLLM

Each person with a key has a LiteLLM internal user, which holds their budget.
The portal labels it with the person's username (`user_alias`), so LiteLLM's
user list shows who is who. Each key is bound to that user, and its alias,
`<subject>-<username>-<random>`, names the person as well. When someone logs
in with a new username, the limit sync updates both labels.

Keys carry no email address. Keys issued before this version still have one
in their metadata, which is left as it is: rewriting a key's metadata would
also replace settings an admin made there in LiteLLM. It goes when the key is
regenerated.

With `LITELLM_USER_EMAIL=true` the portal also records the person's email
address on their user. That is what makes them findable: LiteLLM's user
search only matches user IDs and addresses. Turn it on only where every
address is unique across everything connected to the gateway, other portals
included. LiteLLM's own SSO login, for its admin UI, looks users up by
address, and with two users sharing one it attaches the login to either of
them. Turning the option on or off changes nothing until each key is pushed
again; run `ai-self-service -resync-limits` to do that for all of them.
Turning it off does not remove addresses already recorded.

After upgrading to the version that added the labels, the first sync updates
every unexpired key once, so every user and key is labelled.

### Several portals on one gateway

A person's budget, the widest quota window of their profile, is held on a
LiteLLM internal user rather than on their key, so it survives a key rotation.
That user is named after the person's OIDC subject. Two portals that use the
same gateway and the same identity provider therefore share every person's
budget, and each portal's limit sync overwrites what the other one set.

Give every portal on a shared gateway but at most one its own
`LITELLM_USER_ID_PREFIX`. Its users are then named `<prefix>:<subject>`, and
the portals no longer touch each other's budgets. A single portal needs no
prefix, and existing deployments keep their users and budgets unchanged.

Set the prefix before the portal issues its first key. Changing it later is
not supported: keys stay bound to the old users, and keep counting against
their budgets, until each key is updated again. If it has to be done, run
`ai-self-service -resync-limits` afterwards. The sync then moves every
unexpired key to the new users, which start with nothing spent. The old users
are left in the gateway; don't delete them while another portal's keys still
belong to them, as deleting a LiteLLM user also deletes its keys.

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
serves everyone else (#116).

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
per-user path does not create a time series per user. Scrapers that send an
OpenMetrics `Accept` header (Prometheus does by default) get the OpenMetrics
format; other clients get the classic Prometheus text format. In the deployment Caddy
restricts the endpoint to the monitoring host.

| Metric | Meaning |
| ------ | ------- |
| `aiselfservice_http_requests_total` | requests by route, method, status |
| `aiselfservice_http_request_duration_seconds` | latency by route |
| `aiselfservice_key_operations_total` | key issue/extend/delete/revoke by outcome |
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
