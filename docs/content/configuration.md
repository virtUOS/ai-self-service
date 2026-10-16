# Configuration


Copy `.env.example` to `.env` and fill in the values. The example is preset
for the local development stack in `dev/`, so for that nothing needs filling
in:

| Variable             | Required | Default     | Description                                                        |
| -------------------- | -------- | ----------- | ------------------------------------------------------------------ |
| `LITELLM_BASE_URL`   | yes      | —           | Base URL of the LiteLLM proxy. Keyosk manages keys there, and the dashboard shows it, with `/v1`, as the API base URL, so it must be an address users can reach |
| `LITELLM_MASTER_KEY` | yes      | —           | LiteLLM master key for key management                              |
| `OIDC_ISSUER_URL`    | yes      | —           | OIDC provider issuer URL                                           |
| `OIDC_CLIENT_ID`     | yes      | —           | OIDC client ID                                                     |
| `OIDC_CLIENT_SECRET` | yes      | —           | OIDC client secret                                                 |
| `OIDC_REDIRECT_URL`  | yes      | —           | Callback URL (must match OIDC client config)                       |
| `OIDC_EXTRA_SCOPES`  | no       | —           | Space-separated scopes requested on top of `openid profile email`, e.g. `groups` for IdPs that send groups only when asked |
| `ADMIN_GROUP`        | no       | —           | IdP group whose members are the admins; see [Administrators](administrators.md). Unset, admins are managed in the panel |
| `ADMIN_GROUP_CLAIM`  | no       | `groups`    | Dot path to the ID token claim that lists the groups, e.g. `realm_access.roles` for Keycloak realm roles. Only with `ADMIN_GROUP` |
| `FRONTEND_URL`       | yes      | —           | Public base URL of this app, used for the link in expiry emails and as the redirect after logout |
| `LITELLM_USER_ID_PREFIX` | no   |             | Only for several Keyosk instances on one gateway; see [Several Keyosk instances on one gateway](concepts/gateway.md#several-keyosk-instances-on-one-gateway) |
| `LITELLM_USER_EMAIL` | no       | `false`     | Record people's email addresses on their LiteLLM users, so LiteLLM's user search finds them. Only where addresses are unique across everything connected to the gateway; see [People in LiteLLM](concepts/gateway.md#people-in-litellm) |
| `DB_PATH`            | no       | `./data.db` | Path to the SQLite database file. It runs in WAL mode, so `-wal` and `-shm` files sit next to it; keep the directory on a local filesystem (not NFS), and back up with `keyosk -backup <file>` rather than copying the file (see [Backups](installation.md#backups)) |
| `LISTEN_ADDR`        | no       | `:8080`     | Address and port to listen on                                      |
| `TRUSTED_PROXIES`    | no       | private ranges | Comma-separated CIDRs or IPs of reverse proxies whose `X-Forwarded-For` is believed for the client address in the request log. The default is loopback and the private ranges (`127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`); `none` trusts nobody and always logs the connecting address |
| `COOKIE_SECURE`      | no       | `false`     | Set `true` when serving over HTTPS. Cookies then get the `__Host-` prefix, which no other host under the same domain can set, so the app must be served at the root of its own host name |
| `SESSION_DURATION`   | no       | `24h`       | How long a login session lasts                                     |
| `KEY_DURATION_DAYS`  | no       | `90`        | Default key validity; profiles may override it                     |
| `BUDGET_UNIT`        | no       | `$`         | Unit label for quota amounts; use a word such as `credits` when model prices are nominal |
| `USAGE_HISTORY_DAYS` | no       | `30`        | How far back the usage chart and per-model table reach; see [Usage reporting](concepts/usage-limits.md#usage-reporting) |
| `SUCCESSOR_URL`      | no       |             | Set on a Keyosk instance being retired: the dashboard shows a banner sending users to this address. Keys can no longer be created or extended here, only deleted; existing keys expire on their own |
| `PRIVACY_NOTICE_FILE_DE` | no   | —           | Path to an HTML fragment shown at `/privacy` (no login needed) as this deployment's privacy notice, and linked from the dashboard. A named file that is missing or empty stops startup |
| `PRIVACY_NOTICE_FILE_EN` | no   | —           | The same in English. With only one language set, readers of the other get that one |
| `SMTP_HOST`          | no       | —           | `host:port` of a mail relay; unset disables expiry emails          |
| `SMTP_FROM`          | no       | `noreply@example.com` | Sender address for expiry emails                   |
| `SMTP_USERNAME`      | no       | —           | Only if the relay requires authentication                          |
| `SMTP_PASSWORD`      | no       | —           | Only if the relay requires authentication                          |
| `METRICS_TOKEN`      | no       | —           | Bearer token `/metrics` requires. Without it the endpoint is open. See [Metrics](monitoring.md#metrics) |
| `LOG_LEVEL`          | no       | `info`      | `debug`, `info`, `warn` or `error`                                 |
| `LIMIT_SYNC_WORKERS` | no       | `1`         | How many limit updates Keyosk sends to the gateway at once. One is gentle on the gateway but slow for large changes: about 25 minutes for 5,000 keys |
| `LIMIT_SYNC_INTERVAL` | no      | `5m`        | How often the limit sync retries keys whose update failed. Admin changes start a run straight away and do not wait for it |
| `KEY_MISSING_GRACE_PERIOD` | no | `24h`       | How long a key the gateway no longer has stays marked as missing before Keyosk removes its record. A key that returns in time is unmarked. See [Keys deleted in the gateway](concepts/profiles.md#how-profile-changes-reach-existing-keys) |


## OIDC client registration

Register the application with your OIDC provider:

- **Redirect URI**: `{FRONTEND_URL}/callback`
- **Post-logout redirect URI**: `{FRONTEND_URL}/` (optional)
- **Back-channel logout URI**: `{FRONTEND_URL}/backchannel-logout` (optional)
- Required scopes: `openid`, `email`, `profile`, plus whatever your IdP needs
  to send groups when `ADMIN_GROUP` is set (see `OIDC_EXTRA_SCOPES`)
