# UI-managed admin rights: design

Date: 2026-09-18. Admin rights are currently fixed at deploy time. Granting
someone the admin panel means editing the deployment script and redeploying,
which is slow and puts an ordinary staffing change in the hands of whoever
holds the deploy credentials.

## Problem

There are two sources of admin rights today, both deploy-time
(`internal/config/config.go`):

- `ADMIN_ROLE` — a realm role from the IdP, read per-request from the ID token
  by `Config.HasAdminRole`. When set it wins outright.
- `ADMIN_IDS` — a static comma-separated list of OIDC subjects or email
  addresses, checked by `Config.IsAdmin`. This deployment uses this one.

Neither can be changed without a redeploy, and no admin can grant rights to
another. There is no database representation of an admin at all.

## Decisions

1. **A third source, in the database.** A new `admin_grants` table holds
   admins created through the UI. The existing two sources keep working
   unchanged.

2. **Precedence: role, then env list, then table.** Checked in that order.
   The realm role still wins so that deployments managing staff in the IdP are
   unaffected.

3. **The env list is a floor that the UI cannot remove.** An entry in
   `ADMIN_IDS` always grants admin and has no remove button. Lockout is
   therefore impossible and the deploy script stays the recovery path. This is
   the reason the table adds to the env list rather than being seeded from it.

4. **Grants store both subject and email.** `oidc_sub` is nullable: an admin
   granted before their first login is identified by email only, and the
   subject is recorded the first time they log in. The subject is then what
   authorises them. This is the migration the comment at `config.go:27-30`
   already asks for — an email is reassignable by the IdP, a subject is not.
   A grant still resolving by email is logged, matching the existing warning
   in `Admin.Middleware`.

5. **One resolver, not two call sites.** `Admin.Middleware`
   (`internal/handlers/admin.go`) and the `IsAdmin` flag on the dashboard
   (`internal/handlers/ui.go:143`) both call a single resolver that applies the
   precedence above. `config.IsAdmin` stays pure and database-free; the table
   lookup is a separate step the resolver owns. Duplicating precedence across
   two call sites is how the panel and the nav link drift apart.

6. **Self-revoke is refused.** An admin cannot remove their own grant. It is
   the obvious foot-gun and the check is one comparison.

7. **Grants and revocations are audited.** Two new actions alongside those in
   `internal/database/models.go`: `admin.granted` and `admin.revoked`, with the
   acting admin as actor and the affected person as subject.

## UI

A third section on `/admin`, alongside Profiles and Users:

- A list of current admins. Each row shows where the right comes from. Rows
  derived from `ADMIN_ROLE` or `ADMIN_IDS` render a "from configuration" badge
  and no remove button; only `admin_grants` rows can be removed.
- Granting is an email field on the tab, which works whether or not the person
  has ever logged in. An earlier draft also put a "Make admin" button on the
  existing user rows; it was dropped as redundant, since the field covers that
  case too and each user's address is already shown beside it.

## Schema

`admin_grants`:

| column             | type    | notes                                  |
| ------------------ | ------- | -------------------------------------- |
| `id`               | integer | pk, autoincrement                      |
| `oidc_sub`         | text    | nullable until the admin first logs in |
| `email`            | text    | not null                               |
| `granted_by_email` | text    | not null, who granted it               |
| `created_at`       | time    | not null                               |

Unique on `email`. A new migration file following the existing numbering in
`internal/database/migrations/`.

## Testing

- Precedence table tests: role beats env, env beats table, and a revoke
  against the table cannot strip an env or role admin.
- Self-revoke is rejected.
- A grant made by email alone authorises, records the subject on first login,
  and authorises by subject thereafter.
- Middleware test per source.

## Out of scope

- Per-admin permission levels. Admin is one flag; splitting it into
  capabilities is a separate design if it is ever wanted.
- Removing `ADMIN_IDS`. It stays as the floor, by decision 3.
