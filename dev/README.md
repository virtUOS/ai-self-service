# Local development environment

The app requires an OIDC provider at startup — it fetches the discovery
document before it will serve traffic. Production uses the university
Keycloak (`https://login.uni-osnabrueck.de/realms/virtuos`), so local dev runs
the same software rather than a different IdP, to avoid bugs that only appear
against the real provider.

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
| `admin`   | `admin`   | admin@example.com            | admin (matches `ADMIN_EMAILS`) |

## Point the app at it

In `.env`:

```
OIDC_ISSUER_URL=http://localhost:8081/realms/virtuos
OIDC_CLIENT_ID=ai-self-service
OIDC_CLIENT_SECRET=local-dev-secret
OIDC_REDIRECT_URL=http://localhost:8080/callback
FRONTEND_URL=http://localhost:8080
ADMIN_EMAILS=admin@example.com
```

Then `go run ./cmd/server` and open <http://localhost:8080>.

## Automated tests

The auth-path tests do **not** need Docker: `internal/oidc/mockprovider_test.go`
runs an in-process issuer shaped like the university's Keycloak (same endpoint
layout, same claims, back-channel logout). Run `go test ./...` as usual.
