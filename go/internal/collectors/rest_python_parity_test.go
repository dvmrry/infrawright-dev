package collectors

// rest_python_parity_test.go ports
// node-tests/rest-collector-python-parity.test.ts's "fetch query float
// tokens match Python urllib encoding after registry load" test.
//
// The Node test spawns a real Python oracle process
// (urllib.parse.urlencode(json.loads(...))) at test time to derive the
// expected query string in the registry's own *source* key order
// (integer, decimal, exponent, negative_zero, tiny). This package cannot
// reproduce that key order (see queryPair's doc comment in rest.go: the
// metadata package's canonjson-decoded map[string]any has already lost
// source key order by the time a FetchEntry's Query field is built), so
// this port's expected string is instead the oracle's own output for the
// *sorted*-key ordering this package actually produces --
//
//	python3 -c 'import json, urllib.parse; d = json.loads(...); \
//	  print(urllib.parse.urlencode(dict(sorted(d.items()))))'
//
// -- verified once by hand against the same Python oracle the Node test
// uses (see this port's report). What both the Node test and this one
// actually pin is per-value token parity with Python's str()/repr(float)
// (integer "1", "1.0" for both a literal decimal and 1e0, "-0.0" for
// negative zero, "1e-07" for a small magnitude in scientific notation) --
// exactly the canonjson.CanonicalNumberToken/FiniteFloatToken behavior
// queryScalar in rest.go delegates to; only the *ordering* of the encoded
// pairs differs from the Node oracle run, and only because of the
// already-documented, already-accepted key-order-loss divergence.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchQueryFloatTokensMatchPythonNumberRepr(t *testing.T) {
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
	// python3 -c 'import json, urllib.parse; d = json.loads(<queryJSON>); print(urllib.parse.urlencode(dict(sorted(d.items()))))'
	want := "decimal=1.0&exponent=1.0&integer=1&negative_zero=-0.0&tiny=1e-07"
	if got := transport.requests[0].URL.RawQuery; got != want {
		t.Errorf("query = %s, want %s", got, want)
	}
}
