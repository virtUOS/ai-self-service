# AI Self-Service Portal — Production Plan

Status: BLOCKED-1 resolved by testing. BLOCKED-2 needs a credential.
Verified findings below supersede the LiteLLM docs where they disagree.

Environment facts established during evaluation:

- LiteLLM **v1.97.0** (OpenAPI `info.version`; pinned in
  `litellm-setup/templates/docker-compose.yml:14`).
- `budget_limits` (stacked rolling budget windows) **is** in the v1.97.0
  `GenerateKeyRequest` / `UpdateKeyRequest` schemas.
- `.env` in this repo points at the **testing** instance
  (`litellm-virtuos-openstack`), not production (`litellm.uni-osnabrueck.de`).
- Models are stored in LiteLLM's **database** (`STORE_MODEL_IN_DB`), not in
  `litellm-setup` config. There is no static `model_list`.
- `disable_spend_logs: true` is set in `templates/config/config.yml`.

---

## RESOLVED-1 — Budget enforcement works; stacked windows do NOT

Verified on the testing instance (probe keys created, tested, deleted).

`disable_spend_logs: true` is **not** a problem: `spend` is a live counter on
the key, independent of the `LiteLLM_SpendLogs` table.

| Test | Config | Spend vs cap | Result |
|---|---|---|---|
| Probe 1 | `budget_limits: [{1h, 1e-06}]` | 0.5 vs 1e-06 | **allowed** — not enforced |
| Probe 2 | classic `max_budget` + `budget_duration` | 0.5 vs 1e-06 | **429 `budget_exceeded`** |

Keys were identical apart from that field. `budget_limits` is accepted,
persisted, and given a computed `reset_at` — but is not checked at request
time on v1.97.0. It would have shipped as a silent no-op.

**Decision: use classic `max_budget` + `budget_duration`.** Consequence: one
rolling window per key, not stacked. "500k/day AND 5M/month" is unavailable;
pick the period that matters (daily is the usual fair-use lever). Stacked
windows would need a LiteLLM upgrade or the separate budget-object API —
either must be verified the same way before being relied on.

Residual assumption: enforcement was proven with a synthetic `spend` value.
Token->cost accrual was not verified end-to-end because no chat backend was
reachable from testing (`vllm-12...` does not resolve). Low risk, but unproven.

---


## BLOCKED-2 — Model pricing: needs a credential I do not have

**Rate decided: `input_cost_per_token = output_cost_per_token = 1e-07`** for
all local models, uniform, admin-overridable per model.

Equal input/output cost makes a token quota exact:
`max_budget = tokens x 1e-07`. 1M tokens = 0.10, 5M = 0.50. No input/output
mix assumption is needed anywhere in the admin UI.
(Supersedes the earlier per-model LibreChat-matching table: a uniform rate was
chosen so quotas mean "tokens", not "tokens weighted by model choice".)

Current state on testing — why this matters:

| Model | input | output | effect on a budget |
|---|---|---|---|
| `openai/gpt-oss-120b` | `None` | `None` | accrues nothing -> **cap never triggers** |
| `Qwen/Qwen3.5-122B-A10B-FP8` | `1e-07` | `5e-07` | works |
| `bge-m3` | `0.0` | `0.0` | accrues nothing -> **cap never triggers** |

**Blocker:** the key in `.env` is `lkmanager`, scoped to
`allowed_routes: ["management_routes"]`. It manages *keys* (all the portal
needs) but cannot manage *models* — `PATCH /model/{id}/update` returns 403 for
all three models. Setting prices needs the real master key, a key whose
`allowed_routes` include model management, or admin-UI access.

Model IDs on testing, ready to patch once a credential exists:

- `openai/gpt-oss-120b` -> `ccb7cd6a-10d9-4c67-82c7-00d0d831edbb`
- `Qwen/Qwen3.5-122B-A10B-FP8` -> `75d1c2f2-797f-436a-b4fd-bede6e93ad1c`
- `bge-m3` -> `f5472ae5-5c44-4231-aeee-2110eed3df32`

Production pricing: read the current model list, present a per-model
before/after diff, apply only after explicit confirmation.


---

## Phase 1 — Security (blockers, no dependencies)

1. **CSRF protection** on all mutating routes. Currently the only defense is
   `SameSite=Lax`. Highest-impact target is `/admin/users/{id}/profile`
   (privilege escalation onto an unlimited profile).
2. **Get the raw API key out of the URL.** `ui.go:198` redirects to
   `/?newkey=<secret>` — leaks into browser history, `Referer`, proxy logs,
   APM. Replace with a one-time server-side flash (nonce → render once → delete).
3. **Stop storing plaintext keys.** Store prefix + LiteLLM key ID only.
   Requires confirming revoke-by-ID works on v1.97.0.
4. **Remove auth debug logging** (`ui.go:33`, `session.go:49`); move to `slog`.
5. **Security headers** — CSP, HSTS, `X-Content-Type-Options`.

## Phase 2 — Data model correctness

6. **`UNIQUE` on `api_keys.user_id`** — enforces the one-key-per-user rule that
   handler code currently only assumes. Verified: two keys for one user insert
   fine today. Keep `api_keys` a separate table so multi-key is a later
   constraint-drop, not a migration.
7. **Transactional key rotation** — today a LiteLLM failure can delete the local
   row and leave a live orphaned key (a "revoked" key that still works).
8. **Single-default-profile constraint** — two `is_default` profiles insert fine
   today; `GetDefaultProfile` silently picks the first.
9. **Delete dead `parseModels`** (`db.go:144`) — verified no-op; bun already
   handles the round-trip.
10. **Postgres: fix or drop.** `Where("is_default = 1")` (`db.go:39`, `db.go:96`)
    fails on Postgres booleans, and the migration DDL is SQLite-only
    (`AUTOINCREMENT`, `DATETIME`, `REAL`). The README advertises it as working.

## Phase 3 — Profiles (the feature work)

11. **Move expiry onto the profile.** Today `KEY_DURATION_DAYS` is one global
    env var used in 5 places; students vs. lecturers need different values.
    Keep the env var as the seed default. `/key/extend` must resolve the
    user's profile rather than the global constant.
12. **Quota window on the profile** — one `max_budget` + `budget_duration`
    per key (stacked windows verified non-functional; see RESOLVED-1).

```go
type Profile struct {
    Name, Description string
    Models            []string
    KeyDurationDays   int            // per-profile expiry
    QuotaTokens       int64          // admin enters tokens
    QuotaPeriod       string         // "1h" | "24h" | "7d" | "30d"
    TPMLimit, RPMLimit *int64        // burst control, complements quota
    IsDefault         bool
}

```

    Admins enter **tokens**; conversion to `max_budget` happens in the provider
    adapter using the nominal price. Users never see synthetic dollars.
    Adapter converts: `max_budget = QuotaTokens * 1e-07` (exact at the flat
    rate). Keep TPM/RPM alongside — they stop a runaway script inside 60s;
    the quota window enforces fair share across the day/week.

13. **Keep the budget/cost fields.** Reversal of my earlier advice: commercial
    models are coming, and the same machinery provides quotas now.

## Phase 4 — Admin capability

14. **Admin key revocation** — `POST /admin/users/{id}/key/revoke`, remote +
    local in one transaction.
15. **Key visibility in the users table** — prefix, expiry, created.
16. **Audit log** — who issued/revoked which key, when. Expect to want this the
    first time a student asks why their key died.
17. **Consider profile assignment from an OIDC claim** — manual assignment does
    not scale to a student cohort. Check what the IdP emits.

## Phase 5 — Provider abstraction

18. **`KeyProvider` interface.** Coupling is shallow — ~6 call sites, all in
    `handlers/ui.go`, plus one constructor. Main near-term payoff is testability
    (handlers against a fake provider) rather than swapping backends.
    Rename `api_keys.litellm_key` → `provider_key_ref`, add a `provider` column.

## Phase 5b — Deployment (separate `ai-self-service-setup` Ansible repo)

House convention (modelled on `transcription-whisper-ansible`): **app code and
deployment live in separate repos.** This repo stays code-only; a sibling
Ansible repo owns deployment.

That sibling repo should carry:

- `hosts-testing.yml` / `hosts-production.yml` inventories
- `group_vars/{all,testing,production}/vars.yml` + **`vault.yml`**
  (`$ANSIBLE_VAULT;1.1;AES256` encrypted at rest, committed)
- `.read-vault-pass` — reads the vault password from gopass, prompting as a
  fallback:
  `gopass show -o -f uos/ai-self-service/ansible-vault || read -rsp "Vault Password: "`
- `ansible.cfg` with `vault_password_file = .read-vault-pass`,
  `roles_path = ./.roles`, `collections_path = ./.collections`
- shared roles: `lkiesow.verify_galaxy_versions`, `user_setup` (from
  `site_admins`), `secure_sshd`, `lkiesow.dnf_autoupdate`, `docker_install`,
  `firewalld_info`
- `.gitlab-ci.yml` with a linting stage (ansible-lint + yamllint)

**This resolves the earlier "how do we build a vault" question.** Ansible Vault
is already the house answer; gopass holds only the single vault password rather
than every individual secret. Applying it here means `LITELLM_MASTER_KEY`,
`OIDC_CLIENT_SECRET` and the DB DSN live in an encrypted, version-controlled
`vault.yml` instead of an untracked `.env` — and the same convention should be
applied back to the per-host vLLM API keys that are currently inconsistent.

## Phase 6 — Operations

19. Graceful shutdown + `ReadHeaderTimeout` (`main.go:109` is bare
    `ListenAndServe`).
20. `/healthz`, `/readyz`.
21. **Background session cleanup** — `DeleteExpiredSessions` runs once at
    startup and never again.
22. Dockerfile + CI.
23. **Expiry notification emails** — biggest functional gap. The product premise
    is expiring keys and users get zero warning; per-profile (possibly short)
    expiry makes silent key death more likely, not less.

## Phase 7 — Tests

Zero tests exist today. Priority order: store against in-memory SQLite (works
well and is fast — used it during evaluation), handlers against a fake
`KeyProvider`, then the quota conversion math.

---

## Scope note

Any production pricing change touches real spend accounting on
`litellm.uni-osnabrueck.de`. Testing moves first; production is a separate
explicit decision.
