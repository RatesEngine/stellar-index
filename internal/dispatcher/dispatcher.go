// Package dispatcher consumes ledger-meta values from
// internal/ledgerstream, extracts Soroban contract events per
// transaction, and routes each event to a decoder registered by
// one of the internal/sources/<venue> packages. Per
// docs/architecture/ingest-pipeline.md this is the SINGLE
// production ingest codepath — every trade / oracle update that
// lands in Timescale goes through Dispatcher.ProcessLedger.
//
// Dispatcher is intentionally small. The decoders carry all
// protocol-specific logic (topic matching, SCVal parsing,
// correlation buffers for swap+sync or 8-field swaps); the
// dispatcher does only three things:
//
//  1. Walk a LedgerCloseMeta through ingest.NewLedgerTransactionReader...
//  2. Call tx.GetTransactionEvents() for each transaction and flatten
//     to a stream of events.Event values.
//  3. Invoke each registered Decoder in order; the first one whose
//     Matches() returns true owns the event.
//
// This means every source-specific concern stays inside the source
// package, and the dispatcher has nothing protocol-specific to
// change when a new source is added.
package dispatcher

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the contract every source package implements to
// participate in dispatch. Adding a new source is one file:
// export a NewDecoder() returning a value that satisfies this
// interface, then register it with the Dispatcher at startup.
//
// Methods are:
//
//   - Name: canonical source name, stamped into metrics +
//     canonical.Trade.Source / canonical.OracleUpdate.Source.
//   - Matches: byte-equality predicate on the event topic. Cheap —
//     avoid SCVal parsing.
//   - Decode: process one event; optionally emit consumer.Event
//     values (Trade / OracleUpdate wrappers). Sources with
//     correlation state (Soroswap swap+sync, Phoenix 8-field) may
//     return no outputs for intermediate events and emit on
//     completion.
//
// Decode's error is non-fatal — the dispatcher counts it (via the
// caller's metrics hook) and moves on to the next event.
type Decoder interface {
	Name() string
	Matches(ev events.Event) bool
	Decode(ev events.Event) ([]consumer.Event, error)
}

// OpDecoder is the contract for decoders that operate on classic
// Stellar operations (ManageOffer, PathPayment, …) rather than
// Soroban contract events. SDEX is the primary user; any future
// classic-path source (e.g. liquidity-pool trades outside Soroban)
// follows the same shape.
//
// One transaction has many operations and each op has its own
// result. The dispatcher passes both in an OpContext so the
// decoder can correlate them without re-walking the envelope.
//
// Same non-fatal-error contract as [Decoder]: Decode returning an
// error is a "skip + count" signal, not "stop dispatching."
type OpDecoder interface {
	Name() string
	// Matches is a cheap predicate on the op (typically checks
	// op.Body.Type). Called before Decode.
	Matches(op xdr.Operation) bool
	// Decode emits zero or more canonical outputs from one op +
	// its result. See OpContext for the per-op fields available.
	Decode(ctx OpContext) ([]consumer.Event, error)
}

// OpContext carries everything an OpDecoder needs to decode one
// classic operation: the op itself, its result, and the tx-level
// metadata (ledger, close time, tx hash, source account). Built by
// the dispatcher during ProcessLedger.
type OpContext struct {
	Ledger   uint32
	ClosedAt time.Time
	TxHash   string
	// TxSource is the strkey G-address of the transaction source
	// account — the account charged the fee, often the one whose
	// offer was placed. Ops can override via their own SourceAccount
	// (see OpSource).
	TxSource string
	// OpSource is the strkey of the per-op source account when the
	// op carries one, otherwise empty (meaning "tx-level source").
	// Classic trades don't strictly need this (the claim atoms
	// identify the counterparty), but it's useful for attribution.
	OpSource string
	OpIndex  int
	Op       xdr.Operation
	OpResult xdr.OperationResult
}

// ContractCallDecoder is the contract for decoders that observe
// Soroban InvokeContract calls *regardless of whether the contract
// emits an event*. The canonical use case is Band's Soroban
// StandardReference: its `relay()` / `force_relay()` methods update
// storage but publish no events (verified
// docs/discovery/oracles/band.md) — a conventional event-based
// Decoder would never run on a Band update. ContractCallDecoder
// observes the InvokeContract op itself, decoding the call's
// arguments as the authoritative payload.
//
// Matching is by (contract_id, function_name) — cheap string
// compares, no SCVal parsing on the hot path. The source package
// supplies the args decoding.
//
// Same non-fatal-error contract as [Decoder] and [OpDecoder]:
// returning an error is a "skip + count" signal, not
// "stop dispatching."
type ContractCallDecoder interface {
	Name() string
	// Matches reports whether this decoder owns the given call.
	// contractID is the C-strkey of the invoked contract;
	// functionName is the Symbol the caller targets (e.g. "relay").
	Matches(contractID, functionName string) bool
	// Decode emits zero or more canonical outputs from one
	// InvokeContract call. See ContractCallContext for the fields
	// available at decode time.
	Decode(ctx ContractCallContext) ([]consumer.Event, error)
}

// ContractCallContext carries everything a ContractCallDecoder
// needs to decode one Soroban InvokeContract call: identity of the
// contract + function, base64-encoded argument slice, and tx-level
// metadata. Built by the dispatcher during ProcessLedger for every
// successful InvokeContract op.
//
// Args are base64-encoded SCVal blobs — same format as
// events.Event.OpArgs / events.Event.Topic — so decoders use
// internal/scval.Parse to unwrap.
type ContractCallContext struct {
	Ledger       uint32
	ClosedAt     time.Time
	TxHash       string
	TxSource     string
	OpSource     string
	OpIndex      int
	ContractID   string // C-strkey of invoked contract
	FunctionName string // Symbol the caller invoked
	Args         []string
}

// Dispatcher owns the registered decoders. Construct with New(),
// register exactly once at startup, then call ProcessLedger per
// xdr.LedgerCloseMeta delivered by internal/ledgerstream.
//
// Not safe for concurrent ProcessLedger calls — caller should
// serialize. (The ledgerstream callback model naturally
// serializes, so this is the intended usage.)
type Dispatcher struct {
	decoders             []Decoder
	opDecoders           []OpDecoder
	contractCallDecoders []ContractCallDecoder

	// Error counters — read via Stats(). Production wiring in
	// cmd/ratesengine-indexer increments obs.SourceDecodeErrorsTotal
	// per source name on decode failures; internal counters here are
	// for test assertions.
	decodeErrors  map[string]int
	unmatchedHits int
}

// New constructs a Dispatcher with the given Soroban-event
// decoders. Registration order determines first-match precedence
// — earlier wins. Classic-op decoders register via AddOpDecoder
// after construction.
func New(decoders ...Decoder) *Dispatcher {
	return &Dispatcher{
		decoders:     decoders,
		decodeErrors: map[string]int{},
	}
}

// AddOpDecoder registers a classic-operation decoder. Use for
// SDEX and any future non-Soroban path. Called once at startup;
// not safe concurrent with ProcessLedger.
func (d *Dispatcher) AddOpDecoder(od OpDecoder) {
	d.opDecoders = append(d.opDecoders, od)
}

// AddContractCallDecoder registers a decoder that observes Soroban
// InvokeContract calls directly (i.e. bypasses events). Required
// for sources that don't emit on-chain events — Band's Soroban
// StandardReference is the canonical case. Registration order
// determines first-match precedence.
func (d *Dispatcher) AddContractCallDecoder(ccd ContractCallDecoder) {
	d.contractCallDecoders = append(d.contractCallDecoders, ccd)
}

// Stats is a snapshot of the dispatcher's internal counters. Keyed
// by source name for decode errors; includes an "unmatched" total
// for events no decoder claimed. Zero-copy read — caller should
// treat as immutable.
type Stats struct {
	DecodeErrors  map[string]int
	UnmatchedHits int
}

func (d *Dispatcher) Stats() Stats {
	copied := make(map[string]int, len(d.decodeErrors))
	for k, v := range d.decodeErrors {
		copied[k] = v
	}
	return Stats{DecodeErrors: copied, UnmatchedHits: d.unmatchedHits}
}

// ProcessLedger walks lcm's transactions, extracts Soroban events,
// and routes each one to the matching decoder. Returns the
// collected outputs across all events in the ledger.
//
// passphrase must match the network the ledger came from
// (mainnet / testnet). The SDK uses it to compute transaction
// hashes during iteration.
//
// Errors:
//   - A failure to construct the transaction reader (bad LCM)
//     returns an error immediately.
//   - Per-transaction read errors are skipped with an internal
//     counter bump (future: promoted to obs metric when wired).
//   - Per-event decode errors are skipped; the caller sees a
//     successful return with fewer outputs.
//
// Caller controls goroutine placement. This function blocks until
// the ledger is fully processed.
func (d *Dispatcher) ProcessLedger(lcm xdr.LedgerCloseMeta, passphrase string) ([]consumer.Event, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	reader, err := ingest.NewLedgerTransactionReaderFromLedgerCloseMeta(passphrase, lcm)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: build reader for ledger %d: %w",
			lcm.LedgerSequence(), err)
	}
	defer func() { _ = reader.Close() }()

	ledgerSeq := lcm.LedgerSequence()
	// ClosedAt as RFC 3339 so the events.Event JSON shape matches
	// stellar-rpc's getEvents response exactly — decoders that parse
	// it (events.Event.EventClosedAt) work without transport-
	// awareness.
	closedAt := lcm.ClosedAt().UTC().Format(time.RFC3339)

	var outputs []consumer.Event
	for {
		tx, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Skip the transaction but keep going; one malformed tx
			// should not abort the whole ledger.
			continue
		}
		if !tx.Result.Successful() {
			// Failed transactions don't produce real price signal.
			// stellar-extract/trades.go does the same.
			continue
		}

		txHash := hex.EncodeToString(tx.Result.TransactionHash[:])

		// ─── Soroban InvokeContract calls (once per tx) ──────
		// Walk operations once, build an invokeCalls slice keyed by
		// opIdx. This powers three downstream consumers:
		//   1. events.Event.OpArgs for event-path decoders that
		//      need the tx's args (Redstone).
		//   2. ContractCallDecoder routing (Band and any future
		//      source that doesn't emit events).
		//   3. No-op for non-InvokeContract ops (classic, wasm
		//      upload, etc.) — the slot is nil.
		invokeCalls := extractInvokeContractCalls(tx.Envelope.Operations())
		txSource, _ := accountIDToStrkey(tx.Envelope.SourceAccount().ToAccountId())
		parsedClosedAt := mustParseRFC3339(closedAt)
		ops := tx.Envelope.Operations()

		// ─── Soroban contract events ─────────────────────────
		if txEvents, err := tx.GetTransactionEvents(); err == nil && len(txEvents.OperationEvents) > 0 {
			for opIdx, opEvents := range txEvents.OperationEvents {
				var args []string
				if opIdx < len(invokeCalls) && invokeCalls[opIdx] != nil {
					args = invokeCalls[opIdx].Args
				}
				for _, ce := range opEvents {
					ev := contractEventToEventsEvent(ce, ledgerSeq, txHash, opIdx, closedAt, args)
					if ev == nil {
						continue
					}
					outs, err := d.dispatchOne(*ev)
					if err != nil {
						continue
					}
					outputs = append(outputs, outs...)
				}
			}
		}

		// ─── Soroban InvokeContract call routing ─────────────
		// Runs for every successful InvokeContract op, whether
		// or not it emitted events. Band-style sources live here.
		if len(d.contractCallDecoders) > 0 {
			for opIdx, call := range invokeCalls {
				if call == nil {
					continue
				}
				opSource := ""
				if opIdx < len(ops) && ops[opIdx].SourceAccount != nil {
					opSource, _ = accountIDToStrkey(ops[opIdx].SourceAccount.ToAccountId())
				}
				ccCtx := ContractCallContext{
					Ledger:       ledgerSeq,
					ClosedAt:     parsedClosedAt,
					TxHash:       txHash,
					TxSource:     txSource,
					OpSource:     opSource,
					OpIndex:      opIdx,
					ContractID:   call.ContractID,
					FunctionName: call.FunctionName,
					Args:         call.Args,
				}
				outs, err := d.dispatchContractCall(ccCtx)
				if err != nil {
					continue
				}
				outputs = append(outputs, outs...)
			}
		}

		// ─── Classic operations (SDEX and friends) ───────────
		if len(d.opDecoders) == 0 {
			continue // skip op walking if no classic decoders registered
		}
		opResults, haveResults := tx.Result.Result.OperationResults()
		if !haveResults {
			continue
		}

		for opIdx, op := range ops {
			if opIdx >= len(opResults) {
				break
			}
			opSource := ""
			if op.SourceAccount != nil {
				opSource, _ = accountIDToStrkey(op.SourceAccount.ToAccountId())
			}
			opCtx := OpContext{
				Ledger:   ledgerSeq,
				ClosedAt: parsedClosedAt,
				TxHash:   txHash,
				TxSource: txSource,
				OpSource: opSource,
				OpIndex:  opIdx,
				Op:       op,
				OpResult: opResults[opIdx],
			}
			outs, err := d.dispatchOp(opCtx)
			if err != nil {
				continue
			}
			outputs = append(outputs, outs...)
		}
	}
	return outputs, nil
}

// dispatchContractCall runs one InvokeContract op through the
// contract-call decoder chain. First matching decoder owns it.
func (d *Dispatcher) dispatchContractCall(ctx ContractCallContext) ([]consumer.Event, error) {
	for _, ccd := range d.contractCallDecoders {
		if !ccd.Matches(ctx.ContractID, ctx.FunctionName) {
			continue
		}
		outs, err := ccd.Decode(ctx)
		if err != nil {
			d.decodeErrors[ccd.Name()]++
			return nil, err
		}
		return outs, nil
	}
	return nil, nil
}

// RouteContractCall is the test-harness entry point for
// contract-call decoders, symmetric with Route / RouteOp.
func (d *Dispatcher) RouteContractCall(ctx ContractCallContext) ([]consumer.Event, error) {
	return d.dispatchContractCall(ctx)
}

// dispatchOp runs one operation through the op-decoder chain.
func (d *Dispatcher) dispatchOp(ctx OpContext) ([]consumer.Event, error) {
	for _, od := range d.opDecoders {
		if !od.Matches(ctx.Op) {
			continue
		}
		outs, err := od.Decode(ctx)
		if err != nil {
			d.decodeErrors[od.Name()]++
			return nil, err
		}
		return outs, nil
	}
	return nil, nil
}

// RouteOp is the test-harness entry point for classic-op
// decoders, symmetric with Route for Soroban events.
func (d *Dispatcher) RouteOp(ctx OpContext) ([]consumer.Event, error) {
	return d.dispatchOp(ctx)
}

// Route feeds one event through the Matches/Decode chain and
// returns the emitted consumer.Events. Returns an error only when
// the matching decoder fails Decode — mismatch is silent (events
// no decoder claimed are counted in Stats().UnmatchedHits and
// return (nil, nil)).
//
// Exposed for test-harness and fixture-replay use; ProcessLedger
// calls Route internally for every event it extracts.
func (d *Dispatcher) Route(ev events.Event) ([]consumer.Event, error) {
	return d.dispatchOne(ev)
}

// dispatchOne runs one event through the Matches/Decode chain and
// returns outputs. Returns an error only when the matching decoder
// fails Decode — mismatch is silent (events not claimed by any
// decoder are counted and dropped).
func (d *Dispatcher) dispatchOne(ev events.Event) ([]consumer.Event, error) {
	for _, dec := range d.decoders {
		if !dec.Matches(ev) {
			continue
		}
		outs, err := dec.Decode(ev)
		if err != nil {
			d.decodeErrors[dec.Name()]++
			return nil, err
		}
		return outs, nil
	}
	d.unmatchedHits++
	return nil, nil
}

// contractEventToEventsEvent flattens an xdr.ContractEvent into
// our transport-neutral events.Event. Returns nil for non-contract
// events (e.g. diagnostic) — the decoders never match on those, so
// we drop them before routing rather than handing them through.
//
// opArgs carries the base64-encoded SCVal arguments of the
// InvokeContract call that produced this op's events, if any; left
// empty for non-InvokeContract ops.
func contractEventToEventsEvent(ce xdr.ContractEvent, ledgerSeq uint32, txHash string, opIdx int, closedAt string, opArgs []string) *events.Event {
	if ce.Type != xdr.ContractEventTypeContract {
		return nil
	}
	if ce.ContractId == nil {
		return nil
	}
	// Topic + Value are ScVals — base64 them so the events.Event
	// format matches stellar-rpc getEvents output byte-for-byte.
	// Decoders byte-equality-match on these, so any deviation from
	// the RPC shape would silently break topic routing.
	body := ce.Body
	if body.V != 0 {
		// Only V=0 is currently defined; anything else is a protocol
		// bump we haven't audited.
		return nil
	}
	v0, ok := body.GetV0()
	if !ok {
		return nil
	}

	topic := make([]string, 0, len(v0.Topics))
	for i := range v0.Topics {
		raw, err := v0.Topics[i].MarshalBinary()
		if err != nil {
			return nil
		}
		topic = append(topic, base64.StdEncoding.EncodeToString(raw))
	}
	rawVal, err := v0.Data.MarshalBinary()
	if err != nil {
		return nil
	}

	contractID, err := contractIDToStrkey(*ce.ContractId)
	if err != nil {
		return nil
	}

	return &events.Event{
		Type:                     "contract",
		Ledger:                   ledgerSeq,
		LedgerClosedAt:           closedAt,
		ContractID:               contractID,
		OperationIndex:           opIdx,
		TxHash:                   txHash,
		InSuccessfulContractCall: true,
		Topic:                    topic,
		Value:                    base64.StdEncoding.EncodeToString(rawVal),
		OpArgs:                   opArgs,
	}
}

// invokeCall is the per-op snapshot of a Soroban InvokeContract
// call. Contract ID is a C-strkey, function name is the raw
// Symbol string, args are base64-encoded SCVal blobs matching
// the events.Event.OpArgs wire format.
type invokeCall struct {
	ContractID   string
	FunctionName string
	Args         []string
}

// extractInvokeContractCalls returns, per operation, the full
// invokeCall snapshot when the op is an InvokeHostFunction invoking
// a contract; nil otherwise. Result is indexed parallel to ops —
// ops[i] → result[i]. Non-InvokeContract ops (wasm upload, create
// contract, classic ops) yield a nil slot.
//
// Called once per tx by ProcessLedger. Fuels both the event-path
// OpArgs enrichment and the ContractCallDecoder routing, so doing
// the XDR walk here saves duplicate work.
func extractInvokeContractCalls(ops []xdr.Operation) []*invokeCall { //nolint:gocognit // dispatch-heavy; splitting would reduce linearity
	if len(ops) == 0 {
		return nil
	}
	out := make([]*invokeCall, len(ops))
	for i, op := range ops {
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
			continue
		}
		ihf, ok := op.Body.GetInvokeHostFunctionOp()
		if !ok {
			continue
		}
		if ihf.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
			continue
		}
		ic, ok := ihf.HostFunction.GetInvokeContract()
		if !ok {
			continue
		}
		// ContractAddress may be account-typed in rare composed
		// calls; Band's real target is always a ScAddressTypeContract.
		// accountIDToStrkey / contract-address strkey are handled by
		// separate helpers; we mirror dispatcher.contractIDToStrkey
		// here via the address-kind switch for safety.
		contractStrkey := ""
		switch ic.ContractAddress.Type {
		case xdr.ScAddressTypeScAddressTypeContract:
			cid := ic.ContractAddress.MustContractId()
			if s, err := contractIDToStrkey(cid); err == nil {
				contractStrkey = s
			}
		case xdr.ScAddressTypeScAddressTypeAccount:
			// InvokeContract against an account address is invalid at
			// the protocol level, but we defensively skip rather than
			// emitting a malformed strkey.
			continue
		}
		if contractStrkey == "" {
			continue
		}
		args := make([]string, 0, len(ic.Args))
		argsOK := true
		for j := range ic.Args {
			raw, err := ic.Args[j].MarshalBinary()
			if err != nil {
				// Marshal failure on a locally-sourced ScVal means a
				// broken envelope or SDK drift. Surface as "no args";
				// decoders that require them (Redstone, Band) will
				// surface their own error and skip.
				argsOK = false
				break
			}
			args = append(args, base64.StdEncoding.EncodeToString(raw))
		}
		if !argsOK {
			args = nil
		}
		out[i] = &invokeCall{
			ContractID:   contractStrkey,
			FunctionName: string(ic.FunctionName),
			Args:         args,
		}
	}
	return out
}

// contractIDToStrkey encodes a 32-byte ContractId into its C-strkey
// form (56 chars). SDK's strkey package owns the canonical encoding.
func contractIDToStrkey(cid xdr.ContractId) (string, error) {
	return strkey.Encode(strkey.VersionByteContract, cid[:])
}

// accountIDToStrkey encodes an xdr.AccountId to its G-strkey form.
// Returns the empty string if the account isn't an Ed25519 — classic
// Stellar accounts always are, but muxed shapes can surprise us.
func accountIDToStrkey(aid xdr.AccountId) (string, error) {
	if aid.Type != xdr.PublicKeyTypePublicKeyTypeEd25519 {
		return "", fmt.Errorf("accountIDToStrkey: unsupported account type %d", aid.Type)
	}
	pub := aid.Ed25519
	return strkey.Encode(strkey.VersionByteAccountID, pub[:])
}

// mustParseRFC3339 parses a closedAt string; panics on malformed
// input because the dispatcher itself formatted it upstream. Any
// failure here is a programming bug, not runtime input.
func mustParseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("dispatcher: malformed closedAt (self-generated): " + err.Error())
	}
	return t
}
