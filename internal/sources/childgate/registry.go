// Package childgate provides the shared contract-identity registry
// that factory-anchored Soroban decoders use to gate Matches().
//
// Per ADR-0035, a decoder must only accept events from contracts that
// belong to its protocol: the protocol's factory (a hard-coded trust
// root) plus every child contract the factory creates — fanning out
// recursively. Topic symbols (`swap`, `supply`, `deploy`, …) are NOT
// unique across protocols, so matching on the topic alone mis-attributes
// foreign contracts' events. The factory check is decoder-specific (the
// decoder knows which classify() result is its creation event); this
// package owns the second half: the set of factory-descended child
// contract IDs, plus the live-upsert persistence hook.
//
// A Registry is a small piece of state every gated decoder embeds. It is
// seeded three ways, all rooted at the factory and all funneling through
// [Registry.Seed]:
//
//   - Live: the decoder calls Seed(childID, ledger) when it decodes a
//     factory creation event (e.g. Blend `deploy`, Soroswap-style
//     `new_pair`). Seed fires the persistence hook so the mapping is
//     durably recorded.
//   - DB warm: at process start the pipeline loads the persisted child
//     set (the `protocol_contracts` table) and constructs the decoder
//     with WithSeed — so a restart resumes with a complete registry even
//     though the projector cursor has advanced past the creation events.
//   - Genesis / reconcile: an operator command (or the ADR-0033 reconcile
//     pre-seed) walks the lake for the factory's creation events from the
//     factory's deploy ledger and Seeds each child.
//
// Concurrency: the dispatcher and projector are serial per source, but
// Seed may be called from operator tooling concurrently with reads, so
// the set is mutex-guarded (belt-and-braces, matching soroswap's
// Decoder).
package childgate

import "sync"

// Registry is a set of factory-descended child contract IDs (C-strkeys)
// plus the protocol's factory trust-root set, with an optional live-upsert
// persistence hook. The zero value is not usable — construct with [New].
//
// A protocol may have MORE THAN ONE factory (verified empirically: e.g.
// Blend was redeployed and has two pool factories — an early one and the
// documented V2 — that BOTH deploy real pools). Gating the creation event
// on a single factory would silently drop the other factory's children, so
// the factory trust root is a SET, not a scalar.
type Registry struct {
	mu        sync.RWMutex
	set       map[string]struct{} // discovered children (factory descendants)
	factories map[string]struct{} // trust roots (hard-coded, verified)
	hook      func(childID, factoryID string, firstLedger uint32)
}

// New constructs a Registry, applying any options (WithFactories, WithSeed,
// WithHook).
func New(opts ...Option) *Registry {
	r := &Registry{
		set:       make(map[string]struct{}),
		factories: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Option configures a Registry at construction time.
type Option func(*Registry)

// WithFactories sets the protocol's factory trust-root set — the contract
// IDs whose creation events the decoder honors (and from which every child
// is seeded). This is a verified, hard-coded SET (a protocol can have
// several factories); see the per-source list in the decoder package. The
// completeness of this set is load-bearing: a missing factory means its
// children's events are silently dropped.
func WithFactories(factoryIDs []string) Option {
	return func(r *Registry) {
		for _, id := range factoryIDs {
			if id != "" {
				r.factories[id] = struct{}{}
			}
		}
	}
}

// WithSeed pre-loads the child set from a durable warm (the
// `protocol_contracts` table). Does NOT fire the persistence hook — these
// IDs are already persisted. Safe to combine with WithHook.
func WithSeed(childIDs []string) Option {
	return func(r *Registry) {
		for _, id := range childIDs {
			if id != "" {
				r.set[id] = struct{}{}
			}
		}
	}
}

// WithHook installs the live-upsert callback fired by Seed whenever a new
// child is discovered from a factory creation event. The hook receives
// the child's C-strkey, the C-strkey of the factory that deployed it (for
// provenance — a protocol can have several factories), and the ledger of
// the creation event. Keep it cheap — it runs on the decode path (a
// queued/timeout-bounded ExecContext is fine; a blocking network call is
// not). The hook is invoked WITHOUT the registry lock held.
func WithHook(fn func(childID, factoryID string, firstLedger uint32)) Option {
	return func(r *Registry) { r.hook = fn }
}

// Has reports whether contractID is a registered factory descendant.
// This is the load-bearing gate the decoder's Matches() consults for
// every non-creation event.
func (r *Registry) Has(contractID string) bool {
	r.mu.RLock()
	_, ok := r.set[contractID]
	r.mu.RUnlock()
	return ok
}

// IsFactory reports whether contractID is one of the protocol's trust-root
// factories. The decoder's Matches() consults this for the creation event
// (e.g. Blend `deploy`) — only a genuine factory may announce a child, so a
// foreign contract can't inject one into the registry.
func (r *Registry) IsFactory(contractID string) bool {
	r.mu.RLock()
	_, ok := r.factories[contractID]
	r.mu.RUnlock()
	return ok
}

// Factories returns the protocol's factory trust-root set (sorted-agnostic;
// callers needing order should sort). Used by the genesis-seed walk to
// enumerate every factory whose creation events to replay.
func (r *Registry) Factories() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for id := range r.factories {
		out = append(out, id)
	}
	return out
}

// Seed registers a child contract (idempotent) and fires the persistence
// hook (if any). The decoder calls this from Decode when it observes a
// factory creation event. factoryID is the C-strkey of the factory that
// deployed the child (the creation event's emitter) — recorded for
// provenance. firstLedger is the ledger of that creation event — recorded
// for operator visibility / ordering, not used by the gate. The hook fires
// on every call (idempotent upsert downstream) so a re-observed creation
// event refreshes the durable row harmlessly.
func (r *Registry) Seed(childID, factoryID string, firstLedger uint32) {
	if childID == "" {
		return
	}
	r.mu.Lock()
	r.set[childID] = struct{}{}
	hook := r.hook
	r.mu.Unlock()
	if hook != nil {
		hook(childID, factoryID, firstLedger)
	}
}

// Len returns the number of registered children. Operator/test visibility.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.set)
}
