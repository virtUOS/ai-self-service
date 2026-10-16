# Upgrading from ai-self-service


Keyosk is the new name of *AI API Key Self-Service Portal*. The rename changes
a few things you have to follow in your deployment; data and behaviour are
unchanged.

| Before | Now |
| --- | --- |
| Repository `virtUOS/ai-self-service` | `virtUOS/keyosk` (GitHub redirects the old URL) |
| Image `ghcr.io/virtuos/ai-self-service` | `ghcr.io/virtuos/keyosk` |
| Binary `ai-self-service`, release files `ai-self-service-<tag>.linux-x86_64` | `keyosk`, `keyosk-<tag>.linux-x86_64` |
| `OIDC_CLIENT_ID=ai-self-service` in the example configuration | `keyosk`. The value is read from your configuration, so an existing registration keeps working until you rename it at the identity provider |
| `ADMIN_GROUP=ai-self-service-admin` in the example configuration and the dev stack | `keyosk-admin`. Only the dev stack's group and role are renamed; production keeps whatever group `ADMIN_GROUP` names |
| Metrics `keyosk_*` | `keyosk_*`. Update dashboards and alert rules; the old series stop |
| Go module `github.com/virtuos/ai-self-service` | `github.com/virtuos/keyosk` |

Everyone signed in sees one failed form submission after the upgrade, because
the CSRF tokens are derived from a seed that carries the name. Reloading the
page fixes it. Sessions, keys and the database are untouched.
