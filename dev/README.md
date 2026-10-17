# Local development environment

`docker-compose.yml` starts everything the app needs, and `.env.example` is
preset for it:

```bash
cp .env.example .env
docker compose -f dev/docker-compose.yml up -d --wait
go run ./cmd/server
```

Open <http://localhost:8080>. `podman compose` works the same way, and
`make up` / `make down` do the same with whichever of the two is installed.

| Service    | Address                 | What for |
| ---------- | ----------------------- | -------- |
| app        | <http://localhost:8080> | `go run ./cmd/server` |
| OIDC mock  | <http://localhost:8081> | login |
| Keycloak   | <http://localhost:8082> | login, opt-in (see below) |
| LiteLLM    | <http://localhost:4000> | the gateway keys are issued on |
| Mailpit    | <http://localhost:8025> | every mail the app sends |

The first start takes a couple of minutes: the LiteLLM image is large, and it
sets up its database before it answers. Later starts are quicker.

Stop it all with `docker compose -f dev/docker-compose.yml down`, and add `-v`
to also drop LiteLLM's database, which holds its keys, users and spend.

## Login

The app requires an OIDC provider at startup — it fetches the discovery
document before it will serve traffic. There are two to choose from; the mock
starts by default, and `.env.example` is preset for it.

| | Mock | Keycloak |
| --- | --- | --- |
| Start | by default | `--profile keycloak` |
| Issuer | `http://localhost:8081` | `http://localhost:8082/realms/keyosk` |
| Ready in | under a second | ~20s (realm import) |
| Fidelity | different implementation | same software as production |
| Back-channel logout | **no** | yes |

Use the **mock** for everyday iteration where login is just a step on the way
to something else. It is quicker and needs no realm import, but it serves no
back-channel logout endpoint, so the `/logout/backchannel` path cannot be
exercised against it.

Use **Keycloak** when touching anything auth-shaped, and to reproduce
production behaviour: it is the same software the university runs
(`https://login.uni-osnabrueck.de/realms/virtuos`), so it catches bugs that
only appear against the real provider.

The two use different ports and run side by side, so switching is only a
matter of `OIDC_ISSUER_URL`.

### The mock

The `.env.example` values work unchanged: the mock accepts any client id and
secret without registration, and the issuer is already `http://localhost:8081`.

At the login prompt, enter the subject of the user you want to be, matching
`dev/mock-users.json`:

| Subject   | Email                        | Role in the app |
| --------- | ---------------------------- | --------------- |
| `student` | student@uni-osnabrueck.de    | regular user    |
| `admin`   | admin@example.com            | admin (in the `keyosk-admin` group) |

The file holds more users, the personas of `make seed` (see
[Seed data](#seed-data)); any of them can log in here too. The mock reads the
file when it starts, so after it changes, restart it with
`docker compose -f dev/docker-compose.yml restart oidc-mock`.

`admin` is an admin because `mock-users.json` puts it in the
`keyosk-admin` group and `.env.example` sets `ADMIN_GROUP` to that
group. The Admins tab is then read-only.

To work on the panel's own admin management instead, comment out
`ADMIN_GROUP` in `.env`, log in as `admin` once, and mark the account against
the dev database (`DB_PATH=./data.db` in `.env.example`):

```bash
sqlite3 data.db "UPDATE users SET is_admin = 1 WHERE oidc_sub = 'admin'"
```

The mock's subjects are fixed rather than generated, which is why the subject
can be written into the command. Further admins are then promoted on the
panel's Admins tab.

### Keycloak

```bash
docker compose -f dev/docker-compose.yml --profile keycloak up -d --wait
```

Admin console: <http://localhost:8082> (`admin` / `admin`).

The `keyosk` realm is imported automatically with a confidential client and
the same two users as the mock, with passwords:

| User      | Password  | Email                        | Role in the app |
| --------- | --------- | ---------------------------- | --------------- |
| `student` | `student` | student@uni-osnabrueck.de    | regular user    |
| `admin`   | `admin`   | admin@example.com            | admin (in the `keyosk-admin` group) |

The client id and secret in `.env.example` (`keyosk` /
`local-dev-secret`) are the realm's, so only the issuer changes in `.env`:

```
OIDC_ISSUER_URL=http://localhost:8082/realms/keyosk
```

The realm has a `keyosk-admin` group with `admin` as its member, and
a group-membership mapper that puts the `groups` claim in the ID token with
"Full group path" off, so the claim lists the bare name `keyosk-admin`
rather than `/keyosk-admin`. That is what `ADMIN_GROUP` in
`.env.example` expects, so admin works without further changes.

The realm also has a `keyosk-admin` realm role, assigned to `admin`,
with a mapper that puts realm roles in the ID token. To try roles instead of
groups, set `ADMIN_GROUP_CLAIM=realm_access.roles`.

Without `ADMIN_GROUP`, Keycloak mints new user ids on each import, so the
`admin` user's subject is not fixed. Log in once, then look the account up and
mark it:

```bash
sqlite3 data.db "SELECT id, oidc_sub, email FROM users"
sqlite3 data.db "UPDATE users SET is_admin = 1 WHERE id = <id from above>"
```

Set the issuer back to `http://localhost:8081` to return to the mock. Stop
Keycloak with `docker compose -f dev/docker-compose.yml --profile keycloak down`.

## LiteLLM

A real LiteLLM gateway, with its Postgres database, configured by
`litellm-config.yaml`. Its master key is `sk-local-dev`.

None of its models reaches a provider. The chat models answer every request
with a canned text, which still goes through LiteLLM's key checks, budgets and
spend logging — so a key generated in Keyosk can be used for real, and the
dashboard shows the usage:

| Model        | Kind      | Cost of a short request |
| ------------ | --------- | ----------------------- |
| `mock-small` | chat      | about 0.05              |
| `mock-large` | chat      | about 0.50              |
| `mock-embed` | embedding | listed only             |

```bash
curl http://localhost:4000/chat/completions \
  -H "Authorization: Bearer sk-…the key from the dashboard…" \
  -H "Content-Type: application/json" \
  -d '{"model": "mock-small", "max_tokens": 20, "messages": [{"role": "user", "content": "hi"}]}'
```

Usage can take up to a minute to reach the dashboard: LiteLLM writes its spend
log in batches, and the dashboard caches what it read for 60 seconds.

With a small budget, send `max_tokens` as the example does. LiteLLM reserves
a request's worst-case cost against the budget until the answer is settled,
and without `max_tokens` that is the model's whole output limit: when it is
more than the budget has left, a second request sent right after the first is
refused for a moment.

LiteLLM checks for budgets to reset every few seconds here rather than every
ten minutes, so a profile with a short window, such as `1h`, visibly resets
during a session.

## Mail

Mailpit accepts whatever the app sends on port 1025 and shows it at
<http://localhost:8025>; nothing leaves the machine. `.env.example` points
`SMTP_HOST` at it.

## Seed data

```bash
make seed
```

starts an app instance of its own on <http://127.0.0.1:8090> and fills it in
about a minute with users, profiles, keys and usage, so the dashboard and the
admin panel show every state they have. It keeps running until Ctrl-C.

It does not touch your own setup: it runs from `dev/.seed/` with its own
database, leaves `.env` and `data.db` alone, and names its users in LiteLLM
with a prefix of its own (`seed-<time>:`). Each run starts from scratch and
first deletes what the previous one created in LiteLLM. Its cookies are those
of `127.0.0.1`, so a browser keeps you signed in on `localhost:8080` and on the
seed at the same time.

Log in with the subjects it prints; they are in `dev/mock-users.json`:

| Subject      | Profile    | Shows |
| ------------ | ---------- | ----- |
| `admin`      | default    | admin (in the `keyosk-admin` group) |
| `admin2`     | default    | a second admin |
| `student`    | default    | a key with usage on two models |
| `nokey`      | default    | no key yet |
| `keyflow`    | default    | no key; for the browser tests' key flow |
| `logout`     | default    | no key; for the browser tests' logout |
| `heavy`      | Tight      | a nearly full quota bar |
| `stacked`    | Stacked    | three quota bars |
| `restricted` | Small only | one model, with rate limits |
| `expiring`   | Short keys | a key that expires in three days (its reminder is in Mailpit) |
| `expired`    | default    | an expired key |
| `missing`    | default    | a key the gateway no longer has |
| `guest`      | Stacked    | Stacked for a week, then the key is revoked |
| `intern`     | Small only | Small only for a month, then Tight |
| `lapsed`     | default    | an assignment that ran out, back on the default |
| `oezlem`     | default    | a name with umlauts and a long address |

Everything a user or admin can do, the seed does through the app's own pages,
so keys, limits and the audit log are what the app makes of them. Only an
expired key and a passed deadline are written to the database directly, since
no page can make them.

The personas and profiles are defined in `internal/devseed/personas.go`. The
browser tests use them as fixtures, so a change there is a change to the
tests.

## Automated tests

`go test ./...` needs none of this. The auth-path tests run an in-process
issuer (`internal/oidc/mockprovider_test.go`) shaped like the university's
Keycloak: same endpoint layout, same claims, back-channel logout.

The LiteLLM end-to-end tests run against the gateway above, and are built only
with the `e2e` tag. Most are in `internal/litellm`; the limit sync has one too:

```bash
docker compose -f dev/docker-compose.yml up -d --wait litellm
go test -tags e2e ./internal/litellm/ ./internal/limitsync/
```

The browser tests drive the pages in Chromium through Playwright
(playwright-go), and are built only with the `browser` tag. They start an
instance of their own on `127.0.0.1:8091`, seed it as `make seed` does, and
need the OIDC mock as well as LiteLLM. The Playwright driver and Chromium are
downloaded once:

```bash
make browser-install
make browser
```

A failing test saves a trace in `internal/browsertest/testdata/traces/`; the
test's log says how to open it. When the run ends, the seed's keys and users
are deleted from LiteLLM again.

`make check` runs everything CI runs, these tests included, and starts the
stack first; run it before committing. CI runs the same targets on every push. `LITELLM_BASE_URL` and `LITELLM_MASTER_KEY` point
them at another gateway, which then needs the same models.
