# How profile changes reach existing keys


Limits are pushed to the gateway when a key is issued. After that, a background
**limit sync** keeps every key in line with its owner's profile. It reacts to
everything that can change what a key should enforce:

- an admin edits a profile's models, TPM/RPM limits or quota windows,
- an admin moves a user to another profile,
- another profile becomes the default (this affects every user without a
  profile of their own),
- a time-limited assignment runs out,
- a person logs in with a new username or email address, which their LiteLLM
  user is labelled with (see [People in LiteLLM](gateway.md#people-in-litellm)).

Renaming a profile, rewording its description or changing its key validity
does not touch any key, because none of that changes what the gateway
enforces.

**How it works.** Each profile has a revision number that goes up whenever its
limits change, and each key records the profile and revision it was last
updated with. A key whose owner's profile differs from that record is out of
date. The sync works through the out-of-date keys one at a time (more with
`LIMIT_SYNC_WORKERS`). An admin change starts it immediately; the admin's page
does not wait for it. One update takes three calls to the gateway, so a change
to a profile with 5,000 users takes about 25 minutes with the default single
worker.

Expired keys are left alone: they enforce nothing, and updating one would
still rewrite its owner's allowance in the gateway. A key that is extended
later is updated then, if it is out of date.

There is no queue of changes. A key always receives what its owner's profile
says at the moment it is updated, so if a profile is saved twice in quick
succession, keys not reached yet only ever see the second version, and keys
that already got the first are updated again.

**Failures.** An update that fails, for example because the gateway is down,
is retried every `LIMIT_SYNC_INTERVAL`. The admin panel lists failed keys with
the gateway's error. A key whose owner has no profile at all, because no
default profile exists, is never updated with empty limits, which the gateway
would read as "unlimited"; it is reported as failed instead.

**Keys deleted in the gateway.** A key deleted in LiteLLM directly, outside
Keyosk, cannot be restored: Keyosk does not have what it would need to
recreate it. But "the gateway does not have this key" is also what a Keyosk
instance pointed at the wrong LiteLLM, or at one restored from an old backup,
sees for every key. So Keyosk does not forget such a key at once. When the
sync, the dashboard or an extension finds that the gateway no longer has a
key, the key is **marked as missing** and a `key.missing` entry goes to the
audit log. From then on:

- The owner's dashboard says the gateway does not recognise the key and offers
  a new one. Extending is not offered. The owner can still delete the key or
  generate a new one, which replaces it.
- The admin panel shows the key as missing since its first sighting.
- The sync stops updating its limits and no expiry reminder is sent for it.
- Every sync run asks the gateway about each marked key. One that is back is
  unmarked (`key.found`) and updated like any out-of-date key, so changes made
  while it was gone reach it. One that stays missing past
  `KEY_MISSING_GRACE_PERIOD` is removed (`key.gone`); the owner can then
  generate a new key. If the gateway cannot answer, the key is left as it is
  and the grace period still counts from the first sighting.

Removal therefore takes the grace period plus up to `LIMIT_SYNC_INTERVAL`.
Deleting or revoking a key the gateway no longer has succeeds, since the key is
gone either way.

Only LiteLLM's own "not found" answer counts. A 404 from anything in front of
it, such as a reverse proxy whose route changed or a maintenance page, is an
ordinary failure: the record is kept and the call retried.

If a Keyosk instance is pointed at the wrong LiteLLM, every key is marked
within one sync run and the monitoring dashboard shows it (see
[Metrics](../operations.md#metrics)): correct `LITELLM_BASE_URL` within the grace period and
the next run unmarks them all. The same run asks the gateway about every marked key, one request
each, so this costs as many requests as there are keys.

**After upgrading** to the version that introduced the sync, every existing
key counts as out of date once, so the first run updates all keys. With many
users this takes a while; the admin panel shows the progress.

**Changes made directly in the gateway** are invisible to Keyosk. To undo
them, mark every key as out of date:

```bash
keyosk -resync-limits
```

The command needs only `DB_PATH`, changes nothing in the gateway itself, and
exits at once; the running server then updates every key on its next run,
within `LIMIT_SYNC_INTERVAL`. It must run where the database is, so against a
container use `docker exec <container> keyosk -resync-limits`, or
`kubectl exec` into the pod. Run it from cron to do this nightly.

The dashboard never pushes limits. It only reads, and when it cannot load the
user's profile, key or usage, it says so rather than showing defaults.

Clearing a quota sends an explicit `null`. LiteLLM leaves an omitted field
untouched, so a profile that loses its allowance would otherwise keep enforcing
the previous one.
