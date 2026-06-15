// Package blend_backstop decodes Blend's Backstop contract events on
// Stellar (Soroban) — a SEPARATE event surface from the Blend pool /
// pool-factory decoder (internal/sources/blend). Do NOT fold this
// into that package; the two share neither contract addresses nor
// event vocabulary.
//
// The backstop is the protocol's insurance / shared-liquidity module:
// depositors stake the backstop token (BLND:USDC LP) into a per-pool
// backstop, earn emissions, and absorb bad debt via draw/donate. 10
// event types (topic[0] = Symbol):
//
//	deposit            — stake into a pool's backstop
//	claim              — claim accrued emissions
//	donate             — donate tokens to a pool's backstop
//	queue_withdrawal   — queue an unstake (with expiration)
//	withdraw           — execute a queued unstake
//	distribute         — distribute emissions across backstops
//	gulp_emissions     — pull emissions for a token
//	dequeue_withdrawal — cancel a queued unstake
//	draw               — draw backstop funds to cover bad debt
//	rw_zone_add        — add a pool to the reward zone
//
// SCHEMA PROVENANCE: the per-event field layouts here were
// REVERSE-ENGINEERED from real mainnet lake samples (2026-06-15) and
// validated against golden frames in decode_test.go. They are pending
// Blend-team confirmation. This source is LIVE-CAPTURE ONLY until then
// — see events.go + README.md §Provenance.
//
// Per ADR-0013 this decoder reads SCVal exclusively through
// internal/scval — it never imports go-stellar-sdk/xdr directly
// (enforced by scripts/ci/lint-imports.sh).
//
// Wiring: decode.go decodes; consumer.go projects each event into the
// canonical blend_backstop.Event row; dispatcher_adapter.go is the
// dispatcher Decoder; the sink persists via
// Store.InsertBlendBackstopEvent into blend_backstop_events
// (migration 0063). See README.md §Wiring.
package blend_backstop

import (
	"errors"
	"fmt"

	"github.com/StellarIndex/stellar-index/internal/events"
	"github.com/StellarIndex/stellar-index/internal/scval"
)

// ErrUnknownEvent flags an event whose topic[0] symbol isn't one of
// the backstop's ten. Defensive — Classify already gates, and the
// dispatcher's Matches gates on contract id too.
var ErrUnknownEvent = errors.New("blend_backstop: unknown event topic")

// ErrMalformedTopic flags a topic slice shorter than the event type
// requires (a genuinely malformed event — counted + skipped by the
// dispatcher).
var ErrMalformedTopic = errors.New("blend_backstop: malformed event topics")

// ErrMalformedBody surfaces a body whose SCVal shape doesn't match the
// event type at all (e.g. neither i128 nor a Vec where the schema
// requires one). Distinct from a single promoted-field shape
// mismatch, which degrades gracefully into Attributes rather than
// erroring the whole row.
var ErrMalformedBody = errors.New("blend_backstop: malformed event body")

// errOrShort returns vecErr when the Vec parse itself failed, else a
// "too short" error describing the element count (every backstop Vec
// body needs at least 2 elements). It guarantees a non-nil error so
// callers can wrap it with %w (errorlint) — the AsVec path returns
// nil-error-but-short-slice, which would otherwise leave the wrap verb
// with a nil to format.
func errOrShort(vecErr error, got int) error {
	if vecErr != nil {
		return vecErr
	}
	return fmt.Errorf("got %d elements, want >= 2", got)
}

// Classify reports which backstop event the given Event is, or empty
// string if topic[0] doesn't match. Contract-ID filtering happens
// DOWNSTREAM (Matches) — these symbols overlap with Blend POOL events,
// so Classify alone never decides a backstop match.
func Classify(e *events.Event) string {
	if len(e.Topic) < 1 {
		return ""
	}
	switch e.Topic[0] {
	case TopicSymbolDeposit:
		return EventDeposit
	case TopicSymbolClaim:
		return EventClaim
	case TopicSymbolDonate:
		return EventDonate
	case TopicSymbolQueueWithdrawal:
		return EventQueueWithdrawal
	case TopicSymbolWithdraw:
		return EventWithdraw
	case TopicSymbolDistribute:
		return EventDistribute
	case TopicSymbolGulpEmissions:
		return EventGulpEmissions
	case TopicSymbolDequeueWithdrawal:
		return EventDequeueWithdrawal
	case TopicSymbolDraw:
		return EventDraw
	case TopicSymbolRwZoneAdd:
		return EventRwZoneAdd
	}
	return ""
}

// decoded is the intermediate shape Decode* helpers fill — the
// promoted columns plus the per-kind Attributes remainder. consumer.go
// stamps the universal identity fields on top.
type decoded struct {
	Pool        string
	UserAddress string
	Amount      string
	Amount2     string
	Attributes  map[string]any
}

// parseTopicAddr parses topic[i] as an Address strkey. A genuine
// malformed topic (wrong SCVal kind / bad checksum) is an error — a
// promoted address that the schema REQUIRES must be present.
func parseTopicAddr(e *events.Event, i int, field string) (string, error) {
	sv, err := scval.Parse(e.Topic[i])
	if err != nil {
		return "", fmt.Errorf("blend_backstop: %s topic[%d] parse: %w", field, i, err)
	}
	addr, err := scval.AsAddressStrkey(sv)
	if err != nil {
		return "", fmt.Errorf("blend_backstop: %s address: %w", field, err)
	}
	return addr, nil
}

// i128String decodes a parsed i128 SCVal (already through scval.Parse)
// from a base64 body string to its decimal string.
func i128String(b64 string) (string, error) {
	sv, err := scval.Parse(b64)
	if err != nil {
		return "", err
	}
	amt, err := scval.AsAmountFromI128(sv)
	if err != nil {
		return "", err
	}
	return amt.String(), nil
}

// twoI128 reads a Vec[i128, i128] body and returns both as decimal
// strings. The two-amount shape recurs (deposit / withdraw /
// gulp_emissions), so it is shared. A body that is not a 2-element Vec
// of i128s is a genuinely malformed event.
//
// Note: the Vec elements are kept in a `:=`-inferred local and fed
// straight back into scval.As* — this file never NAMES the xdr type,
// per ADR-0013 / lint-imports B/xdr-scoped-to-scval. Same convention
// as internal/sources/defindex/decode.go.
func twoI128(e *events.Event, kind string) (a, b string, err error) {
	body, perr := scval.Parse(e.Value)
	if perr != nil {
		return "", "", fmt.Errorf("blend_backstop: %s body parse: %w", kind, perr)
	}
	vec, verr := scval.AsVec(body)
	if verr != nil || len(vec) < 2 {
		return "", "", fmt.Errorf("%w: %s body not a 2-Vec: %w", ErrMalformedBody, kind, errOrShort(verr, len(vec)))
	}
	av, aerr := scval.AsAmountFromI128(vec[0])
	if aerr != nil {
		return "", "", fmt.Errorf("%w: %s amount[0]: %w", ErrMalformedBody, kind, aerr)
	}
	bv, berr := scval.AsAmountFromI128(vec[1])
	if berr != nil {
		return "", "", fmt.Errorf("%w: %s amount[1]: %w", ErrMalformedBody, kind, berr)
	}
	return av.String(), bv.String(), nil
}

// ─── per-event decoders ──────────────────────────────────────────

// decodeDeposit: topics=[sym, pool, user]; data=Vec[i128 amount, i128 shares].
func decodeDeposit(e *events.Event) (decoded, error) {
	if len(e.Topic) < 3 {
		return decoded{}, fmt.Errorf("%w: deposit needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "deposit")
	if err != nil {
		return decoded{}, err
	}
	user, err := parseTopicAddr(e, 2, "deposit")
	if err != nil {
		return decoded{}, err
	}
	amount, shares, err := twoI128(e, "deposit")
	if err != nil {
		return decoded{}, err
	}
	return decoded{Pool: pool, UserAddress: user, Amount: amount, Amount2: shares, Attributes: map[string]any{}}, nil
}

// decodeClaim: topics=[sym, user]; data=i128 amount. NO pool.
func decodeClaim(e *events.Event) (decoded, error) {
	if len(e.Topic) < 2 {
		return decoded{}, fmt.Errorf("%w: claim needs 2 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	user, err := parseTopicAddr(e, 1, "claim")
	if err != nil {
		return decoded{}, err
	}
	amount, err := i128String(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("%w: claim amount: %w", ErrMalformedBody, err)
	}
	return decoded{UserAddress: user, Amount: amount, Attributes: map[string]any{}}, nil
}

// decodeDonate: topics=[sym, pool, from(contract)]; data=i128 amount.
// pool + amount promoted; from stashed in attributes.
func decodeDonate(e *events.Event) (decoded, error) {
	if len(e.Topic) < 3 {
		return decoded{}, fmt.Errorf("%w: donate needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "donate")
	if err != nil {
		return decoded{}, err
	}
	amount, err := i128String(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("%w: donate amount: %w", ErrMalformedBody, err)
	}
	attrs := map[string]any{}
	// `from` is a promoted-into-attributes field; a shape mismatch
	// degrades (note it) rather than erroring the whole row.
	if from, ferr := parseTopicAddr(e, 2, "donate"); ferr == nil {
		attrs["from"] = from
	} else {
		attrs["from_error"] = ferr.Error()
	}
	return decoded{Pool: pool, Amount: amount, Attributes: attrs}, nil
}

// decodeQueueWithdrawal: topics=[sym, pool, user];
// data=Vec[i128 shares, u64 expiration].
func decodeQueueWithdrawal(e *events.Event) (decoded, error) {
	if len(e.Topic) < 3 {
		return decoded{}, fmt.Errorf("%w: queue_withdrawal needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "queue_withdrawal")
	if err != nil {
		return decoded{}, err
	}
	user, err := parseTopicAddr(e, 2, "queue_withdrawal")
	if err != nil {
		return decoded{}, err
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("blend_backstop: queue_withdrawal body parse: %w", err)
	}
	vec, err := scval.AsVec(body)
	if err != nil || len(vec) < 2 {
		return decoded{}, fmt.Errorf("%w: queue_withdrawal body not a 2-Vec: %w", ErrMalformedBody, errOrShort(err, len(vec)))
	}
	shares, err := scval.AsAmountFromI128(vec[0])
	if err != nil {
		return decoded{}, fmt.Errorf("%w: queue_withdrawal shares: %w", ErrMalformedBody, err)
	}
	attrs := map[string]any{}
	if exp, eerr := scval.AsU64(vec[1]); eerr == nil {
		attrs["expiration"] = exp
	} else {
		attrs["expiration_error"] = eerr.Error()
	}
	return decoded{Pool: pool, UserAddress: user, Amount: shares.String(), Attributes: attrs}, nil
}

// decodeWithdraw: topics=[sym, pool, user];
// data=Vec[i128 amount, i128 shares].
func decodeWithdraw(e *events.Event) (decoded, error) {
	if len(e.Topic) < 3 {
		return decoded{}, fmt.Errorf("%w: withdraw needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "withdraw")
	if err != nil {
		return decoded{}, err
	}
	user, err := parseTopicAddr(e, 2, "withdraw")
	if err != nil {
		return decoded{}, err
	}
	amount, shares, err := twoI128(e, "withdraw")
	if err != nil {
		return decoded{}, err
	}
	return decoded{Pool: pool, UserAddress: user, Amount: amount, Amount2: shares, Attributes: map[string]any{}}, nil
}

// decodeDistribute: topics=[sym]; data=i128 amount. amount only.
func decodeDistribute(e *events.Event) (decoded, error) {
	amount, err := i128String(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("%w: distribute amount: %w", ErrMalformedBody, err)
	}
	return decoded{Amount: amount, Attributes: map[string]any{}}, nil
}

// decodeGulpEmissions: topics=[sym, token(contract)];
// data=Vec[i128, i128]. token stashed; amount=data[0], amount2=data[1].
func decodeGulpEmissions(e *events.Event) (decoded, error) {
	if len(e.Topic) < 2 {
		return decoded{}, fmt.Errorf("%w: gulp_emissions needs 2 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	amount, amount2, err := twoI128(e, "gulp_emissions")
	if err != nil {
		return decoded{}, err
	}
	attrs := map[string]any{}
	if token, terr := parseTopicAddr(e, 1, "gulp_emissions"); terr == nil {
		attrs["token"] = token
	} else {
		attrs["token_error"] = terr.Error()
	}
	return decoded{Amount: amount, Amount2: amount2, Attributes: attrs}, nil
}

// decodeDequeueWithdrawal: topics=[sym, pool, user]; data=i128 amount.
func decodeDequeueWithdrawal(e *events.Event) (decoded, error) {
	if len(e.Topic) < 3 {
		return decoded{}, fmt.Errorf("%w: dequeue_withdrawal needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "dequeue_withdrawal")
	if err != nil {
		return decoded{}, err
	}
	user, err := parseTopicAddr(e, 2, "dequeue_withdrawal")
	if err != nil {
		return decoded{}, err
	}
	amount, err := i128String(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("%w: dequeue_withdrawal amount: %w", ErrMalformedBody, err)
	}
	return decoded{Pool: pool, UserAddress: user, Amount: amount, Attributes: map[string]any{}}, nil
}

// decodeDraw: topics=[sym, pool]; data=Vec[Address to, i128 amount].
// pool promoted; to stashed; amount=data[1].
func decodeDraw(e *events.Event) (decoded, error) {
	if len(e.Topic) < 2 {
		return decoded{}, fmt.Errorf("%w: draw needs 2 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}
	pool, err := parseTopicAddr(e, 1, "draw")
	if err != nil {
		return decoded{}, err
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("blend_backstop: draw body parse: %w", err)
	}
	vec, err := scval.AsVec(body)
	if err != nil || len(vec) < 2 {
		return decoded{}, fmt.Errorf("%w: draw body not a 2-Vec: %w", ErrMalformedBody, errOrShort(err, len(vec)))
	}
	amount, err := scval.AsAmountFromI128(vec[1])
	if err != nil {
		return decoded{}, fmt.Errorf("%w: draw amount: %w", ErrMalformedBody, err)
	}
	attrs := map[string]any{}
	if to, terr := scval.AsAddressStrkey(vec[0]); terr == nil {
		attrs["to"] = to
	} else {
		attrs["to_error"] = terr.Error()
	}
	return decoded{Pool: pool, Amount: amount.String(), Attributes: attrs}, nil
}

// decodeRwZoneAdd: topics=[sym]; data=Vec[Address pool, u32 index].
// pool=data[0] promoted; index stashed.
func decodeRwZoneAdd(e *events.Event) (decoded, error) {
	body, err := scval.Parse(e.Value)
	if err != nil {
		return decoded{}, fmt.Errorf("blend_backstop: rw_zone_add body parse: %w", err)
	}
	vec, err := scval.AsVec(body)
	if err != nil || len(vec) < 2 {
		return decoded{}, fmt.Errorf("%w: rw_zone_add body not a 2-Vec: %w", ErrMalformedBody, errOrShort(err, len(vec)))
	}
	pool, err := scval.AsAddressStrkey(vec[0])
	if err != nil {
		return decoded{}, fmt.Errorf("%w: rw_zone_add pool: %w", ErrMalformedBody, err)
	}
	attrs := map[string]any{}
	if idx, ierr := scval.AsU32(vec[1]); ierr == nil {
		attrs["index"] = idx
	} else {
		attrs["index_error"] = ierr.Error()
	}
	return decoded{Pool: pool, Attributes: attrs}, nil
}
