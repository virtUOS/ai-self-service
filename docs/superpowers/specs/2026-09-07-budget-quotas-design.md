# Budget-denominated quotas: design

Date: 2026-09-07. Outcome of an internal discussion about how usage should be
shown once LiteLLM serves models with different per-token prices.

## Problem

LiteLLM enforces quotas as **spend**, not tokens. The portal stores quotas in
tokens and converts at one rate for every model (`Client.TokensToBudget` /
`BudgetToTokens`, README "How usage limits work"). That is exact only while
every model costs the same per token. Once prices differ, every token figure
on the dashboard — used, remaining, and the limit itself — becomes wrong,
because spend divided by one rate no longer corresponds to any real token
count.

## Decisions

1. **Quotas are budgets.** A profile's quota window is an amount of spend per
   period, stored as a float in the currency the gateway prices models in.
   Admins enter budgets directly. The token→spend conversion is removed.
2. **Percentage is the headline.** Each quota window shows "N% used" with a
   bar. Both spend and cap come straight from LiteLLM, so the percentage is
   exact whatever models were used.
3. **Cost shown alongside.** The subtitle under each bar reads
   "$0.06 of $0.10 · resets in 12 days". The unit label is configurable
   (`BUDGET_UNIT`, default `$`) so a deployment using nominal prices can label
   them "credits" instead of implying money.
4. **No remaining-token estimate.** Tokens left depends on which model is used
   next, so it is a guess. It is not shown.
5. **Token history stays, and gets a per-model table.** The per-day token
   chart is real data from the spend log (re-enabled 2026-08-25) and is what
   research users want. A table under it breaks the last 30 days down by
   model: requests, input tokens, output tokens, total.
6. **Total-only fallback shows spend.** When per-request logging is off, the
   key's cumulative counter is spend, and is shown as spend rather than
   converted to tokens.
7. **Migration converts existing token quotas at the nominal rate**
   (0.0000001 per token, the rate this deployment's models are priced at).
   Existing enforced caps therefore do not change size on upgrade. The
   dashboard re-applies profile limits on every load, so what is stored is
   what becomes enforced.
8. **Pricing check narrows to unpriced models.** A model priced at zero
   accrues no spend and escapes every quota; startup still warns about those.
   Warnings about models *disagreeing* on price go away, since disagreement is
   now the expected state.

## Out of scope

- A per-model "what your remaining budget buys" estimate table. Can be added
  later, clearly labelled as an estimate.
- Charting spend per day (the chart stays in tokens).
