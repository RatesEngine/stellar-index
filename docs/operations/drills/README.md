---
title: SEV drill framework
last_verified: 2026-05-03
status: ratified
---

# SEV drill framework

Rates Engine validates its incident-response posture (Coverage
matrix item **#20 SEV-1/SEV-2 dry-run**) through a three-tier
exercise cadence pinned in
[sev-playbook.md §8](../sev-playbook.md):

| Tier | Cadence | Duration | Touches systems? | Output |
| --- | --- | --- | --- | --- |
| Monthly tabletop | every month | ~30 min | no | drill writeup |
| Quarterly chaos | every quarter | ~2 h | yes (staging only) | drill writeup |
| Annual DR | every year | ~4 h | yes (production failover) | drill writeup |

Each drill produces a writeup in this directory with the same
action-item discipline as a postmortem. Action items go to the
issue tracker with owners + due dates and feed back into the
playbook + relevant runbooks.

## Layout

```
docs/operations/drills/
├── README.md                          (this file)
├── scenarios/                          tabletop scripts (canonical)
│   ├── sev1-timescale-primary-failover.md   (storage tier — disk-full)
│   ├── sev1-anomaly-freeze-stuck.md         (aggregator — stuck freeze marker)
│   ├── sev2-source-decoder-regression.md    (ingest — protocol upgrade)
│   └── sev2-redis-sentinel-failover.md      (cache tier — master swap)
└── YYYY-MM-<short-name>.md              one writeup per executed drill
```

Pick scenarios so a launch readiness drill cycle covers all
four critical paths: storage HA, cache HA, ingest robustness,
and aggregator anomaly response.

The scripts under `scenarios/` are the **canonical inputs** —
re-used across drills. Each drill executed against a script
produces a dated writeup at the top level (e.g.
`2026-05-tabletop-timescale-failover.md`) that names the
script + records what actually happened.

## Tabletop drill protocol (the "monthly")

The simplest tier. Tests the playbook itself, not the systems.
~30 minutes, ~3 people minimum (oncall + commander + scribe).

1. **Pre-read** (5 min). Drill leader picks a scenario from
   `scenarios/`. Participants skim it. They are also expected to
   skim the runbook the scenario links to (e.g.
   `timescale-primary-down.md`) before the meeting.

2. **Setup** (2 min). Leader reads the *Initial conditions* +
   *Trigger event* sections aloud. Scribe clones
   [`_template.md`](_template.md) to
   `docs/operations/drills/<YYYY-MM>-<short-name>.md` and starts
   filling in the trigger + response-narrative sections live.

3. **Walk-through** (15–20 min). Leader narrates the
   *Injection timeline* one beat at a time. After each beat,
   participants describe what they would do — referring to the
   linked runbook. Scribe records:
   - what was said vs what the runbook actually says (gaps =
     action items);
   - any "I don't know where that runbook is" moments
     (documentation gaps = action items);
   - any "we don't have that command/dashboard yet" moments
     (tooling gaps = action items).

4. **Validation** (3–5 min). Leader reads the *Validation
   criteria* aloud and the team scores how well the walk-through
   matched. Scribe records the scores.

5. **Action items + writeup** (within 24 h). Drill leader files
   the writeup and opens issues for each action item with the
   `drill-action` label.

## Chaos drill protocol (the "quarterly")

Same shape as a tabletop, but the *Trigger event* is **really
injected** in staging. Two extra rules:

- **Staging only.** Production is never the chaos surface. We
  use the staging environment with synthetic load running.
- **Pre-announced.** Stakeholders know the window. The drill
  leader posts T-7d / T-1h / T-0 / T+resolution announcements.

The script's *Injection timeline* describes how to inject the
fault — typically a `kubectl delete pod` / `iptables` block /
`pumba` command run by the drill leader.

## Annual DR protocol

Annual DR is the only drill that touches production: a
controlled flip from primary region to DR region for ~1 hour,
with all customer-visible fields (`stale`, `reduced_redundancy`)
exercised. Detailed protocol lives in
[sev-playbook.md §8.3](../sev-playbook.md) and
[ha-plan.md §6](../../architecture/ha-plan.md).

## Writeup template

Drill writeups follow the same Postmortem-but-shorter shape as
the [SEV playbook §6](../sev-playbook.md):

```markdown
---
title: <YYYY-MM> <Short scenario name>
date: YYYY-MM-DD
type: tabletop | chaos | annual-dr
scenario: scenarios/<file>.md
participants: [name, name, …]
---

## Trigger
<What was injected — copied from the script>

## Response narrative
<5–10 bullets: what the team did, in order, with timestamps>

## Gaps observed
<Bullets: what the runbook missed, what tooling missed, what
the playbook missed>

## Action items
<bulleted, each with owner + due date + issue link>

## Score
<against the scenario's Validation criteria — pass/partial/fail
per criterion>
```

## References

- [sev-playbook.md](../sev-playbook.md) — the canonical incident
  response procedure that drills exercise.
- [Coverage matrix #20](../../architecture/coverage-matrix.md) —
  the launch-readiness item this directory satisfies.
- [SRE workbook §28 "Postmortem culture"](https://sre.google/workbook/postmortem-culture/)
  — the writeup discipline.
