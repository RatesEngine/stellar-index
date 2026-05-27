// Package sep41_transfers is the SEP-41 audit-trail decoder
// (F-0021 closure from audit-2026-05-26). Every SEP-41 transfer /
// approve / set_admin / set_authorized event is materialised into
// a queryable hypertable so per-account net-position becomes a
// first-class API surface — the Stellar moat feature CG/CMC
// structurally cannot offer.
//
// Scope split with [sep41_supply]:
//
//	mint / burn / clawback         -> sep41_supply (Algorithm 3 supply)
//	transfer / approve / set_admin
//	/ set_authorized               -> sep41_transfers (this package)
//
// # Topic shapes (SEP-41 v0.4.1 + cap-67-unified-events.md)
//
//	transfer        topics: ("transfer", from, to[, sep0011_asset])
//	                data:   i128 amount  OR  map { amount: i128, to_muxed_id: ... }
//	approve         topics: ("approve", from, spender)
//	                data:   [i128 amount, u32 live_until_ledger]
//	set_admin       topics: ("set_admin"[, admin])
//	                data:   Address(new_admin)
//	set_authorized  topics: ("set_authorized", id[, sep0011_asset])
//	                data:   bool authorize
package sep41_transfers
