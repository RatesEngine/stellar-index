# W05 — Canonical identity, numeric safety, serialization

## Scope

Audit how value flows from XDR/wire to API JSON without loss
of precision or impersonation of identity.

In scope:
- `internal/canonical/*` (Asset, AssetCrypto, AssetFiat, Amount,
  Pair, Trade, Oracle, Strkey, Errors, Discovery)
- `internal/scval/*` (SCVal helpers)
- `internal/cachekeys/*` (key builder; ADR-0007 sole-builder)
- `internal/events/*` (transport-neutral event types)

## Inputs

- ADR-0003 (i128/u128 never to int64)
- ADR-0010 (off-chain fiat representation)
- ADR-0013 (go-stellar-sdk/xdr scoped to scval)
- ADR-0014 (crypto-ticker representation)

## Per-file checklist

| File | Role | Tests | Status |
| --- | --- | --- | --- |
| `internal/canonical/asset.go` + `_test.go` | Asset parse/format | | |
| `internal/canonical/asset_crypto.go` + `_test.go` | classic + SEP-41 representation | | |
| `internal/canonical/asset_fiat.go` + `_test.go` | `fiat:USD` representation (ADR-0010) | | |
| `internal/canonical/amount.go` + `_test.go` + `_edge_test.go` | i128 ↔ big.Int | | |
| `internal/canonical/pair.go` + `_test.go` + `_validate_test.go` | Pair canonicalisation | | |
| `internal/canonical/trade.go` + `_test.go` | Trade record schema | | |
| `internal/canonical/oracle.go` + `_test.go` | Oracle record schema | | |
| `internal/canonical/strkey.go` + `_test.go` | Stellar key parsing | | |
| `internal/canonical/errors.go` | typed error set | | |
| `internal/canonical/discovery/*` | what does it discover; trust boundary | | |
| `internal/canonical/doc.go` | package doc accurate | | |
| `internal/scval/*.go` (every file) | XDR helpers; bounds checks | | |
| `internal/cachekeys/*.go` | sole builder claim; round-trip stable | | |
| `internal/events/*.go` (every file) | transport-neutral; no xdr leak | | |

## ADR-0003 grep check

```sh
# Find any cast from xdr.Int128Parts.Lo to int64
grep -RnE 'int64\([a-zA-Z_]+\.Lo\)' --include='*.go' internal/ cmd/
```

Expected: zero hits, OR any hit is in `_test.go` deliberately
demonstrating the bad pattern (and asserting it's NEVER used in
runtime code).

## ADR-0014 round-trip test

For every supported asset shape:
- `native`
- `XLM-G…`
- `USDC-G…`
- `C…` (SEP-41 contract id)
- `fiat:USD`

Verify `Asset.String() → ParseAsset(s) → Asset` is identity. If
case folding differs, finding.

## ADR-0007 sole-builder check

```sh
grep -RnE 'redisclient\.|redis\.NewClient' --include='*.go' internal/ cmd/ | grep -v 'cachekeys/' | grep -v '_test\.go'
```

Expected: redis-client construction limited to
`internal/storage/redisclient` and key construction to
`internal/cachekeys`.

## JSON boundary check

For every API handler that emits a numeric:
- amounts must be JSON strings (not numbers)
- timestamps must be ISO-8601 strings
- pagination cursor must be opaque string

Sample pages: `/v1/markets`, `/v1/trades`, `/v1/price`.

## SCVal type-test pattern

For every `MustI128()` call site, prove a type-test guard exists
upstream (per CLAUDE.md SEP-41 transfer surprise: data can be
i128 or a map containing amount + to_muxed_id).

## Adversarial vectors

- A1.1 i128 amounts at `int64.Max + 1`
- A1.5 SEP-41 transfer body that's a map vs raw i128
- A1.10 CAP-67 unified event with malformed `sep0011_asset` topic
- B3.4 Asset ID with extreme length → cache key length blowup

## Cross-workstream dependencies

- W06 (dispatcher) consumes `internal/events`
- W07 (decoders) consumes `internal/scval` + `internal/canonical`
- W09 (storage) writes `*big.Int` as NUMERIC
- W11 (API) emits via canonical types

## Closure criteria

- Every per-file row terminal
- ADR-0003 grep returns zero (in runtime)
- ADR-0014 round-trip table complete
- ADR-0007 sole-builder grep returns zero violations
- JSON boundary check completed for sampled handlers
