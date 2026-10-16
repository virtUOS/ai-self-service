# Administrators

Admin rights are recorded on the account, as a flag that the admin panel
checks on every request. An email address never grants them: the IdP can
reassign an address to someone else. The flag is set in one of two ways, and a
deployment uses exactly one:

- **From the login system**, with `ADMIN_GROUP` set: the members of that group
  are the admins. Membership is managed in the IdP and the panel cannot change
  it.
- **In the panel**, without `ADMIN_GROUP`: admins promote and remove each
  other, and the first one is marked with a command on the server.

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
person logs in once, so Keyosk has their account, and finds their user ID on
the **Account settings** page. Whoever runs the server then marks it:

```sh
keyosk -make-admin 7
```

The command needs only `DB_PATH`, the database file from the configuration,
and must run where the database is: against a container use
`docker compose exec keyosk keyosk -make-admin 7`, as in
[Installation](installation.md#the-first-admin). It names the account it
marked, records the promotion in the audit log, and refuses when `ADMIN_GROUP`
is set, since the next login would take the right away again. It is safe while
Keyosk runs, and takes effect on the person's next request.

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
