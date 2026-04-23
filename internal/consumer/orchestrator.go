package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/obs"
)

// CursorStore is the subset of the storage API the orchestrator
// needs. Kept narrow so tests can plug in an in-memory fake.
//
// The real implementation is internal/storage/timescale; we define
// the interface here so this package has no hard dep on it.
type CursorStore interface {
	GetCursor(ctx context.Context, source, sub string) (Cursor, error)
	UpsertCursor(ctx context.Context, source, sub string, lastLedger uint32) error
}

// Cursor mirrors [timescale.Cursor] at the interface boundary.
type Cursor struct {
	Source     string
	Sub        string
	LastLedger uint32
	UpdatedAt  time.Time
}

// ErrNoCursor is what CursorStore.GetCursor returns when a source
// has never persisted a cursor. Implementations translate their
// driver-specific not-found error into this.
var ErrNoCursor = errors.New("consumer: no cursor for source")

// Config tunes orchestrator behaviour. Zero-values use the defaults
// documented on each field.
type Config struct {
	// MinBackoff is the initial retry sleep after a source error.
	// Default 1 s.
	MinBackoff time.Duration

	// MaxBackoff caps exponential growth. Default 60 s.
	MaxBackoff time.Duration

	// CursorPersistEvery — the orchestrator checkpoints the cursor
	// this often (not every event). Default every 30 s.
	CursorPersistEvery time.Duration

	// BackfillFromLedger is the default start ledger when a source
	// has no persisted cursor AND the source is "new". Config-
	// supplied; 0 means "start from tip" (live only).
	BackfillFromLedger uint32

	// BackfillBeforeLive: if a persisted cursor is more than this
	// many ledgers behind the source's reported tip, the
	// orchestrator calls BackfillRange first, then StreamLive.
	// Default 1000 (~1.4 h at 5s/ledger).
	BackfillBeforeLiveThreshold uint32
}

func (c *Config) applyDefaults() {
	if c.MinBackoff <= 0 {
		c.MinBackoff = 1 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.CursorPersistEvery <= 0 {
		c.CursorPersistEvery = 30 * time.Second
	}
	if c.BackfillBeforeLiveThreshold == 0 {
		c.BackfillBeforeLiveThreshold = 1000
	}
}

// Orchestrator drives a fleet of [Source] implementations.
// Each Source gets its own goroutine with an independent restart
// loop + cursor.
//
// Events are emitted on the single Events() channel. Consumers
// (indexer main.go) type-switch on the Event concrete type.
type Orchestrator struct {
	cursors CursorStore
	sources []Source
	cfg     Config
	logger  *slog.Logger

	events chan Event
}

// New constructs an Orchestrator. Pass in the CursorStore + sources
// to run + optional Config.
func New(cursors CursorStore, sources []Source, cfg Config, logger *slog.Logger) *Orchestrator {
	cfg.applyDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		cursors: cursors,
		sources: sources,
		cfg:     cfg,
		logger:  logger,
		events:  make(chan Event, 1024),
	}
}

// Events returns the channel every Source emits into. Closed when
// Run returns.
func (o *Orchestrator) Events() <-chan Event { return o.events }

// Run blocks until ctx is cancelled. Spawns one goroutine per
// source, each running [runSource] forever-until-ctx-done.
// Closes the Events channel on return.
func (o *Orchestrator) Run(ctx context.Context) error {
	if len(o.sources) == 0 {
		return fmt.Errorf("orchestrator: no sources registered")
	}

	var wg sync.WaitGroup
	for _, src := range o.sources {
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.runSource(ctx, src)
		}()
	}

	wg.Wait()
	close(o.events)
	return ctx.Err()
}

// runSource is the per-source loop: load cursor, pick mode,
// execute, persist, backoff on error, repeat.
//
// A panic inside the source (nil deref in a decoder, arithmetic
// overflow, etc.) is converted to an error + exponential backoff.
// One misbehaving source MUST NOT kill the whole indexer — the
// recover keeps the other sources running.
func (o *Orchestrator) runSource(ctx context.Context, src Source) {
	log := o.logger.With("source", src.Name())
	backoff := o.cfg.MinBackoff

	// Enabled=1 while this source is in the runSource loop; flipped
	// to 0 on exit. Source-stopped alerts use this to qualify
	// zero-event-rate ("but it was supposed to be running").
	obs.SourceEnabled.WithLabelValues(src.Name()).Set(1)
	defer obs.SourceEnabled.WithLabelValues(src.Name()).Set(0)

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		err := o.runOneSafe(ctx, src, log)
		if err == nil || errors.Is(err, context.Canceled) {
			// Clean exit — source returned without error. Reset backoff.
			backoff = o.cfg.MinBackoff
			continue
		}

		log.Error("source iteration failed",
			"err", err,
			"sleep", backoff.String())
		if !sleepCtx(ctx, jitter(backoff)) {
			return
		}
		backoff = nextBackoff(backoff, o.cfg.MaxBackoff)
	}
}

// runOneSafe is runOne with a recover so panics become errors. The
// restart loop then applies exponential backoff rather than
// crashing the process.
func (o *Orchestrator) runOneSafe(ctx context.Context, src Source, log *slog.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("source panicked — recovered",
				"panic", fmt.Sprintf("%v", r))
			err = fmt.Errorf("orchestrator: %s panicked: %v", src.Name(), r)
		}
	}()
	return o.runOne(ctx, src, log)
}

// runOne runs a single "load cursor → execute → persist" cycle.
// Returns nil on graceful completion of one cycle (StreamLive
// itself can run for hours between returns; this cycle boundary
// is driven by the Source's own error path).
func (o *Orchestrator) runOne(ctx context.Context, src Source, log *slog.Logger) error {
	cursor, err := o.cursors.GetCursor(ctx, src.Name(), "")
	switch {
	case err == nil:
		log.Info("cursor loaded", "last_ledger", cursor.LastLedger)
	case errors.Is(err, ErrNoCursor):
		// The source seeds its own startLedger from tip via
		// rpc.LatestLedgerSequence on StreamLive entry; the
		// cursorPersister floor is still 0 until the source
		// reports its first LastLedger. backfill_from_ledger
		// becomes relevant only once the backfill bootstrap lands.
		log.Info("no cursor — source will seed from network tip",
			"config_backfill_from", o.cfg.BackfillFromLedger)
	default:
		return fmt.Errorf("load cursor: %w", err)
	}

	// Start a cursor persister that watches the source's health +
	// checkpoints LastEvent-derived ledgers periodically. This is
	// the "checkpoint every 30 s" behaviour.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	persistDone := make(chan struct{})
	go func() {
		defer close(persistDone)
		o.cursorPersister(ctx, src, cursor.LastLedger)
	}()

	// For the moment the orchestrator runs StreamLive exclusively.
	// Backfill bootstrap lands in the next revision — it needs to
	// know the source's tip, and we don't yet have a
	// reportedTip() method on Source.
	err = src.StreamLive(ctx, o.events)

	cancel()
	<-persistDone
	return err
}

// cursorPersister is a companion goroutine that periodically upserts
// the cursor derived from the source's Health().LastLedger. On
// source restart we'll resume from this checkpoint (give or take
// the interval, which is why trade inserts are idempotent).
//
// We never persist a cursor that's BEHIND the seed — that would be
// a regression (e.g. source just restarted with LastLedger=0
// because it hasn't processed anything yet). Instead we keep the
// seed as the floor and only advance it.
//
// On ctx cancellation the persister does a FINAL flush before
// returning, so a graceful shutdown never loses the last up-to-
// CursorPersistEvery seconds of advancement. The flush uses a
// fresh context (parent is cancelled) with a short deadline.
func (o *Orchestrator) cursorPersister(ctx context.Context, src Source, seedLastLedger uint32) {
	t := time.NewTicker(o.cfg.CursorPersistEvery)
	defer t.Stop()
	log := o.logger.With("source", src.Name())
	lastPersisted := seedLastLedger

	// flushFinal runs once on exit. Uses a detached context because
	// the parent is typically the shutdown-triggering one.
	defer func() {
		h := src.Health()
		if h.LastLedger > lastPersisted {
			lastPersisted = h.LastLedger
		}
		if lastPersisted == 0 {
			return
		}
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := o.cursors.UpsertCursor(flushCtx, src.Name(), "", lastPersisted); err != nil {
			log.Warn("shutdown cursor flush failed",
				"err", err, "last_ledger", lastPersisted)
			return
		}
		log.Info("shutdown cursor flushed", "last_ledger", lastPersisted)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		h := src.Health()
		// Mirror every health snapshot into Prometheus regardless of
		// Connected — dashboards want to see lag stay high during
		// an outage rather than the metric flatline.
		obs.SourceLagLedgers.WithLabelValues(src.Name()).Set(float64(h.LagLedgers))

		if !h.Connected {
			continue
		}
		// Advance only. A source that hasn't yet observed any event
		// in this session returns LastLedger=0; we don't regress the
		// checkpoint in that case.
		if h.LastLedger > lastPersisted {
			lastPersisted = h.LastLedger
		}
		if lastPersisted == 0 {
			// Nothing to persist yet — fresh deploy, no seed, no
			// observed events. Come back next tick.
			continue
		}
		if err := o.cursors.UpsertCursor(ctx, src.Name(), "", lastPersisted); err != nil {
			log.Warn("persist cursor failed", "err", err)
			continue
		}
		// Mirror the persisted cursor value into Prometheus so the
		// cursor-stuck alert can see it.
		obs.CursorLastLedger.WithLabelValues(src.Name()).Set(float64(lastPersisted))
	}
}

// ─── Helpers ─────────────────────────────────────────────────────

// nextBackoff doubles the interval, capping at max. Pure function.
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// jitter adds ±25% randomness to a duration to prevent thundering-
// herd reconnects after a shared outage.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	factor := 0.75 + rand.Float64()*0.5 // 0.75 … 1.25
	return time.Duration(float64(d) * factor)
}

// sleepCtx sleeps for d or until ctx is done. Returns true if it
// slept fully, false if ctx cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
