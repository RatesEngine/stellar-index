<!--
Release notes template for a Rates Engine binary release.

The release.yml workflow auto-builds release notes by extracting
the matching CHANGELOG section. This template is the SHAPE that
CHANGELOG sections should follow — keep your CHANGELOG entry
matching this layout and the workflow output is publication-ready.

If you need to edit the notes after publication (e.g. add a
"Tested against protocol XX" line the workflow couldn't infer),
the auto-generated content already has the right scaffolding.

Every section is mandatory. If a section has no entries, write
"None." rather than deleting the heading — readers learn from the
absence of a Migration block as much as from its presence.
-->

# Rates Engine vX.Y.Z

**Operator action required: yes / no**

<!-- One-paragraph release summary. The 30-second pitch an operator
needs to decide whether to upgrade. Lead with the most operationally
relevant change. -->

## Tested against

- Stellar pubnet protocol **XX**
- `go-stellar-sdk` v?.?.?
- Galexie image tag `?.?.?`
- TimescaleDB `?.?.?`

## `pkg/*` versions included

<!-- One line per public Go module shipped at this binary tag. Bump
each independently per the SemVer rules in
docs/architecture/semver-policy.md. -->

- `pkg/client v0.?.?`

## Migration notes

<!-- Anything an operator MUST do to upgrade safely. Examples:
config schema additions, DB migrations, runbook changes, behaviour
toggles. Link the relevant runbook for each. If none, write "None."

Use this checklist if migration steps exist:
- [ ] Stop API + indexer
- [ ] Run `ratesengine-migrate up`
- [ ] Update `/etc/ratesengine.toml` — see config-reference diff
- [ ] Restart API + indexer
- [ ] Verify <metric> on the post-launch dashboard
-->

None.

## Added

<!-- New features. Cite the PR in parens. -->

## Changed

<!-- Behaviour changes that aren't strictly additive. Flag breaking
changes loudly here AND in the summary paragraph. -->

## Deprecated

<!-- Public identifiers (`pkg/*`) marked with godoc `Deprecated:` in
this release. Per the SemVer policy these stay in place for at
least one minor version before removal. -->

## Removed

<!-- Public identifiers removed in this release. Each must have been
flagged Deprecated in a prior release. -->

## Fixed

<!-- Bug fixes. Cite the originating issue/PR. -->

## Security

<!-- CVEs addressed, dependency bumps that close advisories,
auth-path hardening. If none, write "None." — readers should be able
to tell the absence of security work apart from the absence of
applicability. -->

None.

## Full changelog

See [`CHANGELOG.md`](../CHANGELOG.md#vXYZ).
