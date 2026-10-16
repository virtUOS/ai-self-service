# Getting started

## How it works

1. Users log in via OIDC (e.g. Keycloak, Dex, Authentik).
2. After login, each user lands on a dashboard where they can generate a personal LiteLLM API key.
3. The key is created directly in LiteLLM with the parameters of the user's assigned profile.
4. The key expires after a configurable number of days; users can extend or regenerate it at any time.
5. Admins can manage profiles and assign users to them via `/admin`.

The interface and the expiry emails are in German and English. The interface
follows the browser's language, and the switch in the header overrides it.

## Try it

Keyosk needs a LiteLLM gateway and an OIDC provider; it brings neither. With
both at hand, and a client for Keyosk registered at the provider with the
redirect URI `http://localhost:8080/callback`, one command starts it:

```sh
docker run --rm -p 8080:8080 \
  -e LITELLM_BASE_URL=https://litellm.example.org \
  -e LITELLM_MASTER_KEY=sk-... \
  -e OIDC_ISSUER_URL=https://login.example.org/realms/example \
  -e OIDC_CLIENT_ID=keyosk \
  -e OIDC_CLIENT_SECRET=... \
  -e OIDC_REDIRECT_URL=http://localhost:8080/callback \
  -e FRONTEND_URL=http://localhost:8080 \
  ghcr.io/virtuos/keyosk:main
```

Open <http://localhost:8080> and log in. The database lives in the container
and is gone when it stops, and nobody is an admin yet. To keep Keyosk running,
follow [Installation](installation.md). To work on Keyosk itself, see
[Development](development.md); its dev stack runs a gateway and an identity
provider for you.
