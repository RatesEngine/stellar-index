package v1

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RatesEngine/rates-engine/internal/incidents"
)

// IncidentsList is the wire shape returned by /v1/incidents.
// `incidents` is sorted started_at desc by the loader.
type IncidentsList struct {
	Incidents []incidents.Incident `json:"incidents"`
	Count     int                  `json:"count"`
}

// handleIncidents serves GET /v1/incidents.
//
// Returns every customer-facing incident post the binary has
// embedded (`internal/incidents/data/*.md`), parsed at startup
// and cached on the Server. New posts ship with a redeploy.
//
// No filtering / pagination today — the corpus is small enough
// (few entries per year of operation) that a flat list is fine.
// If we ever cross 100 entries, an `?since=` filter is the
// natural next step.
func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	// Nil-to-empty: a fresh deployment with no embedded posts
	// (s.incidents == nil) would otherwise marshal as `null` rather
	// than `[]`, breaking the pkg/client SDK + explorer JS that
	// .map() over the array.
	rows := s.incidents
	if rows == nil {
		rows = []incidents.Incident{}
	}
	writeJSON(w, IncidentsList{
		Incidents: rows,
		Count:     len(rows),
	}, Flags{})
}

// atomFeed mirrors the RFC-4287 Atom syndication shape we emit.
// Hand-rolled rather than pulling in a feed-generator dep —
// Atom has only a handful of required fields and the writer
// stays under a screen of code.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Link    []atomLink  `xml:"link"`
	Updated string      `xml:"updated"`
	Author  *atomAuthor `xml:"author,omitempty"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type atomEntry struct {
	ID      string     `xml:"id"`
	Title   string     `xml:"title"`
	Link    []atomLink `xml:"link"`
	Updated string     `xml:"updated"`
	Summary string     `xml:"summary,omitempty"`
	Content struct {
		Type     string `xml:"type,attr"`
		CharData string `xml:",chardata"`
	} `xml:"content"`
}

// handleIncidentsAtom serves GET /v1/incidents.atom.
//
// Atom 1.0 feed of every customer-facing incident — same data
// as /v1/incidents but in the syndication format that Feedly,
// Slack's RSS bot, etc. consume out of the box. URLs in
// <link> point at the status page so subscribers click straight
// through to the human-readable post.
//
// Per RFC 4287 the feed's `<updated>` is the most recent
// `<entry>`'s `<updated>`. Each entry's `<id>` is a stable URN
// derived from the slug so feed readers dedupe correctly across
// crawls.
func (s *Server) handleIncidentsAtom(w http.ResponseWriter, r *http.Request) {
	const baseURL = "https://status.ratesengine.net"
	feed := atomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		ID:      baseURL + "/feed",
		Title:   "Rates Engine — incident history",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Link: []atomLink{
			{Rel: "self", Href: "https://api.ratesengine.net/v1/incidents.atom", Type: "application/atom+xml"},
			{Rel: "alternate", Href: baseURL + "/", Type: "text/html"},
		},
		Author: &atomAuthor{Name: "Rates Engine", URI: "https://ratesengine.net"},
	}

	for _, inc := range s.incidents {
		updated := inc.StartedAt
		if inc.ResolvedAt != nil && inc.ResolvedAt.After(updated) {
			updated = *inc.ResolvedAt
		}
		entry := atomEntry{
			ID:      fmt.Sprintf("urn:ratesengine:incident:%s", inc.Slug),
			Title:   inc.Title,
			Updated: updated.UTC().Format(time.RFC3339),
			Link: []atomLink{
				// Per-incident detail page. Was previously the
				// homepage with an `#<slug>` anchor, but the home
				// page doesn't render an `id` per incident, so feed
				// readers landed on `https://status.ratesengine.net/`
				// with no scroll target. Use the canonical
				// /incident/{slug} route so subscribers land on the
				// postmortem they clicked.
				{Rel: "alternate", Href: baseURL + "/incident/" + inc.Slug, Type: "text/html"},
			},
			Summary: summaryFromMarkdown(inc.BodyMarkdown),
		}
		entry.Content.Type = "text"
		entry.Content.CharData = inc.BodyMarkdown
		feed.Entries = append(feed.Entries, entry)
	}

	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		s.logger.Warn("incidents atom write header", "err", err)
		return
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(feed); err != nil {
		s.logger.Warn("incidents atom encode", "err", err)
	}
}

// summaryFromMarkdown extracts the first paragraph from a markdown
// post — same heuristic the status page's `normaliseIncident` uses,
// kept simple so it stays readable in a feed reader.
func summaryFromMarkdown(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	// Split on blank line (paragraph break) — take the first
	// non-frontmatter paragraph.
	parts := strings.Split(body, "\n\n")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Skip headings and HTML comments.
		if strings.HasPrefix(p, "#") || strings.HasPrefix(p, "<!--") {
			continue
		}
		if len(p) > 400 {
			// Walk back to the nearest rune boundary at or before
			// byte 397 so multi-byte UTF-8 codepoints aren't split
			// in half. A naive `p[:397]` would slice mid-rune for
			// incident posts containing accented characters
			// (é/ñ/ü/etc.) or any non-ASCII text, producing invalid
			// UTF-8 in the atom feed body — strict feed validators
			// (W3C feedvalidator.org, some Atom-1.0 readers) reject
			// the whole entry, and the explorer's render shows a
			// replacement character instead of the trailing rune.
			end := 397
			for end > 0 && !utf8.RuneStart(p[end]) {
				end--
			}
			p = p[:end] + "..."
		}
		return p
	}
	return ""
}
