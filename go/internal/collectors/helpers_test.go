package collectors

// helpers_test.go holds the repository, pack-tree, transport, and performance
// fixtures shared by collector tests.

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// repoRoot walks up from this test file's path until it finds the committed
// full pack profile.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, packsErr := os.Stat(filepath.Join(dir, "packs", "full.packset.json"))
		if packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding packs/full.packset.json", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func requireCollectorPackSelection(t *testing.T, selection metadata.PackSelection) {
	t.Helper()
	packsRoot := filepath.Join(repoRoot(t), "packs")
	missing := make([]string, 0)
	for _, pack := range selection.Packs {
		if _, err := os.Stat(filepath.Join(packsRoot, pack)); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, pack)
				continue
			}
			t.Fatalf("os.Stat(pack %q) error: %v", pack, err)
		}
	}
	for _, shared := range selection.Shared {
		if _, err := os.Stat(filepath.Join(packsRoot, "_shared", shared)); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, "_shared/"+shared)
				continue
			}
			t.Fatalf("os.Stat(shared %q) error: %v", shared, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Skipf("required pack paths are not installed: %v", missing)
	}
}

func installedCollectorRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := filepath.Join(repoRoot(t), "packs")
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", packsRoot, err)
	}
	packs := make([]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "_shared" {
			packs = append(packs, entry.Name())
		}
	}
	shared := []any{}
	sharedRoot := filepath.Join(packsRoot, "_shared")
	sharedEntries, err := os.ReadDir(sharedRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("os.ReadDir(%q) error: %v", sharedRoot, err)
	}
	for _, entry := range sharedEntries {
		if entry.IsDir() {
			shared = append(shared, entry.Name())
		}
	}
	profile := filepath.Join(t.TempDir(), "installed.packset.json")
	data, err := json.Marshal(metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1, "packs": packs, "shared": shared,
	})
	if err != nil {
		t.Fatalf("json.Marshal(installed pack set) error: %v", err)
	}
	if err := os.WriteFile(profile, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", profile, err)
	}
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot(installed packs) error: %v", err)
	}
	return loaded
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// snapshotDirectory ports the snapshotDirectory helper from
// the original test corpus.
func snapshotDirectory(t *testing.T, directory string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read dir %s: %v", directory, err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	sort.Strings(names)
	output := make(map[string]string, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		output[name] = string(data)
	}
	return output
}

// jsonResponse builds a 200 (or status) HTTPResponse whose body is value
// marshaled with the stdlib encoder -- matching compatibility tests's own
// `response(value)` helper's plain JSON.stringify, not this package's
// canonical/lossless renderer (that renderer is for artifacts this
// package writes, not for fixture provider responses it reads). Pass an
// int64 (never float64) for any integer fixture value that must survive
// past float64's 2^53 safe-integer range: encoding/json marshals an int64
// as exact decimal digits, with no float64 round-trip to lose precision
// in.
func jsonResponse(t *testing.T, value any, status int) HTTPResponse {
	t.Helper()
	rendered, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode test JSON: %v", err)
	}
	return HTTPResponse{Status: status, Headers: map[string][]string{}, Body: rendered}
}

// testAdapter ports the `adapter()` test helper from
// the original test corpus.
func testAdapter(product string, acquisitions *[]string) CollectorAdapter {
	if product == "" {
		product = "sample"
	}
	return CollectorAdapter{
		Product: product,
		Acquire: func(CollectorAcquireInput) (CollectorAuthContext, error) {
			if acquisitions != nil {
				*acquisitions = append(*acquisitions, product)
			}
			return CollectorAuthContext{
				Headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer shared"},
			}, nil
		},
		ComposeURL: func(input CollectorComposeUrlInput) (*url.URL, error) {
			return url.Parse("https://" + product + ".example/api/" + input.Path)
		},
	}
}

// testEntry ports the `entry()` test helper from
// the original test corpus.
func testEntry(pagination PaginationStyle, extra FetchEntry) FetchEntry {
	base := FetchEntry{
		Product:              "sample",
		Path:                 "items",
		Pagination:           pagination,
		Query:                map[string]any{},
		OptionalHTTPStatuses: map[int]struct{}{},
	}
	if extra.Product != "" {
		base.Product = extra.Product
	}
	if extra.Path != "" {
		base.Path = extra.Path
	}
	if extra.Envelope != "" {
		base.Envelope = extra.Envelope
	}
	if extra.Expand != nil {
		base.Expand = extra.Expand
	}
	if extra.MergePaths != nil {
		base.MergePaths = extra.MergePaths
	}
	if extra.FollowPaths != nil {
		base.FollowPaths = extra.FollowPaths
	}
	if extra.Query != nil {
		base.Query = extra.Query
	}
	if extra.OptionalHTTPStatuses != nil {
		base.OptionalHTTPStatuses = extra.OptionalHTTPStatuses
	}
	return base
}

// queueTransport ports the QueueTransport test double from
// the original test corpus: a fixed, ordered queue of canned
// responses, one per request, asserting it is never asked for more
// requests than it has responses queued.
type queueTransport struct {
	t         *testing.T
	mu        sync.Mutex
	responses []HTTPResponse
	requests  []HTTPRequest
}

func newQueueTransport(t *testing.T, responses ...HTTPResponse) *queueTransport {
	return &queueTransport{t: t, responses: responses}
}

func (q *queueTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requests = append(q.requests, request)
	if len(q.responses) == 0 {
		q.t.Fatalf("unexpected request %s", request.URL)
	}
	next := q.responses[0]
	q.responses = q.responses[1:]
	return next, nil
}

func (q *queueTransport) Close() error { return nil }

func (q *queueTransport) requestSearches() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.requests))
	for i, request := range q.requests {
		out[i] = "?" + request.URL.RawQuery
		if request.URL.RawQuery == "" {
			out[i] = ""
		}
	}
	return out
}

func (q *queueTransport) requestPaths() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.requests))
	for i, request := range q.requests {
		// EscapedPath preserves the encoded path emitted by expandedPaths;
		// URL.Path would decode values such as "A%20B".
		out[i] = request.URL.EscapedPath()
	}
	return out
}

// delayedPathTransport ports the DelayedPathTransport test double from
// the original test corpus: canned responses keyed by request
// path, each released after an optional artificial delay, tracking the
// maximum number of concurrently in-flight requests.
type delayedPathTransport struct {
	t            *testing.T
	responses    map[string]HTTPResponse
	delays       map[string]time.Duration
	beforeReturn func(string) error

	mu        sync.Mutex
	active    int
	maxActive int
	requests  []string
}

func newDelayedPathTransport(t *testing.T, responses map[string]HTTPResponse, delays map[string]time.Duration) *delayedPathTransport {
	return &delayedPathTransport{t: t, responses: responses, delays: delays}
}

func (d *delayedPathTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	pathname := request.URL.Path
	d.mu.Lock()
	d.requests = append(d.requests, pathname)
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active--
		d.mu.Unlock()
	}()
	if delay := d.delays[pathname]; delay > 0 {
		time.Sleep(delay)
	}
	if d.beforeReturn != nil {
		if err := d.beforeReturn(pathname); err != nil {
			return HTTPResponse{}, err
		}
	}
	value, ok := d.responses[pathname]
	if !ok {
		d.t.Fatalf("unexpected request %s", pathname)
	}
	return value, nil
}

func (d *delayedPathTransport) Close() error { return nil }

// fakePerformanceRecorder is a minimal, self-contained PerformanceRecorder
// test double: it does not reproduce the original implementation's
// report() rendering (grouping, sorting, safeLabel validation, quantiles --
// see types.go's PerformanceRecorder doc comment for why that package is
// out of this port's scope), only enough bookkeeping to assert what
// rest.go actually calls: SetFetchConcurrency's single-value invariant,
// and the recorded span list itself (for asserting phase/resource_family
// ordering and counts, as compatibility tests's own performance.report() assertions
// do at a higher level).
type fakePerformanceRecorder struct {
	mu               sync.Mutex
	clock            float64
	concurrency      int
	concurrencyIsSet bool
	spans            []PerformanceSpan
}

func (f *fakePerformanceRecorder) Now() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock++
	return f.clock
}

func (f *fakePerformanceRecorder) DurationSince(startedMs float64) float64 {
	return f.Now() - startedMs
}

func (f *fakePerformanceRecorder) SetFetchConcurrency(value int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value <= 0 {
		return errors.New("fetch concurrency must be a positive safe integer")
	}
	if f.concurrencyIsSet && f.concurrency != value {
		return errors.New("fetch concurrency changed within one performance report")
	}
	f.concurrency, f.concurrencyIsSet = value, true
	return nil
}

func (f *fakePerformanceRecorder) RecordSpan(span PerformanceSpan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spans = append(f.spans, span)
	return nil
}

func (f *fakePerformanceRecorder) spansByPhase(phase string) []PerformanceSpan {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PerformanceSpan
	for _, span := range f.spans {
		if span.Phase == phase {
			out = append(out, span)
		}
	}
	return out
}
