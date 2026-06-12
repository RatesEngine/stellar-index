package sac_balances

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// Observer is the dispatcher-facing SAC balance observer per
// ADR-0022 PR 4/5. Implements
// [dispatcher.LedgerEntryChangeDecoder].
//
// Watched-contract driven: the observer's NewObserver takes a
// map of SAC contract IDs → asset_keys. Match runs:
//
//  1. Type discriminator (LedgerEntryTypeContractData)
//  2. Contract scAddress == one of the watched contracts
//  3. Key shape is `Vec(Symbol("Balance"), Address)`
//
// PR 5/5 wires the operator TOML
// (`[supply.sac_wrappers]`) into NewObserver's argument.
type Observer struct {
	// wrappers maps SAC contract C-strkey → asset_key (CODE:ISSUER).
	wrappers map[string]string
}

var (
	ErrEmptyWrapperMap = errors.New("sac_balances: cannot construct Observer with empty SAC wrapper map")
	ErrNotSACBalance   = errors.New("sac_balances: change is not a SAC balance entry")
	ErrUnknownValShape = errors.New("sac_balances: balance value is neither i128 nor map-with-amount")
)

// balanceKeySymbol is the SCVal symbol the SEP-41 contracttype
// enum encodes for `DataKey::Balance(addr)`. Pre-encoded at init
// for cheap byte-equality comparison.
var balanceKeySymbol = scval.MustEncodeSymbol("Balance")

// NewObserver constructs the SAC observer with the supplied
// contract→asset_key map. Empty input is rejected as a config
// error.
func NewObserver(wrappers map[string]string) (*Observer, error) {
	if len(wrappers) == 0 {
		return nil, ErrEmptyWrapperMap
	}
	cleaned := make(map[string]string, len(wrappers))
	for cid, ak := range wrappers {
		if cid == "" {
			return nil, errors.New("sac_balances: empty SAC contract id in wrapper map")
		}
		if ak == "" {
			return nil, fmt.Errorf("sac_balances: empty asset_key for SAC contract %s", cid)
		}
		cleaned[cid] = ak
	}
	return &Observer{wrappers: cleaned}, nil
}

func (*Observer) Name() string { return SourceName }

// Matches implements [dispatcher.LedgerEntryChangeDecoder].
func (o *Observer) Matches(change xdr.LedgerEntryChange) bool {
	cd, _, ok := contractDataFromChange(change)
	if !ok {
		return false
	}
	contractID, ok := contractIDFromScAddress(cd.Contract)
	if !ok {
		return false
	}
	if _, watched := o.wrappers[contractID]; !watched {
		return false
	}
	return isSEP41BalanceKey(cd.Key)
}

// Decode emits one Observation per matched change.
func (o *Observer) Decode(ctx dispatcher.LedgerEntryChangeContext) ([]consumer.Event, error) {
	cd, isRemoval, ok := contractDataFromChange(ctx.Change)
	if !ok {
		return nil, ErrNotSACBalance
	}
	contractID, ok := contractIDFromScAddress(cd.Contract)
	if !ok {
		return nil, fmt.Errorf("%w: contract scAddress not a contract id", ErrNotSACBalance)
	}
	assetKey, watched := o.wrappers[contractID]
	if !watched {
		return nil, fmt.Errorf("%w: contract %s not in wrapper map", ErrNotSACBalance, contractID)
	}
	holder, err := holderFromBalanceKey(cd.Key)
	if err != nil {
		return nil, err
	}
	if isRemoval {
		return []consumer.Event{Observation{
			ContractID: contractID,
			AssetKey:   assetKey,
			Holder:     holder,
			Ledger:     ctx.Ledger,
			ObservedAt: ctx.ClosedAt,
			Balance:    big.NewInt(0),
			IsRemoval:  true,
		}}, nil
	}
	balance, err := extractBalanceAmount(cd.Val)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{Observation{
		ContractID: contractID,
		AssetKey:   assetKey,
		Holder:     holder,
		Ledger:     ctx.Ledger,
		ObservedAt: ctx.ClosedAt,
		Balance:    balance,
	}}, nil
}

// contractDataFromChange returns the ContractDataEntry +
// is-removal flag for any change variant. Removed-variant rows
// derive ContractData from the LedgerKey (which carries Contract
// + Key but no Val).
func contractDataFromChange(change xdr.LedgerEntryChange) (cd *xdr.ContractDataEntry, isRemoval bool, ok bool) {
	var entry *xdr.LedgerEntry
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		entry = change.Created
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		entry = change.Updated
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		entry = change.Restored
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		k := change.Removed
		if k == nil || k.Type != xdr.LedgerEntryTypeContractData || k.ContractData == nil {
			return nil, false, false
		}
		// Removed: synthesize a ContractDataEntry from the LedgerKey
		// fields (no Val available). Caller checks the isRemoval
		// flag and skips Val parsing.
		return &xdr.ContractDataEntry{
			Contract:   k.ContractData.Contract,
			Key:        k.ContractData.Key,
			Durability: k.ContractData.Durability,
		}, true, true
	default:
		return nil, false, false
	}
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeContractData {
		return nil, false, false
	}
	return entry.Data.ContractData, false, entry.Data.ContractData != nil
}

// contractIDFromScAddress returns the C-strkey form when the
// scAddress is a Contract variant.
func contractIDFromScAddress(addr xdr.ScAddress) (string, bool) {
	if addr.Type != xdr.ScAddressTypeScAddressTypeContract {
		return "", false
	}
	raw := addr.MustContractId()
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		return "", false
	}
	return s, true
}

// isSEP41BalanceKey reports whether key matches the
// `DataKey::Balance(Address)` shape — Vec of length 2 with
// Symbol("Balance") at index 0.
func isSEP41BalanceKey(key xdr.ScVal) bool {
	vec, err := scval.AsVec(key)
	if err != nil || len(vec) != 2 {
		return false
	}
	if vec[0].Type != xdr.ScValTypeScvSymbol {
		return false
	}
	// Compare the encoded symbol against our pre-encoded Balance
	// blob — byte-equality is cheap and avoids a string parse on
	// the hot path.
	enc, err := scval.EncodeSymbol(string(*vec[0].Sym))
	if err != nil {
		return false
	}
	return enc == balanceKeySymbol
}

// holderFromBalanceKey extracts the Address scVal (index 1) from
// a confirmed-shape balance key, returning the strkey.
func holderFromBalanceKey(key xdr.ScVal) (string, error) {
	vec, err := scval.AsVec(key)
	if err != nil || len(vec) < 2 {
		return "", errors.New("sac_balances: balance key Vec too short")
	}
	addr, err := scval.AsAddressStrkey(vec[1])
	if err != nil {
		return "", fmt.Errorf("sac_balances: extract holder: %w", err)
	}
	return addr, nil
}

// extractBalanceAmount returns the i128 amount from a SEP-41
// balance value. Tries i128 first; falls back to a map with an
// "amount" symbol field (the native SAC's BalanceValue shape).
// Returns ErrUnknownValShape on any other shape.
func extractBalanceAmount(val xdr.ScVal) (*big.Int, error) {
	if val.Type == xdr.ScValTypeScvI128 {
		amt, err := scval.AsAmountFromI128(val)
		if err != nil {
			return nil, err
		}
		return amt.BigInt(), nil
	}
	if val.Type == xdr.ScValTypeScvMap {
		entries, err := scval.AsMap(val)
		if err != nil {
			return nil, err
		}
		amountVal, ok := scval.MapField(entries, "amount")
		if !ok {
			return nil, fmt.Errorf("%w: map has no `amount` field", ErrUnknownValShape)
		}
		if amountVal.Type != xdr.ScValTypeScvI128 {
			return nil, fmt.Errorf("%w: map.amount is %s, want I128", ErrUnknownValShape, amountVal.Type)
		}
		amt, err := scval.AsAmountFromI128(amountVal)
		if err != nil {
			return nil, err
		}
		return amt.BigInt(), nil
	}
	return nil, fmt.Errorf("%w: val type %s", ErrUnknownValShape, val.Type)
}

var _ dispatcher.LedgerEntryChangeDecoder = (*Observer)(nil)
