package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

type fakeMarketsReader struct {
	allPoolsCalls atomic.Int64
	delay         time.Duration
	err           error
}

func (f *fakeMarketsReader) DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]Pool, string, error) {
	f.allPoolsCalls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	if f.err != nil {
		return nil, "", f.err
	}
	return []Pool{{Source: "aquarius", Base: "native"}}, "", nil
}

func (f *fakeMarketsReader) PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error) {
	return Market{}, false, nil
}

func (f *fakeMarketsReader) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return nil, nil
}

func TestCachedMarketsReader_AllPoolsCachesByKey(t *testing.T) {
	up := &fakeMarketsReader{}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	for i := 0; i < 4; i++ {
		_, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := up.allPoolsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times for same key; want 1", got)
	}
}

func TestCachedMarketsReader_AllPoolsDifferentKeys(t *testing.T) {
	up := &fakeMarketsReader{}
	c := NewCachedMarketsReader(up, 60*time.Second)

	_, _, _ = c.AllPools(context.Background(),
		timescale.PoolsFilter{Sources: []string{"aquarius"}}, "", 50, timescale.MarketsOrderVolume24hDesc)
	_, _, _ = c.AllPools(context.Background(),
		timescale.PoolsFilter{Sources: []string{"phoenix"}}, "", 50, timescale.MarketsOrderVolume24hDesc)
	if got := up.allPoolsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times across 2 distinct keys; want 2", got)
	}
}

func TestCachedMarketsReader_AllPoolsSingleFlight(t *testing.T) {
	up := &fakeMarketsReader{delay: 100 * time.Millisecond}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
		}()
	}
	wg.Wait()

	if got := up.allPoolsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times under single-flight; want 1", got)
	}
}

func TestCachedMarketsReader_AllPoolsErrorIsNotCached(t *testing.T) {
	up := &fakeMarketsReader{err: errors.New("db down")}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	_, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
	if err == nil {
		t.Fatal("first call: want error")
	}
	up.err = nil
	_, _, err = c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := up.allPoolsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (error wasn't cached)", got)
	}
}
