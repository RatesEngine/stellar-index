package sep41_transfers

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

var (
	ErrUnknownSymbol = errors.New("sep41_transfers: topic[0] is not transfer/approve/set_admin/set_authorized")
	ErrShortTopic    = errors.New("sep41_transfers: topic too short for event variant")
	ErrBadValue      = errors.New("sep41_transfers: event Value has unexpected type")
)

// classify returns the topic[0] symbol for an audit-trail event,
// or empty otherwise. mint/burn/clawback are intentionally absent
// here — they belong to sep41_supply.
func classify(ev *events.Event) string {
	if len(ev.Topic) == 0 {
		return ""
	}
	switch ev.Topic[0] {
	case TopicSymbolTransfer:
		return SymbolTransfer
	case TopicSymbolApprove:
		return SymbolApprove
	case TopicSymbolSetAdmin:
		return SymbolSetAdmin
	case TopicSymbolSetAuthorized:
		return SymbolSetAuthorized
	default:
		return ""
	}
}

// decodeTransfer parses ("transfer", from, to[, sep0011_asset]) +
// i128 OR map{amount, to_muxed_id} body. Per CLAUDE.md SEP-41
// "transfer data EITHER simple i128 OR map" — type-test before
// MustI128.
func decodeTransfer(ev *events.Event) (string, string, *big.Int, error) {
	if len(ev.Topic) < 3 {
		return "", "", nil, fmt.Errorf("%w: transfer expects 3 topics, got %d", ErrShortTopic, len(ev.Topic))
	}
	from, err := decodeAddrTopic(ev, 1)
	if err != nil {
		return "", "", nil, fmt.Errorf("sep41_transfers: transfer.from: %w", err)
	}
	to, err := decodeAddrTopic(ev, 2)
	if err != nil {
		return "", "", nil, fmt.Errorf("sep41_transfers: transfer.to: %w", err)
	}
	amount, err := decodeTransferAmount(ev)
	if err != nil {
		return "", "", nil, err
	}
	return from, to, amount, nil
}

func decodeTransferAmount(ev *events.Event) (*big.Int, error) {
	sv, err := scval.Parse(ev.Value)
	if err != nil {
		return nil, fmt.Errorf("sep41_transfers: parse Value: %w", err)
	}
	switch sv.Type {
	case xdr.ScValTypeScvI128:
		amt, perr := scval.AsAmountFromI128(sv)
		if perr != nil {
			return nil, fmt.Errorf("sep41_transfers: transfer.amount i128: %w", perr)
		}
		return amt.BigInt(), nil
	case xdr.ScValTypeScvMap:
		entries, perr := scval.AsMap(sv)
		if perr != nil {
			return nil, fmt.Errorf("sep41_transfers: transfer.amount map: %w", perr)
		}
		amtVal, ok := scval.MapField(entries, "amount")
		if !ok {
			return nil, fmt.Errorf("%w: transfer map missing `amount` field", ErrBadValue)
		}
		if amtVal.Type != xdr.ScValTypeScvI128 {
			return nil, fmt.Errorf("%w: transfer map `amount` is %s, want I128", ErrBadValue, amtVal.Type)
		}
		amt, perr := scval.AsAmountFromI128(amtVal)
		if perr != nil {
			return nil, fmt.Errorf("sep41_transfers: transfer map amount i128: %w", perr)
		}
		return amt.BigInt(), nil
	default:
		return nil, fmt.Errorf("%w: transfer Value is %s, want I128 or Map", ErrBadValue, sv.Type)
	}
}

// decodeApprove parses ("approve", from, spender) + Vec[i128, u32].
func decodeApprove(ev *events.Event) (string, string, *big.Int, uint32, error) {
	if len(ev.Topic) < 3 {
		return "", "", nil, 0, fmt.Errorf("%w: approve expects 3 topics, got %d", ErrShortTopic, len(ev.Topic))
	}
	from, err := decodeAddrTopic(ev, 1)
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: approve.from: %w", err)
	}
	spender, err := decodeAddrTopic(ev, 2)
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: approve.spender: %w", err)
	}
	sv, err := scval.Parse(ev.Value)
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: parse approve Value: %w", err)
	}
	if sv.Type != xdr.ScValTypeScvVec {
		return "", "", nil, 0, fmt.Errorf("%w: approve Value is %s, want Vec[i128, u32]", ErrBadValue, sv.Type)
	}
	vec, err := scval.AsVec(sv)
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: approve vec: %w", err)
	}
	if len(vec) != 2 {
		return "", "", nil, 0, fmt.Errorf("%w: approve Vec arity %d, want 2", ErrBadValue, len(vec))
	}
	if vec[0].Type != xdr.ScValTypeScvI128 {
		return "", "", nil, 0, fmt.Errorf("%w: approve[0] is %s, want I128", ErrBadValue, vec[0].Type)
	}
	amt, perr := scval.AsAmountFromI128(vec[0])
	if perr != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: approve.amount i128: %w", perr)
	}
	if vec[1].Type != xdr.ScValTypeScvU32 {
		return "", "", nil, 0, fmt.Errorf("%w: approve[1] is %s, want U32", ErrBadValue, vec[1].Type)
	}
	liveUntil, perr := scval.AsU32(vec[1])
	if perr != nil {
		return "", "", nil, 0, fmt.Errorf("sep41_transfers: approve.live_until_ledger u32: %w", perr)
	}
	return from, spender, amt.BigInt(), liveUntil, nil
}

// decodeSetAdmin parses ("set_admin"[, admin]) + Address(new_admin).
// Per stellar-docs SAC example token contract, the SetAdmin event
// uses `data_format = "single-value"` with the new admin Address
// in the body.
func decodeSetAdmin(ev *events.Event) (string, string, error) {
	var from string
	if len(ev.Topic) >= 2 {
		var err error
		from, err = decodeAddrTopic(ev, 1)
		if err != nil {
			return "", "", fmt.Errorf("sep41_transfers: set_admin.admin (topic[1]): %w", err)
		}
	}
	sv, err := scval.Parse(ev.Value)
	if err != nil {
		return "", "", fmt.Errorf("sep41_transfers: parse set_admin Value: %w", err)
	}
	if sv.Type != xdr.ScValTypeScvAddress {
		return "", "", fmt.Errorf("%w: set_admin Value is %s, want Address(new_admin)", ErrBadValue, sv.Type)
	}
	newAdmin, perr := scval.AsAddressStrkey(sv)
	if perr != nil {
		return "", "", fmt.Errorf("sep41_transfers: set_admin.new_admin address: %w", perr)
	}
	return from, newAdmin, nil
}

// decodeSetAuthorized parses ("set_authorized", id, asset?) + bool.
func decodeSetAuthorized(ev *events.Event) (string, bool, error) {
	if len(ev.Topic) < 2 {
		return "", false, fmt.Errorf("%w: set_authorized expects >=2 topics, got %d", ErrShortTopic, len(ev.Topic))
	}
	id, err := decodeAddrTopic(ev, 1)
	if err != nil {
		return "", false, fmt.Errorf("sep41_transfers: set_authorized.id: %w", err)
	}
	sv, perr := scval.Parse(ev.Value)
	if perr != nil {
		return "", false, fmt.Errorf("sep41_transfers: parse set_authorized Value: %w", perr)
	}
	if sv.Type != xdr.ScValTypeScvBool {
		return "", false, fmt.Errorf("%w: set_authorized Value is %s, want Bool", ErrBadValue, sv.Type)
	}
	authorize, perr := scval.AsBool(sv)
	if perr != nil {
		return "", false, fmt.Errorf("sep41_transfers: set_authorized.authorize bool: %w", perr)
	}
	return id, authorize, nil
}

func decodeAddrTopic(ev *events.Event, idx int) (string, error) {
	if len(ev.Topic) <= idx {
		return "", fmt.Errorf("%w: want topic[%d], have len=%d", ErrShortTopic, idx, len(ev.Topic))
	}
	sv, err := scval.Parse(ev.Topic[idx])
	if err != nil {
		return "", fmt.Errorf("parse topic[%d]: %w", idx, err)
	}
	if sv.Type == xdr.ScValTypeScvVoid {
		return "", nil
	}
	return scval.AsAddressStrkey(sv)
}
