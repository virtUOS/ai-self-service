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
- **Profile system** — per-user key validity, fair-use token quotas, model restrictions and TPM/RPM limits
- **OIDC authentication** — login, logout, and back-channel logout support
- **SQLite storage** — single file, no separate database server
- **Admin panel** — manage profiles and assign them to users

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
| `ADMIN_EMAILS`       | no       | —           | Comma-separated list of admin email addresses                      |
| `DB_PATH`            | no       | `./data.db` | Path to the SQLite database file                                   |
| `LISTEN_ADDR`        | no       | `:8080`     | Address and port to listen on                                      |
| `COOKIE_SECURE`      | no       | `false`     | Set `true` when serving over HTTPS                                 |
| `SESSION_DURATION`   | no       | `24h`       | How long a login session lasts                                     |
| `KEY_DURATION_DAYS`  | no       | `90`        | Default key validity; profiles may override it                     |

## Running

```bash
go run ./cmd/server
```

The server runs database migrations and seeds a default profile on startup.

## Admin panel

Users whose email appears in `ADMIN_EMAILS` see an **Admin** link in the header. The admin panel at `/admin` provides:

- **Profiles** — create and edit profiles with model restrictions, TPM/RPM limits, and budget caps. Mark one profile as default; it applies to users with no explicit profile assignment.
- **Users** — view all users who have ever logged in and assign them to a profile.

Profile fields:

| Field            | Description                                                              |
| ---------------- | ------------------------------------------------------------------------ |
| Models           | Comma-separated list of allowed model names (empty = all models)          |
| Key validity     | How long a generated key lasts, in days (blank = `KEY_DURATION_DAYS`)     |
| Usage limit      | Fair-use allowance in **tokens** per period (blank = unlimited)           |
| Limit resets     | `hourly`, `daily`, `weekly` or `monthly`                                  |
| TPM limit        | Maximum tokens per minute — burst control, complements the usage limit    |
| RPM limit        | Maximum requests per minute                                               |

Different cohorts get different profiles: students might get 30-day keys with a
1M-token daily allowance, lecturers 365-day keys with no quota.

### How usage limits work

Admins configure quotas in **tokens**; LiteLLM enforces spend. The portal
converts using a nominal per-token price (`internal/litellm/quota.go`), so a
1,000,000-token daily allowance becomes a $0.10 cap that resets every 24h.
Requests fail with HTTP 429 once the allowance is spent and resume when the
period resets.

This requires every model in LiteLLM to carry that same nominal price. A model
priced at `0` or `null` accrues no spend, so a quota over it never triggers.

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
| `POST` | `/key/extend`                 | Extend the key expiry by `KEY_DURATION_DAYS`              |
| `POST` | `/key/delete`                 | Delete the user's API key                                 |
| `GET`  | `/admin`                      | Admin panel                                               |
| `POST` | `/admin/profiles`             | Create a profile                                          |
| `POST` | `/admin/profiles/{id}`        | Update a profile                                          |
| `POST` | `/admin/profiles/{id}/delete` | Delete a profile                                          |
| `POST` | `/admin/users/{id}/profile`   | Assign a profile to a user                                |

## OIDC client registration

Register the application with your OIDC provider:

- **Redirect URI**: `{FRONTEND_URL}/callback`
- **Post-logout redirect URI**: `{FRONTEND_URL}/` (optional)
- **Back-channel logout URI**: `{FRONTEND_URL}/backchannel-logout` (optional)
- Required scopes: `openid`, `email`, `profile`

## Technology stack

- **Go** with [chi](https://github.com/go-chi/chi) router
- **bun** ORM over SQLite
- **coreos/go-oidc** for OIDC/OAuth2
- Server-rendered HTML templates (no JavaScript framework)
