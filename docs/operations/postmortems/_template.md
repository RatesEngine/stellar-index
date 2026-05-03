---
title: "[SEV-N] one-line summary — YYYY-MM-DD"
date: YYYY-MM-DD
severity: SEV-?
status: draft | published
duration: HH:MM
authors: [ash]
---

<!--
Postmortem template. Copy this file to
`YYYY-MM-DD-short-slug.md` in the same directory; replace the
frontmatter; fill in each section.

The point of the postmortem is to give future-us (and external
readers, where applicable) the information needed to avoid the
same incident class. NOT to assign blame. Write it for the
person who joins the team in six months and inherits the
runbook the incident motivated.

Publish to the public status page only AFTER stakeholder review.
The drafting period (status: draft) is for getting facts
straight; the publishing period (status: published) is for the
permanent record.
-->

# [SEV-N] one-line summary — YYYY-MM-DD

## TL;DR

<!-- 2-3 sentences. The "what" + the "what we did about it". A
reader who only reads this paragraph should know whether the
incident is relevant to their concerns. -->

## Impact

<!-- Concrete customer-visible effect:
- Which surfaces were affected (`/v1/price`, etc.)
- Which pairs/assets if scope was narrower
- Duration of customer-visible impact (may be < the
  total-incident duration if internal triage took longer)
- Estimated request count affected, or "unmeasured but
  bounded by N" -->

## Timeline (UTC)

<!-- Bulleted, terse. Include:
- First user/external signal
- First internal alert
- Triage decision
- Mitigation start
- Mitigation complete
- All-clear

A timeline is the most-cited part of any postmortem. Get it
right; correct from logs/git/journalctl, not memory. -->

- HH:MM — …
- HH:MM — …

## Root cause

<!-- The actual mechanism. Not "the engineer made a mistake";
the latent condition + the trigger that combined to produce
the failure. Cite code paths, configs, runbook gaps, etc.
Include reproducible steps if applicable. -->

## What went well

<!-- Detection, response, communication, runbooks that worked,
graceful-degradation paths that engaged. Naming things that
worked is as important as naming things that didn't —
otherwise we lose the institutional memory of "this saved us
that one time". -->

## What went poorly

<!-- The gaps. Slow detection, missing alert, runbook step that
turned out to be wrong, communication delay. Be specific.
"Detection was slow" is not actionable; "the alert
`ratesengine_X` fires after 30 min but the customer signal
arrived at 5 min" is. -->

## What we got lucky on

<!-- The near-misses. The thing that didn't happen but easily
could have, the action we almost took but didn't.
Documenting these calibrates future risk-tolerance and
guides where to invest in defence-in-depth. -->

## Action items

<!-- Each one a tracked PR or ticket. "Investigate X" is not
an action item; "PR to add alert Y, owned by Z, due A" is.
Format:

- [ ] Action — owner — link to PR/issue
-->

- [ ] …
- [ ] …

## Lessons (for future-us)

<!-- 1-3 bullets, the takeaways someone should remember even
without reading the full postmortem. The kind of thing that
makes it into a CLAUDE.md "Things that will surprise you"
entry, or into the SEV playbook's checklist. -->

-

## Related

- Alert(s) that fired:
- Runbook(s) that triggered:
- PR(s) shipping the fix:
- Code paths involved:
