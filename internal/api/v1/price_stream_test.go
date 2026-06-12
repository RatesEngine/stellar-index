package v1_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/api/streaming"
	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestPriceStream_NoHub_Returns503 — the endpoint is mounted but
// returns 503 until a Hub is wired (typical pre-aggregator state).
func TestPriceStream_NoHub_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/price/stream?asset=native&quote=fiat:USD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "stream-unavailable") {
		t.Errorf("error type missing: %s", body)
	}
}

// TestPriceStream_RejectsGranularity — closed-bucket stream is fixed
// at 1m; ?granularity= returns 400.
func TestPriceStream_RejectsGranularity(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := v1.New(v1.Options{Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/price/stream?asset=native&quote=fiat:USD&granularity=1m")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "invalid-stream-param") {
		t.Errorf("error type missing: %s", body)
	}
}

func TestPriceStream_MissingAsset_Returns400(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := v1.New(v1.Options{Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/price/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestPriceStream_HubPublishReachesSubscriber — drive the full path:
// connect, publish into the Hub on the right topic, observe the
// SSE frame on the wire.
func TestPriceStream_HubPublishReachesSubscriber(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := v1.New(v1.Options{Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	topic := v1.PriceStreamTopic(xlm, usd)

	// Open the stream. The handler subscribes synchronously before
	// calling streaming.Stream's loop.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/price/stream?asset=native&quote=fiat:USD", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Brief sleep for the handler's Subscribe to register before we
	// publish — without this, the publish can race and reach the
	// buffer alone without a live subscriber.
	time.Sleep(50 * time.Millisecond)

	hub.Publish(topic, "price_update", []byte(`{"price":"0.42"}`))

	br := bufio.NewReader(resp.Body)
	frame := readPriceStreamFrame(t, br, 2*time.Second)
	if frame == "" {
		t.Fatal("no frame received")
	}
	if !strings.Contains(frame, `data: {"price":"0.42"}`) {
		t.Errorf("frame missing payload: %q", frame)
	}
	if !strings.Contains(frame, "event: price_update\n") {
		t.Errorf("frame missing event type: %q", frame)
	}
	if !strings.Contains(frame, "id: ") {
		t.Errorf("frame missing id: %q", frame)
	}
}

// TestPriceStream_TopicIsolation — a publish on a DIFFERENT pair's
// topic doesn't reach this subscriber. Sanity check that the handler
// computes the topic key correctly per (asset, quote).
func TestPriceStream_TopicIsolation(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := v1.New(v1.Options{Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	usdc, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	otherTopic := v1.PriceStreamTopic(usdc, usd) + "-different-pair-suffix"

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/price/stream?asset=native&quote=fiat:USD", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	time.Sleep(50 * time.Millisecond)
	hub.Publish(otherTopic, "price_update", []byte(`{"x":1}`))

	br := bufio.NewReader(resp.Body)
	frame := readPriceStreamFrame(t, br, 600*time.Millisecond)
	if frame != "" {
		t.Errorf("subscriber received foreign-topic event: %q", frame)
	}
}

// TestPriceStream_LastEventIDResume — publish 3 events into the Hub
// before any subscriber connects, then connect with Last-Event-ID
// pointing at the first one. Subscriber sees only the 2 newer events
// replayed from the buffer.
func TestPriceStream_LastEventIDResume(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := v1.New(v1.Options{Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	topic := v1.PriceStreamTopic(xlm, usd)

	id1 := hub.Publish(topic, "price_update", []byte(`{"p":"0.10"}`))
	hub.Publish(topic, "price_update", []byte(`{"p":"0.11"}`))
	hub.Publish(topic, "price_update", []byte(`{"p":"0.12"}`))

	req, _ := http.NewRequest(http.MethodGet,
		ts.URL+"/v1/price/stream?asset=native&quote=fiat:USD", nil)
	req.Header.Set("Last-Event-ID", id1) // resume after first
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	frames := make([]string, 0, 2)
	for len(frames) < 2 {
		f := readPriceStreamFrame(t, br, 2*time.Second)
		if f == "" {
			break
		}
		frames = append(frames, f)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2 (replay of 0.11 + 0.12)", len(frames))
	}
	if !strings.Contains(frames[0], "0.11") {
		t.Errorf("first replay frame = %q, want 0.11", frames[0])
	}
	if !strings.Contains(frames[1], "0.12") {
		t.Errorf("second replay frame = %q, want 0.12", frames[1])
	}
}

// readPriceStreamFrame reads one full SSE frame (id/event/data
// terminated by a blank line) from body, skipping comment lines.
// Returns "" on EOF or timeout.
func readPriceStreamFrame(t *testing.T, br *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var sb strings.Builder
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "\n" {
			if sb.Len() > 0 {
				return sb.String()
			}
			continue
		}
		sb.WriteString(line)
	}
	return sb.String()
}
