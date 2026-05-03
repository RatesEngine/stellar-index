// Package redispub bridges the aggregator's closed-bucket events
// to the API binary's in-process [streaming.Hub] via Redis pub/sub.
//
// # Architecture
//
// The aggregator and API binaries are separate processes. The
// orchestrator's [orchestrator.StreamPublisher] interface declares
// the producer seam; this package's [Publisher] is the Redis-backed
// implementation, and the matching [Subscriber] (PR 2) listens on
// the same Redis channel and republishes each event on the local
// Hub.
//
//	aggregator binary          Redis             API binary
//	  ┌────────────────┐      ┌─────┐         ┌─────────────┐
//	  │ orchestrator   │──Pub─→│ ch  │──Sub──→│ Subscriber  │
//	  │ StreamPublisher│      └─────┘         │   ↓         │
//	  └────────────────┘                       │ streaming   │
//	                                            │  .Hub       │
//	                                            │   ↓         │
//	                                            │ /v1/price/  │
//	                                            │   stream    │
//	                                            └─────────────┘
//
// # Wire format
//
// One event per (pair, window) closed-bucket VWAP write, encoded
// as the JSON shape [ClosedBucketEvent]. Channel name is
// configurable but defaults to [DefaultChannel].
//
// # Reliability
//
// Plain Redis pub/sub is fire-and-forget — a subscriber that's down
// when an event is published will not see it on reconnect. This is
// acceptable for the closed-bucket stream because the durable
// source-of-truth is the VWAP cache key + the trades hypertable;
// the stream is enrichment for SSE subscribers who want push
// semantics. Subscribers that need replay use the SSE
// `Last-Event-ID` cursor against the Hub's per-topic ring buffer
// (see [internal/api/streaming.Hub.Subscribe]).
package redispub
