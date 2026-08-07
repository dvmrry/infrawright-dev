package collectors

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func followEntry(extra FetchEntry) FetchEntry {
	base := FetchEntry{
		Path: "locations",
		FollowPaths: []FollowPath{
			{Path: "locations/{id}/sublocations", FromField: "id"},
		},
	}
	if extra.Path != "" {
		base.Path = extra.Path
	}
	if extra.FollowPaths != nil {
		base.FollowPaths = extra.FollowPaths
	}
	base.Expand = extra.Expand
	base.MergePaths = extra.MergePaths
	return testEntry(PaginationSingle, base)
}

// TestFetchResourceFollowPathsWalksEachBaseItem is the motivating case: the
// locations list returns parents only, and each parent's sublocations arrive
// from a per-parent follow-up, concatenated after the base items.
func TestFetchResourceFollowPathsWalksEachBaseItem(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, []any{
			map[string]any{"id": json.Number("7"), "name": "HQ"},
			map[string]any{"id": json.Number("9"), "name": "Branch"},
		}, 200),
		jsonResponse(t, []any{map[string]any{"id": json.Number("71"), "name": "HQ-Guest", "parentId": json.Number("7")}}, 200),
		jsonResponse(t, []any{map[string]any{"id": json.Number("91"), "name": "Branch-Guest", "parentId": json.Number("9")}}, 200),
	)
	items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: followEntry(FetchEntry{}), Mode: AuthModeOneAPI,
		ResourceType: "zia_location_management", Transport: transport,
	})
	if err != nil {
		t.Fatalf("FetchResource(follow): %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4 (2 parents + 2 sublocations)", len(items))
	}
	names := make([]string, len(items))
	for i, raw := range items {
		names[i], _ = raw.(map[string]any)["name"].(string)
	}
	wantNames := []string{"HQ", "Branch", "HQ-Guest", "Branch-Guest"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("item names = %v, want base-then-follow order %v", names, wantNames)
	}
	wantPaths := []string{"/api/locations", "/api/locations/7/sublocations", "/api/locations/9/sublocations"}
	if got := transport.requestPaths(); !equalStrings(got, wantPaths) {
		t.Errorf("request paths = %v, want %v", got, wantPaths)
	}
}

func TestFetchResourceFollowPathsSkipsItemsWithoutTheField(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, []any{
			map[string]any{"id": json.Number("7"), "name": "HQ"},
			map[string]any{"name": "no id at all"},
			map[string]any{"id": nil, "name": "null id"},
		}, 200),
		jsonResponse(t, []any{map[string]any{"id": json.Number("71")}}, 200),
	)
	items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: followEntry(FetchEntry{}), Mode: AuthModeOneAPI,
		ResourceType: "sample", Transport: transport,
	})
	if err != nil {
		t.Fatalf("FetchResource(follow, partial fields): %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4 (3 base + 1 followed)", len(items))
	}
	wantPaths := []string{"/api/locations", "/api/locations/7/sublocations"}
	if got := transport.requestPaths(); !equalStrings(got, wantPaths) {
		t.Errorf("request paths = %v, want only the item carrying the field: %v", got, wantPaths)
	}
}

func TestFetchResourceFollowPathsPropagatesFailure(t *testing.T) {
	transport := newQueueTransport(t,
		jsonResponse(t, []any{map[string]any{"id": json.Number("7")}}, 200),
		jsonResponse(t, map[string]any{"error": "boom"}, 500),
	)
	_, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: followEntry(FetchEntry{}), Mode: AuthModeOneAPI,
		ResourceType: "sample", Transport: transport,
	})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("FetchResource(follow failure) error = %v, want the follow request's HTTP failure", err)
	}
}

func TestFetchResourceFollowPathsRevalidatesAtCollectorBoundary(t *testing.T) {
	tests := []struct {
		name  string
		entry FetchEntry
		want  string
	}{
		{
			name:  "unsafe_path",
			entry: FetchEntry{FollowPaths: []FollowPath{{Path: "../{id}/x", FromField: "id"}}},
			want:  "must not contain raw or percent-encoded dot path segments",
		},
		{
			name:  "missing_placeholder",
			entry: FetchEntry{FollowPaths: []FollowPath{{Path: "locations/sublocations", FromField: "id"}}},
			want:  `must contain the placeholder "{id}" exactly once`,
		},
		{
			name:  "repeated_placeholder",
			entry: FetchEntry{FollowPaths: []FollowPath{{Path: "l/{id}/x/{id}", FromField: "id"}}},
			want:  `must contain the placeholder "{id}" exactly once`,
		},
		{
			name:  "undeclared_braces",
			entry: FetchEntry{FollowPaths: []FollowPath{{Path: "l/{id}/{other}", FromField: "id"}}},
			want:  "must not contain undeclared expansion braces",
		},
		{
			name: "duplicate_follow_path",
			entry: FetchEntry{FollowPaths: []FollowPath{
				{Path: "l/{id}/x", FromField: "id"}, {Path: "l/{id}/x", FromField: "id"},
			}},
			want: "is declared more than once",
		},
		{
			name: "with_expand",
			entry: FetchEntry{
				FollowPaths: []FollowPath{{Path: "l/{id}/x", FromField: "id"}},
				Expand:      map[string][]string{"kind": {"a"}},
			},
			want: "cannot be combined with expand",
		},
		{
			name: "with_merge_paths",
			entry: FetchEntry{
				FollowPaths: []FollowPath{{Path: "l/{id}/x", FromField: "id"}},
				MergePaths:  []string{"other"},
			},
			want: "cannot be combined with merge paths",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := newQueueTransport(t)
			_, err := FetchResource(FetchResourceOptions{
				Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
				Entry: followEntry(test.entry), Mode: AuthModeOneAPI,
				ResourceType: "sample", Transport: transport,
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

func TestFetchResourceWithoutFollowPathsIsUnchanged(t *testing.T) {
	transport := newQueueTransport(t, jsonResponse(t, []any{map[string]any{"id": json.Number("7")}}, 200))
	items, err := FetchResource(FetchResourceOptions{
		Adapter: testAdapter("", nil), Auth: sharedAuth, Context: sharedContext,
		Entry: testEntry(PaginationSingle, FetchEntry{Path: "locations"}),
		Mode:  AuthModeOneAPI, ResourceType: "sample", Transport: transport,
	})
	if err != nil {
		t.Fatalf("FetchResource(no follow): %v", err)
	}
	if len(items) != 1 {
		t.Errorf("items = %d, want 1", len(items))
	}
	if got := transport.requestPaths(); !equalStrings(got, []string{"/api/locations"}) {
		t.Errorf("request paths = %v, want only the base path", got)
	}
}
