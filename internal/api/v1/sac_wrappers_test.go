package v1_test

import (
	"net/http"
	"strings"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
)

// TestSACWrappers_EmptyMapWhenUnconfigured pins the
// "deployment without [supply.sac_wrappers]" path: handler
// returns `data: {}` not `data: null`. The explorer reads this
// at AssetLabel resolve-time; a null payload would JS-error
// the table render. The handler degrades to an empty map
// when opts.SACWrappers is nil.
func TestSACWrappers_EmptyMapWhenUnconfigured(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/sac-wrappers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	// Empty object — `{}` not `null`. Anchored on the bracket pair
	// so a regression that ships `null` is caught.
	if !strings.Contains(body, `"data":{}`) {
		t.Errorf("expected `\"data\":{}` in body, got: %s", body)
	}
}

// TestSACWrappers_ReturnsConfiguredMap pins the wire shape with
// real entries — keys are SAC C-strkey contract IDs, values are
// underlying classic asset keys ("CODE-ISSUER" form).
func TestSACWrappers_ReturnsConfiguredMap(t *testing.T) {
	wrappers := map[string]string{
		"CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA": "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
		"CALMVMRX7N2P22NRKK5EI3FZ4OGKDDYGCV3RVQ6UPCFWMYDPSCQHFJDF": "AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA",
	}
	srv := v1.New(v1.Options{SACWrappers: wrappers})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/sac-wrappers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA":"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"`,
		`"CALMVMRX7N2P22NRKK5EI3FZ4OGKDDYGCV3RVQ6UPCFWMYDPSCQHFJDF":"AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestSACWrappers_PostMethodNotAllowed — the surface is GET-only;
// non-GET methods should reach the mux's 405 handler (rewritten to
// problem+json by the Envelope404 middleware). Pin it so a future
// route change doesn't accidentally accept POSTs.
func TestSACWrappers_PostMethodNotAllowed(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/sac-wrappers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
