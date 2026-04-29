package streaming

// ring is a fixed-capacity event ring buffer. Newer events overwrite
// older ones once full — the oldest still-present event is the
// floor for Last-Event-ID resume.
//
// Single-writer, multi-reader: all access is serialised by the
// owning topicState's mutex; ring itself does NOT lock.
type ring struct {
	cap    int
	events []Event // len(events) ≤ cap
}

func newRing(capacity int) *ring {
	return &ring{
		cap:    capacity,
		events: make([]Event, 0, capacity),
	}
}

// push appends ev. When the ring is at capacity, the oldest event
// is discarded. The slice is kept ordered ascending by ID so that
// snapshotAfter can do a linear scan.
func (r *ring) push(ev Event) {
	if len(r.events) < r.cap {
		r.events = append(r.events, ev)
		return
	}
	// Full — shift left by 1, append at tail. O(cap), but cap is
	// small (256 default) and pushes happen at the publisher's
	// rate (~ once per second worst-case for tip), so this is well
	// inside the per-publish budget.
	copy(r.events, r.events[1:])
	r.events[len(r.events)-1] = ev
}

// snapshotAfter returns a copy of every buffered event with ID
// strictly greater than lastEventID. The returned slice is in
// publish (and therefore ID) order.
//
// When lastEventID is empty, no replay is wanted — returns nil.
// When lastEventID is older than the buffer's oldest event, returns
// EVERY buffered event (the client sees an ID jump and can detect
// the gap).
func (r *ring) snapshotAfter(lastEventID string) []Event {
	if lastEventID == "" {
		return nil
	}
	out := make([]Event, 0, len(r.events))
	for i := range r.events {
		if r.events[i].ID > lastEventID {
			out = append(out, r.events[i])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
