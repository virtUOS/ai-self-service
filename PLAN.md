# Status and next steps

Last updated 2026-08-26. Written as a handover: what is done, what is not, and
the things that are surprising enough to waste an afternoon rediscovering.

## Where it runs

| | |
|---|---|
| Testing | <https://ai-keys-testing-virtuos-openstack.uni-osnabrueck.de> — live, tracks `:main` |
| Production | not provisioned |
| App repo | GitHub `virtUOS/ai-self-service` (Actions → GHCR) |
| Deployment | GitLab `…/digitale-dienste/ki/ai-self-service-setup` (Ansible) |
| Dashboard | Grafana “AI Self-Service”, datasource `virtuos-prometheus` |
| Latest release | `v0.3.2` (main is ahead: stacked quotas, unreleased) |

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
- **Stacked quota windows** — a profile holds several allowances at once
  (100k/day AND 1M/month); the gateway enforces each independently.

146 tests, no skips. `go test ./...` needs nothing external.

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
- **Never name a Go file `*_windows.go` or `*_test_windows.go`.** Go reads the
  text after the last underscore as a build constraint, and `windows` is a
  GOOS, so the file is silently excluded everywhere else. A migration appeared
  not to run and its tests reported “no tests to run”, with no error anywhere.
  The same applies to `_linux`, `_darwin`, `_amd64` and friends.
- **`/user/new` echoes `budget_limits` back but does not store them.** Posting
  stacked windows to an internal user returns 200 with the array reflected in
  the response body, so the create call looks like it worked. Reading the user
  back with `/user/info` shows the field absent: only the single `max_budget` /
  `budget_duration` pair persists. Verified against the testing gateway on
  2026-08-26. Trusting the create response would ship quotas that enforce one
  window while the UI promises several. Stacking works on *keys*, not users.
- **SQLite ignores `ON DELETE CASCADE`** unless `PRAGMA foreign_keys` is set on
  every connection, which the driver does not do here. Rows referencing a
  deleted parent are simply orphaned — delete children explicitly, in the same
  transaction.
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

## Where this was left (2026-08-26)

Open PRs, both green and unmerged:

- **#28** — the profiles table clips its right-hand columns; adds `min-width`
  so it scrolls. Small, safe to merge.

Merged today but **not yet released**: stacked quota windows (#25), the
documentation correction that unblocked them (#24), quota validation and a
profile-name quoting fix (#27). Testing runs all of it; `v0.4.0` is warranted
when someone wants a tagged build, since the schema changed.

### The thing to look at first: issue #26

A user pointed out that **regenerating a key resets the quota**, so anyone at
their limit can simply issue a new key and carry on. That is a real hole, and
it comes from a decision recorded in this file: usage was scoped to the current
key because usage lives on the key upstream, and carrying it across rotations
was judged not obviously wanted. #26 is that judgement being wrong.

Fixing it means the quota has to follow the *user*, not the key. Three routes:

- **LiteLLM internal users (recommended).** The gateway already has the exact
  primitive: `/user/new` takes `max_budget` and `budget_duration`, and an
  internal user's budget applies across *every* key that user owns. Generating
  a key with `user_id` set attaches it to that budget, so rotation no longer
  resets anything — the spend lives on the user, which is what #26 asks for.
  `/user/update` re-applies limits when a profile is edited, and `/user/info`
  reports the user's spend plus a per-key breakdown, replacing the per-key
  quota read on the dashboard. Caveat: internal-user budgets are **not**
  enforced for keys that belong to a team; this portal does not use teams, and
  it must not start without revisiting this.

  An internal user holds **one** window, not stacked ones — see the trap below,
  which is what makes stacked quotas the open design question here rather than
  a detail.
- Carry spend forward on rotation — read the old key's spend and pre-load the
  new key with it. LiteLLM has no "set spend" call, so it needs a compensating
  budget adjustment and drifts. Superseded by the route above.
- Track usage per user in the portal and enforce there, using the gateway only
  for a coarse backstop. Correct, but duplicates what the gateway does.

Worth deciding before building. The dashboard text ("a newly generated key
starts again at zero") documents the current behaviour honestly, so nothing is
lying to users in the meantime.

### Also outstanding

- `test quota` profile on testing has a mangled name (`"test quota"` with
  literal quotes) from the quoting bug #27 fixed. Editing and saving it in the
  admin panel rewrites it correctly; the fix stops it worsening but does not
  clean up what is already stored.
- **LiteLLM production** has neither the re-enabled spend logs nor the removal
  of the nightly restart. Both are merged in `litellm-setup` and deployed to
  testing only, deliberately — the restart bounded a memory leak, and it was
  measured over days while the check that replaced it ran for minutes. Watch
  RSS on the testing gateway before production follows.

## Suggested next steps

1. Decide how issue #26 should work — quota per user rather than per key — and
   implement it. It is the only open item that lets someone bypass a limit.
2. Provision production: VM, DNS, Keycloak client, vault secrets, pricing.
3. Ask the IdP team what group or affiliation claim the realm emits, then map
   profiles onto it.
