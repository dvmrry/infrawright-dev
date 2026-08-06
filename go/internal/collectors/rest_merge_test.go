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

// TestFetchResourceMergePathsRevalidatesAtCollectorBoundary pins the second
// half of the merge contract's single source of truth: a FetchEntry built
// directly by a library caller (never parsed from a registry, so
// metadata.validateMergePaths never saw it) is refused before any URL is
// composed. Each case would otherwise reach the transport.
func TestFetchResourceMergePathsRevalidatesAtCollectorBoundary(t *testing.T) {
	tests := []struct {
		name  string
		entry FetchEntry
		want  string
	}{
		{
			name:  "unsafe_merge_path",
			entry: FetchEntry{Path: "security", MergePaths: []string{"../secrets"}},
			want:  "must not contain raw or percent-encoded dot path segments",
		},
		{
			name:  "unsafe_base_path",
			entry: FetchEntry{Path: "security?x=1", MergePaths: []string{"security/advanced"}},
			want:  "must not contain query or fragment delimiters",
		},
		{
			name:  "duplicate_path",
			entry: FetchEntry{Path: "security", MergePaths: []string{"security"}},
			want:  "is declared more than once",
		},
		{
			name:  "expansion_braces",
			entry: FetchEntry{Path: "security", MergePaths: []string{"security/{kind}"}},
			want:  "must not contain expansion braces",
		},
		{
			name: "with_expand",
			entry: FetchEntry{
				Path: "security", MergePaths: []string{"security/advanced"},
				Expand: map[string][]string{"kind": {"a"}},
			},
			want: "cannot be combined with expand",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := newQueueTransport(t)
			_, err := FetchResource(FetchResourceOptions{
				Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
				Entry: testEntry(PaginationSingle, test.entry),
				Mode:  AuthModeOneAPI, ResourceType: "sample", Transport: transport,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("FetchResource(%s) error = %v, want substring %q", test.name, err, test.want)
			}
			if got := transport.requestPaths(); len(got) != 0 {
				t.Errorf("FetchResource(%s) issued requests %v, want none before validation", test.name, got)
			}
		})
	}
}

func TestFetchResourceMergePathsRefusesNonSinglePagination(t *testing.T) {
	transport := newQueueTransport(t)
	_, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationZia, FetchEntry{
			Path: "security", MergePaths: []string{"security/advanced"},
		}),
		Mode: AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err == nil || !strings.Contains(err.Error(), `requires pagination "single"`) {
		t.Fatalf("FetchResource(zia pagination) error = %v, want singleton-pagination refusal", err)
	}
	if got := transport.requestPaths(); len(got) != 0 {
		t.Errorf("issued requests %v, want none before validation", got)
	}
}
