package collectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchQueryPreservesCanonicalNumberTokens(t *testing.T) {
	directory := t.TempDir()
	packDirectory := filepath.Join(directory, "sample")
	if err := os.MkdirAll(packDirectory, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	packJSON, _ := json.Marshal(map[string]any{"provider_prefixes": map[string]string{"sample_": "sample"}})
	if err := os.WriteFile(filepath.Join(packDirectory, "pack.json"), packJSON, 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}
	queryJSON := `{"integer":1,"decimal":1.0,"exponent":1e0,"negative_zero":-0.0,"tiny":1e-7}`
	registryJSON := `{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"items","query":` + queryJSON + `}}}`
	if err := os.WriteFile(filepath.Join(packDirectory, "registry.json"), []byte(registryJSON), 0o644); err != nil {
		t.Fatalf("write registry.json: %v", err)
	}
	root := loadRootFromPacksDir(t, directory)

	transport := &recordingTransport{responses: []HTTPResponse{jsonResponse(t, []any{}, 200)}}
	result, err := FetchResources(FetchResourcesOptions{
		Adapters:        map[string]CollectorAdapter{"sample": testAdapter("sample", nil)},
		Context:         sharedContext,
		Environment:     Environment{},
		Mode:            AuthModeOneAPI,
		OutputDirectory: filepath.Join(directory, "pulls"),
		Root:            root,
		Selectors:       []string{"sample_resource"},
		Transport:       transport,
	})
	if err != nil {
		t.Fatalf("FetchResources: %v", err)
	}
	if !equalStrings(result.Processed, []string{"sample_resource"}) {
		t.Fatalf("processed = %v, want [sample_resource]", result.Processed)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(transport.requests))
	}
	want := "decimal=1.0&exponent=1.0&integer=1&negative_zero=-0.0&tiny=1e-07"
	if got := transport.requests[0].URL.RawQuery; got != want {
		t.Errorf("query = %s, want %s", got, want)
	}
}
