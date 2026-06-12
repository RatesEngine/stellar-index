package blend

import (
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// ─── Topic-arity expectations ──────────────────────────────────
//
// Per pool/src/events.rs at blend-contracts-v2 commit
// c19abee5b9be4f49e0cda9057e87d343e5dcc095:
//
//	supply / withdraw / supply_collateral / withdraw_collateral /
//	borrow / repay:                3 topics  [Symbol, asset, from]
//	flash_loan:                    4 topics  [Symbol, asset, from, contract]
//	gulp:                          2 topics  [Symbol, asset]
//	claim:                         2 topics  [Symbol, from]
//	reserve_emission_update:       1 topic   [Symbol]
//	gulp_emissions:                1 topic   [Symbol]
//	bad_debt:                      3 topics  [Symbol, user, asset]
//	defaulted_debt:                2 topics  [Symbol, asset]
//	set_admin:                     2 topics  [Symbol, admin]
//	update_pool:                   2 topics  [Symbol, admin]
//	queue_set_reserve:             2 topics  [Symbol, admin]
//	cancel_set_reserve:            2 topics  [Symbol, admin]
//	set_reserve:                   1 topic   [Symbol]
//	set_status:                    1 or 2    [Symbol] (auto) | [Symbol, admin]
//	deploy (pool-factory):         1 topic   [Symbol]

// ─── Decoded event types ───────────────────────────────────────

// PositionEvent is the decoded shape of every money-market event
// that changes a (user, asset, pool) position: supply / withdraw /
// supply_collateral / withdraw_collateral / borrow / repay /
// flash_loan. They share a body shape (two i128 amounts) so a
// single struct handles all seven.
//
// EventKind discriminates which of the seven — one of:
// EventSupply, EventWithdraw, EventSupplyCollateral,
// EventWithdrawCollateral, EventBorrow, EventRepay, EventFlashLoan.
//
// TokenAmount + BOrDAmount are *big.Int — i128 amounts per
// ADR-0003; the storage layer writes them as NUMERIC, the JSON
// wire shape as a decimal string.
type PositionEvent struct {
	Pool string // emitting pool contract C-strkey
	Kind string // one of the seven money-market Event* constants

	Asset        string // topic[1] asset Address (G or C)
	User         string // topic[2] from / user Address (G or C)
	Counterparty string // flash_loan only: topic[3] borrowing contract; "" otherwise

	TokenAmount *big.Int // body[0]: tokens_in / tokens_out i128
	BOrDAmount  *big.Int // body[1]: b_or_d_tokens minted / burnt i128

	Ledger     uint32
	TxHash     string
	OpIndex    uint32
	EventIndex uint32 // distinguishes multiple same-(asset,user,kind) position events in one op (PK component, migration 0054)
	Timestamp  time.Time
}

// EmissionEvent is the decoded shape of the four emission /
// credit-risk events (gulp / claim / reserve_emission_update /
// gulp_emissions / bad_debt / defaulted_debt). Heterogeneous
// bodies, so individual fields are nullable (zero value = absent
// for the string fields, nil for *big.Int).
//
// EventKind discriminates which — one of: EventGulp, EventClaim,
// EventReserveEmissions, EventGulpEmissions, EventBadDebt,
// EventDefaultedDebt.
type EmissionEvent struct {
	Pool string
	Kind string

	// Promoted typed fields. Populated per event kind:
	//   gulp:                    Asset, Amount(=token_delta)
	//   claim:                   User(=from), Amount(=amount_claimed),
	//                            ReserveTokenIDs (from body)
	//   reserve_emission_update: ResTokenID, EmissionsPerSec, Expiration
	//   gulp_emissions:          Amount
	//   bad_debt:                User, Asset, Amount(=d_tokens)
	//   defaulted_debt:          Asset, Amount(=d_tokens_burnt)
	Asset string
	User  string

	Amount *big.Int // primary i128 amount (see per-kind mapping above)

	// reserve_emission_update extras (NULL for everything else).
	ResTokenID      uint32
	EmissionsPerSec uint64
	Expiration      uint64

	// claim extras (NULL for everything else).
	ReserveTokenIDs []uint32

	Ledger     uint32
	TxHash     string
	OpIndex    uint32
	EventIndex uint32 // distinguishes multiple same-kind emission events in one op (PK component)
	Timestamp  time.Time
}

// AdminEvent is the decoded shape of every pool-config / admin /
// pool-factory lifecycle event: set_admin, update_pool,
// queue_set_reserve, cancel_set_reserve, set_reserve, set_status,
// deploy.
//
// ContractID is the EMITTING contract — pool C-strkey for the six
// pool events, pool-factory C-strkey for `deploy`.
type AdminEvent struct {
	ContractID string
	Kind       string

	// Promoted typed fields.
	//   set_admin / update_pool / queue_set_reserve /
	//   cancel_set_reserve / set_status (admin variant):  Admin
	//   queue_set_reserve / cancel_set_reserve / set_reserve: Asset
	//   set_admin.new_admin / deploy.pool_address:           Target
	Admin  string
	Asset  string
	Target string

	// update_pool body fields.
	BackstopTakeRate uint32
	MaxPositions     uint32
	MinCollateral    *big.Int // i128 per ADR-0003

	// set_reserve body field; queue_set_reserve.metadata.index.
	ReserveIndex uint32

	// set_status body field.
	NewStatus uint32
	// True when the set_status variant included an admin topic
	// (set_status_admin in events.rs); false for the non-admin
	// `set_status(new_status)` variant.
	ByAdmin bool

	// queue_set_reserve.metadata — full ReserveConfig.
	// Stored as a map for round-trip parity with the on-wire
	// struct; the storage layer marshals to jsonb. Nil when the
	// event kind doesn't carry a ReserveConfig.
	ReserveConfig map[string]any

	Ledger     uint32
	TxHash     string
	OpIndex    uint32
	EventIndex uint32 // distinguishes multiple same-kind admin events in one op (PK component)
	Timestamp  time.Time
}

// ─── classify (extended) ───────────────────────────────────────
//
// The original classify() in decode.go covers only the three
// auction topics. classifyAny is the extended switch — every
// topic the package declares is mapped to its Event* name. The
// dispatcher adapter (Matches/Decode) uses classifyAny; classify()
// is now only exercised by the auction-scoped decode_test.go cases.
func classifyAny(e *events.Event) string { //nolint:gocyclo,cyclop // one case per Blend topic; flattening makes the dispatch table easier to audit against pool/src/events.rs + pool-factory/src/events.rs.
	if len(e.Topic) == 0 {
		return ""
	}
	switch e.Topic[0] {
	// Auction events (handled by the auction-specific decoders).
	case TopicSymbolNewAuction:
		return EventNewAuction
	case TopicSymbolFillAuction:
		return EventFillAuction
	case TopicSymbolDeleteAuction:
		return EventDeleteAuction

	// Money-market events.
	case TopicSymbolSupply:
		return EventSupply
	case TopicSymbolWithdraw:
		return EventWithdraw
	case TopicSymbolSupplyCollateral:
		return EventSupplyCollateral
	case TopicSymbolWithdrawCollateral:
		return EventWithdrawCollateral
	case TopicSymbolBorrow:
		return EventBorrow
	case TopicSymbolRepay:
		return EventRepay
	case TopicSymbolFlashLoan:
		return EventFlashLoan

	// Emission + credit-risk events.
	case TopicSymbolGulp:
		return EventGulp
	case TopicSymbolClaim:
		return EventClaim
	case TopicSymbolReserveEmissions:
		return EventReserveEmissions
	case TopicSymbolGulpEmissions:
		return EventGulpEmissions
	case TopicSymbolBadDebt:
		return EventBadDebt
	case TopicSymbolDefaultedDebt:
		return EventDefaultedDebt

	// Admin / status.
	case TopicSymbolSetAdmin:
		return EventSetAdmin
	case TopicSymbolUpdatePool:
		return EventUpdatePool
	case TopicSymbolQueueSetReserve:
		return EventQueueSetReserve
	case TopicSymbolCancelSetReserve:
		return EventCancelSetReserve
	case TopicSymbolSetReserve:
		return EventSetReserve
	case TopicSymbolSetStatus:
		return EventSetStatus

	// Pool factory.
	case TopicSymbolDeploy:
		return EventDeploy

	default:
		return ""
	}
}

// ─── Position-event decoder ────────────────────────────────────

// decodePositionEvent parses a money-market position-changing
// event. Topic + body shapes per the events.rs comment block at
// the top of this file. The seven kinds share enough structure
// that one function handles them all — flash_loan is the only one
// with an extra topic for the borrowing contract.
//
// Returns ErrMalformedPayload (wrapped) on schema drift.
func decodePositionEvent(e *events.Event, kind string, closedAt time.Time) (PositionEvent, error) {
	wantTopics := 3
	if kind == EventFlashLoan {
		wantTopics = 4
	}
	if len(e.Topic) != wantTopics {
		return PositionEvent{}, fmt.Errorf("%w: %s expected %d topics, got %d",
			ErrMalformedPayload, kind, wantTopics, len(e.Topic))
	}

	asset, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	user, err := decodeAddressTopic(e.Topic[2])
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: from: %w", ErrMalformedPayload, err)
	}

	var counterparty string
	if kind == EventFlashLoan {
		counterparty, err = decodeAddressTopic(e.Topic[3])
		if err != nil {
			return PositionEvent{}, fmt.Errorf("%w: contract: %w", ErrMalformedPayload, err)
		}
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 2)
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	tokenAmt, err := scval.AsAmountFromI128(tuple[0])
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: token amount: %w", ErrMalformedPayload, err)
	}
	bdAmt, err := scval.AsAmountFromI128(tuple[1])
	if err != nil {
		return PositionEvent{}, fmt.Errorf("%w: b/d-token amount: %w", ErrMalformedPayload, err)
	}

	return PositionEvent{
		Pool:         e.ContractID,
		Kind:         kind,
		Asset:        asset,
		User:         user,
		Counterparty: counterparty,
		TokenAmount:  tokenAmt.BigInt(),
		BOrDAmount:   bdAmt.BigInt(),
		Ledger:       e.Ledger,
		TxHash:       e.TxHash,
		OpIndex:      uint32(e.OperationIndex),
		Timestamp:    closedAt,
	}, nil
}

// ─── Emission-event decoder ────────────────────────────────────

// decodeGulp parses a `gulp` event.
//
//	topics: [Symbol("gulp"), Address(asset)]
//	body:   i128(token_delta)
func decodeGulp(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 2 {
		return EmissionEvent{}, fmt.Errorf("%w: gulp expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	asset, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	amt, err := scval.AsAmountFromI128(body)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: token_delta: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:      e.ContractID,
		Kind:      EventGulp,
		Asset:     asset,
		Amount:    amt.BigInt(),
		Ledger:    e.Ledger,
		TxHash:    e.TxHash,
		OpIndex:   uint32(e.OperationIndex),
		Timestamp: closedAt,
	}, nil
}

// decodeClaim parses a `claim` event.
//
//	topics: [Symbol("claim"), Address(from)]
//	body:   (reserve_token_ids: Vec<u32>, amount_claimed: i128)
func decodeClaim(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 2 {
		return EmissionEvent{}, fmt.Errorf("%w: claim expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	from, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: from: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 2)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	idsVec, err := scval.AsVec(tuple[0])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: reserve_token_ids: %w", ErrMalformedPayload, err)
	}
	ids := make([]uint32, 0, len(idsVec))
	for i, sv := range idsVec {
		id, err := scval.AsU32(sv)
		if err != nil {
			return EmissionEvent{}, fmt.Errorf("%w: reserve_token_ids[%d]: %w", ErrMalformedPayload, i, err)
		}
		ids = append(ids, id)
	}
	amt, err := scval.AsAmountFromI128(tuple[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: amount_claimed: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:            e.ContractID,
		Kind:            EventClaim,
		User:            from,
		Amount:          amt.BigInt(),
		ReserveTokenIDs: ids,
		Ledger:          e.Ledger,
		TxHash:          e.TxHash,
		OpIndex:         uint32(e.OperationIndex),
		Timestamp:       closedAt,
	}, nil
}

// decodeReserveEmissionUpdate parses a `reserve_emission_update` event.
//
//	topics: [Symbol("reserve_emission_update")]
//	body:   (res_token_id: u32, eps: u64, expiration: u64)
func decodeReserveEmissionUpdate(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 1 {
		return EmissionEvent{}, fmt.Errorf("%w: reserve_emission_update expected 1 topic, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 3)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	resID, err := scval.AsU32(tuple[0])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: res_token_id: %w", ErrMalformedPayload, err)
	}
	eps, err := scval.AsU64(tuple[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: eps: %w", ErrMalformedPayload, err)
	}
	exp, err := scval.AsU64(tuple[2])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: expiration: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:            e.ContractID,
		Kind:            EventReserveEmissions,
		ResTokenID:      resID,
		EmissionsPerSec: eps,
		Expiration:      exp,
		Ledger:          e.Ledger,
		TxHash:          e.TxHash,
		OpIndex:         uint32(e.OperationIndex),
		Timestamp:       closedAt,
	}, nil
}

// decodeGulpEmissions parses a `gulp_emissions` event.
//
//	topics: [Symbol("gulp_emissions")]
//	body:   i128(emissions)
func decodeGulpEmissions(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 1 {
		return EmissionEvent{}, fmt.Errorf("%w: gulp_emissions expected 1 topic, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	amt, err := scval.AsAmountFromI128(body)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: emissions: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:      e.ContractID,
		Kind:      EventGulpEmissions,
		Amount:    amt.BigInt(),
		Ledger:    e.Ledger,
		TxHash:    e.TxHash,
		OpIndex:   uint32(e.OperationIndex),
		Timestamp: closedAt,
	}, nil
}

// decodeBadDebt parses a `bad_debt` event.
//
//	topics: [Symbol("bad_debt"), Address(user), Address(asset)]
//	body:   i128(d_tokens)
func decodeBadDebt(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 3 {
		return EmissionEvent{}, fmt.Errorf("%w: bad_debt expected 3 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	user, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: user: %w", ErrMalformedPayload, err)
	}
	asset, err := decodeAddressTopic(e.Topic[2])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	amt, err := scval.AsAmountFromI128(body)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: d_tokens: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:      e.ContractID,
		Kind:      EventBadDebt,
		User:      user,
		Asset:     asset,
		Amount:    amt.BigInt(),
		Ledger:    e.Ledger,
		TxHash:    e.TxHash,
		OpIndex:   uint32(e.OperationIndex),
		Timestamp: closedAt,
	}, nil
}

// decodeDefaultedDebt parses a `defaulted_debt` event.
//
//	topics: [Symbol("defaulted_debt"), Address(asset)]
//	body:   i128(d_tokens_burnt)
func decodeDefaultedDebt(e *events.Event, closedAt time.Time) (EmissionEvent, error) {
	if len(e.Topic) != 2 {
		return EmissionEvent{}, fmt.Errorf("%w: defaulted_debt expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	asset, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	amt, err := scval.AsAmountFromI128(body)
	if err != nil {
		return EmissionEvent{}, fmt.Errorf("%w: d_tokens_burnt: %w", ErrMalformedPayload, err)
	}
	return EmissionEvent{
		Pool:      e.ContractID,
		Kind:      EventDefaultedDebt,
		Asset:     asset,
		Amount:    amt.BigInt(),
		Ledger:    e.Ledger,
		TxHash:    e.TxHash,
		OpIndex:   uint32(e.OperationIndex),
		Timestamp: closedAt,
	}, nil
}

// ─── Admin-event decoders ──────────────────────────────────────

// decodeSetAdmin parses a `set_admin` event.
//
//	topics: [Symbol("set_admin"), Address(admin)]
//	body:   Address(new_admin)
func decodeSetAdmin(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 2 {
		return AdminEvent{}, fmt.Errorf("%w: set_admin expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	admin, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: admin: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	newAdmin, err := scval.AsAddressStrkey(body)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: new_admin: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID: e.ContractID,
		Kind:       EventSetAdmin,
		Admin:      admin,
		Target:     newAdmin,
		Ledger:     e.Ledger,
		TxHash:     e.TxHash,
		OpIndex:    uint32(e.OperationIndex),
		Timestamp:  closedAt,
	}, nil
}

// decodeUpdatePool parses an `update_pool` event.
//
//	topics: [Symbol("update_pool"), Address(admin)]
//	body:   (backstop_take_rate: u32, max_positions: u32, min_collateral: i128)
func decodeUpdatePool(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 2 {
		return AdminEvent{}, fmt.Errorf("%w: update_pool expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	admin, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: admin: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 3)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	rate, err := scval.AsU32(tuple[0])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: backstop_take_rate: %w", ErrMalformedPayload, err)
	}
	maxPos, err := scval.AsU32(tuple[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: max_positions: %w", ErrMalformedPayload, err)
	}
	minCol, err := scval.AsAmountFromI128(tuple[2])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: min_collateral: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID:       e.ContractID,
		Kind:             EventUpdatePool,
		Admin:            admin,
		BackstopTakeRate: rate,
		MaxPositions:     maxPos,
		MinCollateral:    minCol.BigInt(),
		Ledger:           e.Ledger,
		TxHash:           e.TxHash,
		OpIndex:          uint32(e.OperationIndex),
		Timestamp:        closedAt,
	}, nil
}

// decodeQueueSetReserve parses a `queue_set_reserve` event.
//
//	topics: [Symbol("queue_set_reserve"), Address(admin)]
//	body:   (asset: Address, metadata: ReserveConfig)
//
// ReserveConfig is the soroban-sdk #[contracttype] struct from
// pool/src/storage.rs:45 — serialised as ScvMap with sorted-by-
// name keys. We decode the full struct into the AdminEvent's
// ReserveConfig map for round-trip parity (storage marshals to
// jsonb attributes).
func decodeQueueSetReserve(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 2 {
		return AdminEvent{}, fmt.Errorf("%w: queue_set_reserve expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	admin, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: admin: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 2)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	asset, err := scval.AsAddressStrkey(tuple[0])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	cfg, err := decodeReserveConfig(tuple[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: metadata: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID:    e.ContractID,
		Kind:          EventQueueSetReserve,
		Admin:         admin,
		Asset:         asset,
		ReserveConfig: cfg,
		Ledger:        e.Ledger,
		TxHash:        e.TxHash,
		OpIndex:       uint32(e.OperationIndex),
		Timestamp:     closedAt,
	}, nil
}

// decodeCancelSetReserve parses a `cancel_set_reserve` event.
//
//	topics: [Symbol("cancel_set_reserve"), Address(admin)]
//	body:   Address(asset)
func decodeCancelSetReserve(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 2 {
		return AdminEvent{}, fmt.Errorf("%w: cancel_set_reserve expected 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	admin, err := decodeAddressTopic(e.Topic[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: admin: %w", ErrMalformedPayload, err)
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	asset, err := scval.AsAddressStrkey(body)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID: e.ContractID,
		Kind:       EventCancelSetReserve,
		Admin:      admin,
		Asset:      asset,
		Ledger:     e.Ledger,
		TxHash:     e.TxHash,
		OpIndex:    uint32(e.OperationIndex),
		Timestamp:  closedAt,
	}, nil
}

// decodeSetReserve parses a `set_reserve` event.
//
//	topics: [Symbol("set_reserve")]
//	body:   (asset: Address, index: u32)
func decodeSetReserve(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 1 {
		return AdminEvent{}, fmt.Errorf("%w: set_reserve expected 1 topic, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	tuple, err := scval.AsTupleN(body, 2)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: body shape: %w", ErrMalformedPayload, err)
	}
	asset, err := scval.AsAddressStrkey(tuple[0])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: asset: %w", ErrMalformedPayload, err)
	}
	idx, err := scval.AsU32(tuple[1])
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: index: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID:   e.ContractID,
		Kind:         EventSetReserve,
		Asset:        asset,
		ReserveIndex: idx,
		Ledger:       e.Ledger,
		TxHash:       e.TxHash,
		OpIndex:      uint32(e.OperationIndex),
		Timestamp:    closedAt,
	}, nil
}

// decodeSetStatus parses a `set_status` event. Two variants:
//
//	non-admin: topics [Symbol("set_status")]                 body u32(new_status)
//	admin:     topics [Symbol("set_status"), Address(admin)] body u32(pool_status)
//
// Both arities are accepted; the ByAdmin flag distinguishes them.
func decodeSetStatus(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 1 && len(e.Topic) != 2 {
		return AdminEvent{}, fmt.Errorf("%w: set_status expected 1 or 2 topics, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	out := AdminEvent{
		ContractID: e.ContractID,
		Kind:       EventSetStatus,
		Ledger:     e.Ledger,
		TxHash:     e.TxHash,
		OpIndex:    uint32(e.OperationIndex),
		Timestamp:  closedAt,
	}
	if len(e.Topic) == 2 {
		admin, err := decodeAddressTopic(e.Topic[1])
		if err != nil {
			return AdminEvent{}, fmt.Errorf("%w: admin: %w", ErrMalformedPayload, err)
		}
		out.Admin = admin
		out.ByAdmin = true
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	status, err := scval.AsU32(body)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: new_status: %w", ErrMalformedPayload, err)
	}
	out.NewStatus = status
	return out, nil
}

// decodeDeploy parses a `deploy` event from the pool-factory.
//
//	topics: [Symbol("deploy")]
//	body:   Address(pool_address)
func decodeDeploy(e *events.Event, closedAt time.Time) (AdminEvent, error) {
	if len(e.Topic) != 1 {
		return AdminEvent{}, fmt.Errorf("%w: deploy expected 1 topic, got %d",
			ErrMalformedPayload, len(e.Topic))
	}
	body, err := scval.Parse(e.Value)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: parse body: %w", ErrMalformedPayload, err)
	}
	addr, err := scval.AsAddressStrkey(body)
	if err != nil {
		return AdminEvent{}, fmt.Errorf("%w: pool_address: %w", ErrMalformedPayload, err)
	}
	return AdminEvent{
		ContractID: e.ContractID,
		Kind:       EventDeploy,
		Target:     addr,
		Ledger:     e.Ledger,
		TxHash:     e.TxHash,
		OpIndex:    uint32(e.OperationIndex),
		Timestamp:  closedAt,
	}, nil
}

// ─── ReserveConfig decoder helper ──────────────────────────────

// reserveConfigKeys mirrors pool/src/storage.rs::ReserveConfig.
// Decoded by name (resilient to field reordering) per
// docs/architecture/contract-schema-evolution.md.
//
// Field decoding rules:
//   - i128 → decimal string (preserved full precision per ADR-0003)
//   - u32  → uint64 (jsonb-safe)
//   - bool → bool
var reserveConfigKeys = []struct {
	Name string
	Type string // "u32" | "i128" | "bool"
}{
	{"index", "u32"},
	{"decimals", "u32"},
	{"c_factor", "u32"},
	{"l_factor", "u32"},
	{"util", "u32"},
	{"max_util", "u32"},
	{"r_base", "u32"},
	{"r_one", "u32"},
	{"r_two", "u32"},
	{"r_three", "u32"},
	{"reactivity", "u32"},
	{"supply_cap", "i128"},
	{"enabled", "bool"},
}

// decodeReserveConfig decodes an ScvMap-shaped ReserveConfig into
// a key-value map. Missing fields surface as ErrMalformedPayload —
// any contract upgrade that drops a field fails loud rather than
// silently writing partial data.
//
// `enabled` is a bool. The soroban-sdk emits booleans as ScvBool;
// we decode it via scval.AsBool below.
func decodeReserveConfig(sv scval.ScVal) (map[string]any, error) {
	entries, err := scval.AsMap(sv)
	if err != nil {
		return nil, fmt.Errorf("ReserveConfig shape: %w", err)
	}
	out := make(map[string]any, len(reserveConfigKeys))
	for _, k := range reserveConfigKeys {
		val, ok := scval.MapField(entries, k.Name)
		if !ok {
			return nil, fmt.Errorf("ReserveConfig missing %q", k.Name)
		}
		switch k.Type {
		case "u32":
			n, err := scval.AsU32(val)
			if err != nil {
				return nil, fmt.Errorf("ReserveConfig.%s: %w", k.Name, err)
			}
			out[k.Name] = uint64(n)
		case "i128":
			amt, err := scval.AsAmountFromI128(val)
			if err != nil {
				return nil, fmt.Errorf("ReserveConfig.%s: %w", k.Name, err)
			}
			out[k.Name] = amt.String() // i128 as decimal string
		case "bool":
			b, err := scval.AsBool(val)
			if err != nil {
				return nil, fmt.Errorf("ReserveConfig.%s: %w", k.Name, err)
			}
			out[k.Name] = b
		}
	}
	return out, nil
}
