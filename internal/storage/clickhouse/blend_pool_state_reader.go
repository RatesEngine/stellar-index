package clickhouse

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/sources/blend"
)

// BlendReserveState is one Blend reserve's decoded current state +
// derived metrics (ADR-0039), read from the certified lake.
type BlendReserveState struct {
	Pool    string
	Asset   string // reserve underlying token (C-strkey)
	Data    blend.ReserveData
	Config  blend.ReserveConfig
	Metrics blend.ReserveMetrics
}

// BlendPoolReserves reads the CURRENT reserve state for a Blend pool
// from the lake (ADR-0039). It builds every storage key it needs up
// front — ResData + ResConfig per reserve asset, plus the pool's
// instance entry (for the backstop take rate) — and fetches them in a
// SINGLE batched `key_xdr IN (...)` query (one columnar pass over
// contract_data, not one scan per key). Assets the caller doesn't
// supply, or reserves with no captured entry, are simply absent.
func (r *ExplorerReader) BlendPoolReserves(ctx context.Context, pool string, assets []string) ([]BlendReserveState, error) {
	poolID, err := contractIDFromStrkey(pool)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: blend pool id %q: %w", pool, err)
	}

	keys, refByKey := blendReserveKeys(poolID, assets)
	if len(keys) == 0 {
		return nil, nil
	}

	// One batched lookup: latest entry_xdr per key (argMax over the
	// ReplacingMergeTree versions). Bounded to a recent ledger window
	// so it's partition-pruned (intDiv 1M) instead of full-scanning the
	// ~270 M-row contract_data set — `key_xdr` has no skip-index, so an
	// unbounded `key_xdr IN (…)` is a ~20 s full scan. An active Blend
	// pool rewrites its reserve entries on nearly every interaction, so
	// the latest state is always within this window; a reserve untouched
	// for longer is reported as absent (consistent with "captured
	// window"). A bloom_filter index on key_xdr would remove the bound
	// (and speed the wasm/code-history readers too) — deferred (heavy
	// MATERIALIZE over a shared host).
	const reserveWindowLedgers = 250_000 // ~14 days at 5s
	const q = `SELECT key_xdr, argMax(entry_xdr, ledger_seq)
		FROM stellar.ledger_entry_changes
		WHERE entry_type = 'contract_data'
		  AND ledger_seq > (SELECT max(ledger_seq) FROM stellar.ledger_entry_changes) - ?
		  AND key_xdr IN (?) AND entry_xdr != ''
		GROUP BY key_xdr`
	rows, err := r.conn.Query(ctx, q, uint32(reserveWindowLedgers), keys)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: blend reserves lookup: %w", err)
	}
	defer func() { _ = rows.Close() }()

	parts, bstop, err := scanBlendReserveParts(rows, refByKey)
	if err != nil {
		return nil, err
	}

	// Assemble in the caller's asset order; a reserve needs both
	// ResData + ResConfig to produce metrics.
	out := make([]BlendReserveState, 0, len(assets))
	for _, asset := range assets {
		p := parts[asset]
		if p == nil || p.data == nil || p.config == nil {
			continue
		}
		out = append(out, BlendReserveState{
			Pool:    pool,
			Asset:   asset,
			Data:    *p.data,
			Config:  *p.config,
			Metrics: blend.Metrics(*p.data, *p.config, bstop),
		})
	}
	return out, nil
}

// keyRef maps a built storage key back to what it is.
type keyRef struct {
	asset string
	kind  string // "ResData" | "ResConfig" | "Instance"
}

// blendReserveKeys builds every storage key BlendPoolReserves needs —
// the pool instance entry (for the backstop rate) + ResData/ResConfig
// per reserve asset — and a reverse index from key to (asset, kind).
func blendReserveKeys(poolID xdr.ContractId, assets []string) ([]string, map[string]keyRef) {
	refByKey := make(map[string]keyRef)
	keys := make([]string, 0, len(assets)*2+2)
	if instanceKeys, err := instanceKeyXDR(xdr.Hash(poolID)); err == nil {
		for _, k := range instanceKeys {
			refByKey[k] = keyRef{kind: "Instance"}
			keys = append(keys, k)
		}
	}
	for _, asset := range assets {
		assetID, err := contractIDFromStrkey(asset)
		if err != nil {
			continue
		}
		for _, kind := range []string{"ResData", "ResConfig"} {
			k, err := poolDataKeyXDR(poolID, kind, assetID)
			if err != nil {
				continue
			}
			refByKey[k] = keyRef{asset: asset, kind: kind}
			keys = append(keys, k)
		}
	}
	return keys, refByKey
}

// scanBlendReserveParts decodes the batched lookup's rows into per-asset
// ResData/ResConfig parts + the pool's backstop rate.
func scanBlendReserveParts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, refByKey map[string]keyRef,
) (map[string]*blendReserveParts, uint32, error) {
	parts := make(map[string]*blendReserveParts)
	var bstop uint32
	for rows.Next() {
		var keyXDR, b64 string
		if err := rows.Scan(&keyXDR, &b64); err != nil {
			return nil, 0, fmt.Errorf("clickhouse: scan blend reserve: %w", err)
		}
		ref, ok := refByKey[keyXDR]
		if !ok {
			continue
		}
		val, ok := contractDataValue(b64)
		if !ok {
			continue
		}
		switch ref.kind {
		case "Instance":
			bstop = backstopRateFromInstance(val)
		case "ResData":
			if rd, err := blend.DecodeReserveData(val); err == nil {
				ensureReserve(parts, ref.asset).data = &rd
			}
		case "ResConfig":
			if rc, err := blend.DecodeReserveConfig(val); err == nil {
				ensureReserve(parts, ref.asset).config = &rc
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("clickhouse: blend reserves rows: %w", err)
	}
	return parts, bstop, nil
}

// blendReserveParts accumulates a reserve's ResData + ResConfig as the
// batched lookup's rows stream in (they arrive in arbitrary order).
type blendReserveParts struct {
	data   *blend.ReserveData
	config *blend.ReserveConfig
}

func ensureReserve(m map[string]*blendReserveParts, asset string) *blendReserveParts {
	if m[asset] == nil {
		m[asset] = &blendReserveParts{}
	}
	return m[asset]
}

// contractDataValue unmarshals a base64 LedgerEntry and returns its
// ContractData value ScVal.
func contractDataValue(b64 string) (xdr.ScVal, bool) {
	var entry xdr.LedgerEntry
	if xdr.SafeUnmarshalBase64(b64, &entry) != nil {
		return xdr.ScVal{}, false
	}
	cd, ok := entry.Data.GetContractData()
	if !ok {
		return xdr.ScVal{}, false
	}
	return cd.Val, true
}

// backstopRateFromInstance pulls PoolConfig.bstop_rate (7 decimals)
// from a contract instance entry's storage map (Symbol "Config"). 0 on
// any miss → the supply-APR is then the gross rate (backstop take
// unaccounted).
func backstopRateFromInstance(val xdr.ScVal) uint32 {
	inst, ok := val.GetInstance()
	if !ok || inst.Storage == nil {
		return 0
	}
	for _, e := range *inst.Storage {
		if e.Key.Type == xdr.ScValTypeScvSymbol && e.Key.Sym != nil && string(*e.Key.Sym) == "Config" {
			if pc, err := blend.DecodePoolConfig(e.Val); err == nil {
				return pc.BstopRate
			}
			return 0
		}
	}
	return 0
}

// poolDataKeyXDR builds the base64 LedgerKey for a Blend
// PoolDataKey::<variant>(asset) persistent contract_data entry under
// the pool contract — matching the `key_xdr` column verbatim. The
// #[contracttype] enum variant encodes as Vec[Symbol(variant),
// Address(asset)].
func poolDataKeyXDR(poolID xdr.ContractId, variant string, assetID xdr.ContractId) (string, error) {
	pid := poolID
	sym := xdr.ScSymbol(variant)
	assetAddr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &assetID}
	vec := &xdr.ScVec{
		{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
		{Type: xdr.ScValTypeScvAddress, Address: &assetAddr},
	}
	key := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vec}
	lk := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &pid},
			Key:        key,
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}
	return xdr.MarshalBase64(lk)
}

// contractIDFromStrkey decodes a C-strkey into an xdr.ContractId.
func contractIDFromStrkey(c string) (xdr.ContractId, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, c)
	if err != nil {
		return xdr.ContractId{}, fmt.Errorf("not a contract strkey: %w", err)
	}
	var id xdr.ContractId
	copy(id[:], raw)
	return id, nil
}
