package v1_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// testServerImpl wraps httptest.Server so asset + server tests
// share one construction path.
type testServerImpl struct {
	URL string
	ts  *httptest.Server
}

func startHTTPTest(t *testing.T, h http.Handler) *testServerImpl {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &testServerImpl{URL: ts.URL, ts: ts}
}
