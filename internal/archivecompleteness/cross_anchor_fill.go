package archivecompleteness

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Source is one upstream URL from which a missing checkpoint file
// can be fetched. The full URL we GET is
// `<URL>/ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz`.
type Source struct {
	// Name is a stable label used in metrics + logs (e.g.
	// "sdf-core-live-001"). Stays in lockstep with the
	// archive_completeness_repair_attempts_total metric label.
	Name string

	// URL is the source's archive root. Trailing slashes are
	// stripped; we append the standard
	// `ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz` path under it.
	URL string
}

// DefaultCrossAnchorSources returns the canonical fallback chain
// for the cross-anchor archive: SDF's three primary archives, then
// the tier-1 validator archives the captive-core config already
// trusts. The order matches what the bash `cross-anchor-fill`
// script used during the 2026-04-28 bootstrap.
func DefaultCrossAnchorSources() []Source {
	return []Source{
		{Name: "sdf-core-live-001", URL: "https://history.stellar.org/prd/core-live/core_live_001"},
		{Name: "sdf-core-live-002", URL: "https://history.stellar.org/prd/core-live/core_live_002"},
		{Name: "sdf-core-live-003", URL: "https://history.stellar.org/prd/core-live/core_live_003"},
		{Name: "publicnode-bootes", URL: "https://bootes-history.publicnode.org"},
		{Name: "publicnode-lyra", URL: "https://lyra-history.publicnode.org"},
		{Name: "publicnode-hercules", URL: "https://hercules-history.publicnode.org"},
		{Name: "lobstr-v1", URL: "https://archive.v1.stellar.lobstr.co"},
		{Name: "lobstr-v2", URL: "https://archive.v2.stellar.lobstr.co"},
		{Name: "lobstr-v5", URL: "https://archive.v5.stellar.lobstr.co"},
	}
}

// CrossAnchorFiller fetches missing checkpoint files from the
// multi-source fallback chain and writes them into the local
// archive at the SDF history-archive layout.
//
// Concurrency: Fill spawns Workers goroutines internally. Concurrent
// Fill calls on the same Filler are safe but each grabs the same
// worker pool size — typically you call Fill once per check cycle
// and let it drain.
type CrossAnchorFiller struct {
	archiveRoot  string
	sources      []Source
	httpClient   *http.Client
	workers      int
	ownerUID     int
	ownerGID     int
	chownEnabled bool
}

// FillerOptions configures a [CrossAnchorFiller] at construction.
type FillerOptions struct {
	// ArchiveRoot is the SDF-layout archive root we write into.
	// Same path the cross-anchor checker scans (typically
	// `/srv/history-archive`).
	ArchiveRoot string

	// Sources is the ordered fallback chain. Empty slice falls
	// back to [DefaultCrossAnchorSources].
	Sources []Source

	// HTTPClient is the transport for source fetches. Nil falls
	// back to a client with a 30s per-request timeout. Operators
	// can pass a tuned client for tighter timeouts or for
	// mocking in tests.
	HTTPClient *http.Client

	// Workers controls fetch parallelism. 0 falls back to 8
	// (matches the bash script's PARALLEL default that empirically
	// avoided Cloudflare rate-limiting during the bootstrap).
	Workers int

	// OwnerUser / OwnerGroup are the local user / group that
	// should own each placed file. Empty strings disable chown
	// (placed files own to the running process). Use
	// stellar:stellar in production so subsequent reads by
	// stellar-archivist see the right ownership.
	OwnerUser  string
	OwnerGroup string
}

// NewCrossAnchorFiller constructs a Filler. Returns an error if
// chown is requested but the user/group lookup fails (operator
// config issue — fail loud at startup).
func NewCrossAnchorFiller(opts FillerOptions) (*CrossAnchorFiller, error) {
	if opts.ArchiveRoot == "" {
		return nil, errors.New("archivecompleteness: ArchiveRoot is required")
	}
	st, err := os.Stat(opts.ArchiveRoot)
	if err != nil {
		return nil, fmt.Errorf("archivecompleteness: stat ArchiveRoot %q: %w", opts.ArchiveRoot, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("archivecompleteness: ArchiveRoot %q is not a directory", opts.ArchiveRoot)
	}

	sources := opts.Sources
	if len(sources) == 0 {
		sources = DefaultCrossAnchorSources()
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 8
	}

	f := &CrossAnchorFiller{
		archiveRoot: opts.ArchiveRoot,
		sources:     sources,
		httpClient:  httpClient,
		workers:     workers,
	}

	if opts.OwnerUser != "" {
		u, err := user.Lookup(opts.OwnerUser)
		if err != nil {
			return nil, fmt.Errorf("archivecompleteness: lookup user %q: %w", opts.OwnerUser, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return nil, fmt.Errorf("archivecompleteness: parse uid for %q: %w", opts.OwnerUser, err)
		}
		f.ownerUID = uid
		f.chownEnabled = true
	}
	if opts.OwnerGroup != "" {
		g, err := user.LookupGroup(opts.OwnerGroup)
		if err != nil {
			return nil, fmt.Errorf("archivecompleteness: lookup group %q: %w", opts.OwnerGroup, err)
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			return nil, fmt.Errorf("archivecompleteness: parse gid for %q: %w", opts.OwnerGroup, err)
		}
		f.ownerGID = gid
		f.chownEnabled = true
	}
	return f, nil
}

// FillResult summarises the outcome of [CrossAnchorFiller.Fill].
type FillResult struct {
	// Filled is the count of checkpoints successfully placed.
	Filled int

	// Failed lists the checkpoint sequences that exhausted every
	// source without a successful fetch. Caller should log these
	// at WARN level and either retry later or escalate.
	Failed []FillFailure

	// PerSourceSuccess counts successful fetches by source name.
	// Helps operators see which upstream is healthy vs degraded.
	PerSourceSuccess map[string]int
}

// FillFailure describes one missing checkpoint that the filler
// couldn't recover.
type FillFailure struct {
	Seq    uint32
	Reason string
}

// Fill iterates the supplied missing-checkpoint list and places
// each via the multi-source fallback chain. Returns when every
// entry has either succeeded or exhausted the chain.
//
// For each checkpoint:
//
//  1. Shuffle the source order (spreads burst load across SDF /
//     tier-1 validators rather than hammering source[0] first).
//  2. Walk the shuffled chain. The first source returning 200 +
//     non-empty body + valid gzip wins; place it atomically via
//     `<final>.new` → `<final>` rename.
//  3. If every source fails, record a [FillFailure] with the last
//     error message.
//
// Cancellation: Fill respects ctx.Done() between checkpoints — a
// cancellation mid-walk leaves placed files intact (idempotent
// next run will skip them) but stops fetching new ones.
func (f *CrossAnchorFiller) Fill(ctx context.Context, missing []uint32) FillResult {
	if len(missing) == 0 {
		return FillResult{PerSourceSuccess: map[string]int{}}
	}

	type job struct{ seq uint32 }
	jobs := make(chan job, f.workers)

	var (
		mu        sync.Mutex
		filled    int
		failures  []FillFailure
		perSource = map[string]int{}
	)

	var wg sync.WaitGroup
	for i := 0; i < f.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID))) //nolint:gosec // shuffling for load-spread; not security-relevant
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				if sourceName, err := f.fetchOne(ctx, j.seq, rng); err == nil {
					mu.Lock()
					filled++
					perSource[sourceName]++
					mu.Unlock()
				} else {
					mu.Lock()
					failures = append(failures, FillFailure{Seq: j.seq, Reason: err.Error()})
					mu.Unlock()
				}
			}
		}(i)
	}

	for _, seq := range missing {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return FillResult{Filled: filled, Failed: failures, PerSourceSuccess: perSource}
		case jobs <- job{seq: seq}:
		}
	}
	close(jobs)
	wg.Wait()

	return FillResult{Filled: filled, Failed: failures, PerSourceSuccess: perSource}
}

// fetchOne fetches one checkpoint via the shuffled fallback chain.
// Returns the source name on success or an error describing the
// last failure.
func (f *CrossAnchorFiller) fetchOne(ctx context.Context, seq uint32, rng *rand.Rand) (string, error) {
	hex := hexSeq8(seq)
	relPath := fmt.Sprintf("ledger/%s/%s/%s/ledger-%s.xdr.gz", hex[0:2], hex[2:4], hex[4:6], hex)
	finalPath := filepath.Join(f.archiveRoot, relPath)
	tmpPath := finalPath + ".new"

	// Ensure the parent directory exists BEFORE the first GET —
	// the bash-script bug from 2026-04-28 was placing curl -o
	// into a non-existent dir, which fails fast even when the
	// HTTP source is healthy.
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return "", fmt.Errorf("mkdir parent: %w", err)
	}

	shuffled := make([]Source, len(f.sources))
	copy(shuffled, f.sources)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	var lastErr error
	for _, src := range shuffled {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		err := f.fetchAndValidate(ctx, src, relPath, tmpPath)
		if err == nil {
			if err := os.Rename(tmpPath, finalPath); err != nil {
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("rename %q → %q: %w", tmpPath, finalPath, err)
			}
			f.applyOwnership(finalPath)
			return src.Name, nil
		}
		_ = os.Remove(tmpPath)
		lastErr = fmt.Errorf("%s: %w", src.Name, err)
	}
	if lastErr == nil {
		lastErr = errors.New("no sources configured")
	}
	return "", lastErr
}

// fetchAndValidate performs one source GET and writes the response
// to tmpPath atomically. Validates gzip integrity before returning
// nil. On any failure leaves tmpPath in an unspecified state; the
// caller is responsible for cleanup.
func (f *CrossAnchorFiller) fetchAndValidate(ctx context.Context, src Source, relPath, tmpPath string) error {
	url := src.URL + "/" + relPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drain a little of the body for diagnostics; bound it.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	// Write to tmpPath. Cap size at 16 MiB — checkpoint files are
	// ~5–250 KiB per the bootstrap data; anything bigger is
	// suspicious and we'd rather fail than fill the disk.
	const maxSize = 16 << 20
	out, err := os.Create(tmpPath) //nolint:gosec // path constructed from validated archiveRoot + checkpoint hex
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	written, err := io.Copy(out, io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if written > maxSize {
		_ = out.Close()
		return fmt.Errorf("response too large (>%d bytes)", maxSize)
	}
	if written == 0 {
		_ = out.Close()
		return errors.New("empty response body")
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	// gzip-validate the placed file. Cheap insurance against a
	// truncated download or a 200-with-html-body case.
	if err := validateGzip(tmpPath); err != nil {
		return fmt.Errorf("gzip validate: %w", err)
	}
	return nil
}

// maxDecompressedBytes caps the decompressed size we accept during
// gzip validation. Stellar history-archive ledger checkpoint files
// hold 64 LedgerHeaderHistoryEntry records (~250 bytes each) → ~16 KB
// decompressed in practice. 4 MiB is generous for the legitimate
// case while preventing zip-bomb DoS.
const maxDecompressedBytes = 4 << 20

// validateGzip reads the file, attempts to decompress it, and
// confirms the gzip footer is intact. Returns nil if valid.
//
// Decompression is bounded to [maxDecompressedBytes] to prevent
// a malicious source from sending a tiny compressed payload that
// decompresses to gigabytes (G110 zip-bomb DoS).
func validateGzip(path string) error {
	f, err := os.Open(path) //nolint:gosec // path constructed from validated archiveRoot
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	// Cap the decompressed read; +1 so we can detect when the
	// limit was exceeded vs hit exactly.
	limited := io.LimitReader(gz, maxDecompressedBytes+1)
	n, err := io.Copy(io.Discard, limited)
	if err != nil {
		return err
	}
	if n > maxDecompressedBytes {
		return fmt.Errorf("decompressed size > %d bytes (zip-bomb guard)", maxDecompressedBytes)
	}
	return nil
}

// applyOwnership chowns the placed file when chown is enabled.
// Best-effort — a failure here logs but doesn't fail the fetch
// (the bytes are correct; ownership can be fixed by a follow-up
// chown -R).
func (f *CrossAnchorFiller) applyOwnership(path string) {
	if !f.chownEnabled {
		return
	}
	_ = os.Chown(path, f.ownerUID, f.ownerGID)
}
