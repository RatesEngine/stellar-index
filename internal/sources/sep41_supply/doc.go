// Package sep41_supply is the canonical SEP-41 supply-event
// observer per ADR-0023. Plugs into the dispatcher's events-
// based [dispatcher.Decoder] hook and emits one Event per
// mint / burn / clawback event observed on a watched SEP-41
// contract.
//
// Operator usage: populate `[supply] watched_sep41_contracts`
// with the C-strkey of each SEP-41 contract you want
// Algorithm 3 supply data for. Match fast-path is
// (contract_id ∈ watched_set) AND (topic[0] symbol ∈
// {mint, burn, clawback}).
//
// # Why we ignore `transfer`
//
// Algorithm 3's running sum is `Σ mint − Σ(burn + clawback)`.
// Transfers move ownership between holders without changing
// total supply, so they're filtered at Match. The discovery
// sniffer in `internal/canonical/discovery` records transfer
// sightings (for the discovered_assets table); this observer
// is supply-only.
//
// # Topic shapes
//
// The SEP-41 spec emits three supply-affecting topic shapes
// (post-P23 / CAP-67 — earlier shapes are equivalent for our
// purposes since we read by topic[0] and amount-from-Value):
//
//	mint     ["mint", admin, to, sep0011_asset?]
//	burn     ["burn", from,    sep0011_asset?]
//	clawback ["clawback", admin, from, sep0011_asset?]
//
// Body (event.Value) is the i128 amount in stroops (per the
// asset's decimals — SEP-41 is decimal-agnostic at the wire
// level; total / circulating in `asset_supply_history` carry
// the wire stroop value verbatim).
//
// # Counterparty extraction
//
// `mint` → topic[2] (`to`); `burn` → topic[1] (`from`);
// `clawback` → topic[2] (`from`). The observer stamps this on
// each row so operators can audit which holders the supply
// came from / went to.
package sep41_supply
