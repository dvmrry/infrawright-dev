package openapiadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestLegacyMapIsDetachedAndAllowsMissingInfo(t *testing.T) {
	t.Parallel()
	bytes := []byte(`{"openapi":"3.1.0","servers":[{"url":"https://api.example.test"}],"paths":{"/widgets":{"get":{},"POST":{}},"/widgets/{id}":{"get":{},"patch":{}}},"components":{"schemas":{"Widget":{}}}}`)
	document, err := ParseForMetadata(context.Background(), legacyStatus(bytes))
	if err != nil {
		t.Fatalf("ParseForMetadata() error = %v", err)
	}
	first, err := document.LegacyMap(context.Background())
	if err != nil {
		t.Fatalf("LegacyMap() error = %v", err)
	}
	if first.Title != nil || first.Version == nil || *first.Version != "3.1.0" || first.ComponentSchemaCount != 1 {
		t.Fatalf("LegacyMap() = %#v", first)
	}
	if len(first.Paths) != 2 || len(first.Paths[0].Methods) != 1 || first.Paths[0].Methods[0] != "get" {
		t.Fatalf("LegacyMap paths = %#v", first.Paths)
	}
	first.ServerURLs[0] = "mutated"
	first.Paths[0].Methods[0] = "mutated"
	second, err := document.LegacyMap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.ServerURLs[0] != "https://api.example.test" || second.Paths[0].Methods[0] != "get" {
		t.Fatalf("LegacyMap leaked caller mutation: %#v", second)
	}
}

func TestLegacyMapObservesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	document, err := ParseForMetadata(context.Background(), legacyStatus([]byte(`{"openapi":"3.0.3","paths":{}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.LegacyMap(ctx); err == nil {
		t.Fatal("LegacyMap(cancelled) error = nil")
	}
}

func TestLegacyMapTreatsMissingAndNonObjectPathsAsEmptyInventory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "missing paths", body: `{"openapi":"3.0.3"}`, want: 0},
		{name: "non-object paths", body: `{"openapi":"3.0.3","paths":{"/bad":7,"/good":{"get":{}}}}`, want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			document, err := ParseForMetadata(context.Background(), legacyStatus([]byte(test.body)))
			if err != nil {
				t.Fatal(err)
			}
			view, err := document.LegacyMap(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Paths) != test.want {
				t.Fatalf("paths = %#v, want %d rows", view.Paths, test.want)
			}
			if test.want == 2 && (view.Paths[0].Template != "/bad" || len(view.Paths[0].Methods) != 0 || view.Paths[1].Template != "/good") {
				t.Fatalf("path inventory = %#v", view.Paths)
			}
		})
	}
}

func legacyStatus(bytes []byte) sourcebind.OpenAPIStatus {
	sum := sha256.Sum256(bytes)
	return sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{Path: "root.json", Bytes: bytes, SHA256: hex.EncodeToString(sum[:])}}}
}
