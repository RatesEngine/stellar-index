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
// Supply-affecting events arrive in three on-chain shapes that
// differ in counterparty POSITION — the topic count alone does
// not disambiguate them (lake-verified on r1, 2026-06-15):
//
//	legacy SAC    mint     ["mint", admin, to]                  (to @ topic[2])
//	              clawback ["clawback", admin, from]            (from @ topic[2])
//	CAP-67/Whisk  mint     ["mint", to, sep0011_asset]          (to @ topic[1])  ← dominant (≈99.96%)
//	              clawback ["clawback", from, sep0011_asset]    (from @ topic[1]) ← dominant (100%)
//	              burn     ["burn", from, sep0011_asset]         (from @ topic[1])
//	bare SEP-41   mint     ["mint", to]                          (to @ topic[1])
//	              burn     ["burn", from]                         (from @ topic[1])
//
// CAP-67 (Whisk, mainnet 2025-09-03) replaced the legacy admin-
// prefixed SAC form with the SEP-41-spec form + a trailing
// sep0011_asset STRING — so the same topic count (3) can carry
// the counterparty at a DIFFERENT index. sep0011_asset is a
// String (ScvString), not an Address.
//
// Body (event.Value) is the i128 amount in stroops (per the
// asset's decimals — SEP-41 is decimal-agnostic at the wire
// level; total / circulating in `asset_supply_history` carry
// the wire stroop value verbatim).
//
// # Counterparty extraction
//
// Shape-aware (see [decodeCounterparty]): the counterparty is
// topic[2] iff topic[2] is an Address (legacy admin-prefixed
// form), else topic[1] (CAP-67 / bare-spec); burn is always
// topic[1]. The observer stamps this on each row so operators
// can audit which holders the supply came from / went to.
package sep41_supply
