package platform

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
)

// UsageEvent is one row in the api_usage_events hypertable —
// emitted (asynchronously, via Redis stream) for each
// authenticated request.
//
// Anonymous-tier requests are NOT recorded here (would blow up
// row volume); they continue to be reflected only in Prometheus
// histograms.
type UsageEvent struct {
	Timestamp  time.Time
	AccountID  uuid.UUID
	KeyID      string
	Route      string // registered pattern, e.g. "/v1/price"
	Method     string
	Status     int
	DurationMs int
	BytesOut   int
	ClientIP   net.IP
	GeoCountry string // ISO 3166-1 alpha-2; empty when unknown
	RequestID  string
}

// UsageRollup is a continuous-aggregate row from api_usage_5m,
// api_usage_1h, or api_usage_1d. Same shape across all three
// aggregates; only the bucket interval differs.
type UsageRollup struct {
	Bucket          time.Time
	AccountID       uuid.UUID
	KeyID           string
	Route           string
	Method          string
	Status          int
	Requests        int64
	TotalDurationMs int64
	TotalBytes      int64
	P95Ms           int
	P99Ms           int
}

// UsageQuery scopes a rollup read.
type UsageQuery struct {
	AccountID   uuid.UUID
	KeyID       string    // empty = all keys for the account
	From        time.Time // inclusive
	To          time.Time // exclusive
	Granularity string    // "5m" / "1h" / "1d"
}

// UsageStore persists [UsageEvent] and reads [UsageRollup].
//
// Write path: AppendEvent is called by an async worker draining
// a Redis stream — never directly from the request hot path.
// Read path: QueryRollup reads from the matching CAGG.
type UsageStore interface {
	// AppendEvent inserts one event. Errors are logged by the
	// worker but don't block the request the event describes.
	AppendEvent(ctx context.Context, e UsageEvent) error

	// AppendEventsBatch is the bulk variant used by the worker
	// to batch ~1000 events per round-trip.
	AppendEventsBatch(ctx context.Context, events []UsageEvent) error

	// QueryRollup returns matching rows from the CAGG named by
	// q.Granularity. Caller pre-validates Granularity is one of
	// the three legal values.
	QueryRollup(ctx context.Context, q UsageQuery) ([]UsageRollup, error)

	// MonthToDateRequests returns the request count for the
	// account so far this calendar month — used by the quota
	// burn-rate widget.
	MonthToDateRequests(ctx context.Context, accountID uuid.UUID, now time.Time) (int64, error)
}
