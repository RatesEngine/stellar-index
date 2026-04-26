package bitstamp

import "testing"

func TestRestBase_emptyEndpointFallsBackToREST(t *testing.T) {
	s := &Streamer{}
	if got := s.restBase(); got != RESTEndpoint {
		t.Errorf("restBase() = %q, want %q", got, RESTEndpoint)
	}
}

func TestRestBase_wsEndpointFallsBackToREST(t *testing.T) {
	for _, prefix := range []string{"ws://example.com", "wss://example.com"} {
		s := &Streamer{Endpoint: prefix}
		if got := s.restBase(); got != RESTEndpoint {
			t.Errorf("restBase(%q) = %q, want %q", prefix, got, RESTEndpoint)
		}
	}
}

func TestRestBase_httpEndpointPassesThrough(t *testing.T) {
	s := &Streamer{Endpoint: "http://127.0.0.1:9999"}
	if got := s.restBase(); got != "http://127.0.0.1:9999" {
		t.Errorf("restBase() = %q, want passthrough", got)
	}
}
