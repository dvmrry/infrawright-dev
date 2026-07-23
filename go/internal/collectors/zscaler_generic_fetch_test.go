package collectors

// zscaler_generic_fetch_test.go ports
// the original test corpus's "generic Fetch collects real
// ZCC and ZTC registries from a Python-free external root" test: an
// end-to-end run of the real committed zcc/ztc pack registries (copied
// into a scratch root, with any Python artifact stripped) through
// ResolveCollectorAdapters + FetchResources against the built-in Zscaler
// adapters and a fake transport, in OneAPI mode.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// zscalerFixtureTransport is invoked from this package's own worker pool
// (rest.go's runFetchWorkers), which calls HttpTransport.Request
// concurrently from multiple goroutines whenever FetchResourcesOptions.
// Concurrency > 1 (see this test's Concurrency: intPtr(2)) -- so, like
// every other HttpTransport implementation a real caller supplies, it
// must serialize its own mutable state itself; nothing in this package's
// seam does that on a transport's behalf. queueTransport and
// delayedPathTransport in helpers_test.go do the same locking for the
// same reason.
type zscalerFixtureTransport struct {
	mu       sync.Mutex
	requests []HTTPRequest
}

func (z *zscalerFixtureTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	z.mu.Lock()
	z.requests = append(z.requests, request)
	z.mu.Unlock()
	if request.Method == "POST" && request.URL.Path == "/oauth2/v1/token" {
		return jsonResponseNoT(map[string]any{"access_token": "fixture-token"})
	}
	if request.Method == "GET" && request.URL.Path == "/zcc/papi/public/v1/webTrustedNetwork/listByCompany" {
		return jsonResponseNoT(map[string]any{
			"trustedNetworkContracts": []any{map[string]any{"id": "zcc-1", "name": "Trusted Network"}},
		})
	}
	if request.Method == "GET" && request.URL.Path == "/ztw/api/v1/networkServices" {
		return jsonResponseNoT([]any{map[string]any{"id": "ztc-1", "name": "HTTPS", "ports": []any{443}}})
	}
	return HTTPResponse{}, errors.New("unexpected request " + request.Method + " " + request.URL.Path)
}

func (z *zscalerFixtureTransport) Close() error { return nil }

func jsonResponseNoT(value any) (HTTPResponse, error) {
	rendered, err := json.Marshal(value)
	if err != nil {
		return HTTPResponse{}, err
	}
	return HTTPResponse{Status: 200, Headers: map[string][]string{}, Body: rendered}, nil
}

func TestGenericFetchCollectsRealZccAndZtcRegistriesFromPythonFreeRoot(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	packsRoot := filepath.Join(directory, "packs")
	for _, product := range []string{"zcc", "ztc"} {
		if err := copyDir(filepath.Join(root, "packs", product), filepath.Join(packsRoot, product)); err != nil {
			t.Fatalf("copy %s: %v", product, err)
		}
	}
	if err := copyDir(filepath.Join(root, "packs", "_shared", "zscaler"), filepath.Join(packsRoot, "_shared", "zscaler")); err != nil {
		t.Fatalf("copy _shared/zscaler: %v", err)
	}

	packRoot := loadRootFromPacksDir(t, packsRoot)
	adapters, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zcc_trusted_network", "ztc_network_services"},
		Root:          packRoot,
	})
	if err != nil {
		t.Fatalf("ResolveCollectorAdapters: %v", err)
	}
	products := make(map[string]struct{}, len(adapters))
	for product := range adapters {
		products[product] = struct{}{}
	}
	environment := Environment{
		"ZSCALER_CLIENT_ID":     "fixture-client",
		"ZSCALER_CLIENT_SECRET": "fixture-secret",
		"ZSCALER_VANITY_DOMAIN": "fixture",
	}
	context, err := NewCollectorContext(NewCollectorContextInput{Environment: environment, NeededProducts: products})
	if err != nil {
		t.Fatalf("NewCollectorContext: %v", err)
	}
	transport := &zscalerFixtureTransport{}
	outputDirectory := filepath.Join(directory, "pulls")
	result, err := FetchResources(FetchResourcesOptions{
		Adapters:        adapters,
		Concurrency:     intPtr(2),
		Context:         context,
		Environment:     environment,
		Mode:            AuthModeOneAPI,
		OutputDirectory: outputDirectory,
		Root:            packRoot,
		Selectors:       []string{"zcc_trusted_network", "ztc_network_services"},
		Transport:       transport,
	})
	if err != nil {
		t.Fatalf("FetchResources: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Errorf("failed = %v, want none", result.Failed)
	}
	if !equalStrings(result.Processed, []string{"zcc_trusted_network", "ztc_network_services"}) {
		t.Errorf("processed = %v, want [zcc_trusted_network ztc_network_services]", result.Processed)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("skipped = %v, want none", result.Skipped)
	}

	postCount := 0
	var getPaths []string
	for _, request := range transport.requests {
		if request.Method == "POST" {
			postCount++
		} else if request.Method == "GET" {
			getPaths = append(getPaths, request.URL.EscapedPath()+"?"+request.URL.RawQuery)
		}
	}
	if postCount != 1 {
		t.Errorf("POST count = %d, want 1 (OneAPI authentication must be shared across selected products)", postCount)
	}
	wantGets := map[string]bool{
		"/zcc/papi/public/v1/webTrustedNetwork/listByCompany?page=1&pageSize=1000": true,
		"/ztw/api/v1/networkServices?page=1&pageSize=1000":                         true,
	}
	if len(getPaths) != len(wantGets) {
		t.Fatalf("GET paths = %v, want %v", getPaths, wantGets)
	}
	for _, got := range getPaths {
		if !wantGets[got] {
			t.Errorf("unexpected GET path %s", got)
		}
	}

	zccBytes, err := os.ReadFile(filepath.Join(outputDirectory, "zcc_trusted_network.json"))
	if err != nil {
		t.Fatalf("read zcc_trusted_network.json: %v", err)
	}
	if want := "[\n  {\n    \"id\": \"zcc-1\",\n    \"name\": \"Trusted Network\"\n  }\n]\n"; string(zccBytes) != want {
		t.Errorf("zcc_trusted_network.json = %q, want %q", zccBytes, want)
	}
	ztcBytes, err := os.ReadFile(filepath.Join(outputDirectory, "ztc_network_services.json"))
	if err != nil {
		t.Fatalf("read ztc_network_services.json: %v", err)
	}
	if want := "[\n  {\n    \"id\": \"ztc-1\",\n    \"name\": \"HTTPS\",\n    \"ports\": [\n      443\n    ]\n  }\n]\n"; string(ztcBytes) != want {
		t.Errorf("ztc_network_services.json = %q, want %q", ztcBytes, want)
	}
}
