# Working with the LiteLLM gateway

## People in LiteLLM

Each person with a key has a LiteLLM internal user, which holds their budget.
Keyosk labels it with the person's username (`user_alias`), so LiteLLM's
user list shows who is who. Each key is bound to that user, and its alias,
`<subject>-<username>-<random>`, names the person as well. When someone logs
in with a new username, the limit sync updates both labels.

Keys carry no email address. Keys issued before this version still have one
in their metadata, which is left as it is: rewriting a key's metadata would
also replace settings an admin made there in LiteLLM. It goes when the key is
regenerated.

With `LITELLM_USER_EMAIL=true` Keyosk also records the person's email
address on their user. That is what makes them findable: LiteLLM's user
search only matches user IDs and addresses. Turn it on only where every
address is unique across everything connected to the gateway, other Keyosk instances
included. LiteLLM's own SSO login, for its admin UI, looks users up by
address, and with two users sharing one it attaches the login to either of
them. Turning the option on or off changes nothing until each key is pushed
again; run `keyosk -resync-limits` to do that for all of them.
Turning it off does not remove addresses already recorded.

After upgrading to the version that added the labels, the first sync updates
every unexpired key once, so every user and key is labelled.

## Several Keyosk instances on one gateway

A person's budget, the widest quota window of their profile, is held on a
LiteLLM internal user rather than on their key, so it survives a key rotation.
That user is named after the person's OIDC subject. Two instances that use the
same gateway and the same identity provider therefore share every person's
budget, and each instance's limit sync overwrites what the other one set.

Give every instance on a shared gateway but at most one its own
`LITELLM_USER_ID_PREFIX`. Its users are then named `<prefix>:<subject>`, and
the instances no longer touch each other's budgets. A single instance needs no
prefix, and existing deployments keep their users and budgets unchanged.

Set the prefix before Keyosk issues its first key. Changing it later is
not supported: keys stay bound to the old users, and keep counting against
their budgets, until each key is updated again. If it has to be done, run
`keyosk -resync-limits` afterwards. The sync then moves every
unexpired key to the new users, which start with nothing spent. The old users
are left in the gateway; don't delete them while another instance's keys still
belong to them, as deleting a LiteLLM user also deletes its keys.
