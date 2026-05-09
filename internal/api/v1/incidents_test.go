package v1

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/incidents"
)

// TestHandleIncidentsAtom_LinkTarget pins the per-entry alternate
// link to the canonical /incident/{slug} route.
//
// The atom feed previously emitted `https://status.ratesengine.net/#<slug>`,
// expecting subscribers to land on the home page and scroll to a
// matching anchor — but the home page doesn't render `id="<slug>"`
// on the per-incident summary, so feed readers landed on the home
// page with nothing to scroll to. Per-incident detail lives at
// `/incident/{slug}`, which is what the feed should point at.
func TestHandleIncidentsAtom_LinkTarget(t *testing.T) {
	resolved := time.Date(2026, 5, 6, 22, 39, 0, 0, time.UTC)
	s := &Server{
		incidents: []incidents.Incident{
			{
				Slug:         "2026-05-06-postgres-lock-table-full",
				Title:        "[SEV-3] Indexer dropping ~1% of trades — Postgres lock-table-full — 2026-05-06",
				StartedAt:    time.Date(2026, 5, 6, 15, 0, 0, 0, time.UTC),
				ResolvedAt:   &resolved,
				BodyMarkdown: "Some trades arriving on `coinbase` were not landing in `prices_1m`.",
			},
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents.atom", nil)
	s.handleIncidentsAtom(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/atom+xml") {
		t.Errorf("Content-Type = %q, want application/atom+xml; charset=utf-8", got)
	}

	var feed atomFeed
	body := rec.Body.Bytes()
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("unmarshal feed: %v\nbody:\n%s", err, body)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(feed.Entries))
	}
	const wantHref = "https://status.ratesengine.net/incident/2026-05-06-postgres-lock-table-full"
	var got string
	for _, l := range feed.Entries[0].Link {
		if l.Rel == "alternate" {
			got = l.Href
			break
		}
	}
	if got != wantHref {
		t.Errorf("entry alternate link = %q, want %q", got, wantHref)
	}
	// Belt-and-braces: the broken `/#<slug>` form should NOT appear
	// in the body either, in case the regression sneaks back in
	// via a different code path.
	if strings.Contains(string(body), "/#2026-05-06-postgres-lock-table-full") {
		t.Errorf("body contains broken /#<slug> form")
	}
}
