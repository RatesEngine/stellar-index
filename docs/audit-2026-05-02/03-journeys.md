# Mandatory Journeys

Every journey must be traced end-to-end with evidence from code.

## J1. On-Chain Trade Ingest

`ledgerstream` or backfill input -> dispatcher -> source decoder ->
pipeline sink -> Timescale write -> aggregate surfaces.

## J2. Oracle Observation Ingest

Reflector / Redstone / Band decode path -> sink -> storage ->
aggregator or API consumer.

## J3. Blend Auction Path

Blend pool event -> decoder -> sink -> `blend_auctions` storage ->
downstream consumer or documented non-consumer boundary.

## J4. External Venue Path

External runner -> venue adapter -> trade sink -> storage ->
aggregation.

## J5. Closed-Bucket Price Path

Timescale closed bucket -> API `/v1/price` -> flags, confidence,
divergence, freeze, triangulation fallback.

## J6. Chart and History Path

Storage aggregates -> `/v1/chart` and `/v1/history/since-inception`
parameter validation -> response contract.

## J7. Tip and Observation Path

Direct latest observation or rolling tip path -> `/v1/ticker`,
`/v1/trades`, `/v1/markets`, or equivalent consumer surfaces.

## J8. Asset Detail / Metadata Path

Asset parse -> storage lookup -> home-domain / SEP-1 overlay ->
F2 enrichment -> response.

## J9. API Key Auth Path

Client key -> middleware -> validator -> rate limit identity ->
authorized handler.

## J10. SEP-10 Auth Path

Challenge -> verify -> JWT -> authenticated request -> logout/expiry
behavior where applicable.

## J11. Supply Snapshot Path

Ledger-derived inputs -> supply compute -> snapshot write ->
asset detail read -> ops tooling read.

## J12. Archive Completeness / Verify-Archive Path

Archive chunk source -> verifier -> metrics / logs / operator output.

## J13. Cross-Region Determinism Path

Region A surface -> comparison tool -> region B surface -> mismatch
classification.

## J14. SLA Probe Path

Probe config -> API fetch -> textfile output -> monitoring rule.

## J15. Hostile Paths

- malformed event payloads
- unsupported assets
- stale cache or Redis miss
- missing trusted proxy
- empty divergence refs
- missing supply watcher config
- partial infra outage
