package v1_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// startObservationsStreamServer wires a v1.Server with the given
// History reader behind an httptest.Server.
func startObservationsStreamServer(t *testing.T, history v1.HistoryReader) string {
	t.Helper()
	srv := v1.New(v1.Options{History: history})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestObservationsStream_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/observations/stream?asset=native&quote=fiat:USD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestObservationsStream_RejectsTierParams — same URL discipline as
// the request endpoint: ?granularity= (closed-bucket) and
// ?window_seconds= (tip) are 400s.
func TestObservationsStream_RejectsTierParams(t *testing.T) {
	url := startObservationsStreamServer(t, &stubHistoryReader{})
	for _, q := range []string{"granularity=1m", "window_seconds=5"} {
		resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&" + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("query %q → status %d, want 400", q, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// TestObservationsStream_InvalidIntervalSeconds — the stream-specific
// interval_seconds knob has its own [1, 60] clamp.
func TestObservationsStream_InvalidIntervalSeconds(t *testing.T) {
	url := startObservationsStreamServer(t, &stubHistoryReader{})
	for _, raw := range []string{"0", "61", "abc"} {
		resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&interval_seconds=" + raw)
		if err != nil {
			t.Fatalf("GET %s: %v", raw, err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("interval_seconds=%q → %d, want 400", raw, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// TestObservationsStream_EmptyArrayInitialEvent — observations
// returns 200 + [] when the pair has no trades; the stream mirrors
// this (does NOT 404). First emitted event has empty data.
func TestObservationsStream_EmptyArrayInitialEvent(t *testing.T) {
	url := startObservationsStreamServer(t, &stubHistoryReader{observations: nil})

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&interval_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if data == "" {
		t.Fatal("no event received")
	}
	if !strings.Contains(data, `"data":[]`) {
		t.Errorf("expected empty data array: %s", data)
	}
	if !strings.Contains(data, `"stale":false`) {
		t.Errorf("stale should be false: %s", data)
	}
}

// TestObservationsStream_HappyPathInitialEvent — populated history
// flows through the initial emit. Multiple sources → single_source
// flag is NOT set (omitempty + false).
func TestObservationsStream_HappyPathInitialEvent(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", now.Add(-2*time.Second), 1, 100),
			mkObservationTrade("phoenix", now.Add(-5*time.Second), 2, 250),
		},
	}
	url := startObservationsStreamServer(t, hist)

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&interval_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	for _, want := range []string{`"source":"soroswap"`, `"source":"phoenix"`} {
		if !strings.Contains(data, want) {
			t.Errorf("payload missing %q: %s", want, data)
		}
	}
}

// TestObservationsStream_SourceFilter — ?source= narrows output the
// same way as the request endpoint, and the reader receives the
// filter string.
func TestObservationsStream_SourceFilter(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", now.Add(-2*time.Second), 1, 100),
			mkObservationTrade("phoenix", now.Add(-5*time.Second), 2, 250),
		},
	}
	url := startObservationsStreamServer(t, hist)

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&source=phoenix&interval_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if !strings.Contains(data, `"source":"phoenix"`) {
		t.Errorf("expected phoenix: %s", data)
	}
	if strings.Contains(data, `"source":"soroswap"`) {
		t.Errorf("source filter leaked: %s", data)
	}
	if hist.lastCall.sourceFilter != "phoenix" {
		t.Errorf("reader sourceFilter = %q, want phoenix", hist.lastCall.sourceFilter)
	}
}

// TestObservationsStream_AggregateLatest — collapse to single newest
// trade applies on the stream too.
func TestObservationsStream_AggregateLatest(t *testing.T) {
	base := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", base.Add(-10*time.Second), 1, 100),
			mkObservationTrade("phoenix", base.Add(-1*time.Second), 2, 250), // newest
			mkObservationTrade("sdex", base.Add(-5*time.Second), 1, 105),
		},
	}
	url := startObservationsStreamServer(t, hist)

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&aggregate=latest&interval_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if !strings.Contains(data, `"source":"phoenix"`) {
		t.Errorf("collapse picked wrong source: %s", data)
	}
	if strings.Contains(data, `"source":"soroswap"`) || strings.Contains(data, `"source":"sdex"`) {
		t.Errorf("collapse leaked older sources: %s", data)
	}
}

// TestObservationsStream_AggregateInvalid — non-empty, non-"latest"
// is a 400 BEFORE the response switches to SSE.
func TestObservationsStream_AggregateInvalid(t *testing.T) {
	url := startObservationsStreamServer(t, &stubHistoryReader{})
	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&aggregate=median")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestObservationsStream_TickEmitsRepeatedly — interval_seconds=1
// produces multiple events in quick succession.
func TestObservationsStream_TickEmitsRepeatedly(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{mkObservationTrade("soroswap", now, 1, 100)},
	}
	url := startObservationsStreamServer(t, hist)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		url+"/v1/observations/stream?asset=native&quote=fiat:USD&interval_seconds=1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	const want = 2
	got := 0
	for got < want {
		ev := readTipStreamEvent(t, br, 2500*time.Millisecond)
		if ev == "" {
			break
		}
		got++
	}
	if got < want {
		t.Errorf("got %d events, want >= %d", got, want)
	}
}

// TestObservationsStream_PreFlightInternalError — a reader error on
// the synchronous first compute returns 500 (problem+json) before
// switching to SSE.
func TestObservationsStream_PreFlightInternalError(t *testing.T) {
	hist := &stubHistoryReader{err: errors.New("hypertable timeout")}
	url := startObservationsStreamServer(t, hist)

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if strings.Contains(string(body[:n]), "hypertable timeout") {
		t.Errorf("internal error leaked: %s", body[:n])
	}
}

// TestObservationsStream_PayloadJSONIsValid — sanity-decode the data
// line as JSON and confirm the envelope keys.
func TestObservationsStream_PayloadJSONIsValid(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{mkObservationTrade("soroswap", now, 1, 100)},
	}
	url := startObservationsStreamServer(t, hist)

	resp, err := http.Get(url + "/v1/observations/stream?asset=native&quote=fiat:USD&interval_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if data == "" {
		t.Fatal("no event")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("payload not valid JSON: %v\nraw: %s", err, data)
	}
	for _, key := range []string{"data", "as_of", "flags"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("payload missing %q: %s", key, data)
		}
	}
	if _, ok := parsed["data"].([]any); !ok {
		t.Errorf("data should be an array: %T", parsed["data"])
	}
}
