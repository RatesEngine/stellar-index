package v1

import (
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/incidents"
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
	s := newTestServerWithLogger()
	s.incidents = []incidents.Incident{
		{
			Slug:         "2026-05-06-postgres-lock-table-full",
			Title:        "[SEV-3] Indexer dropping ~1% of trades — Postgres lock-table-full — 2026-05-06",
			StartedAt:    time.Date(2026, 5, 6, 15, 0, 0, 0, time.UTC),
			ResolvedAt:   &resolved,
			BodyMarkdown: "Some trades arriving on `coinbase` were not landing in `prices_1m`.",
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

// TestSummaryFromMarkdown_ExtractsFirstParagraph pins the heuristic
// the atom feed uses to derive each entry's <summary>. The status
// page uses the same logic; if these drift the feed and the page
// would show different summaries.
func TestSummaryFromMarkdown_ExtractsFirstParagraph(t *testing.T) {
	body := `# Heading should be skipped

The first real paragraph is what we want.
Multi-line is fine.

A second paragraph that should NOT appear.`
	got := summaryFromMarkdown(body)
	want := "The first real paragraph is what we want.\nMulti-line is fine."
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestSummaryFromMarkdown_SkipsHTMLComments — postmortems start
// with a `<!-- … -->` editorial comment per the template; that
// must NOT become the feed summary.
func TestSummaryFromMarkdown_SkipsHTMLComments(t *testing.T) {
	body := `<!--
Internal note for editors.
-->

# Title

The body text.`
	got := summaryFromMarkdown(body)
	want := "The body text."
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestSummaryFromMarkdown_TruncatesLong — long first paragraphs
// get cut at 400 chars with an ellipsis so feed readers don't show
// the whole post in the summary.
func TestSummaryFromMarkdown_TruncatesLong(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := summaryFromMarkdown(long)
	if len(got) != 400 {
		t.Errorf("len(summary) = %d, want 400", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("summary should end in ellipsis, got: %q", got[len(got)-10:])
	}
}

// TestSummaryFromMarkdown_EmptyInput — empty / whitespace-only body
// returns empty string. Atom <summary> stays empty cleanly rather
// than emitting whitespace garbage that some feed parsers choke on.
func TestSummaryFromMarkdown_EmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\n", "<!-- only -->\n"} {
		if got := summaryFromMarkdown(in); got != "" {
			t.Errorf("summary(%q) = %q, want empty", in, got)
		}
	}
}

// TestHandleIncidents_WireShape — pin the envelope shape the
// explorer's /incident pages decode. Inject a synthetic incident
// list onto the Server so the test doesn't depend on what's in
// the embedded data dir.
func TestHandleIncidents_WireShape(t *testing.T) {
	s := &Server{
		incidents: []incidents.Incident{
			{
				Slug:      "2026-05-06-test",
				Title:     "[SEV-3] Test",
				Severity:  incidents.SeverityInformative,
				Status:    incidents.StatusResolved,
				StartedAt: time.Date(2026, 5, 6, 15, 0, 0, 0, time.UTC),
			},
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	s.handleIncidents(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"data":{`,
		`"incidents":[`,
		`"count":1`,
		`"slug":"2026-05-06-test"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestHandleIncidents_EmptyList — fresh deploy with no embedded
// posts emits an empty list, NOT null. The /v1/incidents wire
// contract is `data: { incidents: [], count: 0 }`.
func TestHandleIncidents_EmptyList(t *testing.T) {
	s := &Server{incidents: nil}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	s.handleIncidents(rec, req)

	body := rec.Body.String()
	for _, want := range []string{`"incidents":null`, `"data":null`} {
		if strings.Contains(body, want) {
			t.Errorf("body contains forbidden %q (should serialise as []): %s", want, body)
		}
	}
	if !strings.Contains(body, `"count":0`) {
		t.Errorf("count=0 missing: %s", body)
	}
}

// TestHandleIncidentsAtom_ValidXML — the atom feed must round-trip
// through encoding/xml. Pin core RFC-4287 fields and the per-entry
// urn:ratesengine:incident:<slug> ID convention so feed-reader
// dedupe stays stable across crawls.
func TestHandleIncidentsAtom_ValidXML(t *testing.T) {
	resolved := time.Date(2026, 5, 6, 22, 39, 0, 0, time.UTC)
	s := newTestServerWithLogger()
	s.incidents = []incidents.Incident{
		{
			Slug:         "2026-05-06-postgres-lock",
			Title:        "[SEV-3] Postgres lock-table-full",
			Severity:     incidents.SeverityInformative,
			Status:       incidents.StatusResolved,
			StartedAt:    time.Date(2026, 5, 6, 15, 0, 0, 0, time.UTC),
			ResolvedAt:   &resolved,
			BodyMarkdown: "First paragraph for summary.\n\nSecond paragraph.",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents.atom", nil)
	s.handleIncidentsAtom(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/atom+xml") {
		t.Errorf("Content-Type = %q, want application/atom+xml...", got)
	}
	// Parse the body — proves the XML is well-formed.
	var feed atomFeed
	body := rec.Body.String()
	// Strip the leading <?xml ... ?> — Decoder handles it but we
	// also want to assert the prelude is present.
	if !strings.HasPrefix(body, xml.Header) {
		t.Errorf("body missing xml.Header preamble")
	}
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatalf("xml.Unmarshal: %v\nbody: %s", err, body)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(feed.Entries))
	}
	e := feed.Entries[0]
	if e.ID != "urn:ratesengine:incident:2026-05-06-postgres-lock" {
		t.Errorf("entry ID = %q (urn convention drift breaks dedupe)", e.ID)
	}
	if e.Summary != "First paragraph for summary." {
		t.Errorf("summary = %q (drifted from summaryFromMarkdown)", e.Summary)
	}
	// Updated picks the resolved_at when later than started_at.
	if !strings.HasPrefix(e.Updated, "2026-05-06T22:39") {
		t.Errorf("Updated = %q, want resolved_at when resolved", e.Updated)
	}
}

// newTestServerWithLogger constructs a Server suitable for atom-
// feed testing — needs a non-nil logger because the handler logs
// on encode errors.
func newTestServerWithLogger() *Server {
	return &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
