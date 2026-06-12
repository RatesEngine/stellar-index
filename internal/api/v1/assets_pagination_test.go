package v1_test

import (
	"context"
	"net/http"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// paginatingCoinsReader embeds the full stub and overrides only
// ListCoinsExt, honouring opts.Limit by returning min(Limit, total)
// rows so the handler's overfetch-by-one logic is exercised exactly as
// the real store would drive it.
type paginatingCoinsReader struct {
	stubCoinsReaderExt
	total int
}

func (p *paginatingCoinsReader) ListCoinsExt(_ context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	n := opts.Limit
	if n > p.total {
		n = p.total
	}
	rows := make([]timescale.CoinRow, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, timescale.CoinRow{
			AssetID:          "USDC-GAAA",
			Slug:             "usdc",
			Code:             "USDC",
			ObservationCount: int64(i + 1),
		})
	}
	return rows, nil
}

// TestAssetList_CoinsPaginationEmitsCursor pins F-1326: when the coins
// catalogue holds more than `limit` rows, /v1/assets MUST emit a next
// cursor. The previous handler passed `limit` (not limit+1) to the
// store, so the overfetch sentinel never appeared and the listing was
// stuck on its first page over a ~440K-asset directory.
func TestAssetList_CoinsPaginationEmitsCursor(t *testing.T) {
	srv := v1.New(v1.Options{Coins: &paginatingCoinsReader{total: 1000}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets?limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data       []v1.AssetDetail `json:"data"`
		Pagination *struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data) != 50 {
		t.Fatalf("returned %d rows, want exactly the page size 50 (overfetch row must be trimmed)", len(env.Data))
	}
	if env.Pagination == nil || env.Pagination.Next == "" {
		t.Fatalf("no next cursor emitted despite 1000 > 50 rows available (F-1326)")
	}
}

// TestAssetList_CoinsPaginationLastPageNoCursor confirms the tail page
// (rows ≤ limit) correctly omits the cursor.
func TestAssetList_CoinsPaginationLastPageNoCursor(t *testing.T) {
	srv := v1.New(v1.Options{Coins: &paginatingCoinsReader{total: 30}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets?limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data       []v1.AssetDetail `json:"data"`
		Pagination *struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data) != 30 {
		t.Fatalf("returned %d rows, want 30", len(env.Data))
	}
	if env.Pagination != nil && env.Pagination.Next != "" {
		t.Fatalf("unexpected next cursor on the final page: %q", env.Pagination.Next)
	}
}
