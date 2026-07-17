package modulesgen

// helpers_test.go holds fixtures shared by generator_test.go, the Go port
// of node-tests/module-generator.test.ts: locating the repo root (for the
// committed packs/, packsets/, and tests/fixtures/gen/ trees), writing
// synthetic pack roots (the Go analogue of the TS test's syntheticRoot
// helper), and resolving a real `terraform` executable for the tests that
// exercise the Terraform-backed formatter, skipping them if none is on
// PATH.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// repoRoot walks up from this test file's directory until it finds a
// directory containing "packs", "packsets", and "tests" -- this package's
// fixture set, mirroring go/internal/metadata/gate_test.go's repoRoot
// (each package that needs repo-root fixtures keeps its own copy; see that
// function's doc comment for the convention).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, packsErr := os.Stat(filepath.Join(dir, "packs"))
		_, packsetsErr := os.Stat(filepath.Join(dir, "packsets"))
		_, testsErr := os.Stat(filepath.Join(dir, "tests"))
		if packsErr == nil && packsetsErr == nil && testsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding a directory containing packs/, packsets/, and tests/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// writeJSONFile marshals value as JSON and writes it to path, creating
// parent directories as needed.
func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeRawFile writes content verbatim to path, creating parent
// directories as needed. Used where exact JSON source-text spelling
// (numeric literal shape, in particular) matters to the test.
func writeRawFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// committedRoot loads the repo's committed packs/ under packsets/full.json,
// ported from module-generator.test.ts's committedRoot helper.
func committedRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packsets", "full.json")
	catalogPath := filepath.Join(root, "packsets", "full.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded
}

// simpleSchema is the Go analogue of module-generator.test.ts's
// SIMPLE_SCHEMA fixture: one required string, one optional string, one
// computed-only string, and a "rules" block type (nesting_mode "set",
// min_items 1) with a single required bool attribute.
var simpleSchema = metadata.JsonObject{
	"block": metadata.JsonObject{
		"attributes": metadata.JsonObject{
			"id":   metadata.JsonObject{"type": "string", "optional": true, "computed": true},
			"name": metadata.JsonObject{"type": "string", "required": true},
			"note": metadata.JsonObject{"type": "string", "optional": true},
		},
		"block_types": metadata.JsonObject{
			"rules": metadata.JsonObject{
				"nesting_mode": "set",
				"min_items":    float64(1),
				"block": metadata.JsonObject{
					"attributes": metadata.JsonObject{
						"enabled": metadata.JsonObject{"type": "bool", "required": true},
					},
				},
			},
		},
	},
}

// syntheticRootOptions mirrors the options bag module-generator.test.ts's
// syntheticRoot helper accepts. Zero values reproduce that helper's own
// defaults: Schema falls back to simpleSchema, Pin to "1.2.3", Source to
// "example/sample". OmitPin/OmitSource port the TS helper's
// `options?.pin !== null` / `options?.source !== null` three-state
// handling (unset => default, explicit null => omit the key entirely, any
// other value => use it) -- Go has no third "explicitly omitted" state for
// a plain string field, so that case is these two dedicated bools instead.
type syntheticRootOptions struct {
	Schema       any
	OmitPin      bool
	Pin          string
	OmitSource   bool
	Source       string
	Sample       any
	OverrideText string
	MainOverride string
}

// syntheticRoot ports module-generator.test.ts's syntheticRoot helper: a
// throwaway one-pack, one-resource-type pack root under a fresh temporary
// directory, for tests that need to control the schema/overrides precisely
// rather than exercising the committed Zscaler packs.
func syntheticRoot(t *testing.T, options syntheticRootOptions) (string, metadata.LoadedPackRoot) {
	t.Helper()
	directory := t.TempDir()

	pack := metadata.JsonObject{
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
	}
	if !options.OmitPin {
		pin := options.Pin
		if pin == "" {
			pin = "1.2.3"
		}
		pack["pin"] = pin
	}
	if !options.OmitSource {
		source := options.Source
		if source == "" {
			source = "example/sample"
		}
		pack["provider_sources"] = metadata.JsonObject{"sample": source}
	}
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), pack)
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), metadata.JsonObject{
		"sample_resource": metadata.JsonObject{"generate": true, "product": "sample"},
	})
	schema := options.Schema
	if schema == nil {
		schema = simpleSchema
	}
	writeJSONFile(t, filepath.Join(directory, "sample", "schemas", "provider", "sample.json"), metadata.JsonObject{
		"resource_schemas": metadata.JsonObject{"sample_resource": schema},
	})

	if options.OverrideText != "" {
		writeRawFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource.json"), options.OverrideText)
	} else if options.Sample != nil {
		writeJSONFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource.json"), metadata.JsonObject{
			"sample": options.Sample,
		})
	}
	if options.MainOverride != "" {
		writeRawFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource", "main.tf"), options.MainOverride)
	}

	profile := filepath.Join(directory, "profile.json")
	writeJSONFile(t, profile, metadata.JsonObject{
		"kind":    "infrawright.pack-set",
		"version": float64(1),
		"packs":   []any{"sample"},
		"shared":  []any{},
	})

	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   directory,
		ProfilePath: &profile,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic): %v", err)
	}
	return directory, loaded
}

// terraformExecutable resolves a real `terraform` binary the way this
// port's other Terraform/Python oracle helpers do (TF env var first, then
// PATH), skipping the calling test if none is found -- ported per this
// port's task brief: "Terraform 1.15.4 is installed on this machine --
// where the Node tests use the real formatter, your tests may too
// (skip-if-absent guard, matching the repo's conventions)", the same
// pattern go/internal/pypath/paths_test.go's pythonOracle and
// go/cmd/iw/differential_test.go's node-oracle resolution follow.
func terraformExecutable(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("TF")); configured != "" {
		return configured
	}
	if resolved, err := exec.LookPath("terraform"); err == nil {
		return resolved
	}
	t.Skip("no terraform executable on PATH; set TF to enable this cross-check")
	return ""
}
