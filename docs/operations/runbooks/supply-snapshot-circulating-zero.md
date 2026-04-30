---
title: Runbook — supply-snapshot-circulating-zero
last_verified: 2026-04-30
status: ratified
severity: P2
---

# Runbook — `ratesengine_supply_snapshot_circulating_zero`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_supply_snapshot_circulating_zero` |
| Severity | P2 (page) |
| Detected by | `deploy/monitoring/rules/supply-snapshot.yml` |
| Typical MTTR | 15–60 min |
| Impact | `/v1/assets/native` reports `circulating_supply: 0`, which is visibly wrong to anyone glancing at the response. Customer-visible data-quality incident. |

## Symptoms

- `ratesengine_supply_snapshot_circulating_xlm{asset_key="XLM"} <= 0`
  for ≥ 5 min.
- Per ADR-0011 native XLM circulating = total − Σ(SDF reserves).
  A non-positive value means either:
  - The operator-managed `reserve_balances_stroops` sum equals or
    exceeds the frozen total (config error), or
  - The XLMComputer math is producing nonsense (regression bug).

## Quick diagnosis (≤ 5 min)

```sh
# 1. What's the latest snapshot?
ratesengine-ops supply audit native -config /etc/ratesengine.toml

# 2. What does the operator config say?
grep -A 100 "^\[supply" /etc/ratesengine.toml

# 3. Sum the reserve balances and compare to frozen total.
python3 -c "
import re, sys
content = open('/etc/ratesengine.toml').read()
balances = re.findall(r'^\\s*\"?([A-Z0-9]+)\"?\\s*=\\s*\"?(\\d+)\"?', content, re.M)
total = sum(int(b) for _, b in balances if len(_) == 56)
print(f'sum of reserve balances: {total} stroops = {total/1e7:.2f} XLM')
print(f'frozen total:           500018068120000000 stroops = 50,001,806,812.00 XLM')
print(f'difference:             {500018068120000000 - total} stroops')
"

# 4. Dry-run with current config to confirm reproduction.
ratesengine-ops supply snapshot -config /etc/ratesengine.toml -dry-run
```

## Typical root causes

1. **Reserve balance overstated.** Operator copied an SDF
   announcement value with the wrong scale (e.g. wrote a USD-
   equivalent or an XLM value where stroops were expected,
   inflating by 10^7).
   - Signal: the diagnostic Python sums show
     reserve_total ≈ 10^7 × frozen_total.
   - Mitigation: divide the offending balance entry by 10^7;
     re-run the writer.

2. **All-reserve config — every account labelled "reserve".** A
   mistaken `sdf_reserve_accounts` list that includes the issuer
   account or a payment account.
   - Signal: extra G-strkeys in the list compared to SDF's
     announcement.
   - Mitigation: remove the misclassified accounts.

3. **XLMComputer bug.** Should not happen — the algorithm is
   trivial — but if a recent code change broke it, this would
   fire.
   - Signal: `ratesengine-ops supply snapshot -dry-run` produces
     the same wrong value with verified-correct config.
   - Mitigation: roll back the writer binary; file a P2 bug.

## Mitigation

- [ ] Step 1 — Identify root cause via Quick diagnosis.
- [ ] Step 2 — If config error: fix the TOML, force a run.
- [ ] Step 3 — If algorithm bug: roll back; file a P2 bug.
- [ ] Step 4 — In either case, verify
      `circulating_supply > 0` on the next snapshot.
- [ ] Verification: alert clears within 5 min after a corrected
      snapshot lands.

## Known false-positive patterns

- **Hypothetical post-XLM-burn future.** If Stellar somehow burned
  every XLM in circulation (e.g. a coordinated network shutdown),
  this alert would be correct, not a false positive. ADR-0011's
  zero-is-a-valid-answer note doesn't apply to native XLM
  specifically — XLM is hard-capped and indestructible by design.

## Related

- `supply-snapshot-unit-failed.md` — covers the writer-failure
  path; this alert presumes the writer ran successfully but
  produced a wrong value.
- `supply-cross-check-divergence.md` — divergence between classic
  + SAC counterparts.
- ADR-0011 §"Algorithm 1 — native XLM".

## Changelog

- 2026-04-30 — initial draft alongside #295 (textfile + alerts).
