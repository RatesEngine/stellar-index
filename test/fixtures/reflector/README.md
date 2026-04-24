# Reflector fixtures

Real `stellar-rpc getEvents` captures of Reflector `REFLECTOR.update`
events, used to regression-test `internal/sources/reflector/` decode
against production wire format.

## Why pin fixtures per WASM hash

Reflector contracts support `update_contract` (admin can swap the
WASM in place without changing the contract address). Event body
schemas can change across an upgrade. If we decode today's WASM
shape against a fixture captured under last month's WASM, the test
silently mis-decodes. Per
[docs/architecture/contract-schema-evolution.md](../../../docs/architecture/contract-schema-evolution.md)
fixtures live under `<wasm_hash>/` subdirectories so the decoder
variant and the fixture line up 1:1.

## Capture workflow

```sh
# From r1 (stellar-rpc is on the box's loopback):
WASM_HASH=$(your-probe-for-wasm-hash)  # see note below
scripts/dev/capture-reflector-fixtures.sh \
  -e http://127.0.0.1:8000 \
  -c CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M \
  -n 10
# Files land in test/fixtures/reflector/<wasm_hash>/<ledger>_<tx>.json
```

Contract IDs (mainnet):

| Variant | Contract |
| --- | --- |
| DEX | `CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M` |
| CEX | `CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN` |
| FX  | `CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC` |

### Resolving WASM hash

The capture script doesn't auto-resolve the WASM hash — it accepts
it via `WASM_HASH=…` env var. To pin:

1. `stellar-rpc getLedgerEntries` with a `LedgerKey::ContractData`
   key of `(contract, LedgerKeyContractInstance)` returns the
   instance entry. Its `.executable.wasm` field is the hash.
2. `stellar contract info interface --id <C>` via stellar-cli also
   surfaces it when the cli is configured.

`ratesengine-ops resolve-wasm <contract>` is planned (not yet wired)
to do this in one step.

## Fixture file shape

```json
{
  "contract_id":       "CALI2BYU...",
  "wasm_hash":         "abcdef1234...",
  "ledger":            52003412,
  "tx_hash":           "a1b2c3...",
  "ledger_closed_at":  "2026-04-23T12:34:56Z",
  "topics":            ["AAAADw...", "AAAADw...", "AAAABQA..."],
  "value":             "AAAAEA..."
}
```

All `topics[]` + `value` are base64-encoded XDR, as
`stellar-rpc getEvents` returns them.

## Replaying fixtures in tests

`internal/sources/reflector/decode_test.go` today uses SDK-encoded
fixtures built programmatically. Once a `<wasm_hash>` subdirectory
has captures, add an `integration`-tagged test file that globs
`<wasm_hash>/*.json` and runs each through `decodeUpdate`. The
test must:

- Assert the decoded `[]OracleUpdate` is non-empty.
- Assert each update's `Asset` matches either a Soroban
  (C-strkey) address or an ADR-0010 fiat symbol — no malformed
  slips through.
- Snapshot the decoded output (ideally via
  `github.com/google/go-cmp`); regressions against a WASM-hash-pinned
  fixture indicate either the contract upgraded without our notice
  or the decoder broke.

TODO tracking:
- [ ] First real captures (blocked on operator running the capture
      script against r1 — network access required).
- [ ] Replay test harness.
- [ ] `ratesengine-ops resolve-wasm` subcommand.
