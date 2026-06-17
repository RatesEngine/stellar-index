# Security policy

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report them privately via one of:

1. **Email:** `security@stellarindex.io` (if the mailbox is not
   yet provisioned, use the GitHub Security Advisory path below).
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

- The Stellar Index server binaries (`stellarindex-indexer`, `stellarindex-aggregator`,
  `stellarindex-api`, `stellarindex-ops`, `stellarindex-migrate`).
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

## Public surfaces we intentionally expose

Some endpoints under `/v1/diagnostics/*` return operational
state that, in isolation, could be considered an information
leak. These are public **by design** so that operators (and the
public explorer at <https://stellarindex.io/diagnostics>) can
see the same ingest health a paying customer would — credibility
through transparency. Specifically:

- `/v1/diagnostics/cursors` — per-source ingest-cursor table,
  including completed backfill ranges and their lag. Reveals
  which sources we index, how far back each is hydrated, and
  which ranges are currently mid-fill. **Not gated.**
- `/v1/diagnostics/ingestion` — per-class entry counts +
  freshness. **Not gated.**
- `/v1/diagnostics/density` — coverage roll-up. **Not gated.**

Risk model: an adversary who scrapes these endpoints learns no
more than they can already infer from the price/markets/network
endpoints' wire shape (which sources contribute to which pairs;
where data starts; whether the live tip is fresh). The
operational benefit — every customer can verify our claimed
freshness in real time without filing a support ticket — is
worth the disclosure cost. Findings F-0026 / F-0034
(audit-2026-05-26) reviewed this surface and accepted it as
intentional transparency.

If you find a `/v1/diagnostics/*` endpoint that exposes
**secrets** (credentials, API keys, internal IPs, customer
account state), please report it as a vulnerability per this
file's reporting section — that would be a leak, not the
intentional-transparency posture documented here.

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
