// Package incidents loads + parses customer-facing incident
// post-mortems from `internal/incidents/data/*.md` (embedded at
// build time via go:embed) and exposes the parsed list to API
// handlers.
//
// The directory is the source of truth for the incidents shown
// on status.ratesengine.net. Files are checked in; this package
// is read-only at runtime — there is no admin write path.
//
// File naming follows `<YYYY-MM-DD>-<short-slug>.md`. Files
// starting with `_` are conventional templates / scratch and are
// skipped at parse time. Files that fail YAML frontmatter
// validation (missing required keys, malformed types) are dropped
// with a log entry but never panic the binary — one bad post
// shouldn't break the rest of the feed.
package incidents

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.md
var contentFS embed.FS

// Severity matches the SEV-1/2/3 convention from
// docs/operations/sev-playbook.md. Customer-facing only — internal
// SEV-A/B/C ladders aren't exposed.
type Severity string

const (
	SeverityMajor       Severity = "SEV-1"
	SeverityMinor       Severity = "SEV-2"
	SeverityInformative Severity = "SEV-3"
)

// Status tracks the customer-facing lifecycle of an incident.
// Mirrors the fields used by Statuspage and similar tools so
// status-page UIs can render familiar pills.
type Status string

const (
	StatusInvestigating Status = "investigating"
	StatusIdentified    Status = "identified"
	StatusMonitoring    Status = "monitoring"
	StatusResolved      Status = "resolved"
)

// Incident is one customer-facing incident post.
type Incident struct {
	Slug               string     `json:"slug"`
	Title              string     `json:"title"`
	Severity           Severity   `json:"severity"`
	Status             Status     `json:"status"`
	StartedAt          time.Time  `json:"started_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	AffectedComponents []string   `json:"affected_components,omitempty"`
	PostmortemRef      string     `json:"postmortem,omitempty"`
	BodyMarkdown       string     `json:"body_markdown"`
}

// rawFrontmatter is the YAML shape we parse out of each file's
// `---`-delimited header. Strings everywhere; conversion to
// typed fields happens after parse so a bad date doesn't fail
// the YAML pass.
type rawFrontmatter struct {
	Title              string   `yaml:"title"`
	Severity           string   `yaml:"severity"`
	Status             string   `yaml:"status"`
	StartedAt          string   `yaml:"started_at"`
	ResolvedAt         string   `yaml:"resolved_at"`
	AffectedComponents []string `yaml:"affected_components"`
	Postmortem         string   `yaml:"postmortem"`
	Date               string   `yaml:"date"`
}

// Load parses every `data/*.md` file in the embedded directory and
// returns the resulting slice sorted by `started_at` descending
// (most recent first). Files prefixed with `_` are skipped.
//
// Errors on individual files are logged + skipped; the function
// returns successfully whenever the embedded FS is readable.
func Load(logger *slog.Logger) ([]Incident, error) {
	if logger == nil {
		logger = slog.Default()
	}
	entries, err := contentFS.ReadDir("data")
	if err != nil {
		return nil, fmt.Errorf("incidents: read embedded dir: %w", err)
	}

	var out []Incident
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "_") {
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		inc, err := parseFile(name)
		if err != nil {
			logger.Warn("incidents: skip malformed post",
				"file", name, "err", err)
			continue
		}
		out = append(out, inc)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// parseFile reads `data/<name>` and returns one Incident. The body
// is everything after the closing `---` line.
func parseFile(name string) (Incident, error) {
	raw, err := fs.ReadFile(contentFS, "data/"+name)
	if err != nil {
		return Incident{}, fmt.Errorf("read: %w", err)
	}

	frontYAML, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return Incident{}, fmt.Errorf("split: %w", err)
	}

	var fm rawFrontmatter
	if err := yaml.Unmarshal([]byte(frontYAML), &fm); err != nil {
		return Incident{}, fmt.Errorf("yaml: %w", err)
	}

	startedAt, err := parseTimestamp(fm.StartedAt, fm.Date)
	if err != nil {
		return Incident{}, fmt.Errorf("started_at: %w", err)
	}
	var resolvedAt *time.Time
	if fm.ResolvedAt != "" {
		t, err := parseTimestamp(fm.ResolvedAt, "")
		if err == nil {
			resolvedAt = &t
		}
	}

	slug := strings.TrimSuffix(name, ".md")
	return Incident{
		Slug:               slug,
		Title:              strings.TrimSpace(fm.Title),
		Severity:           Severity(fm.Severity),
		Status:             Status(fm.Status),
		StartedAt:          startedAt,
		ResolvedAt:         resolvedAt,
		AffectedComponents: fm.AffectedComponents,
		PostmortemRef:      strings.TrimSpace(fm.Postmortem),
		BodyMarkdown:       strings.TrimSpace(body),
	}, nil
}

// splitFrontmatter splits a markdown file with YAML frontmatter
// delimited by `---` lines. Returns (frontYAML, body, err).
// File MUST start with `---` on its own line; otherwise we treat
// it as bodyless garbage and return an error.
func splitFrontmatter(src string) (string, string, error) {
	scanner := bufio.NewScanner(strings.NewReader(src))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var (
		state       = "expect-open"
		front, body strings.Builder
		seenClose   bool
	)
	for scanner.Scan() {
		line := scanner.Text()
		switch state {
		case "expect-open":
			if strings.TrimSpace(line) != "---" {
				return "", "", fmt.Errorf("missing opening --- delimiter")
			}
			state = "in-front"
		case "in-front":
			if strings.TrimSpace(line) == "---" {
				state = "in-body"
				seenClose = true
				continue
			}
			front.WriteString(line)
			front.WriteByte('\n')
		case "in-body":
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if !seenClose {
		return "", "", fmt.Errorf("missing closing --- delimiter")
	}
	return front.String(), body.String(), nil
}

// parseTimestamp accepts RFC 3339 first, then falls back to a
// date-only YYYY-MM-DD (treated as midnight UTC). The fallback
// argument lets the parser pick up `date:` from frontmatter when
// `started_at:` is missing — older posts may only have one.
func parseTimestamp(primary, fallback string) (time.Time, error) {
	tries := []string{primary, fallback}
	for _, s := range tries {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), nil
		}
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("no parseable timestamp in %q / %q", primary, fallback)
}
