package v1

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// OracleReader is the storage-side interface for /v1/oracle/latest
// lookups.
type OracleReader interface {
	// LatestOracleUpdatesForAsset returns the most-recent observation
	// per source for asset. sourceFilter="" returns every source;
	// a non-empty value restricts to that single source.
	//
	// Empty slice + nil error means "no observations" — that's
	// distinct from an error.
	LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error)

	// LatestOracleUpdatesForAssets is the multi-key variant —
	// returns the most-recent observation per source across the
	// union of the supplied asset keys. Used by the handler to
	// expand user-facing asset identifiers (e.g. `native`) into
	// the per-oracle internal forms (e.g. `crypto:XLM`).
	LatestOracleUpdatesForAssets(ctx context.Context, assets []canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error)

	// LatestOracleStreams returns one row per (source, asset, quote)
	// triple — the most-recent observation in the trailing 7d
	// window. Backs /v1/oracles/streams (the "every active price
	// stream from every oracle" listing for the explorer).
	LatestOracleStreams(ctx context.Context) ([]canonical.OracleUpdate, error)
}

// oracleAssetCandidates expands the user-facing asset identifier
// into every key form the oracle layer might have stored under.
//
// Reflector — the only on-chain oracle that publishes per-asset
// readings — keys observations by the global crypto ticker
// (`crypto:XLM`, `crypto:USDC`, …) rather than by the per-network
// canonical asset_id. Without this expansion,
// `/v1/oracle/latest?asset=native` returns empty even though
// Reflector publishes XLM under crypto:XLM, and an empty 285 ms
// hypertable scan was the wall-clock cost of proving it.
//
// Returned slice always includes the original asset; subsequent
// entries are best-effort translations the storage layer's
// `WHERE asset = ANY($1)` filter unions over.
func oracleAssetCandidates(a canonical.Asset) []canonical.Asset {
	candidates := []canonical.Asset{a}

	// `native` → also try `crypto:XLM`.
	if a.Type == canonical.AssetNative {
		if x, err := canonical.ParseAsset("crypto:XLM"); err == nil {
			candidates = append(candidates, x)
		}
		return candidates
	}

	// Classic credit asset → also try `crypto:<CODE>` so global
	// stablecoin tickers (USDC, USDT, EURC) match Reflector's
	// per-ticker rows. Harmless on assets Reflector doesn't track
	// (the ANY($1) filter just yields zero rows for that key).
	if a.Type == canonical.AssetClassic && a.Code != "" {
		if x, err := canonical.ParseAsset("crypto:" + a.Code); err == nil {
			candidates = append(candidates, x)
		}
		return candidates
	}

	return candidates
}

// OracleReading is the wire shape for /v1/oracle/latest entries.
//
// Price is rendered as a decimal string scaled by Decimals. We
// report both the normalised decimal AND the raw integer + decimals
// scale so sophisticated clients can verify the rendering.
type OracleReading struct {
	Source     string    `json:"source"`
	ContractID string    `json:"contract_id,omitempty"`
	Asset      string    `json:"asset"`
	Quote      string    `json:"quote"`
	Timestamp  time.Time `json:"ts"`

	// Price is the human-facing decimal string at Decimals scale.
	Price string `json:"price"`

	// PriceRaw is the underlying integer value at Decimals scale,
	// preserved for cross-checks (ADR-0003 — never lose the raw).
	PriceRaw string `json:"price_raw"`

	// Decimals is the source-declared scale. 14 for Reflector.
	Decimals uint8 `json:"decimals"`

	// Confidence is the oracle's own confidence score (0–1) when
	// published. Zero means "not reported", not "zero confidence."
	Confidence float64 `json:"confidence,omitempty"`

	// Observer is the on-chain account that published the update
	// (typically a Reflector relayer). Empty when unknown.
	Observer string `json:"observer,omitempty"`
}

// handleOracleLatest serves GET /v1/oracle/latest?asset=<id>&source=<name>.
//
// With no source filter: returns an array of OracleReading, one per
// source that has observed the asset. With a source filter: returns
// an array of at most one element.
//
// 200 with empty array when no observations exist — callers treat
// this as "nothing to report," not an error. That matches the
// behaviour of /v1/history.
func (s *Server) handleOracleLatest(w http.ResponseWriter, r *http.Request) {
	reader := s.oracle
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/oracle-unavailable",
			"Oracle readings not configured", http.StatusServiceUnavailable,
			"this deployment has no OracleReader wired — check binary configuration")
		return
	}

	rawAsset := r.URL.Query().Get("asset")
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required")
		return
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	source := r.URL.Query().Get("source") // optional
	if source != "" {
		// Validate against the in-memory registry so an unknown
		// source name returns 400 instead of an empty page (the
		// silent-empty-page anti-pattern: a typo in `?source=`
		// looks identical on the wire to "this source has no
		// observation for the asset"). Same fail-fast guard as
		// /v1/markets and /v1/observations.
		if _, ok := external.Registry[source]; !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/unknown-source",
				"Unknown source", http.StatusBadRequest,
				"source must be a registered source name (see /v1/sources for the canonical list); got "+source)
			return
		}
	}

	olCtx, olCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer olCancel()
	updates, err := reader.LatestOracleUpdatesForAssets(
		olCtx, oracleAssetCandidates(asset), source)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("LatestOracleUpdatesForAsset deadline exceeded",
				"asset", asset.String(), "source", source)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/oracle-latest-timeout",
				"Oracle latest query timed out", http.StatusServiceUnavailable,
				"the oracle_updates hypertable scan didn't return in 8s; retry shortly.")
			return
		}
		s.logger.Error("LatestOracleUpdatesForAsset failed",
			"err", err, "asset", asset.String(), "source", source)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	rows := make([]OracleReading, len(updates))
	for i, u := range updates {
		rows[i] = oracleReadingFrom(u)
	}
	writeJSON(w, rows, Flags{})
}

// handleOracleStreams serves GET /v1/oracle/streams.
//
// Returns one row per (source, asset, quote) triple — the latest
// observation each oracle has published in the trailing 7d. No
// query parameters; the catalogue is small enough (~100s of rows
// at peak) that a full dump is the simplest contract.
//
// Empty array + 200 when no oracles have published recently or no
// OracleReader is wired — consistent with /v1/oracle/latest's
// "nothing to report" handling.
func (s *Server) handleOracleStreams(w http.ResponseWriter, r *http.Request) {
	reader := s.oracle
	if reader == nil {
		writeJSON(w, []OracleReading{}, Flags{})
		return
	}
	// 8s ceiling on the oracle_updates hypertable scan. Same
	// pattern as #1082-#1103. Steady-state ~600ms per the
	// 2026-05-08 prod probe, but cold-cache scans of 7d × 80
	// oracle streams can take 5-10s.
	osCtx, osCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer osCancel()
	updates, err := reader.LatestOracleStreams(osCtx)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("LatestOracleStreams deadline exceeded")
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/oracle-streams-timeout",
				"Oracle streams query timed out", http.StatusServiceUnavailable,
				"the oracle_updates hypertable scan didn't return in 8s; retry shortly.")
			return
		}
		s.logger.Error("LatestOracleStreams failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	// Filter to only sources classed as ClassOracle. CoinGecko (an
	// aggregator) and ECB (an authority-sanity feed) write into the
	// same oracle_updates hypertable for divergence-comparison
	// purposes, but they're not oracles and shouldn't appear on the
	// /oracles page. The class is a registry fact, not a per-row
	// flag in the table — filter at the wire boundary.
	rows := make([]OracleReading, 0, len(updates))
	for _, u := range updates {
		if external.Lookup(u.Source).Class != external.ClassOracle {
			continue
		}
		rows = append(rows, oracleReadingFrom(u))
	}
	writeJSON(w, rows, Flags{})
}

// oracleReadingFrom converts canonical.OracleUpdate → wire shape,
// rendering Price at its declared Decimals scale.
func oracleReadingFrom(u canonical.OracleUpdate) OracleReading {
	return OracleReading{
		Source:     u.Source,
		ContractID: u.ContractID,
		Asset:      u.Asset.String(),
		Quote:      u.Quote.String(),
		Timestamp:  u.Timestamp,
		Price:      scaledDecimalString(u.Price.BigInt(), u.Decimals),
		PriceRaw:   u.Price.String(),
		Decimals:   u.Decimals,
		Confidence: u.Confidence,
		Observer:   u.Observer,
	}
}

// scaledDecimalString renders integer/10^decimals as a decimal
// string, truncating (floor) to `decimals` fractional digits.
// Preserves sign correctly. Consistent with priceRatioDecimal /
// ratToDecimal.
func scaledDecimalString(value *big.Int, decimals uint8) string {
	if value == nil {
		return "0"
	}
	if decimals == 0 {
		return value.String()
	}

	sign := ""
	abs := new(big.Int).Abs(value)
	if value.Sign() < 0 {
		sign = "-"
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	integerPart, fracPart := new(big.Int).DivMod(abs, scale, new(big.Int))

	// Pad fractional part to `decimals` digits.
	frac := fracPart.String()
	if len(frac) < int(decimals) {
		pad := int(decimals) - len(frac)
		frac = leftPad(frac, pad, '0')
	}
	return sign + integerPart.String() + "." + frac
}
