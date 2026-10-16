# Admin panel


Admin rights are recorded on the account, as a flag that the admin panel
checks on every request. An email address never grants them: the IdP can
reassign an address to someone else. The flag is set in one of two ways, and a
deployment uses exactly one:

- **From the login system**, with `ADMIN_GROUP` set: the members of that group
  are the admins. Membership is managed in the IdP and the panel cannot change
  it.
- **In the panel**, without `ADMIN_GROUP`: admins promote and remove each
  other, and the first one is marked with a command on the server.

The former settings `ADMIN_IDS`, `ADMIN_EMAILS` and `ADMIN_ROLE` are no longer
read; Keyosk warns at startup if one is still set ([issue #107](https://github.com/virtUOS/keyosk/issues/107)).

## Admins from the login system

Set `ADMIN_GROUP` to a group in the IdP, and make sure the IdP puts the
account's groups into the **ID token**. At every login Keyosk reads the
claim, by default `groups`, and sets the account's flag to whether it lists
the group. Each change is written to the audit log with the login system as
the actor. The **Admins** tab then lists who was a member at their last login,
with nothing to promote or remove.

- **Timing.** Adding someone to the group or removing them takes effect at
  their next login, not straight away. To cut someone off at once, end their
  session in the IdP as well; Keyosk honours back-channel logout.
- **Exact match.** The group name is matched exactly. A token without the
  claim, or with a claim of another shape, makes nobody an admin. When such a
  token demotes an admin, the login logs a warning and the audit entry says
  the claim was absent rather than that they left the group, since the usual
  cause is a missing mapper in the IdP, not a changed membership.
- **Keycloak.** Add a *Group Membership* mapper with the claim name `groups`,
  "Add to ID token" on, and "Full group path" off; with the full path on, the
  claim lists `/name`, so set `ADMIN_GROUP=/name` instead. Realm roles work
  too: set `ADMIN_GROUP_CLAIM=realm_access.roles` and add a mapper that puts
  realm roles in the ID token, since Keycloak sends them only in the access
  token by default. A client role is
  `resource_access.<client id>.roles`. [`dev/realm-export.json`](https://github.com/virtUOS/keyosk/blob/main/dev/realm-export.json) has working
  examples of both mappers.
- **Other IdPs.** Dex, Okta and Authentik send `groups` only when asked: set
  `OIDC_EXTRA_SCOPES=groups`. Microsoft Entra ID lists groups by object ID, so
  `ADMIN_GROUP` is that ID, not the group's name; a user in too many groups
  gets no `groups` claim from Entra at all and is therefore not an admin.

## The first admin

This and the next section apply when the panel manages admins, without
`ADMIN_GROUP`.

Nobody is an admin on a fresh database, and Keyosk says so at startup. The
person logs in once, so Keyosk has their account, and whoever runs the
server marks it:

```sh
sqlite3 "$DB_PATH" "SELECT id, name, email FROM users"
sqlite3 "$DB_PATH" "UPDATE users SET is_admin = 1 WHERE id = 7"
```

`DB_PATH` is the database file from the configuration. When Keyosk runs in
a container, run the command on the host against the mounted file. The update
is safe while Keyosk runs, and takes effect on the person's next request.

The same command is the way back in if every admin is ever lost, for example
because the only admin left. Running the server already means having full
access to it, so this adds no new risk.

## Further admins

The **Admins** tab lists every admin with their name, email and OIDC subject,
and lets an admin promote any account that has logged in before: the form
offers the accounts Keyosk knows, filtered by name or email as you type,
and the right is recorded against the account rather than the address. An
admin can remove any other admin but not themselves, so the panel can never be
left with nobody in charge by accident. A removal takes effect on the person's
next request, and every promotion and removal is written to the audit log.

## Switching between the two

The flags stay as they are when the setting changes. Going from the login
system to the panel keeps the last group members as admins, so nobody is
locked out. Going from the panel to the login system keeps the panel's admins
until each of them next logs in, when the group decides. To end them at once,
clear every flag after the switch; members of the group get theirs back at
their next login:

```sh
sqlite3 "$DB_PATH" "UPDATE users SET is_admin = 0"
```

The admin panel at `/admin` provides:

- **Profiles** — create and edit profiles with model restrictions, TPM/RPM limits, and budget caps. Mark one profile as default; it applies to users with no explicit profile assignment.
- **Users** — view everyone who has logged in, see their key prefix and expiry,
  assign a profile, and revoke a key. An assignment can carry a deadline: set a
  date and the user reverts to the default profile when it passes, or to a
  profile you choose, or their key is deleted if you tick that instead. Leave
  the date empty for a permanent assignment.
- **Admins** — list the admins, and promote and remove them when the panel
  manages them, as described above.
- **Audit log** — the 50 most recent key and profile changes, recording who did
  what to whom. Rows outlive the key and user they describe, so revoking does
  not erase the history. Changes nobody in Keyosk made, such as a profile
  deadline passing or a key found missing from the gateway (`key.missing`,
  and later `key.found` or `key.gone`), are recorded with `system` as the
  actor.
- **Limit sync status** — a card above the tabs says whether all keys
  carry their profile's current limits yet. If not, it shows how many are still
  waiting, whether an update is running, which keys failed and why, and each
  profile shows how many of its keys are waiting. See
  [How profile changes reach existing keys](concepts/profile-changes.md).

Profile fields:

| Field            | Description                                                              |
| ---------------- | ------------------------------------------------------------------------ |
| Models           | Comma-separated list of allowed model names (empty = all models)          |
| Key validity     | How long a generated key lasts, in days (blank = `KEY_DURATION_DAYS`)     |
| Usage limit      | Spend allowance per period, in `BUDGET_UNIT` (blank = unlimited); several windows may apply at once |
| Limit resets     | `hourly`, `daily`, `weekly` or `monthly`                                  |
| TPM limit        | Maximum tokens per minute — burst control, complements the usage limit    |
| RPM limit        | Maximum requests per minute                                               |

Different cohorts get different profiles: students might get 30-day keys with a
$1.00 daily allowance, lecturers 365-day keys with no quota.
