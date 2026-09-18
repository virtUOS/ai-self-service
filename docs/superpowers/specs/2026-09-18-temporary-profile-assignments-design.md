# Time-limited profile assignments: design

Date: 2026-09-18. Any profile can be granted to a user for a fixed period,
after which the assignment reverts on its own. Prompted by one such request —
a higher token limit until 4 October for a master's thesis — but the mechanism
is general: a course, a pilot, a trial of a more generous tier and a temporary
raise for one person are all the same thing to the portal. Granting any of
them today means moving the user onto another profile and remembering to move
them back by hand.

## Problem

`users.profile_id` is a single nullable FK
(`internal/database/models.go`). An assignment has no end: an admin who grants
a raised limit for a fixed period has to diarise the reversion themselves, and
a forgotten one leaves an elevated quota in place indefinitely.

## Decisions

1. **The time limit belongs to the assignment, not the profile.** There is no
   new kind of profile and no flag marking one as temporary. An admin creates
   whatever profile the case needs — exactly as they create any other — then
   assigns it to a user *until a date*. The same profile can be permanent for
   one user and expiring for another, and any profile can carry an expiry,
   including the default one. Nothing in the schema or the UI names a
   particular use for this.

2. **On expiry the user moves to the default profile**, unless the admin
   explicitly picked a different destination from the existing profiles when
   making the assignment. Default-to-default keeps the common case to one
   field.

3. **Deleting the key at expiry is a third, explicitly-chosen option.** It is
   never implied by leaving the destination unset — an unset destination means
   the default profile, per decision 2.

4. **Everything is nullable and additive.** No expiry date means a permanent
   assignment, which is every row that exists today. The migration changes no
   existing behaviour.

5. **The job pushes new limits upstream itself.** It does not rely on the
   dashboard sync at `internal/handlers/ui.go:721`. A user who stops visiting
   the dashboard would otherwise keep the elevated limits on a live key
   indefinitely, which is the exact failure this feature exists to prevent.
   The dashboard sync stays as the convergence path if the push fails.

6. **The user can see the deadline.** While a date is set, the dashboard names
   the profile and the date — "Your *thesis project* profile runs until
   4 October 2026", with whatever the admin called the profile. A limit that
   drops silently is a support ticket.

7. **Deleting a profile nulls any `profile_after_expiry` pointing at it**
   rather than blocking the delete. Those users then fall back to the default
   profile, which is the same outcome as an unset destination.

## Schema

Three nullable columns on `users`:

| column                 | type    | meaning                                   |
| ---------------------- | ------- | ----------------------------------------- |
| `profile_expires_at`   | time    | null = permanent assignment               |
| `profile_after_expiry` | integer | FK to profiles; null = the default profile |
| `revoke_key_at_expiry` | bool    | delete the key instead of switching        |

A new migration file following the existing numbering in
`internal/database/migrations/`.

## Admin form

The profile dropdown on each user row (`Admin.SetUserProfile`,
`internal/handlers/admin.go`) gains:

- an optional date field;
- revealed only once a date is set: a "then switch to" dropdown defaulting to
  *Default profile*, and a "delete the key instead" checkbox.

Leaving the date empty behaves exactly as today. The admin user table shows
the expiry date on the row so pending reversions are visible at a glance.

## Expiry job

A ticker in `cmd/server/main.go` beside the existing reminder job, running
every 15 minutes. A deadline of "4 October" should take effect near midnight
rather than up to an hour late; the query is cheap and indexed on
`profile_expires_at`.

For each user whose `profile_expires_at` has passed:

1. If `revoke_key_at_expiry`, delete the key upstream then locally, reusing
   the ordering in `Admin.RevokeUserKey` — upstream first, so a failure leaves
   the key tracked rather than orphaned.
2. Otherwise set `profile_id` to `profile_after_expiry` (or null for the
   default profile) and push the resulting limits to the gateway.
3. Clear all three columns, so the run is idempotent.
4. Audit the change.

Steps 2 and 3 run in one transaction: a user must never be left with a passed
deadline and the old profile still attached.

## Testing

- Store: expiry moves the user to the named profile, to the default when the
  destination is unset, and revokes the key when flagged.
- Job: a future date is untouched; a past date fires once and is a no-op on
  the second run; a failed upstream push leaves the deadline set for retry.
- Handler: the form round-trips all three fields, and an empty date clears
  them.
- Profile deletion nulls a `profile_after_expiry` referencing it.

## Out of scope

- Notifying the user by email before the profile reverts. The expiry reminder
  machinery in `internal/notify/` could carry it later; the dashboard notice
  from decision 6 is the first cut.
- Recurring or renewable grants. An admin re-assigns with a new date.
