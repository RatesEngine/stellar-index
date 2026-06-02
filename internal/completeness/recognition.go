// Package completeness implements the ADR-0033 verification model —
// the data-derived checks that turn "100% coverage" from an assertion
// into three independently-provable claims:
//
//   - Substrate continuity (ledger_ingest_log: contiguity + hash chain).
//   - Recognition (this file): every event shape on-chain is one a
//     decoder handles.
//   - Projection reconciliation (reconcile.go): every captured event
//     became a row.
//
// The package is a pure consumer of internal/storage/timescale +
// internal/dispatcher (via narrow interfaces); nothing imports it back.
package completeness

import (
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// Recognizer decides whether any decoder claims an event, without
// decoding it. *dispatcher.Dispatcher satisfies this via Recognize.
type Recognizer interface {
	Recognize(events.Event) (string, bool)
}

// RecognitionGap is a distinct on-chain event shape that NO decoder
// recognizes — an event we observe in soroban_events but would
// silently drop on decode. This is what a WASM upgrade that adds a new
// topic looks like (project_schema_evolution), surfaced loudly instead
// of vanishing.
type RecognitionGap struct {
	ContractID string
	Topic0Sym  string // "" when topic[0] is not a Symbol/String
	Count      int64  // events of this shape in the audited range
	MinLedger  uint32
	MaxLedger  uint32
	Reason     string // "no decoder matches" | "unreconstructable: <err>"
}

// AuditRecognition reconstructs a representative event for each
// distinct (contract, topic) shape and returns those the Recognizer
// rejects. An empty result means every event shape present in the
// audited range is handled by some decoder — Claim 2a holds.
//
// A sample that cannot be reconstructed is itself reported as a gap:
// if we can't even rebuild the event from its stored bytes, we
// certainly can't claim to decode it.
func AuditRecognition(samples []timescale.TopicSample, r Recognizer) []RecognitionGap {
	var gaps []RecognitionGap
	for i := range samples {
		s := samples[i]
		ev, err := sorobanevents.Reconstruct(s.Row)
		if err != nil {
			gaps = append(gaps, gapFromSample(s, "unreconstructable: "+err.Error()))
			continue
		}
		if _, ok := r.Recognize(ev); !ok {
			gaps = append(gaps, gapFromSample(s, "no decoder matches"))
		}
	}
	return gaps
}

func gapFromSample(s timescale.TopicSample, reason string) RecognitionGap {
	return RecognitionGap{
		ContractID: s.Row.ContractID,
		Topic0Sym:  s.Row.Topic0Sym,
		Count:      s.Count,
		MinLedger:  s.MinLedger,
		MaxLedger:  s.MaxLedger,
		Reason:     reason,
	}
}
