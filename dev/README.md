# Local development environment

The app requires an OIDC provider at startup — it fetches the discovery
document before it will serve traffic. There are two to choose from.

| | Keycloak | Mock |
| --- | --- | --- |
| Start | `docker compose -f dev/docker-compose.yml up -d` | `docker compose -f dev/docker-compose.yml --profile mock up -d` |
| Ready in | ~20s (realm import) | ~8s |
| Fidelity | same software as production | different implementation |
| Back-channel logout | yes | **no** |

Use **Keycloak** when touching anything auth-shaped, and to reproduce
production behaviour: it is the same software the university runs
(`https://login.uni-osnabrueck.de/realms/virtuos`), so it catches bugs that
only appear against the real provider.

Use the **mock** for everyday iteration where login is just a step on the way
to something else. It is quicker and needs no realm import, but it serves no
back-channel logout endpoint, so the `/logout/backchannel` path cannot be
exercised against it.

Both listen on port 8081, so run one at a time.

## Start Keycloak

```bash
docker compose -f dev/docker-compose.yml up -d
```

Admin console: <http://localhost:8081> (`admin` / `admin`).

The `virtuos` realm is imported automatically with a confidential client and
two users:

| User      | Password  | Email                        | Role in the app |
| --------- | --------- | ---------------------------- | --------------- |
| `student` | `student` | student@uni-osnabrueck.de    | regular user    |
| `admin`   | `admin`   | admin@example.com            | admin (matches `ADMIN_IDS`) |

## Point the app at it

In `.env`:

```
OIDC_ISSUER_URL=http://localhost:8081/realms/virtuos
OIDC_CLIENT_ID=ai-self-service
OIDC_CLIENT_SECRET=local-dev-secret
OIDC_REDIRECT_URL=http://localhost:8080/callback
FRONTEND_URL=http://localhost:8080
ADMIN_IDS=admin@example.com
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

Then `go run ./cmd/server` and open <http://localhost:8080>.

## Start the mock instead

```bash
docker compose -f dev/docker-compose.yml --profile mock up -d
```

It accepts any client id and secret without registration, so the `.env` above
works unchanged except for the issuer:

```
OIDC_ISSUER_URL=http://localhost:8081
```

At the login prompt, enter the subject of the user you want to be — `student`
or `admin`, matching `dev/mock-users.json`. They carry the same emails as the
Keycloak users, so `ADMIN_IDS` behaves identically. The mock's subjects are
fixed rather than generated, so `ADMIN_IDS=admin` also works here.

## Automated tests

The auth-path tests do **not** need Docker: `internal/oidc/mockprovider_test.go`
runs an in-process issuer shaped like the university's Keycloak (same endpoint
layout, same claims, back-channel logout). Run `go test ./...` as usual.
