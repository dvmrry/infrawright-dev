package collectors

// rest_test.go ports node-tests/rest-collector.test.ts.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func intPtr(v int) *int { return &v }

var sharedContext = CollectorContext{CustomerID: "customer"}
var sharedAuth = CollectorAuthContext{Headers: map[string]string{"Accept": "application/json"}}

func TestFetchResourceAllFourPaginationStyles(t *testing.T) {
	ziaFirst := make([]any, 1_000)
	for i := range ziaFirst {
		ziaFirst[i] = map[string]any{"id": i}
	}
	zia := newQueueTransport(t,
		jsonResponse(t, map[string]any{"values": ziaFirst}, 200),
		jsonResponse(t, map[string]any{"values": []any{map[string]any{"id": 1_000}}}, 200),
	)
	ziaItems, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationZia, FetchEntry{Envelope: "values", Query: map[string]any{"customOnly": "true"}}),
		Mode:  AuthModeOneAPI, ResourceType: "sample", Transport: zia,
	})
	if err != nil {
		t.Fatalf("ZIA FetchResource: %v", err)
	}
	if len(ziaItems) != 1_001 {
		t.Errorf("len(ziaItems) = %d, want 1001", len(ziaItems))
	}
	wantSearches := []string{"?customOnly=true&page=1&pageSize=1000", "?customOnly=true&page=2&pageSize=1000"}
	if got := zia.requestSearches(); !equalStrings(got, wantSearches) {
		t.Errorf("ZIA searches = %v, want %v", got, wantSearches)
	}

	zpa := newQueueTransport(t,
		jsonResponse(t, map[string]any{"list": []any{map[string]any{"id": "1"}}, "totalPages": 2}, 200),
		jsonResponse(t, map[string]any{"list": []any{map[string]any{"id": "2"}}, "totalPages": 2}, 200),
	)
	zpaItems, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationZpa, FetchEntry{}), Mode: AuthModeOneAPI, ResourceType: "sample", Transport: zpa,
	})
	if err != nil {
		t.Fatalf("ZPA FetchResource: %v", err)
	}
	wantZpaItems := []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}
	if !reflect.DeepEqual(zpaItems, wantZpaItems) {
		t.Errorf("ZPA items = %#v, want %#v", zpaItems, wantZpaItems)
	}
	wantZpaSearches := []string{"?page=1&pagesize=500", "?page=2&pagesize=500"}
	if got := zpa.requestSearches(); !equalStrings(got, wantZpaSearches) {
		t.Errorf("ZPA searches = %v, want %v", got, wantZpaSearches)
	}

	single := newQueueTransport(t, jsonResponse(t, map[string]any{"id": "1"}, 200))
	singleItems, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{}), Mode: AuthModeOneAPI, ResourceType: "sample", Transport: single,
	})
	if err != nil {
		t.Fatalf("single (object) FetchResource: %v", err)
	}
	if want := []any{map[string]any{"id": "1"}}; !reflect.DeepEqual(singleItems, want) {
		t.Errorf("single (object) items = %#v, want %#v", singleItems, want)
	}

	singleList := newQueueTransport(t, jsonResponse(t, []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}, 200))
	singleListItems, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{}), Mode: AuthModeOneAPI, ResourceType: "sample", Transport: singleList,
	})
	if err != nil {
		t.Fatalf("single (list) FetchResource: %v", err)
	}
	if want := []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}; !reflect.DeepEqual(singleListItems, want) {
		t.Errorf("single (list) items = %#v, want %#v", singleListItems, want)
	}
	if len(singleList.requests) != 1 {
		t.Errorf("single (list) requests = %d, want 1", len(singleList.requests))
	}

	v2 := newQueueTransport(t,
		jsonResponse(t, map[string]any{"items": []any{map[string]any{"id": "1"}}, "count": 100, "limit": 100, "total": 2}, 200),
		jsonResponse(t, map[string]any{"items": []any{map[string]any{"id": "2"}}, "count": 1, "limit": 100, "total": 2}, 200),
	)
	v2Items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationZccV2, FetchEntry{}), Mode: AuthModeOneAPI, ResourceType: "sample", Transport: v2,
	})
	if err != nil {
		t.Fatalf("ZCC v2 FetchResource: %v", err)
	}
	if want := []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}; !reflect.DeepEqual(v2Items, want) {
		t.Errorf("ZCC v2 items = %#v, want %#v", v2Items, want)
	}
	wantV2Searches := []string{"?skip=0&perPage=100", "?skip=100&perPage=100"}
	if got := v2.requestSearches(); !equalStrings(got, wantV2Searches) {
		t.Errorf("ZCC v2 searches = %v, want %v", got, wantV2Searches)
	}
}

func TestFetchResourceExpandedPathsPercentQuoted(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, []any{map[string]any{"id": "1"}}, 200),
		jsonResponse(t, []any{map[string]any{"id": "2"}}, 200),
	)
	items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{
			Path: "rules/{kind}/again/{kind}", Expand: map[string][]string{"kind": {"A B", "slash/value"}},
		}),
		Mode: AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err != nil {
		t.Fatalf("FetchResource: %v", err)
	}
	want := []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %#v, want %#v", items, want)
	}
	wantPaths := []string{"/api/rules/A%20B/again/A%20B", "/api/rules/slash%2Fvalue/again/slash%2Fvalue"}
	if got := transport.requestPaths(); !equalStrings(got, wantPaths) {
		t.Errorf("paths = %v, want %v", got, wantPaths)
	}
}

func TestFetchResourceRejectsUnsafePathsBeforeURLComposition(t *testing.T) {
	for _, fetchPath := range []string{"items\\admin", "items?scope=1", "items/../admin", "items/%2e/admin"} {
		_, err := FetchResource(FetchResourceOptions{
			Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
			Entry: testEntry(PaginationSingle, FetchEntry{Path: fetchPath}), Mode: AuthModeOneAPI,
			ResourceType: "sample", Transport: newQueueTransport(t),
		})
		if err == nil || !strings.Contains(err.Error(), "fetch path must not contain") {
			t.Errorf("path %q: error = %v, want a 'fetch path must not contain' error", fetchPath, err)
		}
	}
	for _, entry := range []FetchEntry{
		testEntry(PaginationSingle, FetchEntry{Path: "items/{literal}"}),
		testEntry(PaginationSingle, FetchEntry{Path: "items/{item}/{other}", Expand: map[string][]string{"item": {"safe"}}}),
	} {
		_, err := FetchResource(FetchResourceOptions{
			Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
			Entry: entry, Mode: AuthModeOneAPI, ResourceType: "sample", Transport: newQueueTransport(t),
		})
		if err == nil || !strings.Contains(err.Error(), "undeclared expansion braces") {
			t.Errorf("entry %+v: error = %v, want an 'undeclared expansion braces' error", entry, err)
		}
	}
	for _, value := range []string{".", ".."} {
		_, err := FetchResource(FetchResourceOptions{
			Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
			Entry: testEntry(PaginationSingle, FetchEntry{
				Path: "items/{item}", Expand: map[string][]string{"item": {value}},
			}),
			Mode: AuthModeOneAPI, ResourceType: "sample", Transport: newQueueTransport(t),
		})
		if err == nil || !regexp.MustCompile(`fetch expansion "item" value must not be`).MatchString(err.Error()) {
			t.Errorf("value %q: error = %v, want a fetch-expansion-value error", value, err)
		}
	}
}

func TestFetchResourceEnvelopesFailClosedWhenMissingOrNonList(t *testing.T) {
	cases := []struct {
		payload any
		want    string
	}{
		{map[string]any{"other": []any{}}, "missing envelope"},
		{map[string]any{"values": map[string]any{}}, "did not contain a list"},
	}
	for _, tc := range cases {
		_, err := FetchResource(FetchResourceOptions{
			Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
			Entry: testEntry(PaginationZia, FetchEntry{Envelope: "values"}), Mode: AuthModeOneAPI,
			ResourceType: "sample", Transport: newQueueTransport(t, jsonResponse(t, tc.payload, 200)),
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("payload %#v: error = %v, want to contain %q", tc.payload, err, tc.want)
		}
	}
}

func TestCommittedCASBPagersHandleFullBoundariesAndWriteDeterministicBytes(t *testing.T) {
	packRoot := loadFullRoot(t)
	run := func(directory string, items []any, resourceType string) (bytes string, searches []string) {
		var pages [2][]any
		if len(items) == 1_000 {
			pages[0], pages[1] = items, []any{}
		} else {
			pages[0], pages[1] = items[:1_000], items[1_000:]
		}
		transport := newQueueTransport(t, jsonResponse(t, pages[0], 200), jsonResponse(t, pages[1], 200))
		result, err := FetchResources(FetchResourcesOptions{
			Adapters:        map[string]CollectorAdapter{"zia": testAdapter("zia", nil)},
			Context:         sharedContext,
			Environment:     Environment{},
			Mode:            AuthModeOneAPI,
			OutputDirectory: directory,
			Root:            packRoot,
			Selectors:       []string{resourceType},
			Transport:       transport,
		})
		if err != nil {
			t.Fatalf("FetchResources: %v", err)
		}
		if !equalStrings(result.Processed, []string{resourceType}) {
			t.Fatalf("processed = %v, want [%s]", result.Processed, resourceType)
		}
		data, err := os.ReadFile(filepath.Join(directory, resourceType+".json"))
		if err != nil {
			t.Fatalf("read artifact: %v", err)
		}
		return string(data), transport.requestSearches()
	}

	directory := t.TempDir()
	for _, tc := range []struct {
		resourceType, ruleType string
	}{
		{"zia_casb_dlp_rules", "OFLCASB_DLP_ITSM"},
		{"zia_casb_malware_rules", "OFLCASB_AVP_ITSM"},
	} {
		for _, itemCount := range []int{1_000, 1_001} {
			items := make([]any, itemCount)
			for i := range items {
				items[i] = map[string]any{"id": i, "name": "Rule " + strconv.Itoa(i), "type": tc.ruleType}
			}
			firstBytes, firstSearches := run(filepath.Join(directory, tc.resourceType, strconv.Itoa(itemCount)+"-first"), items, tc.resourceType)
			secondBytes, _ := run(filepath.Join(directory, tc.resourceType, strconv.Itoa(itemCount)+"-second"), items, tc.resourceType)
			wantSearches := []string{"?page=1&pageSize=1000", "?page=2&pageSize=1000"}
			if !equalStrings(firstSearches, wantSearches) {
				t.Errorf("%s(%d) searches = %v, want %v", tc.resourceType, itemCount, firstSearches, wantSearches)
			}
			if firstBytes != secondBytes {
				t.Errorf("%s(%d) run-to-run bytes differ", tc.resourceType, itemCount)
			}
			var parsed []any
			if err := json.Unmarshal([]byte(firstBytes), &parsed); err != nil {
				t.Fatalf("parse artifact: %v", err)
			}
			if len(parsed) != itemCount {
				t.Errorf("%s(%d) artifact length = %d, want %d", tc.resourceType, itemCount, len(parsed), itemCount)
			}
		}
	}
}

// ziaAdoptionFixture mirrors the shape of
// node-tests/fixtures/zia-adoption-classification-v4.7.26.json well enough
// to rebuild each resource type's exact-order payload
// (skip + system_skip + unsupported + keep, matching the Node test's own
// concatenation order) without re-encoding any evidence item -- each is
// kept as json.RawMessage so the artifact-bytes comparison below is not
// laundered through an intermediate Go value representation.
type ziaAdoptionFixture struct {
	Resources map[string]struct {
		Keep        []json.RawMessage `json:"keep"`
		Skip        []json.RawMessage `json:"skip"`
		SystemSkip  []json.RawMessage `json:"system_skip"`
		Unsupported []json.RawMessage `json:"unsupported"`
	} `json:"resources"`
}

func TestZiaAdoptionClassifiersReceiveExactFetchShapedSystemFields(t *testing.T) {
	packRoot := loadFullRoot(t)
	fixturePath := filepath.Join(repoRoot(t), "node-tests", "fixtures", "zia-adoption-classification-v4.7.26.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture ziaAdoptionFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	directory := t.TempDir()
	for resourceType, evidence := range fixture.Resources {
		var rawItems []json.RawMessage
		rawItems = append(rawItems, evidence.Skip...)
		rawItems = append(rawItems, evidence.SystemSkip...)
		rawItems = append(rawItems, evidence.Unsupported...)
		rawItems = append(rawItems, evidence.Keep...)
		fragments := make([]string, len(rawItems))
		for i, raw := range rawItems {
			fragments[i] = string(raw)
		}
		payloadJSON := "[" + strings.Join(fragments, ",") + "]"

		var bytesOut [2]string
		for i, suffix := range []string{"first", "second"} {
			output := filepath.Join(directory, resourceType, suffix)
			transport := newQueueTransport(t, HTTPResponse{Status: 200, Headers: map[string][]string{}, Body: []byte(payloadJSON)})
			result, err := FetchResources(FetchResourcesOptions{
				Adapters:        map[string]CollectorAdapter{"zia": testAdapter("zia", nil)},
				Context:         sharedContext,
				Environment:     Environment{},
				Mode:            AuthModeOneAPI,
				OutputDirectory: output,
				Root:            packRoot,
				Selectors:       []string{resourceType},
				Transport:       transport,
			})
			if err != nil {
				t.Fatalf("%s: FetchResources: %v", resourceType, err)
			}
			if !equalStrings(result.Processed, []string{resourceType}) {
				t.Fatalf("%s: processed = %v", resourceType, result.Processed)
			}
			if len(transport.requests) != 1 {
				t.Fatalf("%s: requests = %d, want 1", resourceType, len(transport.requests))
			}
			written, err := os.ReadFile(filepath.Join(output, resourceType+".json"))
			if err != nil {
				t.Fatalf("%s: read artifact: %v", resourceType, err)
			}
			bytesOut[i] = string(written)
		}
		if bytesOut[0] != bytesOut[1] {
			t.Errorf("%s: run-to-run Fetch bytes differ", resourceType)
		}
		writtenValue, err := canonjson.ParseDataJSONLosslessly(bytesOut[0])
		if err != nil {
			t.Fatalf("%s: parse written artifact: %v", resourceType, err)
		}
		payloadValue, err := canonjson.ParseDataJSONLosslessly(payloadJSON)
		if err != nil {
			t.Fatalf("%s: parse payload: %v", resourceType, err)
		}
		if !canonjson.JSONEqual(writtenValue, payloadValue) {
			t.Errorf("%s: written artifact does not equal the fixture payload", resourceType)
		}
	}
}

func TestBatchSharesOneAPIAuthSkipsOptionalWritesPythonBytesInvalidatesStaleSkips(t *testing.T) {
	packRoot := loadFullRoot(t)
	directory := t.TempDir()
	stale := filepath.Join(directory, "zia_extranet.json")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	unselected := filepath.Join(directory, "unselected.json")
	if err := os.WriteFile(unselected, []byte("retain\n"), 0o644); err != nil {
		t.Fatalf("write unselected file: %v", err)
	}
	var acquisitions []string
	adapters := map[string]CollectorAdapter{
		"zia": testAdapter("zia", &acquisitions),
		"zpa": testAdapter("zpa", &acquisitions),
	}
	transport := newQueueTransport(t,
		jsonResponse(t, map[string]any{}, 403),
		jsonResponse(t, map[string]any{"list": []any{map[string]any{"id": int64(9_007_199_254_740_992)}}, "totalPages": 1}, 200),
	)
	var diagnostics []string
	result, err := FetchResources(FetchResourcesOptions{
		Adapters:        adapters,
		Context:         sharedContext,
		Environment:     Environment{},
		Mode:            AuthModeOneAPI,
		OnDiagnostic:    func(message string) { diagnostics = append(diagnostics, message) },
		OutputDirectory: directory,
		Root:            packRoot,
		Selectors:       []string{"zia_extranet", "zpa_segment_group"},
		Transport:       transport,
	})
	if err != nil {
		t.Fatalf("FetchResources: %v", err)
	}
	if !equalStrings(acquisitions, []string{"zia"}) {
		t.Errorf("acquisitions = %v, want [zia] (OneAPI auth is shared across products)", acquisitions)
	}
	if !equalStrings(result.Processed, []string{"zpa_segment_group"}) {
		t.Errorf("processed = %v, want [zpa_segment_group]", result.Processed)
	}
	if len(result.Failed) != 0 {
		t.Errorf("failed = %v, want none", result.Failed)
	}
	if _, skipped := result.Skipped["zia_extranet"]; !skipped || len(result.Skipped) != 1 {
		t.Errorf("skipped = %v, want exactly {zia_extranet: ...}", result.Skipped)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Lstat(stale zia_extranet.json) error = %v, want os.ErrNotExist", err)
	}
	if content, err := os.ReadFile(unselected); err != nil || string(content) != "retain\n" {
		t.Errorf("os.ReadFile(unselected.json) = (%q, %v), want unchanged bytes", content, err)
	}
	written, err := os.ReadFile(filepath.Join(directory, "zpa_segment_group.json"))
	if err != nil {
		t.Fatalf("read zpa_segment_group.json: %v", err)
	}
	if want := "[\n  {\n    \"id\": 9007199254740992\n  }\n]\n"; string(written) != want {
		t.Errorf("zpa_segment_group.json = %q, want %q", written, want)
	}
	if !containsSubstring(diagnostics, "1 resource(s) SKIPPED") {
		t.Errorf("diagnostics = %v, want a message mentioning '1 resource(s) SKIPPED'", diagnostics)
	}
}

func TestSharedOneAPIAuthFailureIsolatedIntoEverySelectedProductResult(t *testing.T) {
	packRoot := loadFullRoot(t)
	directory := t.TempDir()
	staleDestinations := []string{
		filepath.Join(directory, "zia_advanced_settings.json"),
		filepath.Join(directory, "zpa_segment_group.json"),
	}
	for _, destination := range staleDestinations {
		if err := os.WriteFile(destination, []byte("stale\n"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v, want nil", destination, err)
		}
	}
	zpaAcquires := 0
	rejecting := testAdapter("zia", nil)
	rejecting.Acquire = func(CollectorAcquireInput) (CollectorAuthContext, error) {
		return CollectorAuthContext{}, NewHTTPStatusError("token request failed: HTTP 401", 401)
	}
	zpa := testAdapter("zpa", nil)
	zpa.Acquire = func(CollectorAcquireInput) (CollectorAuthContext, error) {
		zpaAcquires++
		return sharedAuth, nil
	}
	var diagnostics []string
	result, err := FetchResources(FetchResourcesOptions{
		Adapters:        map[string]CollectorAdapter{"zia": rejecting, "zpa": zpa},
		Context:         sharedContext,
		Environment:     Environment{},
		Mode:            AuthModeOneAPI,
		OnDiagnostic:    func(message string) { diagnostics = append(diagnostics, message) },
		OutputDirectory: directory,
		Root:            packRoot,
		Selectors:       []string{"zia_advanced_settings", "zpa_segment_group"},
		Transport:       newQueueTransport(t),
	})
	if err != nil {
		t.Fatalf("FetchResources: %v", err)
	}
	if zpaAcquires != 0 {
		t.Errorf("zpaAcquires = %d, want 0 (shared OneAPI identity should short-circuit)", zpaAcquires)
	}
	wantFailed := []string{"zia_advanced_settings", "zpa_segment_group"}
	failedKeys := sortedMapKeysOf(result.Failed)
	if !equalStrings(failedKeys, wantFailed) {
		t.Errorf("failed keys = %v, want %v", failedKeys, wantFailed)
	}
	if !strings.HasPrefix(result.Failed["zpa_segment_group"], "auth failed:") {
		t.Errorf("failed[zpa_segment_group] = %q, want an 'auth failed:' prefix", result.Failed["zpa_segment_group"])
	}
	if !containsSubstring(diagnostics, "HTTP 401/403") {
		t.Errorf("diagnostics = %v, want a hint mentioning HTTP 401/403", diagnostics)
	}
	for _, destination := range staleDestinations {
		if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("os.Lstat(%q) error = %v, want os.ErrNotExist after auth failure", destination, err)
		}
	}
}

// writePackJSON/writeRegistryJSON build a minimal isolated single-product
// pack tree, matching the inline pack.json/registry.json fixtures
// node-tests/rest-collector.test.ts's own "bounded resource workers"/
// "concurrent write failures"/"bounded scheduling" tests construct with
// writeFile.
func writePackJSON(t *testing.T, packsRoot, product string) {
	t.Helper()
	dir := filepath.Join(packsRoot, product)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := json.Marshal(map[string]any{"provider_prefixes": map[string]string{product + "_": product}})
	if err != nil {
		t.Fatalf("marshal pack.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), data, 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}
}

func writeRegistryJSON(t *testing.T, packsRoot, product string, registry map[string]any) {
	t.Helper()
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshal registry.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsRoot, product, "registry.json"), data, 0o644); err != nil {
		t.Fatalf("write registry.json: %v", err)
	}
}

func TestBoundedResourceWorkersOverlapWithoutChangingBytesOutcomesAuthDiagnostics(t *testing.T) {
	directory := t.TempDir()
	packsRoot := filepath.Join(directory, "packs")
	writePackJSON(t, packsRoot, "sample")
	registry := map[string]any{}
	suffixes := []string{"a", "b", "c", "d", "e", "f"}
	for _, suffix := range suffixes {
		entry := map[string]any{
			"product": "sample",
			"fetch": map[string]any{
				"pagination": "single",
				"path":       "items-" + suffix,
			},
		}
		if suffix == "e" {
			entry["fetch"].(map[string]any)["optional_http_statuses"] = []int{403}
		}
		registry["sample_"+suffix] = entry
	}
	writeRegistryJSON(t, packsRoot, "sample", registry)
	isolatedRoot := loadRootFromPacksDir(t, packsRoot)

	output := filepath.Join(directory, "pulls")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(output, "sample_e.json"), []byte("stale optional\n"), 0o644); err != nil {
		t.Fatalf("write stale sample_e.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(output, "sample_f.json"), []byte("stale failed\n"), 0o644); err != nil {
		t.Fatalf("write stale sample_f.json: %v", err)
	}

	responses := make(map[string]HTTPResponse, len(suffixes))
	for index, suffix := range suffixes {
		switch suffix {
		case "e":
			responses["/api/items-e"] = jsonResponse(t, map[string]any{}, 403)
		case "f":
			responses["/api/items-f"] = jsonResponse(t, map[string]any{}, 503)
		default:
			responses["/api/items-"+suffix] = jsonResponse(t, []any{
				map[string]any{"id": index + 1}, map[string]any{"id": suffix + "-second"},
			}, 200)
		}
	}

	var baselineResult FetchRunResult
	var baselineDiagnostics []string
	var baselineFiles map[string]string

	for _, concurrency := range []int{1, 2, 4, 8} {
		acquisitions := 0
		selectedAdapter := testAdapter("sample", nil)
		selectedAdapter.Acquire = func(CollectorAcquireInput) (CollectorAuthContext, error) {
			acquisitions++
			return sharedAuth, nil
		}
		delays := make(map[string]time.Duration, len(suffixes))
		for index, suffix := range suffixes {
			delay := time.Duration(0)
			if concurrency != 1 {
				delay = time.Duration(6-index) * 3 * time.Millisecond
			}
			delays["/api/items-"+suffix] = delay
		}
		transport := newDelayedPathTransport(t, responses, delays)
		performance := &fakePerformanceRecorder{}
		var diagnostics []string
		result, err := FetchResources(FetchResourcesOptions{
			Adapters:        map[string]CollectorAdapter{"sample": selectedAdapter},
			Concurrency:     intPtr(concurrency),
			Context:         sharedContext,
			Environment:     Environment{},
			Mode:            AuthModeOneAPI,
			OnDiagnostic:    func(message string) { diagnostics = append(diagnostics, message) },
			OutputDirectory: output,
			Performance:     performance,
			Root:            isolatedRoot,
			Selectors:       []string{"sample"},
			Transport:       transport,
		})
		if err != nil {
			t.Fatalf("concurrency %d: FetchResources: %v", concurrency, err)
		}
		files := snapshotDirectory(t, output)
		if acquisitions != 1 {
			t.Errorf("concurrency %d: acquisitions = %d, want 1", concurrency, acquisitions)
		}
		wantMaxActive := concurrency
		if wantMaxActive > 6 {
			wantMaxActive = 6
		}
		if transport.maxActive > wantMaxActive {
			t.Errorf("concurrency %d: maxActive = %d, want <= %d", concurrency, transport.maxActive, wantMaxActive)
		}
		if len(transport.requests) != 6 {
			t.Errorf("concurrency %d: requests = %d, want 6", concurrency, len(transport.requests))
		}
		if performance.concurrency != concurrency {
			t.Errorf("concurrency %d: recorded SetFetchConcurrency = %d", concurrency, performance.concurrency)
		}
		resourceSpans := performance.spansByPhase("fetch.resource")
		if len(resourceSpans) != 6 {
			t.Errorf("concurrency %d: fetch.resource spans = %d, want 6", concurrency, len(resourceSpans))
		}
		var pagesSum int
		gotResourceFamilies := make([]string, len(resourceSpans))
		for i, span := range resourceSpans {
			gotResourceFamilies[i] = span.ResourceFamily
			if span.Pages != nil {
				pagesSum += *span.Pages
			}
		}
		wantResourceFamilies := []string{"sample_a", "sample_b", "sample_c", "sample_d", "sample_e", "sample_f"}
		if !equalStrings(gotResourceFamilies, wantResourceFamilies) {
			t.Errorf("concurrency %d: fetch.resource span order = %v, want %v (must be registry order regardless of completion order)", concurrency, gotResourceFamilies, wantResourceFamilies)
		}
		if pagesSum != 6 {
			t.Errorf("concurrency %d: total pages = %d, want 6", concurrency, pagesSum)
		}

		if concurrency == 1 {
			if transport.maxActive != 1 {
				t.Errorf("concurrency 1: maxActive = %d, want 1", transport.maxActive)
			}
			wantRequests := make([]string, len(suffixes))
			for i, suffix := range suffixes {
				wantRequests[i] = "/api/items-" + suffix
			}
			if !equalStrings(transport.requests, wantRequests) {
				t.Errorf("concurrency 1: requests = %v, want %v", transport.requests, wantRequests)
			}
			baselineResult, baselineDiagnostics, baselineFiles = result, diagnostics, files
		} else {
			if transport.maxActive <= 1 {
				t.Errorf("concurrency %d: maxActive = %d, want > 1 (workers should have actually overlapped)", concurrency, transport.maxActive)
			}
			if !reflect.DeepEqual(result, baselineResult) {
				t.Errorf("concurrency %d: result = %+v, want %+v", concurrency, result, baselineResult)
			}
			if !equalStrings(diagnostics, baselineDiagnostics) {
				t.Errorf("concurrency %d: diagnostics = %v, want %v", concurrency, diagnostics, baselineDiagnostics)
			}
			if !reflect.DeepEqual(files, baselineFiles) {
				t.Errorf("concurrency %d: files = %v, want %v", concurrency, files, baselineFiles)
			}
		}
	}

	if value, exists := baselineFiles["sample_e.json"]; exists {
		t.Errorf("sample_e.json = %q, want absent after optional skip", value)
	}
	if value, exists := baselineFiles["sample_f.json"]; exists {
		t.Errorf("sample_f.json = %q, want absent after failed fetch", value)
	}
}

// writeFailureBarrierTransport proves write-failure ordering without timing
// assumptions. With exactly two workers and registry order a,b,c,d, b blocks
// in Request while c returns and fails its destination write. The worker that
// recorded c's outcome can only then take d; d closes cRecorded, which is the
// sole release for b. Thus b's write necessarily occurs after c's fatal
// outcome has been recorded while both requests were genuinely in flight.
type writeFailureBarrierTransport struct {
	responses map[string]HTTPResponse
	output    string
	bStarted  chan struct{}
	cRecorded chan struct{}

	mu        sync.Mutex
	active    int
	events    []string
	maxActive int
}

func newWriteFailureBarrierTransport(responses map[string]HTTPResponse, output string) *writeFailureBarrierTransport {
	return &writeFailureBarrierTransport{
		responses: responses,
		output:    output,
		bStarted:  make(chan struct{}),
		cRecorded: make(chan struct{}),
	}
}

func (transport *writeFailureBarrierTransport) recordEvent(event string) {
	transport.mu.Lock()
	transport.events = append(transport.events, event)
	transport.mu.Unlock()
}

func waitForWriteFailureBarrier(signal <-chan struct{}, name string) error {
	select {
	case <-signal:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("timed out waiting for " + name + " write-failure barrier")
	}
}

func (transport *writeFailureBarrierTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	pathname := request.URL.Path
	transport.mu.Lock()
	transport.active++
	if transport.active > transport.maxActive {
		transport.maxActive = transport.active
	}
	transport.events = append(transport.events, "request:"+pathname)
	transport.mu.Unlock()
	defer func() {
		transport.mu.Lock()
		transport.active--
		transport.mu.Unlock()
	}()

	switch pathname {
	case "/api/items-b":
		close(transport.bStarted)
		if err := waitForWriteFailureBarrier(transport.cRecorded, "sample_c outcome"); err != nil {
			return HTTPResponse{}, err
		}
		transport.recordEvent("release:" + pathname)
	case "/api/items-c":
		if err := waitForWriteFailureBarrier(transport.bStarted, "sample_b request"); err != nil {
			return HTTPResponse{}, err
		}
	case "/api/items-d":
		// Reaching d is the proof point: this worker returned from execute(c),
		// recorded c's fatal outcome, and only then asked for more work.
		transport.recordEvent("after-c-record:" + pathname)
		close(transport.cRecorded)
	}
	if pathname == "/api/items-b" || pathname == "/api/items-c" {
		resourceType := "sample_" + strings.TrimPrefix(pathname, "/api/items-")
		if err := os.MkdirAll(filepath.Join(transport.output, resourceType+".json"), 0o755); err != nil {
			return HTTPResponse{}, err
		}
	}

	response, ok := transport.responses[pathname]
	if !ok {
		return HTTPResponse{}, errors.New("unexpected write-failure barrier request " + pathname)
	}
	return response, nil
}

func (transport *writeFailureBarrierTransport) Close() error { return nil }

func (transport *writeFailureBarrierTransport) snapshot() ([]string, int) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	events := append([]string(nil), transport.events...)
	return events, transport.maxActive
}

func indexOfString(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
}

func TestFetchRejectsUnsafeResourceDestinationBeforeAuthOrMutation(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		victimPath   func(string, string) string
	}{
		{
			name:         "unselected sibling",
			resourceType: "sample_/../unselected",
			victimPath: func(_ string, output string) string {
				return filepath.Join(output, "unselected.json")
			},
		},
		{
			name:         "outside output root",
			resourceType: "sample_/../../outside",
			victimPath: func(directory, _ string) string {
				return filepath.Join(directory, "outside.json")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			packsRoot := filepath.Join(directory, "packs")
			writePackJSON(t, packsRoot, "sample")
			writeRegistryJSON(t, packsRoot, "sample", map[string]any{
				test.resourceType: map[string]any{
					"product": "sample",
					"fetch":   map[string]any{"pagination": "single", "path": "items"},
				},
			})
			root := loadRootFromPacksDir(t, packsRoot)
			output := filepath.Join(directory, "pulls")
			if err := os.MkdirAll(output, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v, want nil", output, err)
			}
			victim := test.victimPath(directory, output)
			if err := os.WriteFile(victim, []byte("retain\n"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v, want nil", victim, err)
			}
			acquisitions := 0
			adapter := testAdapter("sample", nil)
			adapter.Acquire = func(CollectorAcquireInput) (CollectorAuthContext, error) {
				acquisitions++
				return sharedAuth, nil
			}
			transport := newDelayedPathTransport(t, map[string]HTTPResponse{
				"/api/items": jsonResponse(t, []any{map[string]any{"id": "a"}}, 200),
			}, nil)

			_, err := FetchResources(FetchResourcesOptions{
				Adapters:        map[string]CollectorAdapter{"sample": adapter},
				Context:         sharedContext,
				Environment:     Environment{},
				Mode:            AuthModeOneAPI,
				OutputDirectory: output,
				Root:            root,
				Selectors:       []string{"sample"},
				Transport:       transport,
			})
			if err == nil || !strings.Contains(err.Error(), "is not a safe output filename") {
				t.Errorf("FetchResources(resourceType=%q) error = %v, want unsafe-output error", test.resourceType, err)
			}
			if acquisitions != 0 {
				t.Errorf("FetchResources(resourceType=%q) acquisitions = %d, want zero", test.resourceType, acquisitions)
			}
			if len(transport.requests) != 0 {
				t.Errorf("FetchResources(resourceType=%q) requests = %v, want none", test.resourceType, transport.requests)
			}
			if content, readErr := os.ReadFile(victim); readErr != nil || string(content) != "retain\n" {
				t.Errorf("os.ReadFile(%q) = (%q, %v), want unchanged victim", victim, content, readErr)
			}
		})
	}
}

func TestFetchInvalidationRefusesDirectoryBeforeResourceRequests(t *testing.T) {
	directory := t.TempDir()
	packsRoot := filepath.Join(directory, "packs")
	writePackJSON(t, packsRoot, "sample")
	writeRegistryJSON(t, packsRoot, "sample", map[string]any{
		"sample_a": map[string]any{
			"product": "sample",
			"fetch":   map[string]any{"pagination": "single", "path": "items-a"},
		},
	})
	root := loadRootFromPacksDir(t, packsRoot)
	output := filepath.Join(directory, "pulls")
	destination := filepath.Join(output, "sample_a.json")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", destination, err)
	}
	transport := newDelayedPathTransport(t, map[string]HTTPResponse{
		"/api/items-a": jsonResponse(t, []any{map[string]any{"id": "a"}}, 200),
	}, nil)

	_, err := FetchResources(FetchResourcesOptions{
		Adapters:        map[string]CollectorAdapter{"sample": testAdapter("sample", nil)},
		Context:         sharedContext,
		Environment:     Environment{},
		Mode:            AuthModeOneAPI,
		OutputDirectory: output,
		Root:            root,
		Selectors:       []string{"sample_a"},
		Transport:       transport,
	})
	wantError := "unlink " + destination + ": is a directory"
	if err == nil || err.Error() != wantError {
		t.Errorf("FetchResources() error = %v, want %q", err, wantError)
	}
	if len(transport.requests) != 0 {
		t.Errorf("FetchResources() requests = %v, want none before invalidation succeeds", transport.requests)
	}
	if info, statErr := os.Lstat(destination); statErr != nil || !info.IsDir() {
		t.Errorf("os.Lstat(%q) = (%v, %v), want preserved directory", destination, info, statErr)
	}
}

func TestFetchInvalidationUnlinksSymlinkWithoutTouchingTarget(t *testing.T) {
	root := loadFullRoot(t)
	directory := t.TempDir()
	output := filepath.Join(directory, "pulls")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v, want nil", output, err)
	}
	target := filepath.Join(directory, "outside.json")
	if err := os.WriteFile(target, []byte("retain\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", target, err)
	}
	destination := filepath.Join(output, "zia_extranet.json")
	if err := os.Symlink(target, destination); err != nil {
		t.Skipf("os.Symlink(%q, %q) unavailable: %v", target, destination, err)
	}

	result, err := FetchResources(FetchResourcesOptions{
		Adapters:        map[string]CollectorAdapter{"zia": testAdapter("zia", nil)},
		Context:         sharedContext,
		Environment:     Environment{},
		Mode:            AuthModeOneAPI,
		OutputDirectory: output,
		Root:            root,
		Selectors:       []string{"zia_extranet"},
		Transport:       newQueueTransport(t, jsonResponse(t, map[string]any{}, 403)),
	})
	if err != nil {
		t.Fatalf("FetchResources() error = %v, want nil", err)
	}
	if _, skipped := result.Skipped["zia_extranet"]; !skipped {
		t.Errorf("FetchResources().Skipped = %v, want zia_extranet", result.Skipped)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Lstat(%q) error = %v, want os.ErrNotExist", destination, err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "retain\n" {
		t.Errorf("os.ReadFile(%q) = (%q, %v), want unchanged target", target, content, err)
	}
}

// TestConcurrentWriteFailuresRetainSelectionOrderedPrimaryError ports
// "concurrent write failures retain selection-ordered primary error and
// prior diagnostics" from node-tests/rest-collector.test.ts. The barrier
// transport proves sample_b and sample_c overlap and that sample_c's fatal
// outcome is recorded before sample_b's response is released, yet the error
// this package surfaces names sample_b. That is only true because
// fetchResourcesBatch never reports a "fatal" outcome as soon as one
// occurs; it collects every outcome first, then walks `wanted` in fixed
// registry order (a, b, c, d) and reports the *first* fatal outcome it finds
// there -- b comes before c in that order, regardless of which one's
// goroutine actually finished first. See runFetchWorkers's doc comment in
// rest.go for why this is provably true, not merely true given these
// specific delay values.
func TestConcurrentWriteFailuresRetainSelectionOrderedPrimaryError(t *testing.T) {
	directory := t.TempDir()
	packsRoot := filepath.Join(directory, "packs")
	writePackJSON(t, packsRoot, "sample")
	registry := map[string]any{}
	for _, suffix := range []string{"a", "b", "c", "d"} {
		registry["sample_"+suffix] = map[string]any{
			"product": "sample",
			"fetch":   map[string]any{"pagination": "single", "path": "items-" + suffix},
		}
	}
	writeRegistryJSON(t, packsRoot, "sample", registry)
	isolatedRoot := loadRootFromPacksDir(t, packsRoot)

	responses := map[string]HTTPResponse{
		"/api/items-a": jsonResponse(t, []any{map[string]any{"id": "a"}}, 200),
		"/api/items-b": jsonResponse(t, []any{map[string]any{"id": "b"}}, 200),
		"/api/items-c": jsonResponse(t, []any{map[string]any{"id": "c"}}, 200),
		"/api/items-d": jsonResponse(t, []any{map[string]any{"id": "d"}}, 200),
	}

	for _, concurrency := range []int{1, 2} {
		output := filepath.Join(directory, "pulls-"+strconv.Itoa(concurrency))
		var transport HttpTransport
		var serialTransport *delayedPathTransport
		var barrierTransport *writeFailureBarrierTransport
		if concurrency == 1 {
			serialTransport = newDelayedPathTransport(t, responses, nil)
			serialTransport.beforeReturn = func(pathname string) error {
				if pathname != "/api/items-b" {
					return nil
				}
				return os.MkdirAll(filepath.Join(output, "sample_b.json"), 0o755)
			}
			transport = serialTransport
		} else {
			barrierTransport = newWriteFailureBarrierTransport(responses, output)
			transport = barrierTransport
		}
		var diagnostics []string
		_, err := FetchResources(FetchResourcesOptions{
			Adapters:        map[string]CollectorAdapter{"sample": testAdapter("sample", nil)},
			Concurrency:     intPtr(concurrency),
			Context:         sharedContext,
			Environment:     Environment{},
			Mode:            AuthModeOneAPI,
			OnDiagnostic:    func(message string) { diagnostics = append(diagnostics, message) },
			OutputDirectory: output,
			Root:            isolatedRoot,
			Selectors:       []string{"sample"},
			Transport:       transport,
		})
		if err == nil {
			t.Fatalf("concurrency %d: expected an error, got none", concurrency)
		}
		// os.WriteFile reports this exact *fs.PathError for the
		// selection-first fatal destination (Go-native wording; see
		// docs/go-runtime-v2.md §2 -- filesystem error text is not part of
		// the compatibility contract). Comparing the full string also
		// proves that a faster sample_c failure never wins.
		wantError := "open " + filepath.Join(output, "sample_b.json") + ": is a directory"
		if err.Error() != wantError {
			t.Errorf("concurrency %d: FetchResources() error = %q, want %q", concurrency, err.Error(), wantError)
		}
		wantDiagnostics := []string{"wrote " + filepath.Join(output, "sample_a.json") + " (1 items)"}
		if !equalStrings(diagnostics, wantDiagnostics) {
			t.Errorf("concurrency %d: diagnostics = %v, want %v", concurrency, diagnostics, wantDiagnostics)
		}
		written, err := os.ReadFile(filepath.Join(output, "sample_a.json"))
		if err != nil {
			t.Fatalf("concurrency %d: read sample_a.json: %v", concurrency, err)
		}
		if want := "[\n  {\n    \"id\": \"a\"\n  }\n]\n"; string(written) != want {
			t.Errorf("concurrency %d: sample_a.json = %q, want %q", concurrency, written, want)
		}
		if concurrency == 1 {
			wantRequests := []string{"/api/items-a", "/api/items-b"}
			if !equalStrings(serialTransport.requests, wantRequests) {
				t.Errorf("concurrency 1: requests = %v, want %v (sequential mode must never reach c after b's fatal outcome)", serialTransport.requests, wantRequests)
			}
			continue
		}

		events, maxActive := barrierTransport.snapshot()
		if maxActive < 2 {
			t.Errorf("concurrency 2: maxActive = %d, want >= 2 to prove sample_b/sample_c overlap; events=%v", maxActive, events)
		}
		bRequest := indexOfString(events, "request:/api/items-b")
		cRequest := indexOfString(events, "request:/api/items-c")
		cRecorded := indexOfString(events, "after-c-record:/api/items-d")
		bReleased := indexOfString(events, "release:/api/items-b")
		if bRequest < 0 || cRequest < 0 || cRecorded < 0 || bReleased < 0 {
			t.Errorf("concurrency 2: barrier events = %v, want b request, c request, post-c-record d request, and b release", events)
		} else if bRequest >= cRecorded || cRequest >= cRecorded || cRecorded >= bReleased {
			t.Errorf("concurrency 2: barrier events = %v, want b/c requests before c recorded and b release strictly afterward", events)
		}
		if _, err := os.Stat(filepath.Join(output, "sample_d.json")); err != nil {
			t.Errorf("concurrency 2: os.Stat(sample_d.json) error = %v, want d worker to complete after proving c was recorded", err)
		}
	}
}

func TestFetchConcurrencyRejectsInvalidLibraryValues(t *testing.T) {
	packRoot := loadFullRoot(t)
	// Only the integer-representable invalid values from the Node test
	// are ported (0, -1, 65): the Node source additionally covers 1.5 and
	// NaN, both structurally unrepresentable as Go's *int concurrency
	// field -- Go's static typing already provides the guarantee those
	// two cases exist to prove at compile time, so there is no runtime
	// behavior left to port for them (see this port's report).
	for _, concurrency := range []int{0, -1, 65} {
		acquisitions := 0
		adapter := testAdapter("zia", nil)
		adapter.Acquire = func(CollectorAcquireInput) (CollectorAuthContext, error) {
			acquisitions++
			return sharedAuth, nil
		}
		_, err := FetchResources(FetchResourcesOptions{
			Adapters:        map[string]CollectorAdapter{"zia": adapter},
			Concurrency:     intPtr(concurrency),
			Context:         sharedContext,
			Environment:     Environment{},
			Mode:            AuthModeOneAPI,
			OutputDirectory: t.TempDir(),
			Root:            packRoot,
			Selectors:       []string{"zia_advanced_settings"},
			Transport:       newQueueTransport(t),
		})
		if err == nil || !strings.Contains(err.Error(), "fetch concurrency must be a positive integer") {
			t.Errorf("concurrency %d: error = %v, want a 'fetch concurrency must be a positive integer' error", concurrency, err)
		}
		if acquisitions != 0 {
			t.Errorf("concurrency %d: acquisitions = %d, want 0 (validation must precede authentication)", concurrency, acquisitions)
		}
	}
}

func TestBoundedSchedulingRotatesProductsInsteadOfDrainingOneProductFirst(t *testing.T) {
	directory := t.TempDir()
	packsRoot := filepath.Join(directory, "packs")
	for _, product := range []string{"alpha", "beta"} {
		writePackJSON(t, packsRoot, product)
		registry := map[string]any{}
		for _, suffix := range []string{"a", "b"} {
			registry[product+"_"+suffix] = map[string]any{
				"product": product,
				"fetch":   map[string]any{"pagination": "single", "path": product + "-" + suffix},
			}
		}
		writeRegistryJSON(t, packsRoot, product, registry)
	}
	isolatedRoot := loadRootFromPacksDir(t, packsRoot)

	paths := []string{"/api/alpha-a", "/api/alpha-b", "/api/beta-a", "/api/beta-b"}
	responses := make(map[string]HTTPResponse, len(paths))
	for _, path := range paths {
		responses[path] = jsonResponse(t, []any{}, 200)
	}
	adapters := map[string]CollectorAdapter{"alpha": testAdapter("alpha", nil), "beta": testAdapter("beta", nil)}

	serial := newDelayedPathTransport(t, responses, nil)
	if _, err := FetchResources(FetchResourcesOptions{
		Adapters: adapters, Concurrency: intPtr(1), Context: sharedContext, Environment: Environment{},
		Mode: AuthModeOneAPI, OutputDirectory: filepath.Join(directory, "serial"), Root: isolatedRoot,
		Selectors: nil, Transport: serial,
	}); err != nil {
		t.Fatalf("concurrency 1: FetchResources: %v", err)
	}
	if !equalStrings(serial.requests, paths) {
		t.Errorf("concurrency 1: requests = %v, want %v (sequential mode drains registry order, alpha before beta)", serial.requests, paths)
	}

	delays := make(map[string]time.Duration, len(paths))
	for _, path := range paths {
		delays[path] = 5 * time.Millisecond
	}
	transport := newDelayedPathTransport(t, responses, delays)
	if _, err := FetchResources(FetchResourcesOptions{
		Adapters: adapters, Concurrency: intPtr(2), Context: sharedContext, Environment: Environment{},
		Mode: AuthModeOneAPI, OutputDirectory: filepath.Join(directory, "pulls"), Root: isolatedRoot,
		Selectors: nil, Transport: transport,
	}); err != nil {
		t.Fatalf("concurrency 2: FetchResources: %v", err)
	}
	if len(transport.requests) < 2 {
		t.Fatalf("concurrency 2: requests = %v, want at least 2", transport.requests)
	}
	firstTwo := map[string]struct{}{transport.requests[0]: {}, transport.requests[1]: {}}
	want := map[string]struct{}{"/api/alpha-a": {}, "/api/beta-a": {}}
	if !reflect.DeepEqual(firstTwo, want) {
		t.Errorf("concurrency 2: first two dispatched requests = %v, want one from each product (%v)", firstTwo, want)
	}
}
