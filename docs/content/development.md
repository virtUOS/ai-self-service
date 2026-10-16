# Development

Keyosk is a Go program; the dev stack in `dev/` provides everything it talks
to, so nothing outside the checkout is needed.

## Running locally

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
a full IdP that also serves back-channel logout: start `--profile keycloak`,
which runs next to the mock, and set
`OIDC_ISSUER_URL=http://localhost:8082/realms/virtuos`. See
[`dev/README.md`](https://github.com/virtUOS/keyosk/blob/main/dev/README.md) for details.

## Tests

The auth-path tests need neither the mock nor Keycloak: they run an
in-process issuer (`internal/oidc/oidctest`), so `go test ./...` requires
nothing external. The LiteLLM
end-to-end tests run against the local gateway with
`go test -tags e2e ./internal/litellm/ ./internal/limitsync/`. `make check` runs
everything CI runs, and starts the dev stack if needed.
