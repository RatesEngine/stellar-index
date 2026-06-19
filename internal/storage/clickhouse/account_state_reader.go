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

	// Account entry — the current-state projection (ledger_entries_current)
	// already holds the latest entry per key (ReplacingMergeTree); FINAL forces
	// read-time dedup. A trailing 'removed' = merged away.
	const accQ = `SELECT entry_xdr, change_type, balance, ledger_seq
		FROM stellar.ledger_entries_current FINAL
		WHERE account_id = ? AND entry_type = 'account'
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
	const q = `SELECT asset, entry_xdr AS ex, balance AS bal
		FROM stellar.ledger_entries_current FINAL
		WHERE account_id = ? AND entry_type = 'trustline' AND change_type != 'removed'
		ORDER BY bal DESC`
	rows, err := r.conn.Query(ctx, q, account)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account trustlines: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TrustlineState
	for rows.Next() {
		var asset, ex string
		var bal int64
		if err := rows.Scan(&asset, &ex, &bal); err != nil {
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
	const q = `SELECT entry_xdr AS ex
		FROM stellar.ledger_entries_current FINAL
		WHERE account_id = ? AND entry_type = 'offer' AND change_type != 'removed'`
	rows, err := r.conn.Query(ctx, q, account)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account offers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OfferState
	for rows.Next() {
		var ex string
		if err := rows.Scan(&ex); err != nil {
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
	const holdersQ = `SELECT account_id, balance
		FROM stellar.ledger_entries_current FINAL
		WHERE entry_type = 'trustline' AND asset = ? AND change_type != 'removed' AND balance > 0
		ORDER BY balance DESC
		LIMIT ?`
	rows, err := r.conn.Query(ctx, holdersQ, asset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("clickhouse: asset holders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AssetHolder
	for rows.Next() {
		var h AssetHolder
		if err := rows.Scan(&h.AccountID, &h.Balance); err != nil {
			return nil, 0, fmt.Errorf("clickhouse: scan holder: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	const countQ = `SELECT toInt64(count())
		FROM stellar.ledger_entries_current FINAL
		WHERE entry_type = 'trustline' AND asset = ? AND change_type != 'removed' AND balance > 0`
	var total int64
	if err := r.conn.QueryRow(ctx, countQ, asset).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("clickhouse: asset holder count: %w", err)
	}
	return out, total, nil
}

// AccountWealth is one row of the wealth-ranked accounts directory.
type AccountWealth struct {
	AccountID string
	USD       float64
}

// AccountsByWealth ranks accounts by total USD value of their holdings —
// native XLM (the account entry) plus every trustline asset for which the
// caller supplied a USD price. assets/prices are parallel arrays (assets[i]
// priced at prices[i]; the native XLM key is "native"). Computed over the
// current-state projection in one pass (sum balance×price per account); only
// priced assets contribute. Coverage tracks the entry-change capture +
// backfill — accounts/assets not yet captured simply aren't ranked yet.
func (r *ExplorerReader) AccountsByWealth(ctx context.Context, assets []string, prices []float64, limit int) ([]AccountWealth, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if len(assets) == 0 || len(assets) != len(prices) {
		return nil, nil
	}
	// balance is stroops (1e7); k = "native" for the account entry, else the
	// trustline asset. has(assets, k) keeps only priced rows; indexOf maps the
	// key to its price. Sum per account, rank desc.
	const q = `SELECT account_id,
		sum(toFloat64(balance) / 1e7 * arrayElement(?, indexOf(?, k))) AS usd
		FROM (
			SELECT account_id, balance, if(entry_type = 'account', 'native', asset) AS k
			FROM stellar.ledger_entries_current FINAL
			WHERE change_type != 'removed' AND entry_type IN ('account', 'trustline')
		)
		WHERE has(?, k)
		GROUP BY account_id
		HAVING usd > 0
		ORDER BY usd DESC
		LIMIT ?`
	rows, err := r.conn.Query(ctx, q, prices, assets, assets, limit)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: accounts by wealth: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AccountWealth
	for rows.Next() {
		var w AccountWealth
		if err := rows.Scan(&w.AccountID, &w.USD); err != nil {
			return nil, fmt.Errorf("clickhouse: scan account wealth: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
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

// AccountHomeDomains returns account → home_domain for the given accounts that
// carry a non-empty home_domain in the current-state projection. Batch helper
// for the issuer-enrich backfill: the lake doesn't denormalize home_domain to a
// column, so it's decoded from the account entry XDR. Accounts with no entry /
// no home_domain are simply absent from the map.
func (r *ExplorerReader) AccountHomeDomains(ctx context.Context, accounts []string) (map[string]string, error) {
	if len(accounts) == 0 {
		return map[string]string{}, nil
	}
	const q = `SELECT account_id, entry_xdr FROM stellar.ledger_entries_current FINAL
		WHERE entry_type = 'account' AND account_id IN (?) AND change_type != 'removed' AND entry_xdr != ''`
	rows, err := r.conn.Query(ctx, q, accounts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account home_domains: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var acct, entryXDR string
		if err := rows.Scan(&acct, &entryXDR); err != nil {
			return nil, fmt.Errorf("clickhouse: scan home_domain: %w", err)
		}
		var le xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(entryXDR, &le) != nil {
			continue
		}
		if acc, ok := le.Data.GetAccount(); ok {
			if hd := string(acc.HomeDomain); hd != "" {
				out[acct] = hd
			}
		}
	}
	return out, rows.Err()
}
