---
title: Release process — cutting a Rates Engine binary release
last_verified: 2026-05-03
status: living doc
---

# Release process

End-to-end procedure for cutting a Rates Engine binary release. This
is the runbook the on-rotation release engineer follows; it
implements the policy ratified in
[`docs/architecture/semver-policy.md`](../architecture/semver-policy.md).

CalVer tag format: `YYYY.MM.DD.N` (UTC date + same-day counter).

## Pre-flight

Done **before** cutting the tag — discovering any of these failed
mid-release wastes a tag and forces a `.N+1` cut.

1. **`main` is green.** The latest commit's CI run is all-passing on
   GitHub. No "merged with optional check failures" — every required
   AND optional job must be green.
2. **Working tree matches `main`.** `git checkout main && git pull
   --ff-only origin main`.
3. **CHANGELOG.md `[Unreleased]` is curated.** Walk it top to bottom
   and confirm every entry has a PR citation, every section heading
   that has no entries has been deleted, and that the order matches
   user-relevance (operator-visible at the top, internal refactors
   at the bottom).
4. **`pkg/*` version bumps are tagged.** If this release ships a new
   `pkg/client` version, that module's tag (`pkg/client/vX.Y.Z`)
   already exists on `main` from an earlier landed PR — **do not**
   bump `pkg/*` versions in the same commit as a CalVer release.
5. **Build dry-run is clean.** `make build` completes for every
   checked-in binary without errors.
6. **Stellar protocol is documented.** The protocol version the
   release was tested against is known (e.g. `23` for post-Whisk).
   Pulled from `stellar-core --version` on a test node, or from the
   pubnet block-explorer header.

## Cut

1. **Decide the tag.** Today's UTC date with the next available
   `.N`. Example: `2026.07.15.1` for the first release on 2026-07-15;
   `2026.07.15.2` if `.1` already exists (e.g. a quick rollback fix).
2. **Promote the CHANGELOG `[Unreleased]` block.** In a one-commit
   PR:
   - Replace `## [Unreleased]` with `## [YYYY.MM.DD.N] — YYYY-MM-DD`
   - Add a fresh empty `## [Unreleased]` block above it
   - At the bottom of the file, update the version-comparison links
     to point at the new tag
   - Title the PR `release: YYYY.MM.DD.N`
3. **Merge the release PR.** Squash-merge once CI is green. **Do
   not** tag before this PR has landed on `main` — the tag must
   point at the commit that contains the promoted CHANGELOG block.
4. **Create the tag.**
   ```sh
   git checkout main && git pull --ff-only origin main
   git tag YYYY.MM.DD.N
   git push origin YYYY.MM.DD.N
   ```
5. **Draft the GitHub Release.**
   ```sh
   cp .github/RELEASE_NOTES_TEMPLATE.md /tmp/release-notes.md
   # edit /tmp/release-notes.md — fill in every section
   gh release create YYYY.MM.DD.N \
     --title "Rates Engine YYYY.MM.DD.N" \
     --notes-file /tmp/release-notes.md \
     --verify-tag
   ```
   The release-notes content should be a near-verbatim copy of the
   CHANGELOG section for this version, expanded with the
   "Tested against" / "`pkg/*` versions" / "Migration notes" blocks
   from the template.
6. **Confirm the artefact set manually.** This repo snapshot does
   not ship an in-tree `release.yml`, `.goreleaser.yaml`, or per-binary
   Docker packaging flow. If you publish release artefacts for a tag,
   do so via the currently approved external packaging process and
   verify the uploaded binaries before announcing.

## Post-flight

1. **Announce.** Post the release URL to the operator channel +
   `#rates-engine-public` if applicable.
2. **Update `docs/operations/r1-deployment-state.md`** with the
   running version and any operator action that was taken (e.g.
   migration step, config edit).
3. **Watch dashboards for 1 h.** The standard SLO board + the
   per-pair freshness panel. Any anomaly within the first hour gets
   the same triage as a normal incident — file a SEV before
   considering rollback.
4. **Rollback path** (if needed): see the next section. File a SEV-2
   minimum and a postmortem in `docs/operations/postmortems/`.

## Rollback

The Rates Engine ships as systemd-managed binaries on bare-metal
hosts (per [ADR-0008](../adr/0008-ha-topology.md)) — there is no
container registry to retag and no orchestrator to roll back. A
rollback is a binary swap on each affected host.

### Pre-rollback

1. **Confirm the previous-known-good tag.** Either from `git tag`
   history or from `r1-deployment-state.md`'s "Running version"
   line at the time the current release was cut.
2. **Confirm the previous binary is still on disk.** The deploy
   convention keeps the last 5 release directories under
   `/opt/ratesengine/release-<tag>/`. If it's been pruned,
   rebuild it from the tag (`git checkout <tag> && make build`)
   on a build host before continuing.
3. **Decide the scope.** A bad indexer release does not require
   rolling back the API. Roll back only the affected binary unless
   the failure is shared (e.g. a config schema break).

### Procedure (per host, per binary)

For each affected host (`api-01..03` for API, `indexer-01..03`
for indexer, `aggregator-01..02` for aggregator) and each
affected binary:

```sh
PREVIOUS=2026.05.01.1                         # the known-good tag
BINARY=ratesengine-api                        # or -indexer, -aggregator

ssh root@<host> "
  systemctl stop ${BINARY} && \
  cp /opt/ratesengine/release-${PREVIOUS}/${BINARY} /usr/local/bin/${BINARY} && \
  systemctl start ${BINARY} && \
  systemctl status ${BINARY} --no-pager | head -20
"
```

For the API tier the rollback is **rolling**: drain one host out
of HAProxy via the stats socket (`disable server api_pool/api-01`),
swap that host's binary, re-enable, repeat. Avoids a 30-second
2-of-3-host window during the cutover. Indexer and aggregator are
single-active and can be swapped one at a time without drain.

### Post-rollback

1. Verify the runtime version: `curl -sf http://<host>:3000/v1/version`
   reports the previous tag.
2. The same alert that drove the rollback should clear within 5 min.
3. Update `docs/operations/r1-deployment-state.md` "Running version"
   and note the rollback in the postmortem.
4. The original (broken) tag stays on `main` — DO NOT delete it.
   Cut a `.N+1` hotfix once the underlying bug has a fix.

## Hotfix releases

Same procedure as above, with these differences:

- Branch from the previous release tag (not `main`), apply the fix,
  cut a new `.N` tag on the same day OR a new date if the day has
  changed
- The CHANGELOG entry under the hotfix tag references the originating
  incident's postmortem
- Post-flight notification flags this as a hotfix and includes the
  scope of what changed (one-line + link to PR)

Hotfixes never include unrelated work. If a fix needs additional
changes that aren't strictly required, those go into the next
regular release — never a hotfix.

## Cross-references

- [`docs/architecture/semver-policy.md`](../architecture/semver-policy.md) — the policy this runbook implements
- [`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md) — the template release engineers fill in
- [`CHANGELOG.md`](../../CHANGELOG.md) — every release's entry follows the same structure
- [`docs/operations/sev-playbook.md`](sev-playbook.md) — incident response if a release misbehaves
