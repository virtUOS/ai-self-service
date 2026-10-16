<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/readme-banner-dark.svg">
    <img src="docs/brand/readme-banner-light.svg" alt="Keyosk: Grab a key. Start building." width="100%">
  </picture>
</p>

# Keyosk

Keyosk is your self-service counter for local AI. Users log in with an OIDC provider and generate, manage and renew their own [LiteLLM](https://www.litellm.ai/) API keys. Administrators define usage profiles (model access, rate limits, budgets) and assign them to individual users.

**Documentation: <https://virtuos.github.io/keyosk/>**

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
- **Admins from the login system or the panel** — either the members of an
  IdP group are the admins, or admins promote and remove each other in the
  panel, with the first one marked by one command on the server; never by
  email address
- **Local development** — one `docker compose up` in `dev/` starts an OIDC
  mock, a LiteLLM gateway with mock models, and a mail catcher; Keycloak is
  opt-in

## Quick start

```bash
cp .env.example .env
docker compose -f dev/docker-compose.yml up -d --wait   # or: podman compose
go run ./cmd/server
```

Open <http://localhost:8080>. The example configuration is preset for the dev
stack: an OIDC mock, a LiteLLM gateway with mock models and a mail catcher.

Everything else is in the documentation:

- [Getting started](https://virtuos.github.io/keyosk/getting-started/): how it works, running and local development
- [Configuration](https://virtuos.github.io/keyosk/configuration/): every setting, and the OIDC client registration
- [Admin panel](https://virtuos.github.io/keyosk/admin/): admins from the login system or the panel, profiles, assignments
- [Concepts](https://virtuos.github.io/keyosk/concepts/keys-and-limits/): keys and limits, how profile changes reach keys, the LiteLLM gateway
- [Operating Keyosk](https://virtuos.github.io/keyosk/operations/): usage reporting, expiry emails, metrics, routes

The website is built from [`docs/`](docs/) with
[Zensical](https://zensical.org/); `make website-serve` previews it locally.

## Technology stack

- **Go** with [chi](https://github.com/go-chi/chi) router
- Key issuance behind a `keyprovider.Provider` interface; LiteLLM is one adapter
- **bun** ORM over SQLite
- **coreos/go-oidc** for OIDC/OAuth2
- Server-rendered HTML templates (no JavaScript framework)

## License

[MIT](LICENSE) — Copyright (c) 2026 virtUOS, Osnabrück University
