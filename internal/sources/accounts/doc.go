// Package accounts is the canonical AccountEntry observer per
// ADR-0021. It plugs into the dispatcher's fourth hook
// (LedgerEntryChangeDecoder) and emits one observation per
// LedgerEntryChange touching an operator-watched G-strkey.
//
// Operator usage: populate `[accounts] watched_accounts` in
// operator config with the G-strkeys whose AccountEntry deltas
// should be observed (e.g. the SDF reserve list, issuer accounts
// for the metadata overlay, validator accounts for tier-1
// quorum monitoring).
//
// Output: [Observation] events flowing through the standard
// dispatcher → consumer pipeline. The indexer-side sink writes
// each observation to the `account_observations` hypertable
// (introduced in Task #60); two readers consume that table —
// metadata.LCMHomeDomainResolver (replaces the operator-static
// `[metadata.issuer_home_domains]` map) and supply.LCMReserveBalanceReader
// (replaces the operator-static `[supply.reserve_balances_stroops]`
// map). See ADR-0021 for the full architecture.
//
// The observer is operator-watched-set driven by default — no
// "watch every account" mode at v1. Switching to that mode would
// require a separate ADR; the table-size implications (50M+
// network accounts) are non-trivial and bear further design
// before being on by default.
package accounts
