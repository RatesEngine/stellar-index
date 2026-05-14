package defindex

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// classify decides what kind of DeFindex vault event this is.
//
// Vault event topics are 2-tuples:
//
//	topic[0] = String("DeFindexVault")    — pre-encoded, byte-equal
//	topic[1] = Symbol("deposit"/"withdraw"/...)
//
// Both positions are compared as byte-equal base64 against the
// constants computed at package init. Returns "" if this isn't an
// event we decode (the dispatcher's drop-counter handles "" cases).
func classify(e *events.Event) string {
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
	}
	return ""
}

// decodeFlow converts one classified vault event into a VaultFlow.
//
// Body shapes (from `apps/contracts/vault/src/events.rs` at tag 1.0.0):
//
//	deposit  { depositor: Address, amounts: Vec<i128>, df_tokens_minted: i128, ... }
//	withdraw { withdrawer: Address, amounts_withdrawn: Vec<i128>, df_tokens_burned: i128, ... }
//
// Other body fields (`total_supply_before`,
// `total_managed_funds_before`) describe the pre-state for accurate
// NAV reconstruction; we ignore them at Phase A and pull only the
// user-facing dimensions.
//
// All fields are pulled by name from the top-level Map per
// docs/architecture/contract-schema-evolution.md's decode-by-name
// rule — positional decoding would silently break across upgrades.
func decodeFlow(e *events.Event, kind string) (VaultFlow, error) {
	if _, ok := MainnetVaults[e.ContractID]; !ok {
		return VaultFlow{}, fmt.Errorf("%w: %s", ErrUnknownVault, e.ContractID)
	}
	closedAt, err := e.EventClosedAt()
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	// Common header from the wrapping event.
	flow := VaultFlow{
		Source:     SourceName,
		Ledger:     e.Ledger,
		ClosedAt:   closedAt,
		TxHash:     e.TxHash,
		OpIndex:    e.OperationIndex,
		ContractID: e.ContractID,
		VaultName:  VaultName[e.ContractID],
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: body not a Map: %w", ErrMalformedPayload, err)
	}

	// Per-direction field names.
	var counterpartyField, amountsField, sharesField string
	switch kind {
	case EventDeposit:
		flow.Direction = DirectionDeposit
		counterpartyField = "depositor"
		amountsField = "amounts"
		sharesField = "df_tokens_minted"
	case EventWithdraw:
		flow.Direction = DirectionWithdraw
		counterpartyField = "withdrawer"
		amountsField = "amounts_withdrawn"
		sharesField = "df_tokens_burned"
	default:
		// Defensive — classify() should have filtered. Surface as
		// ErrUnknownEvent so the dispatcher counts it correctly.
		return VaultFlow{}, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
	}

	cpSv, err := scval.MustMapField(entries, counterpartyField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, counterpartyField, err)
	}
	flow.Counterparty, err = scval.AsAddressStrkey(cpSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, counterpartyField, err)
	}

	amountsSv, err := scval.MustMapField(entries, amountsField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, amountsField, err)
	}
	amountVec, err := scval.AsVec(amountsSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, amountsField, err)
	}
	if len(amountVec) == 0 {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s empty vec", ErrMalformedPayload, kind, amountsField)
	}
	flow.Amounts = make([]canonical.Amount, len(amountVec))
	for i, sv := range amountVec {
		amt, err := scval.AsAmountFromI128(sv)
		if err != nil {
			return VaultFlow{}, fmt.Errorf("%w: %s.%s[%d]: %w", ErrMalformedPayload, kind, amountsField, i, err)
		}
		flow.Amounts[i] = amt
	}

	sharesSv, err := scval.MustMapField(entries, sharesField)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, sharesField, err)
	}
	flow.DfTokenDelta, err = scval.AsAmountFromI128(sharesSv)
	if err != nil {
		return VaultFlow{}, fmt.Errorf("%w: %s.%s: %w", ErrMalformedPayload, kind, sharesField, err)
	}

	return flow, nil
}
