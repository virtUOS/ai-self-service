# Working on this repository

## Before committing

Run `make check` and get it passing. It runs what CI runs: gofmt, vet, the unit
tests, a CGO-free build, and the LiteLLM end-to-end tests against the dev
stack, which it starts if needed. The end-to-end tests are part of every check,
not only of changes to `internal/litellm`.

The dev stack (`dev/docker-compose.yml`, see `dev/README.md`) works with Docker
and with Podman, and developers have one or the other. The Makefile uses
`docker compose` when docker is installed and `podman compose` otherwise; set
`COMPOSE` to choose, e.g. `make check COMPOSE="podman compose"`. Don't write
instructions or scripts that assume only one of them.

## Running the app

- **Never edit or overwrite the developer's `.env`.** The app reads `.env` from
  its working directory, so build the server, copy `.env.example` into a
  scratch directory as `.env`, point `DB_PATH` at a file there, and run the
  binary from that directory. `.env.example` works unchanged against the dev
  stack.
- **Port 8080 may already be taken** by the developer's own server. Check
  before starting one, and never stop a server you did not start. On another
  port, set `LISTEN_ADDR`, `FRONTEND_URL` and `OIDC_REDIRECT_URL` to match. The
  OIDC mock accepts any redirect URI; the Keycloak realm only allows
  `http://localhost:8080/callback`.

## Checking usage on the dashboard

Usage from a request reaches the dashboard late, which is not a bug:

- LiteLLM records spend in batches, a few seconds after the request.
- The dashboard caches usage for 60 seconds, so a page loaded right after a
  key was generated keeps showing "no usage" for up to a minute.
- Send test requests with `max_tokens`. LiteLLM reserves a request's
  worst-case cost against the budget until the answer is settled; without
  `max_tokens` that can be the whole remaining budget, and a request sent right
  after it is refused with 422 "Budget has been exceeded".
