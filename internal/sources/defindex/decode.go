package defindex

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// classify decides whether this is a Blend strategy flow event we
// decode. Topics are 2-tuples:
//
//	topic[0] = String("BlendStrategy")     — pre-encoded, byte-equal
//	topic[1] = Symbol("deposit"|"withdraw"|"harvest"|…)
//
// Both positions are compared as byte-equal base64 against the
// constants computed at package init — no SCVal parsing on the
// reject path. Returns "" for non-strategy events.
//
// Per the EVERY-event policy (project_every_event_principle), this
// switch enumerates every topic[1] symbol the BlendStrategy contract
// emits — not just the ones we decode into a StrategyFlow today. The
// audit doc (docs/operations/wasm-audits/defindex.md) is the upstream
// reference for the topic set.
func classify(e *events.Event) string {
	if len(e.Topic) < 2 {
		return ""
	}
	if e.Topic[0] != TopicPrefixStrategy {
		return ""
	}
	switch e.Topic[1] {
	case TopicSymbolDeposit:
		return EventDeposit
	case TopicSymbolWithdraw:
		return EventWithdraw
	case TopicSymbolHarvest:
		return EventHarvest
	}
	return ""
}

// classifyVault is the vault-layer twin of classify. Topics are
// 2-tuples:
//
//	topic[0] = String("DeFindexVault")     — pre-encoded, byte-equal
//	topic[1] = Symbol("deposit"|"withdraw"|<governance/admin>)
//
// Per the EVERY-event policy: classifies all 11 vault-layer topic[1]
// symbols enumerated by the upstream contract (audit-2026-05-14 §
// "Topic structure"). Only deposit + withdraw drive a VaultFlow
// today; the other 9 are governance / admin / multiplexed-rebalance
// events with no decoder (yet) but recognising them satisfies the
// closed-set completeness requirement before flipping BackfillSafe.
func classifyVault(e *events.Event) string {
	if len(e.Topic) < 2 {
		return ""
	}
	if e.Topic[0] != TopicPrefixVault {
		return ""
	}
	switch e.Topic[1] {
	case TopicSymbolDeposit:
		return EventDeposit
	case TopicSymbolWithdraw:
		return EventWithdraw
	case TopicSymbolRescue:
		return EventRescue
	case TopicSymbolPaused:
		return EventPaused
	case TopicSymbolUnpaused:
		return EventUnpaused
	case TopicSymbolNReceiver:
		return EventNReceiver
	case TopicSymbolNManager:
		return EventNManager
	case TopicSymbolNEManager:
		return EventNEManager
	case TopicSymbolRBManager:
		return EventRBManager
	case TopicSymbolDFees:
		return EventDFees
	case TopicSymbolRebalance:
		return EventRebalance
	}
	return ""
}

// classifyFactory is the factory-layer twin of classify /
// classifyVault. Topics are 2-tuples:
//
//	topic[0] = String("DeFindexFactory")    — pre-encoded, byte-equal
//	topic[1] = Symbol("create"|"n_fee")
//
// We recognise factory events so the dispatcher's drop-counter
// doesn't file them as "unmatched topic" — EVERY-event policy
// (project_every_event_principle). Body decode is Phase C; today
// classifyFactory returning non-empty just means "we own this
// event, no decode needed yet." Decoder.Decode returns no
// consumer.Event on a factory match (drops cleanly without
// counting against ErrUnknownEvent).
func classifyFactory(e *events.Event) string {
	if len(e.Topic) < 2 {
		return ""
	}
	if e.Topic[0] != TopicPrefixFactory {
		return ""
	}
	switch e.Topic[1] {
	case TopicSymbolCreate:
		return EventCreate
	case TopicSymbolNFee:
		return EventNFee
	}
	return ""
}

// decodeFlow converts one classified strategy event into a
// StrategyFlow.
//
// Body shape (verified on-chain via scan-soroban-events — identical
// for both deposit and withdraw):
//
//	{ from: Address, amount: i128 }
//
// Fields are pulled by name from the top-level Map per
// docs/architecture/contract-schema-evolution.md's decode-by-name
// rule — positional decoding would silently break across upgrades.
func decodeFlow(e *events.Event, kind string) (StrategyFlow, error) {
	closedAt, err := e.EventClosedAt()
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	flow := StrategyFlow{
		Source:     SourceName,
		Ledger:     e.Ledger,
		ClosedAt:   closedAt,
		TxHash:     e.TxHash,
		OpIndex:    e.OperationIndex,
		ContractID: e.ContractID,
	}

	switch kind {
	case EventDeposit:
		flow.Direction = DirectionDeposit
	case EventWithdraw:
		flow.Direction = DirectionWithdraw
	default:
		// Defensive — classify() should have filtered.
		return StrategyFlow{}, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: body not a Map: %w", ErrMalformedPayload, err)
	}

	fromSv, err := scval.MustMapField(entries, "from")
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: %s.from: %w", ErrMalformedPayload, kind, err)
	}
	flow.From, err = scval.AsAddressStrkey(fromSv)
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: %s.from: %w", ErrMalformedPayload, kind, err)
	}

	amountSv, err := scval.MustMapField(entries, "amount")
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: %s.amount: %w", ErrMalformedPayload, kind, err)
	}
	flow.Amount, err = scval.AsAmountFromI128(amountSv)
	if err != nil {
		return StrategyFlow{}, fmt.Errorf("%w: %s.amount: %w", ErrMalformedPayload, kind, err)
	}

	return flow, nil
}

// decodeVaultFlow converts one classified DeFindexVault event into
// a VaultFlow.
//
// Body shape (per docs/operations/wasm-audits/defindex.md "Body
// shapes", confirmed on-chain via Soroban-RPC getEvents against an
// active wrapper):
//
//	deposit:  { depositor:  Address,
//	            amounts:           Vec<i128>,
//	            df_tokens_minted:  i128,
//	            total_managed_funds_before, total_supply_before }
//	withdraw: { withdrawer: Address,
//	            amounts_withdrawn: Vec<i128>,
//	            df_tokens_burned:  i128,
//	            total_managed_funds_before, total_supply_before }
//
// We ignore the `total_*_before` NAV-snapshot fields at Phase B —
// they're useful for NAV reconstruction but not for flow
// attribution. Fields are pulled by name (decode-by-name per
// contract-schema-evolution.md), so the decoder is robust against
// the vault contract's known mid-life WASM upgrade
// (`ae3409a4…468b` → `07097f83…84b0`) provided the field names
// don't change — and they haven't.
func decodeVaultFlow(e *events.Event, kind string) (VaultFlow, error) {
	closedAt, err := e.EventClosedAt()
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	flow := VaultFlow{
		Source:     SourceName,
		Ledger:     e.Ledger,
		ClosedAt:   closedAt,
		TxHash:     e.TxHash,
		OpIndex:    e.OperationIndex,
		ContractID: e.ContractID,
	}

	// Pick the per-direction field names. The vault layer uses
	// distinct names per direction (depositor vs withdrawer,
	// amounts vs amounts_withdrawn, df_tokens_minted vs df_tokens_burned),
	// unlike the strategy layer which shares names across directions.
	var userField, amountsField, tokensField string
	switch kind {
	case EventDeposit:
		flow.Direction = DirectionDeposit
		userField, amountsField, tokensField = "depositor", "amounts", "df_tokens_minted"
	case EventWithdraw:
		flow.Direction = DirectionWithdraw
		userField, amountsField, tokensField = "withdrawer", "amounts_withdrawn", "df_tokens_burned"
	default:
		return VaultFlow{}, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: body not a Map: %w", ErrMalformedPayload, err)
	}

	// User address (G-strkey for direct deposit, occasionally
	// C-strkey if a router/aggregator deposited on their behalf).
	userSv, err := scval.MustMapField(entries, userField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s: %w", ErrMalformedPayload, kind, userField, err)
	}
	flow.User, err = scval.AsAddressStrkey(userSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s: %w", ErrMalformedPayload, kind, userField, err)
	}

	// Multi-asset amounts vector — Vec<i128>.
	amountsSv, err := scval.MustMapField(entries, amountsField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s: %w", ErrMalformedPayload, kind, amountsField, err)
	}
	elems, err := scval.AsVec(amountsSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s not a Vec: %w", ErrMalformedPayload, kind, amountsField, err)
	}
	flow.Amounts = make([]canonical.Amount, 0, len(elems))
	for i, sv := range elems {
		amt, err := scval.AsAmountFromI128(sv)
		if err != nil {
			return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s[%d]: %w", ErrMalformedPayload, kind, amountsField, i, err)
		}
		flow.Amounts = append(flow.Amounts, amt)
	}

	// Share-token delta (df_tokens_minted on deposit, df_tokens_burned on withdraw).
	tokensSv, err := scval.MustMapField(entries, tokensField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s: %w", ErrMalformedPayload, kind, tokensField, err)
	}
	flow.DfTokens, err = scval.AsAmountFromI128(tokensSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: vault.%s.%s: %w", ErrMalformedPayload, kind, tokensField, err)
	}

	return flow, nil
}
