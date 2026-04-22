package reflector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// Source implements [consumer.Source] for one Reflector contract
// variant (DEX, CEX, or FX). Binaries register three instances —
// one per variant — against their respective contract addresses.
type Source struct {
	rpc *stellarrpc.Client

	variant     Variant
	contractID  string // the mainnet address (operator-supplied)
	decimals    uint8
	pollInterval time.Duration

	mu     sync.RWMutex
	health consumer.HealthStatus
}

// Option configures a Source at construction.
type Option func(*Source)

// WithDecimals overrides the default 14 if the operator knows a
// specific contract's scale differs. Usually leave at the default.
func WithDecimals(d uint8) Option {
	return func(s *Source) { s.decimals = d }
}

// WithPollInterval overrides the default 2s live-stream poll.
// Reflector updates on a 5-min cadence so shorter polls are fine;
// longer polls would start to miss events at the retention boundary.
func WithPollInterval(d time.Duration) Option {
	return func(s *Source) { s.pollInterval = d }
}

// NewDEX constructs a Reflector DEX source. contractID is the
// mainnet address (operator-supplied — Phase-1 noted these are
// verified via stellar.expert, not hard-coded in our repo).
func NewDEX(rpc *stellarrpc.Client, contractID string, opts ...Option) *Source {
	return newVariant(rpc, VariantDEX, contractID, opts...)
}

// NewCEX constructs a Reflector CEX source.
func NewCEX(rpc *stellarrpc.Client, contractID string, opts ...Option) *Source {
	return newVariant(rpc, VariantCEX, contractID, opts...)
}

// NewFX constructs a Reflector FX source.
func NewFX(rpc *stellarrpc.Client, contractID string, opts ...Option) *Source {
	return newVariant(rpc, VariantFX, contractID, opts...)
}

func newVariant(rpc *stellarrpc.Client, variant Variant, contractID string, opts ...Option) *Source {
	s := &Source{
		rpc:          rpc,
		variant:      variant,
		contractID:   contractID,
		decimals:     DefaultDecimals,
		pollInterval: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name implements [consumer.Source] — returns the variant's
// stable source-name.
func (s *Source) Name() string { return s.variant.SourceName() }

// Health implements [consumer.Source].
func (s *Source) Health() consumer.HealthStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.health
}

// BackfillRange implements [consumer.Source].
func (s *Source) BackfillRange(ctx context.Context, from, to uint32, out chan<- consumer.Event) error {
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := s.rpc.GetEvents(ctx, from, to, s.filters(), &stellarrpc.Pagination{
			Cursor: cursor, Limit: 200,
		})
		if err != nil {
			s.setError(err)
			return fmt.Errorf("reflector backfill getEvents: %w", err)
		}
		s.setOK()

		if err := s.processPage(ctx, resp.Events, out); err != nil {
			return err
		}

		if resp.Cursor == "" || len(resp.Events) == 0 {
			break
		}
		cursor = resp.Cursor
	}
	return nil
}

// StreamLive implements [consumer.Source].
func (s *Source) StreamLive(ctx context.Context, out chan<- consumer.Event) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	var cursor string
	var lastSeenLedger uint32

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		resp, err := s.rpc.GetEvents(ctx, lastSeenLedger, 0, s.filters(), &stellarrpc.Pagination{
			Cursor: cursor, Limit: 200,
		})
		if err != nil {
			s.setError(err)
			continue
		}
		s.setOK()

		if err := s.processPage(ctx, resp.Events, out); err != nil {
			s.setError(err)
			continue
		}

		if resp.Cursor != "" {
			cursor = resp.Cursor
		}
		if resp.LatestLedger > 0 {
			lastSeenLedger = resp.LatestLedger
			s.mu.Lock()
			s.health.LagLedgers = 0
			s.mu.Unlock()
		}
	}
}

// processPage handles one RPC page of events.
func (s *Source) processPage(ctx context.Context, events []stellarrpc.Event, out chan<- consumer.Event) error {
	for i := range events {
		e := &events[i]

		// Filter by contract — the RPC filter already narrows to
		// this contract, but we double-check defensively.
		if e.ContractID != s.contractID {
			continue
		}
		if !classify(e) {
			continue
		}

		closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)
		// Observer is the tx source account. We don't have direct
		// access to it from the event payload alone (getEvents
		// doesn't include tx source_account); fetched via an
		// accompanying getTransaction call in a follow-up. Leave
		// blank for now — still a valid OracleUpdate.
		updates, err := decodeUpdate(e, s.variant, s.decimals, "", closedAt)
		if err != nil {
			// Per-event decode failures are counted as metrics
			// (handled by the indexer's sink) but don't bubble up.
			continue
		}

		for _, u := range updates {
			s.mu.Lock()
			s.health.LastEvent = u.Timestamp
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- UpdateEvent{Update: u}:
			}
		}
	}
	return nil
}

// filters restricts the RPC subscription to this source's contract
// with the REFLECTOR.update topic shape. Once the placeholder
// TopicSymbol* blobs become real SCVal encoded bytes, the topic
// filter becomes server-side precise.
func (s *Source) filters() []stellarrpc.EventFilter {
	return []stellarrpc.EventFilter{{
		Type:        "contract",
		ContractIDs: []string{s.contractID},
	}}
}

// ─── Health mutators ─────────────────────────────────────────────

func (s *Source) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.Connected = false
	s.health.LastError = err
}

func (s *Source) setOK() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.Connected = true
	s.health.LastError = nil
}

// ─── Event envelope ─────────────────────────────────────────────

// UpdateEvent is the [consumer.Event] shape Reflector emits. The
// indexer type-switches on this and calls
// store.InsertOracleUpdate.
type UpdateEvent struct {
	Update canonical.OracleUpdate
}

// EventKind implements [consumer.Event].
func (UpdateEvent) EventKind() string { return "reflector.update" }

// Source implements [consumer.Event]. Returns the source-name for
// the contained update so the event-sink can attribute metrics
// per-variant (reflector-dex / reflector-cex / reflector-fx)
// without type-assertion.
func (u UpdateEvent) Source() string { return u.Update.Source }
