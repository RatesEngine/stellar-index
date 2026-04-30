package accounts

import (
	"math/big"
	"time"
)

// SourceName is the canonical identifier for the AccountEntry
// observer. Stamped on metrics labels and on every
// [Observation.Source]. Stable.
const SourceName = "accounts"

// ObservationKind is the consumer.Event.EventKind value emitted by
// the observer. The indexer's sink type-switches on this string
// to route observations to the account_observations hypertable.
const ObservationKind = "accounts.observation"

// Observation is one AccountEntry-delta record. Captures the state
// of the observed account at the ledger that produced the change —
// per ADR-0021 the observer doesn't try to infer "what changed"
// (the reader can compute that from successive observations);
// it just records the post-change AccountEntry fields the
// downstream readers consume.
//
// One Observation per (account, ledger) pair. When an account is
// touched multiple times in a single ledger (e.g. multiple ops in
// one tx, or fee + op in the same tx), the observer emits one
// Observation per change; the writer dedupes via the
// (account_id, ledger) primary key (last-writer-wins, which is
// fine — the AccountEntry is monotonic within a ledger so the
// final post-state is deterministic).
//
// Removed accounts emit an Observation with Balance=0 + a flag —
// see [Observation.IsRemoval]. The reader interprets this as
// "account no longer exists at this ledger."
type Observation struct {
	// AccountID is the G-strkey of the observed account.
	AccountID string

	// Ledger is the ledger sequence at which this delta landed.
	Ledger uint32

	// ObservedAt is the ledger close time, UTC.
	ObservedAt time.Time

	// Balance is the post-change native XLM balance in stroops.
	// big.Int per ADR-0003; XLM is i64 in XDR but we carry NUMERIC
	// upstream for consistency and future-proofing.
	Balance *big.Int

	// HomeDomain is the AccountEntry.HomeDomain value (empty when
	// unset). Maxes at 32 bytes per the protocol.
	HomeDomain string

	// Flags is the AccountEntry.Flags bitmask. Auth* bits relevant
	// for SEP-1 issuers; the operator-curated metadata layer can
	// cross-check.
	Flags uint32

	// SeqNum is the AccountEntry.SeqNum after the change. Only
	// meaningful when this Observation reflects the source of a
	// transaction; for fee or operation-side-effect deltas, the
	// observer still records the post-state but consumers should
	// rely on Ledger for ordering rather than SeqNum.
	SeqNum int64

	// IsRemoval is true when the change removed the AccountEntry
	// (LedgerEntryChangeTypeLedgerEntryRemoved). Balance and
	// HomeDomain are zeroed for removal observations.
	IsRemoval bool
}
