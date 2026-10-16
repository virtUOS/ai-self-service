# Getting started

## How it works

1. Users log in via OIDC (e.g. Keycloak, Dex, Authentik).
2. After login, each user lands on a dashboard where they can generate a personal LiteLLM API key.
3. The key is created directly in LiteLLM with the parameters of the user's assigned profile.
4. The key expires after a configurable number of days; users can extend or regenerate it at any time.
5. Admins can manage profiles and assign users to them via `/admin`.


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
a full IdP that also serves back-channel logout: start `--profile keycloak`,
which runs next to the mock, and set `OIDC_ISSUER_URL=http://localhost:8082/realms/virtuos`. See
[`dev/README.md`](https://github.com/virtUOS/keyosk/blob/main/dev/README.md) for details.

The auth-path tests need neither: `internal/oidc/mockprovider_test.go` runs an
in-process issuer, so `go test ./...` requires nothing external. The LiteLLM
end-to-end tests run against the local gateway with
`go test -tags e2e ./internal/litellm/ ./internal/limitsync/`.
