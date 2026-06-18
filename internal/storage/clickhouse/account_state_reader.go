package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/xdrjson"
)

// AccountState is the current on-chain state of an account, reconstructed from
// the latest ledger_entry_changes per key (ADR-0038 Phase C). Exists=false
// when the account has no live AccountEntry (never created, or merged away).
type AccountState struct {
	Exists             bool
	Balance            int64 // native XLM, stroops
	SeqNum             int64
	NumSubEntries      uint32
	Flags              uint32
	HomeDomain         string
	MasterWeight       byte
	ThreshLow          byte
	ThreshMed          byte
	ThreshHigh         byte
	LastModifiedLedger uint32
	Signers            []AccountSigner
	Trustlines         []TrustlineState
	Offers             []OfferState
}

type AccountSigner struct {
	Key    string
	Weight uint32
}

type TrustlineState struct {
	Asset   string
	Balance int64
	Limit   int64
	Flags   uint32
}

type OfferState struct {
	OfferID int64
	Selling string
	Buying  string
	Amount  int64
	PriceN  int32
	PriceD  int32
}

// AssetHolder is one holder of an asset, ranked by current trustline balance.
type AssetHolder struct {
	AccountID string
	Balance   int64
}

// AccountState reconstructs an account's current state from the lake: the
// latest AccountEntry (balance/signers/thresholds/flags/home-domain), plus its
// live trustlines and offers (latest non-removed change per key). Relies on
// the account_id skip-index (ADR-0038 Phase C). Returns Exists=false (no error)
// for an unknown / merged account.
func (r *ExplorerReader) AccountState(ctx context.Context, account string) (AccountState, error) {
	var st AccountState

	// Account entry — latest change wins; a trailing 'removed' = merged away.
	const accQ = `SELECT entry_xdr, change_type, balance, ledger_seq
		FROM stellar.ledger_entry_changes
		WHERE account_id = ? AND entry_type = 'account'
		ORDER BY ledger_seq DESC, change_index DESC
		LIMIT 1`
	var (
		entryXDR, changeType string
		bal                  int64
		ledgerSeq            uint32
	)
	row := r.conn.QueryRow(ctx, accQ, account)
	if err := row.Scan(&entryXDR, &changeType, &bal, &ledgerSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Unknown account / not in the captured window — the empty
			// state, surfaced via Exists=false rather than an error.
			return st, nil
		}
		return st, fmt.Errorf("clickhouse: account entry %s: %w", account, err)
	}
	if changeType == "removed" || entryXDR == "" {
		return st, nil
	}
	var le xdr.LedgerEntry
	if err := xdr.SafeUnmarshalBase64(entryXDR, &le); err != nil {
		// A corrupt stored entry degrades to "no state" rather than 500-ing
		// the request — the row is the substrate's problem, not the caller's.
		return st, nil //nolint:nilerr // intentional degrade-to-empty on bad data
	}
	acc, ok := le.Data.GetAccount()
	if !ok {
		return st, nil
	}
	st.Exists = true
	st.Balance = bal
	st.SeqNum = int64(acc.SeqNum)
	st.NumSubEntries = uint32(acc.NumSubEntries)
	st.Flags = uint32(acc.Flags)
	st.HomeDomain = string(acc.HomeDomain)
	st.MasterWeight = byte(acc.Thresholds[0])
	st.ThreshLow = byte(acc.Thresholds[1])
	st.ThreshMed = byte(acc.Thresholds[2])
	st.ThreshHigh = byte(acc.Thresholds[3])
	st.LastModifiedLedger = ledgerSeq
	for _, s := range acc.Signers {
		st.Signers = append(st.Signers, AccountSigner{Key: signerAddress(s.Key), Weight: uint32(s.Weight)})
	}

	tl, err := r.accountTrustlines(ctx, account)
	if err != nil {
		return st, err
	}
	st.Trustlines = tl
	of, err := r.accountOffers(ctx, account)
	if err != nil {
		return st, err
	}
	st.Offers = of
	return st, nil
}

func (r *ExplorerReader) accountTrustlines(ctx context.Context, account string) ([]TrustlineState, error) {
	const q = `SELECT asset,
		argMax(entry_xdr, (ledger_seq, change_index)) AS ex,
		argMax(change_type, (ledger_seq, change_index)) AS ct,
		argMax(balance, (ledger_seq, change_index)) AS bal
		FROM stellar.ledger_entry_changes
		WHERE account_id = ? AND entry_type = 'trustline'
		GROUP BY asset
		HAVING ct != 'removed'
		ORDER BY bal DESC`
	rows, err := r.conn.Query(ctx, q, account)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account trustlines: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TrustlineState
	for rows.Next() {
		var asset, ex, ct string
		var bal int64
		if err := rows.Scan(&asset, &ex, &ct, &bal); err != nil {
			return nil, fmt.Errorf("clickhouse: scan trustline: %w", err)
		}
		t := TrustlineState{Asset: asset, Balance: bal}
		var le xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(ex, &le) == nil {
			if tl, ok := le.Data.GetTrustLine(); ok {
				t.Limit = int64(tl.Limit)
				t.Flags = uint32(tl.Flags)
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ExplorerReader) accountOffers(ctx context.Context, account string) ([]OfferState, error) {
	const q = `SELECT
		argMax(entry_xdr, (ledger_seq, change_index)) AS ex,
		argMax(change_type, (ledger_seq, change_index)) AS ct
		FROM stellar.ledger_entry_changes
		WHERE account_id = ? AND entry_type = 'offer'
		GROUP BY key_xdr
		HAVING ct != 'removed'`
	rows, err := r.conn.Query(ctx, q, account)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account offers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OfferState
	for rows.Next() {
		var ex, ct string
		if err := rows.Scan(&ex, &ct); err != nil {
			return nil, fmt.Errorf("clickhouse: scan offer: %w", err)
		}
		var le xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(ex, &le) != nil {
			continue
		}
		o, ok := le.Data.GetOffer()
		if !ok {
			continue
		}
		out = append(out, OfferState{
			OfferID: int64(o.OfferId),
			Selling: xdrjson.AssetID(o.Selling),
			Buying:  xdrjson.AssetID(o.Buying),
			Amount:  int64(o.Amount),
			PriceN:  int32(o.Price.N),
			PriceD:  int32(o.Price.D),
		})
	}
	return out, rows.Err()
}

// AssetHolders returns the top holders of an asset by current trustline
// balance, plus the total count of holders with a positive balance. Pure SQL
// over the asset skip-index + balance column — no per-holder XDR decode.
func (r *ExplorerReader) AssetHolders(ctx context.Context, asset string, limit int) ([]AssetHolder, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const holdersQ = `SELECT account_id,
		argMax(balance, (ledger_seq, change_index)) AS bal,
		argMax(change_type, (ledger_seq, change_index)) AS ct
		FROM stellar.ledger_entry_changes
		WHERE entry_type = 'trustline' AND asset = ?
		GROUP BY account_id
		HAVING ct != 'removed' AND bal > 0
		ORDER BY bal DESC
		LIMIT ?`
	rows, err := r.conn.Query(ctx, holdersQ, asset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("clickhouse: asset holders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AssetHolder
	for rows.Next() {
		var h AssetHolder
		var ct string
		if err := rows.Scan(&h.AccountID, &h.Balance, &ct); err != nil {
			return nil, 0, fmt.Errorf("clickhouse: scan holder: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	const countQ = `SELECT toInt64(count()) FROM (
		SELECT account_id,
			argMax(balance, (ledger_seq, change_index)) AS bal,
			argMax(change_type, (ledger_seq, change_index)) AS ct
		FROM stellar.ledger_entry_changes
		WHERE entry_type = 'trustline' AND asset = ?
		GROUP BY account_id
		HAVING ct != 'removed' AND bal > 0
	)`
	var total int64
	if err := r.conn.QueryRow(ctx, countQ, asset).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("clickhouse: asset holder count: %w", err)
	}
	return out, total, nil
}

// signerAddress renders a SignerKey strkey without panicking on an unknown
// discriminant (degrades to "").
func signerAddress(k xdr.SignerKey) string {
	s, err := k.GetAddress()
	if err != nil {
		return ""
	}
	return s
}
