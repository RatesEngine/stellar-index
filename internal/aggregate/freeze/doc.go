// Package freeze writes + reads anomaly-freeze markers per ADR-0019.
//
// When the anomaly checker (internal/aggregate/anomaly) returns
// ActionFreeze for a (asset, quote) bucket close, the aggregator
// calls [Writer.Mark] to record the freeze in Redis at the
// `freeze:<asset>:<quote>` key. The API's /v1/price handler reads
// the same key via [Looker] (which implements
// internal/api/v1.FrozenLooker) and surfaces flags.frozen=true on
// responses for the affected pair.
//
// Why a separate Redis key (vs embedding the freeze state in the
// price-cache value): the freeze marker has different TTL semantics
// than the price itself (5 min vs longer cache windows), and the API
// hot path reads the marker on every /v1/price response — separating
// the keys lets the price reader stay agnostic of anomaly state.
//
// Both Writer and Looker are thin adapters around a Redis client +
// the cache-key builders in internal/cachekeys; the package itself
// holds no state.
package freeze
