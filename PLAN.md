# Status and next steps

Last updated 2026-08-24. Written as a handover: what is done, what is not, and
the things that are surprising enough to waste an afternoon rediscovering.

## Where it runs

| | |
|---|---|
| Testing | <https://ai-keys-testing-virtuos-openstack.uni-osnabrueck.de> — live, tracks `:main` |
| Production | not provisioned |
| App repo | GitHub `virtUOS/ai-self-service` (Actions → GHCR) |
| Deployment | GitLab `…/digitale-dienste/ki/ai-self-service-setup` (Ansible) |
| Dashboard | Grafana “AI Self-Service”, datasource `virtuos-prometheus` |
| Latest release | `v0.2.0` |

Deploying needs the **university network or VPN** — SSH is filtered from
outside. The app repo is public; the deployment repo is not.

## Done

Phases 1–6 of the original assessment all shipped:

- **Security** — CSRF on every mutating route, the generated key no longer
  travels in the URL, no auth debug logging, CSP/HSTS/X-Frame-Options.
- **Data model** — unique key per user and single default profile enforced by
  the database, transactional key rotation, Postgres support removed (it never
  worked; the DDL was SQLite-only).
- **Profiles** — per-profile key validity and fair-use token quotas.
- **Admin** — key revocation, key visibility, audit log.
- **Provider abstraction** — `keyprovider.Provider`, with LiteLLM as one
  adapter and a fake for tests.
- **Operations** — graceful shutdown, timeouts, health checks, Dockerfile, CI,
  Prometheus metrics, structured logging, expiry warnings.
- **Bilingual UI** — German by default, English when the browser asks.

91 tests, no skips. `go test ./...` needs nothing external.

## Not done

1. **SMTP is unconfigured.** The expiry-notification code is complete and
   tested; `asvc_smtp_host` is empty, so nothing is delivered and the portal
   logs what it would have sent. Needs the university relay address.
2. **Production does not exist.** No VM, no DNS, no Keycloak client
   (`ai-self-service` is unregistered — only `ai-self-service-testing` exists),
   and `group_vars/production/vault.yml` still holds `CHANGEME`.
3. **Production model pricing.** Chat models are priced at `1e-07`; the
   embedding and OCR models are deliberately left at `None`.
4. **Profile assignment from an OIDC claim.** Still manual, which will not
   scale to a student cohort. Needs someone to check what the realm emits.

## Things that will waste your time if you do not know them

- **Stacked quota windows do not work.** LiteLLM v1.97.0 accepts
  `budget_limits` (“100k/day AND 1M/month”), stores it, computes a `reset_at`
  — and never enforces it. Verified three times, including driving spend
  5,000,000× past the cap. Only `max_budget` + `budget_duration` is enforced,
  so a profile gets **one** period. Re-test on a newer LiteLLM before
  promising otherwise.
- **A model priced `0` or `null` accrues no spend**, so any quota over it can
  never trigger. All chat models must carry the nominal price.
- **`LITELLM_MASTER_KEY` is not a master key.** It is `lkmanager`, scoped to
  `management_routes`: it can manage keys but not models. Model changes need a
  different credential.
- **LiteLLM stores models in its database**, not in `litellm-setup` config.
  There is no static `model_list` to edit.
- **Spend takes ~15s to propagate.** An immediate read after a request shows 0.
- **Prometheus lives under a `/prometheus/` route prefix.** API paths need it.
- **`deploy-monitoring.yml` must run without `-u`** — every task is delegated
  to the monitoring host, which each admin reaches under their own account.
- **`docker compose up` reuses a cached tag.** The playbook pulls explicitly;
  a manual redeploy of `:main` without a pull silently keeps the old build.
- **Grafana panels need unique `refId`s.** Duplicates error the panel rather
  than rendering.
- **Templates: `{{T .Lang}}` breaks inside `range`** — the dot is the loop
  element there, so use `$.Lang`.
- **The Grafana dashboard stays English.** Only the app is translated.

## Conventions

- Two repos: application code on GitHub, deployment on GitLab, mirroring
  `transcription-whisper` / `transcription-whisper-ansible`.
- Ansible variables are flat `asvc_*`, deliberately: Ansible **replaces**
  dicts across `group_vars` rather than merging them, so a per-environment
  override of one field silently drops the rest.
- Secrets live in `group_vars/*/vault.yml`, encrypted; gopass holds only the
  vault password (`uos/ai-self-service/ansible-vault`).
- Releases are `v*` tags; the image publishes semver tags and `:main`.
  Testing tracks `:main`, production pins a tag.
- MIT licence, matching the virtUOS norm (38 of the org's licensed repos).

## Suggested next steps

1. Get the SMTP relay address and set `asvc_smtp_host` — the biggest
   functional gap, and it is a config change rather than code.
2. Provision production: VM, DNS, Keycloak client, vault secrets, pricing.
3. Ask the IdP team what group or affiliation claim the realm emits, then map
   profiles onto it.
