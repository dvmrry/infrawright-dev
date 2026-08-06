package collectors

import (
	"reflect"
	"strings"
	"testing"
)

func TestFetchResourceMergePathsCombinesSingletonObjects(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, map[string]any{"whitelistUrls": []any{"https://allow.example"}}, 200),
		jsonResponse(t, map[string]any{"blacklistUrls": []any{"https://deny.example"}}, 200),
	)
	items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{
			Path: "security", MergePaths: []string{"security/advanced"},
		}),
		Mode: AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err != nil {
		t.Fatalf("FetchResource(merge): %v", err)
	}
	want := []any{map[string]any{
		"whitelistUrls": []any{"https://allow.example"},
		"blacklistUrls": []any{"https://deny.example"},
	}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("merged items = %#v, want %#v", items, want)
	}
	wantPaths := []string{"/api/security", "/api/security/advanced"}
	if got := transport.requestPaths(); !equalStrings(got, wantPaths) {
		t.Errorf("request paths = %v, want %v", got, wantPaths)
	}
}

func TestFetchResourceMergePathsRefusesDuplicateKey(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, map[string]any{"whitelistUrls": []any{"a"}, "shared": true}, 200),
		jsonResponse(t, map[string]any{"blacklistUrls": []any{"b"}, "shared": false}, 200),
	)
	_, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{
			Path: "security", MergePaths: []string{"security/advanced"},
		}),
		Mode: AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err == nil || !strings.Contains(err.Error(), `both returned key "shared"`) ||
		!strings.Contains(err.Error(), `"security"`) || !strings.Contains(err.Error(), `"security/advanced"`) {
		t.Fatalf("FetchResource(merge collision) error = %v, want disjoint-key refusal naming both paths", err)
	}
}

func TestFetchResourceMergePathsRefusesNonObjectPayload(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, map[string]any{"whitelistUrls": []any{"a"}}, 200),
		jsonResponse(t, []any{map[string]any{"id": "1"}}, 200),
	)
	_, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{
			Path: "security", MergePaths: []string{"security/advanced"},
		}),
		Mode: AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err == nil || !strings.Contains(err.Error(), "did not return one settings object") {
		t.Fatalf("FetchResource(merge non-object) error = %v, want singleton-object refusal", err)
	}
}
