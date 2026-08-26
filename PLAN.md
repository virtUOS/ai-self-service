# Status and next steps

Last updated 2026-08-25. Written as a handover: what is done, what is not, and
the things that are surprising enough to waste an afternoon rediscovering.

## Where it runs

| | |
|---|---|
| Testing | <https://ai-keys-testing-virtuos-openstack.uni-osnabrueck.de> — live, tracks `:main` |
| Production | not provisioned |
| App repo | GitHub `virtUOS/ai-self-service` (Actions → GHCR) |
| Deployment | GitLab `…/digitale-dienste/ki/ai-self-service-setup` (Ansible) |
| Dashboard | Grafana “AI Self-Service”, datasource `virtuos-prometheus` |
| Latest release | `v0.3.2` |

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
- **Bilingual UI** — German by default, English when the browser asks. Expiry
  notices are bilingual too, since nothing records a per-user language.
- **Expiry email** — delivered through `relay.rz.uni-osnabrueck.de:25`, which
  accepts mail from known hosts without credentials. Verified end to end.
- **Dashboard usage** — the models a key may use (click to copy), what it has
  consumed per day over 30 days, and what is left of the enforced quota.
- **Profile sync** — a profile's limits are re-applied to existing keys on
  every dashboard load, so an edit takes effect without regenerating.
- **Local dev** — Keycloak, or a faster OIDC mock under `--profile mock`.

126 tests, no skips. `go test ./...` needs nothing external.

## Not done

1. **Production does not exist.** No VM, no DNS, no Keycloak client
   (`ai-self-service` is unregistered — only `ai-self-service-testing` exists),
   and `group_vars/production/vault.yml` still holds `CHANGEME`.
2. **Production model pricing.** Chat models are priced at `1e-07`; the
   embedding and OCR models are deliberately left at `None`.
3. **Profile assignment from an OIDC claim.** Still manual, which will not
   scale to a student cohort. Needs someone to check what the realm emits.

## Things that will waste your time if you do not know them

- **Stacked quota windows DO work** on v1.97.0, contrary to an earlier note
  here. A key takes `budget_limits` as a list of
  `{budget_duration, max_budget}` objects, and each window is enforced
  independently: verified by capping 1h tiny with 24h generous (the 1h blocked)
  and then the reverse (the 24h blocked). The error names the window, e.g.
  `ExceededBudget: Key over 1h budget`.

  The earlier finding was made on v1.90.0, where `budget_limits` took a dict
  (`{"1h": 0.0001}`) — that shape is now rejected outright, so the API changed
  along with the behaviour. The portal still exposes only one period per
  profile; supporting stacked windows is issue #1.

- **Budgets reset lazily.** LiteLLM clears a key's spend on the first request
  after the window passes, not on a timer. A key can sit past its
  `budget_reset_at` still reporting the old spend and still rejecting requests,
  until something asks it to serve. The dashboard renders a past reset as “any
  moment now” rather than counting into the negative.
- **Spend logs were re-enabled** (2026-08-25) after being off since v1.90.0 to
  bound a proxy memory leak (BerriAI/litellm#12685, now closed). The nightly
  restart that capped that growth is removed on testing. RSS held flat over 105
  requests, but the original leak was measured over days — watch it before
  production follows.
- **`/spend/logs` changes shape when given dates.** With `start_date` and
  `end_date` it returns pre-aggregated daily rows carrying `spend` only — no
  token counts — and local models are priced so that spend is always zero, so
  the result looks correct and means nothing. Use `?api_key=<sha256(key)>` for
  raw per-request rows with `total_tokens`, and aggregate them yourself.
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

1. Provision production: VM, DNS, Keycloak client, vault secrets, pricing.
2. Ask the IdP team what group or affiliation claim the realm emits, then map
   profiles onto it.
