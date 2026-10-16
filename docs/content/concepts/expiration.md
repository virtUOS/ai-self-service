# Key expiration

## How extending works

Extend sets the expiry to **now + the profile's key validity**. It does not add
to the existing expiry, so clicking twice does not stockpile time, and it does
not use the duration the key was originally created with — that is never
stored.

The consequence is that policy changes apply on the next extend: move a user
from a 30-day profile to a 365-day one and their existing key extends by 365,
without needing to be regenerated.

## Expiry notifications

Keys expire, so users are warned before they do — otherwise a key dies silently
in someone's pipeline.

- The dashboard shows a warning once a key is within 14 days of expiring, and
  an error once it has expired.
- With `SMTP_HOST` set, an email goes out at 14, 3 and 1 days before expiry.
  Each notice is sent at most once per key and threshold; a delivery failure
  leaves it pending so the next run retries.
- The notice is bilingual, German first: nothing records a per-user language,
  so it cannot pick one. Both halves carry the expiry date and the Keyosk link.

The thresholds are a code-level default (`notify.DefaultThresholds`), not an
admin setting.

Without `SMTP_HOST` Keyosk logs what it would have sent. It does not
silently pretend mail was delivered.
