package ledgerstream

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/support/datastore"
)

// fakeStore is a minimal DataStore used to assert TieredDataStore's
// fallback semantics. Only the methods exercised by the tests are
// implemented meaningfully; the rest return ErrNotImplemented so
// any new test path that depends on them fails loudly.
type fakeStore struct {
	name      string
	files     map[string]string // path → body
	listPaths []string          // ListFilePaths result
	listErr   error             // ListFilePaths error
	getErr    map[string]error  // override GetFile errors per path
	calls     map[string]int    // per-method call counter
	exists    map[string]bool   // Exists override; empty falls back to files
	metaData  map[string]map[string]string
}

func newFakeStore(name string) *fakeStore {
	return &fakeStore{
		name:     name,
		files:    map[string]string{},
		getErr:   map[string]error{},
		calls:    map[string]int{},
		exists:   map[string]bool{},
		metaData: map[string]map[string]string{},
	}
}

func (f *fakeStore) bump(method string) { f.calls[method]++ }

func (f *fakeStore) GetFile(_ context.Context, path string) (io.ReadCloser, int64, error) {
	f.bump("GetFile")
	if err, ok := f.getErr[path]; ok {
		return nil, 0, err
	}
	body, ok := f.files[path]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(body)), int64(len(body)), nil
}

func (f *fakeStore) GetFileMetadata(_ context.Context, path string) (map[string]string, error) {
	f.bump("GetFileMetadata")
	if md, ok := f.metaData[path]; ok {
		return md, nil
	}
	if _, ok := f.files[path]; !ok {
		return nil, os.ErrNotExist
	}
	return map[string]string{}, nil
}

func (f *fakeStore) GetFileLastModified(_ context.Context, path string) (time.Time, error) {
	f.bump("GetFileLastModified")
	if _, ok := f.files[path]; !ok {
		return time.Time{}, os.ErrNotExist
	}
	return time.Unix(1700000000, 0), nil
}

func (f *fakeStore) Exists(_ context.Context, path string) (bool, error) {
	f.bump("Exists")
	if v, ok := f.exists[path]; ok {
		return v, nil
	}
	_, ok := f.files[path]
	return ok, nil
}

func (f *fakeStore) Size(_ context.Context, path string) (int64, error) {
	f.bump("Size")
	body, ok := f.files[path]
	if !ok {
		return 0, os.ErrNotExist
	}
	return int64(len(body)), nil
}

func (f *fakeStore) ListFilePaths(_ context.Context, _ datastore.ListFileOptions) ([]string, error) {
	f.bump("ListFilePaths")
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.listPaths...), nil
}

func (f *fakeStore) PutFile(_ context.Context, path string, in io.WriterTo, _ map[string]string) error {
	f.bump("PutFile")
	var sb strings.Builder
	if _, err := in.WriteTo(&sb); err != nil {
		return err
	}
	f.files[path] = sb.String()
	return nil
}

func (f *fakeStore) PutFileIfNotExists(_ context.Context, path string, in io.WriterTo, _ map[string]string) (bool, error) {
	f.bump("PutFileIfNotExists")
	if _, exists := f.files[path]; exists {
		return false, nil
	}
	var sb strings.Builder
	if _, err := in.WriteTo(&sb); err != nil {
		return false, err
	}
	f.files[path] = sb.String()
	return true, nil
}

func (f *fakeStore) Close() error { f.bump("Close"); return nil }

func readAllString(rc io.ReadCloser) string {
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestTiered_GetFile_HotHit(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	hot.files["ledgers/12345.xdr"] = "HOT-BODY"
	cold.files["ledgers/12345.xdr"] = "COLD-BODY"

	ts := NewTieredDataStore(hot, cold, nil)
	rc, _, err := ts.GetFile(context.Background(), "ledgers/12345.xdr")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := readAllString(rc); got != "HOT-BODY" {
		t.Fatalf("want HOT-BODY, got %q", got)
	}
	if cold.calls["GetFile"] != 0 {
		t.Errorf("cold should not be consulted; calls=%d", cold.calls["GetFile"])
	}
}

func TestTiered_GetFile_HotMissColdHit(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	cold.files["ledgers/99999.xdr"] = "COLD-BODY"

	ts := NewTieredDataStore(hot, cold, nil)
	rc, _, err := ts.GetFile(context.Background(), "ledgers/99999.xdr")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := readAllString(rc); got != "COLD-BODY" {
		t.Fatalf("want COLD-BODY, got %q", got)
	}
	if cold.calls["GetFile"] != 1 {
		t.Errorf("cold should be consulted exactly once; got %d", cold.calls["GetFile"])
	}
}

func TestTiered_GetFile_BothMissing(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")

	ts := NewTieredDataStore(hot, cold, nil)
	_, _, err := ts.GetFile(context.Background(), "ledgers/00000.xdr")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}

func TestTiered_GetFile_HotTransientError_DoesNotFallThrough(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	cold.files["ledgers/55555.xdr"] = "COLD-BODY-NEVER-SEEN"

	// Simulate a transient network error on the hot side. Per the
	// fail-loud-not-silent design, this MUST propagate without
	// trying cold — masking a misconfigured hot endpoint is the
	// failure mode this guards against.
	transient := errors.New("dial tcp: i/o timeout")
	hot.getErr["ledgers/55555.xdr"] = transient

	ts := NewTieredDataStore(hot, cold, nil)
	_, _, err := ts.GetFile(context.Background(), "ledgers/55555.xdr")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, transient) {
		t.Fatalf("expected wrapped transient error, got %v", err)
	}
	if cold.calls["GetFile"] != 0 {
		t.Errorf("cold should NOT be consulted on transient hot error; got %d", cold.calls["GetFile"])
	}
}

func TestTiered_Exists_HotTrue(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	hot.files["x"] = "y"

	ts := NewTieredDataStore(hot, cold, nil)
	ok, err := ts.Exists(context.Background(), "x")
	if err != nil || !ok {
		t.Fatalf("Exists: ok=%v err=%v", ok, err)
	}
	if cold.calls["Exists"] != 0 {
		t.Errorf("cold should not be consulted when hot has it")
	}
}

func TestTiered_Exists_FallsThrough(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	cold.files["x"] = "y"

	ts := NewTieredDataStore(hot, cold, nil)
	ok, err := ts.Exists(context.Background(), "x")
	if err != nil || !ok {
		t.Fatalf("Exists: ok=%v err=%v", ok, err)
	}
	if cold.calls["Exists"] != 1 {
		t.Errorf("cold should be consulted once when hot has not; got %d", cold.calls["Exists"])
	}
}

func TestTiered_ListFilePaths_UnionDedup(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	hot.listPaths = []string{"a", "b", "shared"}
	cold.listPaths = []string{"shared", "c", "d"}

	ts := NewTieredDataStore(hot, cold, nil)
	got, err := ts.ListFilePaths(context.Background(), datastore.ListFileOptions{})
	if err != nil {
		t.Fatalf("ListFilePaths: %v", err)
	}
	want := []string{"a", "b", "shared", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d (got=%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q want %q (full=%v)", i, got[i], w, got)
		}
	}
}

func TestTiered_ListFilePaths_ColdErrorFallback(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")
	hot.listPaths = []string{"a", "b"}
	cold.listErr = errors.New("cold-list-failed")

	ts := NewTieredDataStore(hot, cold, nil)
	got, err := ts.ListFilePaths(context.Background(), datastore.ListFileOptions{})
	if err == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	if !strings.Contains(err.Error(), "cold-list-failed") {
		t.Errorf("expected wrapped cold err, got %v", err)
	}
	// hot-only list still returned alongside the error so the
	// caller can decide.
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected hot-only list, got %v", got)
	}
}

func TestTiered_PutFile_HotOnly(t *testing.T) {
	t.Parallel()
	hot := newFakeStore("hot")
	cold := newFakeStore("cold")

	ts := NewTieredDataStore(hot, cold, nil)
	if err := ts.PutFile(context.Background(), "k", strings.NewReader("v"), nil); err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if hot.calls["PutFile"] != 1 {
		t.Errorf("hot.PutFile should be called once; got %d", hot.calls["PutFile"])
	}
	if cold.calls["PutFile"] != 0 {
		t.Errorf("cold.PutFile MUST NOT be called (cold is read-only); got %d", cold.calls["PutFile"])
	}
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"os.ErrNotExist", os.ErrNotExist, true},
		{"sdk ErrNoLedgerFiles", datastore.ErrNoLedgerFiles, true},
		{"sdk ErrNoValidLedgerFiles", datastore.ErrNoValidLedgerFiles, true},
		{"s3 NoSuchKey wrapping", errors.New("operation GetObject: NoSuchKey: key not found"), true},
		{"plain key not found", errors.New("key not found"), true},
		{"fs no such file", errors.New("open /missing: no such file or directory"), true},
		{"transient timeout", errors.New("dial tcp 1.2.3.4:443: i/o timeout"), false},
		{"auth failure", errors.New("AccessDenied: user is not authorized"), false},
		{"throttling", errors.New("SlowDown: please reduce your request rate"), false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsNotFound(c.err); got != c.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
