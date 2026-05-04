---
title: Launch-day operator checklist (L6.4 cutover)
last_verified: 2026-05-03
status: operator runbook
---

# Launch-day operator checklist

End-to-end cutover runbook for **L6.4** in the launch-readiness
backlog. Follow this **on the day**. Each step has a clear pass
condition; do not advance until the prior step passes.

The mental model: every launch-blocking row is already ✅ when
this runbook starts. This doc is the orchestration that flips
the public-facing surface from "private staging" to "production"
without surprises.

## T-7 days — final pre-cut

The week before the cut. Done while everything is still calm.

- [ ] **Open PR pile is drained.** All open `docs/`, ops, and
      reclassification PRs from the launch sprint are merged.
      `gh pr list --state open --limit 100` returns zero
      launch-blocking entries. Anything left becomes
      [post-launch](../architecture/launch-readiness-backlog.md)
      explicitly.
- [ ] **Last L6.5 docs sweep.** One final pass through
      [`docs/`](../) — every `last_verified` date inside
      30 days, every runbook frontmatter `status:` reflects
      reality. Single PR; merge same day.
- [ ] **External security review (L5.6) findings closed.** Each
      reviewer comment has a tracked PR or an explicit "won't
      fix, post-launch" decision recorded.
- [ ] **SEV-1/SEV-2 dry-run (L5.7) done.** A drill that killed
      something on staging, captured the time-to-detect +
      time-to-mitigate, and updated the
      [SEV playbook](sev-playbook.md) with anything that
      surprised the on-call.
- [ ] **Chaos Wave 1 retrospective (L5.5) clean.** All three
      scenarios passed; `test/chaos/reports/<launch-cut>/RETRO.md`
      committed; any code-side fixes the chaos run motivated have
      landed.
- [ ] **Showcase site staged.** Cloudflare Pages project for
      `web/showcase/` is connected per
      [`showcase-deployment.md`](showcase-deployment.md), a
      preview deploy succeeded, `ratesengine.net` custom domain
      is bound but DNS still points at staging. Final cutover is
      step 5 of T-0.

## T-3 days — final freeze

- [ ] **Merge freeze.** No new PRs to `main` except critical
      bug fixes flagged with the `launch-blocker` label.
      Document the freeze in `#rates-engine` and pin the date.
- [ ] **Public-flip dry-run.** Walk the
      [public-flip cut-over mechanics](public-flip.md#cut-over-mechanics)
      against a temporary `RatesEngine/rates-engine-dryrun`
      repo. Verify zero diff between private working tree and
      the orphan-branch initial commit. Delete the dryrun repo.
- [ ] **Customer demo (L6.6) scheduled.** Calendar invite sent;
      demo deck reviewed; demo URL is the soon-to-be-public
      `api.ratesengine.net` (not staging).

## T-1 day — go/no-go

- [ ] **Production environment is green.** Every dashboard panel
      on the SLO board reading nominal:
      - `ratesengine_aggregator_ticks_total` rising on `outcome="ok"`.
      - `ratesengine_source_events_total` rising for every
        configured source (`source_enabled=1`).
      - `ratesengine_aggregator_vwap_writes_total` rising.
      - No fired alerts in Alertmanager.
- [ ] **SLA probe latest pass.** `cmd/ratesengine-sla-probe`
      against the staging URL ran in the last 4 h with `verdict:
      pass`. (Or run it manually now — see "Smoke test" below.)
- [ ] **CDN provisioned.** Per
      [`cdn-setup.md`](cdn-setup.md). DNS for
      `api.ratesengine.net` is proxied through Cloudflare;
      curl headers show `cf-cache-status` for the historical
      surfaces.
- [ ] **Status page provisioned.** Per
      [`status-page-setup.md`](status-page-setup.md). Upptime
      first probe cycle has run; all components show "operational".
- [ ] **Customer comms ready.** Email/Slack draft for the
      announcement is approved by stakeholders, ready to send
      post-cut.
- [ ] **Rollback plan rehearsed.** Walked through
      [`rollback.md`](rollback.md) — operator knows the DNS
      revert, rate-limit reset, and customer-comms templates
      cold.
- [ ] **Go/No-go decision.** All checkboxes above ticked → GO.
      Any unticked → defer cutover; fix what's blocking; revisit
      tomorrow.

## T-0 — cutover

Order matters. Don't skip.

1. **Tag the release (`release-process.md` §Cut).**
   ```sh
   # On private repo first; the tag points at the commit that
   # contains the promoted CHANGELOG block.
   git checkout main && git pull --ff-only origin main
   git tag YYYY.MM.DD.1
   git push origin YYYY.MM.DD.1
   ```

2. **Public-flip (`public-flip.md` §Cut-over mechanics).**
   Follow the 6-step procedure. The orphan-branch verification
   diff at step 4 MUST be zero. Stop and investigate if it
   isn't.

3. **DNS flip — `api.ratesengine.net`.**
   At Cloudflare, the proxied A/CNAME for `api` is already in
   place from the CDN setup. The "flip" here is **enabling
   the public rate-limit tier**: edit the API binary's
   `[api].auth_mode` config from `none` (private staging) to
   the production value, restart the API binary on each
   region. Ansible role does this in one command:
   ```sh
   ansible-playbook -i inventory/r1.yml deploy/ansible/roles/api/restart.yml \
     --extra-vars 'auth_mode=apikey'
   # Repeat for r2, r3.
   ```

4. **Smoke test the public surface.**
   ```sh
   RATESENGINE_PROBE_API_KEY=rek_… \
   ratesengine-sla-probe \
     -base-url https://api.ratesengine.net/v1 \
     -duration 30s \
     -concurrency 4 \
     -report-format text
   ```
   The API key is required — without one the probe trips the
   anonymous-tier rate limit (60 req/min) and the verdict reads
   `fail` on availability for reasons unrelated to actual SLA
   compliance. Mint a fresh load-test key from the operator
   vault. Pass condition: `verdict: pass`. Any `failed_reasons`
   halts the cut → trigger rollback.

5. **Showcase site goes live (`ratesengine.net`).** Per
   [`showcase-deployment.md`](showcase-deployment.md), the
   site is built statically from `web/showcase/` and served
   from Cloudflare Pages. Trigger a fresh build now that the
   API is in production auth-mode so the build-time
   `generateStaticParams` fetch picks up the live coin
   directory:
   ```sh
   # CF Pages: dashboard → Workers & Pages → ratesengine-showcase
   #          → Deployments → Retry deployment
   #
   # Or push an empty commit:
   git commit --allow-empty -m "chore(showcase): rebuild for launch"
   git push origin main
   ```
   Pass condition: `curl -I https://ratesengine.net | head -3`
   returns 200, and `curl -I https://ratesengine.net/coins/USDC/`
   is 200 (not 404).

6. **Status page goes live.** Post the launch-cut maintenance
   window resolved. (Or, if Upptime has been running pre-cut,
   confirm components are still "operational".)

7. **Send customer comms.** Email + Slack templates from T-1 day.
   Public announcement on the project handle if applicable.

8. **Open the L6.7 24-h watch.**
   - On-call clock starts.
   - SLO dashboards open in a window the on-call keeps
     tabbed for the next 24 h.
   - Any alert in the first 24 h is treated as a SEV-2
     minimum (per the
     [release-process post-flight](release-process.md#post-flight))
     regardless of impact, so the team builds muscle memory
     for the actual escalation flow on the day it matters.

## Pass condition for the whole runbook

- The release tag is on `main`, on the public repo, and as a
  GitHub Release.
- `https://api.ratesengine.net/v1/healthz` returns 200 from
  any external network.
- `https://ratesengine.net` returns 200 and renders live
  data in the home Network panel.
- `https://status.ratesengine.net` shows "all systems
  operational".
- The customer-comms message has been delivered.
- The SLA probe has logged at least one passing run against the
  public URL post-cut.
- L6.4 in `launch-readiness-backlog.md` flips 🔴 → ✅.

## If anything fails mid-cut

Stop. Open [`rollback.md`](rollback.md) and follow the matching
failure-mode section. Cutover is reversible up to step 6 (DNS
revert is one-line); after step 6 (customer comms sent) the
rollback also includes a follow-up "we're rolling back" message.

## Cross-references

- [`release-process.md`](release-process.md) — the per-release
  procedure this runbook orchestrates.
- [`public-flip.md`](public-flip.md) — repo cut-over mechanics.
- [`cdn-setup.md`](cdn-setup.md) — CDN provisioning.
- [`showcase-deployment.md`](showcase-deployment.md) — `ratesengine.net` (showcase site) hosting.
- [`status-page-setup.md`](status-page-setup.md) — status page setup.
- [`chaos-wave1-runbook.md`](chaos-wave1-runbook.md) — chaos
  Wave 1 execution.
- [`rollback.md`](rollback.md) — what to do when something breaks.
- [`sev-playbook.md`](sev-playbook.md) — incident escalation.
- [`sla-probe.md`](sla-probe.md) — the post-cut smoke probe.
- L6.4–L6.7 in [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md).
