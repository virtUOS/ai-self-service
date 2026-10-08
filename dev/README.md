# Local development environment

The app requires an OIDC provider at startup — it fetches the discovery
document before it will serve traffic. There are two to choose from; the mock
is the default, and `.env.example` is preset for it.

| | Mock | Keycloak |
| --- | --- | --- |
| Start | `docker compose -f dev/docker-compose.yml --profile mock up -d` | `docker compose -f dev/docker-compose.yml --profile keycloak up -d` |
| Ready in | ~8s | ~20s (realm import) |
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

Both listen on port 8081, so run one at a time. Each sits behind its own
compose profile and neither starts by default, so the profile flag is what
picks one — a bare `up` starts nothing rather than starting Keycloak on top of
whichever you asked for. `podman compose` works the same way.

## Start the mock

```bash
cp .env.example .env    # then set LITELLM_BASE_URL and LITELLM_MASTER_KEY
docker compose -f dev/docker-compose.yml --profile mock up -d
go run ./cmd/server
```

Open <http://localhost:8080>. The `.env.example` values work unchanged: the
mock accepts any client id and secret without registration, and the issuer is
already `http://localhost:8081`.

At the login prompt, enter the subject of the user you want to be, matching
`dev/mock-users.json`:

| Subject   | Email                        | Role in the app |
| --------- | ---------------------------- | --------------- |
| `student` | student@uni-osnabrueck.de    | regular user    |
| `admin`   | admin@example.com            | admin (matches `ADMIN_IDS`) |

The mock's subjects are fixed rather than generated, so `ADMIN_IDS=admin` also
works here.

Stop it with `docker compose -f dev/docker-compose.yml --profile mock down`.

## Switch to Keycloak

Stop the mock first — both use port 8081 — then:

```bash
docker compose -f dev/docker-compose.yml --profile keycloak up -d
```

Admin console: <http://localhost:8081> (`admin` / `admin`).

The `virtuos` realm is imported automatically with a confidential client and
the same two users as the mock, with passwords:

| User      | Password  | Email                        | Role in the app |
| --------- | --------- | ---------------------------- | --------------- |
| `student` | `student` | student@uni-osnabrueck.de    | regular user    |
| `admin`   | `admin`   | admin@example.com            | admin (matches `ADMIN_IDS`) |

The client id and secret in `.env.example` (`ai-self-service` /
`local-dev-secret`) are the realm's, so only the issuer changes in `.env`:

```
OIDC_ISSUER_URL=http://localhost:8081/realms/virtuos
```

The realm also defines an `ai-self-service-admin` role, assigned to the `admin`
user, with a mapper that puts realm roles in the ID token — the configuration
the IdP team would create in production. Set `ADMIN_ROLE=ai-self-service-admin`
and admin comes from the role rather than the list; the app logs no
email-grant warning when that path is used, which is how to tell them apart.

`ADMIN_IDS` takes an OIDC subject or an email address, and production should
prefer subjects. An address is used here because `realm-export.json` does not
pin user ids: Keycloak mints new ones on each import, so a subject written into
`.env` goes stale as soon as the volume is dropped. The app logs a warning on
every email-based grant, which is expected locally.

Stop it with `docker compose -f dev/docker-compose.yml --profile keycloak down`
and set the issuer back to `http://localhost:8081` to return to the mock.

## Automated tests

The auth-path tests do **not** need Docker: `internal/oidc/mockprovider_test.go`
runs an in-process issuer shaped like the university's Keycloak (same endpoint
layout, same claims, back-channel logout). Run `go test ./...` as usual.
