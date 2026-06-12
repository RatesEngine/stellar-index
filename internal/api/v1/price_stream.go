package v1

import (
	"net/http"

	"github.com/StellarAtlas/stellar-atlas/internal/api/streaming"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// PriceStreamTopic returns the Hub topic key for closed-bucket events
// on a given (asset, quote) pair. Exported so the aggregator (the
// only intended publisher) can compute the same key when calling
// Hub.Publish — keeps the wire string in one place.
//
// Format: `closed:<asset>/<quote>` using canonical asset strings.
// The "closed:" prefix lets the same Hub multiplex tip / observations
// fanout in the future without topic-key collisions (today only the
// closed-bucket surface is Hub-driven).
func PriceStreamTopic(asset, quote canonical.Asset) string {
	return "closed:" + asset.String() + "/" + quote.String()
}

// handlePriceStream serves GET /v1/price/stream — the SSE endpoint
// carrying the strict ADR-0015 closed-bucket consistency contract
// that /v1/price serves. Unlike the tip + observations streams (per-
// connection tick), this surface is Hub-driven: the aggregator
// publishes one event per closed bucket, and every subscriber on
// the same (asset, quote) topic receives the same byte-identical
// payload — the same cross-region consistency property that
// /v1/price itself exposes.
//
// Wire shape:
//
//   - On connect: SSE headers, optional buffered-replay from
//     `Last-Event-ID` (Hub maintains a per-topic ring buffer).
//   - Per closed bucket: one `price_update` event with the same
//     envelope shape as a `/v1/price` response.
//   - Heartbeats every 15 s as comment lines.
//
// Pre-flight 503: when no Hub is wired (typical pre-launch state
// where the aggregator isn't running yet), the endpoint returns
// 503 — same posture as the other Hub-dependent surfaces.
func (s *Server) handlePriceStream(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stream-unavailable",
			"Closed-bucket stream not configured", http.StatusServiceUnavailable,
			"this deployment has no streaming Hub wired — typically because the aggregator publish path isn't running yet")
		return
	}

	// Reuse the parameter parser from the request endpoint — same
	// asset+quote contract, same default fiat:USD quote.
	asset, quote, ok := s.parseTipAssetQuote(w, r)
	if !ok {
		return
	}

	// URL discipline: the closed-bucket surface accepts no granularity
	// query param — the bucket size is fixed at 1m for the streaming
	// surface (per ADR-0015's per-window contract). A future
	// granularity-aware closed-bucket stream would live on a different
	// URL.
	if r.URL.Query().Get("granularity") != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-stream-param",
			"granularity is not valid on /v1/price/stream", http.StatusBadRequest,
			"the closed-bucket stream is fixed at 1m; use /v1/history/since-inception for other granularities")
		return
	}

	topic := PriceStreamTopic(asset, quote)
	streaming.Stream(w, r, s.hub, []string{topic}, streaming.StreamOptions{})
}
