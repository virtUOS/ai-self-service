# Installation

Keyosk runs as a single container next to a LiteLLM gateway and an OIDC
provider that you already have. This guide sets it up with Docker Compose;
`podman compose` works with the same files.

## Before you start

You need:

- a **LiteLLM gateway**, v1.105.0 or later, and its master key. Keyosk creates
  the keys and the LiteLLM users on it.
- an **OIDC client** for Keyosk at your identity provider, with the redirect
  URI `https://keyosk.example.org/callback`; see
  [OIDC client registration](configuration.md#oidc-client-registration).
- a **reverse proxy** that serves Keyosk over HTTPS and forwards to port 8080
  of the container.

## Compose file

Put two files in a directory of their own. `compose.yaml`:

```yaml
services:
  keyosk:
    image: ghcr.io/virtuos/keyosk:main
    restart: unless-stopped
    env_file: keyosk.env
    ports:
      - "127.0.0.1:8080:8080"   # the reverse proxy connects here
    volumes:
      - keyosk-data:/data

volumes:
  keyosk-data:
    name: keyosk-data
```

`keyosk.env`, with your values:

```sh
# The gateway
LITELLM_BASE_URL=https://litellm.example.org
LITELLM_MASTER_KEY=sk-...

# The identity provider
OIDC_ISSUER_URL=https://login.example.org/realms/example
OIDC_CLIENT_ID=keyosk
OIDC_CLIENT_SECRET=...
# https here makes the cookies secure
OIDC_REDIRECT_URL=https://keyosk.example.org/callback

# Where people reach Keyosk
FRONTEND_URL=https://keyosk.example.org

# Admins: the members of this group at the identity provider
ADMIN_GROUP=keyosk-admin

# Recommended: protect /metrics, and send expiry emails
METRICS_TOKEN=...
SMTP_HOST=mail.example.org:25
SMTP_FROM=noreply@example.org
```

The first block of seven is required. Everything else has a default; see
[Configuration](configuration.md) for every setting. Keep `keyosk.env` readable
only by you, since it holds the master key and the client secret.

`ghcr.io/virtuos/keyosk:main` is built from the `main` branch. Releases are
tagged `v1.2.3`, `1.2` and `1`; pin one of those to choose when you update.

## Start

```sh
docker compose up -d
docker compose logs -f keyosk
```

On its first start, Keyosk creates its database in the volume, runs the
migrations and creates a default profile. The log names anything that is not
set up yet, such as a missing first admin, an open `/metrics` or missing email.
`GET /readyz` answers `200` once it is ready; see
[Health checks](monitoring.md#health-checks).

## The first admin

With `ADMIN_GROUP` set, as above, the members of that group are the admins
from their first login, and there is nothing else to do. See
[Administrators](administrators.md) for what the identity provider has to send.

Without it, admins are managed in the admin panel, and the first one is marked
with a command. The person logs in once, so that Keyosk has their account, and
looks up their user ID on the **Account settings** page, which opens from the
account menu. Then mark that account in the running container:

```sh
docker compose exec keyosk keyosk -make-admin 1
```

The command names the account it marked, so a mistyped ID shows. It is safe
while Keyosk runs and takes effect on the person's next request. Further admins
are then promoted in the panel.

## Backups

Everything Keyosk keeps is one SQLite database in the volume. Back it up with
SQLite's own backup, which is consistent while Keyosk runs, rather than by
copying the file:

```sh
docker run --rm -v keyosk-data:/data alpine:3.22 sh -c '
  apk add -q sqlite su-exec &&
  su-exec 10001:10001 sqlite3 /data/data.db ".backup /tmp/keyosk.db" &&
  cat /tmp/keyosk.db' > keyosk-backup.db
```

The database holds no usable API keys: a key is shown to its user once, and
only its SHA-256 hash is stored, which LiteLLM accepts for revoking, updating
and reporting on it. It does hold everyone's name, email address and profile.

## Updating

```sh
docker compose pull
docker compose up -d
```

Keyosk runs any new migrations when it starts. Take a backup first: a
migration cannot be rolled back.
