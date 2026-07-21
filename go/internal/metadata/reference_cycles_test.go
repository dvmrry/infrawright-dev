package metadata

import (
	"path/filepath"
	"strings"
	"testing"
)

const referenceCycleRemedy = "resolve one direction via a literal ID or operator expression"

func referenceSpec(referent string) JsonObject {
	return JsonObject{"name_field": "name", "referent": referent}
}

func referenceManifest(references JsonObject) JsonObject {
	return JsonObject{"references": references}
}

func assertReferenceCycle(t *testing.T, value JsonObject, want string) {
	t.Helper()
	_, err := ValidatePackManifest(value, "manifest.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected cycle error, got nil")
	}
	if got := err.Error(); got != want {
		t.Fatalf("cycle error = %q, want %q", got, want)
	}
}

func TestDeclaredReferenceCyclesReportDeterministicClosedPaths(t *testing.T) {
	tests := []struct {
		name       string
		references JsonObject
		want       string
	}{
		{
			name: "self",
			references: JsonObject{
				"sample_self": JsonObject{"self_id": referenceSpec("sample_self")},
			},
			want: "declared reference cycle: sample_self -> sample_self; " + referenceCycleRemedy,
		},
		{
			name: "two nodes",
			references: JsonObject{
				"sample_a": JsonObject{"b_id": referenceSpec("sample_b")},
				"sample_b": JsonObject{"a_id": referenceSpec("sample_a")},
			},
			want: "declared reference cycle: sample_a -> sample_b -> sample_a; " + referenceCycleRemedy,
		},
		{
			name: "long cycle",
			references: JsonObject{
				"sample_a": JsonObject{"b_id": referenceSpec("sample_b")},
				"sample_b": JsonObject{"c_id": referenceSpec("sample_c")},
				"sample_c": JsonObject{"d_id": referenceSpec("sample_d")},
				"sample_d": JsonObject{"b_id": referenceSpec("sample_b")},
			},
			want: "declared reference cycle: sample_b -> sample_c -> sample_d -> sample_b; " + referenceCycleRemedy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertReferenceCycle(t, referenceManifest(test.references), test.want)
		})
	}
}

func TestDeclaredReferenceCyclesDeduplicateFieldsAndRemainDeterministic(t *testing.T) {
	references := JsonObject{
		"sample_a": JsonObject{
			"first_id":  referenceSpec("sample_b"),
			"second_id": referenceSpec("sample_b"),
		},
		"sample_b": JsonObject{"a_id": referenceSpec("sample_a")},
	}
	assertReferenceCycle(
		t,
		referenceManifest(references),
		"declared reference cycle: sample_a -> sample_b -> sample_a; "+referenceCycleRemedy,
	)
}

func TestLoadPackRootRejectsCrossManifestDeclaredReferenceCycle(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "first", "pack.json"), referenceManifest(JsonObject{
		"sample_first": JsonObject{"second_id": referenceSpec("sample_second")},
	}))
	writeJSONFile(t, filepath.Join(directory, "second", "pack.json"), referenceManifest(JsonObject{
		"sample_second": JsonObject{"first_id": referenceSpec("sample_first")},
	}))

	_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err == nil {
		t.Fatal("LoadPackRoot: expected cross-manifest cycle error, got nil")
	}
	want := "declared reference cycle: sample_first -> sample_second -> sample_first; " + referenceCycleRemedy
	if got := err.Error(); got != want {
		t.Fatalf("LoadPackRoot error = %q, want %q", got, want)
	}
}

func TestLoadPackRootUsesLaterManifestFieldOverwriteForCycleValidation(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "alpha", "pack.json"), referenceManifest(JsonObject{
		"sample_a": JsonObject{"target": referenceSpec("sample_b")},
	}))
	writeJSONFile(t, filepath.Join(directory, "beta", "pack.json"), referenceManifest(JsonObject{
		"sample_a": JsonObject{"target": referenceSpec("sample_c")},
	}))
	writeJSONFile(t, filepath.Join(directory, "gamma", "pack.json"), referenceManifest(JsonObject{
		"sample_b": JsonObject{"back": referenceSpec("sample_a")},
	}))

	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot: later sample_a.target declaration must shadow the provisional cycle edge: %v", err)
	}
	if len(loaded.Packs.Manifests) != 3 {
		t.Fatalf("manifest count = %d, want 3", len(loaded.Packs.Manifests))
	}
	if got := []string{loaded.Packs.Manifests[0].Name, loaded.Packs.Manifests[1].Name, loaded.Packs.Manifests[2].Name}; got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Fatalf("manifest order = %v, want [alpha beta gamma]", got)
	}
}

func TestLoadPackRootAllowsShadowedProvisionalSingleManifestCycle(t *testing.T) {
	directory := t.TempDir()
	alpha := referenceManifest(JsonObject{
		"sample_a": JsonObject{"target": referenceSpec("sample_b")},
		"sample_b": JsonObject{"back": referenceSpec("sample_a")},
	})
	writeJSONFile(t, filepath.Join(directory, "alpha", "pack.json"), alpha)
	writeJSONFile(t, filepath.Join(directory, "beta", "pack.json"), referenceManifest(JsonObject{
		"sample_a": JsonObject{"target": referenceSpec("sample_c")},
	}))

	if _, err := ValidatePackManifest(alpha, "alpha/pack.json"); err == nil {
		t.Fatal("ValidatePackManifest: standalone provisional cycle must still fail")
	}
	if _, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory}); err != nil {
		t.Fatalf("LoadPackRoot: later field overwrite must neutralize provisional cycle: %v", err)
	}
}

func TestLoadPackRootReportsLaterStructuralErrorBeforeAggregateCycle(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "alpha", "pack.json"), referenceManifest(JsonObject{
		"sample_a": JsonObject{"target": referenceSpec("sample_b")},
		"sample_b": JsonObject{"back": referenceSpec("sample_a")},
	}))
	writeJSONFile(t, filepath.Join(directory, "beta", "pack.json"), referenceManifest(JsonObject{
		"sample_c": JsonObject{"broken": JsonObject{"name_field": "name"}},
	}))

	_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err == nil {
		t.Fatal("LoadPackRoot: expected structural error, got nil")
	}
	want := filepath.Join(directory, "beta", "pack.json") + ".references.sample_c.broken: missing required key referent"
	if got := err.Error(); got != want {
		t.Fatalf("LoadPackRoot error = %q, want structural error %q", got, want)
	}
}

func TestDeclaredReferenceCyclesRunAfterReferenceStructureValidation(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{"b_id": referenceSpec("sample_b")},
		"sample_b": JsonObject{"a_id": JsonObject{"name_field": "name"}},
	})
	_, err := ValidatePackManifest(manifest, "broken.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected structural error, got nil")
	}
	want := "broken.json.references.sample_b.a_id: missing required key referent"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestCommittedPackReferencesRemainAcyclic(t *testing.T) {
	root := repoRoot(t)
	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: filepath.Join(root, "packs")})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	declaredFields := 0
	for _, manifest := range loaded.Packs.Manifests {
		references, _ := manifest.Data["references"].(JsonObject)
		for _, fields := range references {
			declaredFields += len(fields.(JsonObject))
		}
	}
	if declaredFields != 7 {
		t.Fatalf("declared reference fields = %d, want 7", declaredFields)
	}
}

func TestDeclaredReferenceCyclesIgnoreCrossStateConfiguration(t *testing.T) {
	// Pack manifests have no cross_state_references escape hatch: metadata
	// validation rejects a declared cycle before deployment topology exists.
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{"b_id": referenceSpec("sample_b")},
		"sample_b": JsonObject{"a_id": referenceSpec("sample_a")},
	})
	_, err := ValidatePackManifest(manifest, "manifest.json")
	if err == nil || !strings.Contains(err.Error(), referenceCycleRemedy) {
		t.Fatalf("expected metadata cycle rejection independent of cross-state settings, got %v", err)
	}
}
