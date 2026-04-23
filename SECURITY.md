# Security policy

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report them privately via one of:

1. **Email:** `security@ratesengine.net` (mailbox provisioning
   in Week 1 per
   [docs/discovery/delivery-plan.md](docs/discovery/delivery-plan.md)
   — until then use GitHub Security Advisory below).
2. **GitHub Security Advisory:** use the "Report a vulnerability"
   button on the Security tab of this repo.

We commit to:

- Acknowledging receipt within **72 hours**.
- Providing an initial assessment within **7 days**.
- Fixing HIGH / CRITICAL issues within **30 days** (confidential
  patch), or — if a public fix is required sooner — coordinating
  with the reporter on disclosure timing.
- Public credit to reporters who want it, on a case-by-case basis.

## Scope

In scope:

- The Rates Engine server binaries (`ratesengine-indexer`, `ratesengine-aggregator`,
  `ratesengine-api`, `ratesengine-ops`, `ratesengine-migrate`).
- The Go SDK in `pkg/client/` (planned; will be in scope once it ships —
  tracked in CLAUDE.md repo map).
- The deployment kits in `deploy/`.
- The API surface exposed at our hosted endpoint.

Out of scope (report to the relevant upstream instead):

- Vulnerabilities in `stellar/go-stellar-sdk`, `stellar/stellar-core`,
  `stellar/stellar-rpc`, `stellar/stellar-galexie`,
  `stellar/rs-stellar-archivist`.
- Vulnerabilities in `withObsrvr/stellar-extract` or other
  third-party audited deps (see `VERSIONS.md`).
- Operational issues with third-party Stellar validators, oracles,
  DEXes, or lending protocols we index.

## Responsible disclosure

Embargo period: up to **90 days** after we acknowledge receipt, or
until we ship a fix — whichever comes first. We may extend the
embargo by mutual agreement if coordinated cross-ecosystem fixes
are required.

## Hall of fame

We maintain a public acknowledgements list of reporters at
`docs/operations/security/hall-of-fame.md` (lands when we have
our first disclosure). Reporters may opt out.

## Keys

Our GPG key for encrypted disclosure will be published at
`docs/operations/security/gpg.md` once the team mailbox is
provisioned. Until then, use GitHub Security Advisories.

## Scope of the Stellar network itself

Our service depends on Stellar-network correctness. Vulnerabilities
in the Stellar protocol itself belong to the Stellar Development
Foundation — report via <https://stellar.org/halborn> or the SDF
security contact on <https://stellar.org/foundation/security>.
