// Package streaming provides the shared SSE (Server-Sent Events)
// infrastructure for the v1 streaming endpoints (/v1/price/stream,
// /v1/price/tip/stream, /v1/observations/stream, …).
//
// The model is publish/subscribe with per-topic fanout:
//
//   - A single [Hub] holds per-topic [ring] buffers + active subscribers.
//   - Producers (the aggregator, the dispatcher) call [Hub.Publish]
//     to broadcast an event on a topic.
//   - HTTP handlers call [Stream] which subscribes to one or more
//     topics, replays buffered events from the client's
//     `Last-Event-ID` (RFC 8895 §9), and forwards live events as SSE
//     frames until the request context cancels.
//
// Event ordering is per-topic; consumers MUST treat IDs as opaque
// time-sortable strings (16 lowercase hex chars in this implementation
// — but that format is internal). Cross-topic ordering is not
// guaranteed.
//
// Slow subscribers are dropped, not blocked. When the per-subscriber
// channel is full, the offending subscription is closed; the client
// sees the connection drop and reconnects with `Last-Event-ID` to
// resume from the buffer. This keeps a single misbehaving consumer
// from stalling fanout to healthy peers.
//
// Last-Event-ID resume is best-effort: if the requested ID is older
// than the buffered window, replay starts at the buffer's oldest
// event (so the client knows it lost events because IDs jump
// forward). Buffer size is configurable per topic; default 256
// events ≈ 4–5 minutes at the 1m closed-bucket cadence and
// ≈ 50 minutes at the 5s tip cadence.
package streaming
