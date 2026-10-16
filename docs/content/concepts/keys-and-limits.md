# Keys and usage limits

## How extending works

Extend sets the expiry to **now + the profile's key validity**. It does not add
to the existing expiry, so clicking twice does not stockpile time, and it does
not use the duration the key was originally created with — that is never
stored.

The consequence is that policy changes apply on the next extend: move a user
from a 30-day profile to a 365-day one and their existing key extends by 365,
without needing to be regenerated.

## How usage limits work

Admins configure quotas as **spend budgets** per period, in the unit LiteLLM
prices its models in (`BUDGET_UNIT` labels them on the page; default `$`).
LiteLLM enforces spend directly, so the figure an admin enters is the figure
the gateway enforces, whatever mix of models a key uses. A model priced at `0`
or `null` accrues no spend, so a budget never binds on it; the server warns
about such models at startup.

Quotas used to be stored in tokens and converted at one nominal price. That
was exact only while every model cost the same per token. Migration `20240007`
converts existing token quotas at that nominal rate (0.0000001 per token) so
enforced caps keep their size; deployments that priced models differently
should review profile budgets after upgrading.

Requests fail with HTTP 422 (`budget_exceeded`, "Budget has been exceeded!")
once the allowance is spent and resume when the period resets. The TPM and RPM
rate limits answer 429 instead.

**Several windows per profile.** A profile can hold one allowance per period
(hourly, daily, weekly, monthly) and LiteLLM enforces each independently — a
key takes `budget_limits` as a list of `{budget_duration, max_budget}` objects
and rejects with `ExceededBudget: Key over 1h budget` when any one is spent.
The tightest window binds, so a shorter period may not carry a larger budget
than a longer one; the admin form rejects that. The widest window is held
against the user rather than the key so regenerating a key does not reset it
([issue #26](https://github.com/virtUOS/keyosk/issues/26)). This needs LiteLLM v1.105.0 or later: the dashboard reads what has
been spent in each window from the `budget_limits_usage` that `/key/info`
reports since then ([issue #116](https://github.com/virtUOS/keyosk/issues/116)).
