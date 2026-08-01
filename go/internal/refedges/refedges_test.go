package refedges_test

import (
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/refedges"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/google/go-cmp/cmp"
)

func referenceSpec(referent string) metadata.JsonObject {
	return metadata.JsonObject{"name_field": "name", "referent": referent}
}

func multiMemberFixture() (metadata.LoadedPackRoot, map[string]string) {
	references := metadata.JsonObject{
		"sample_member_a": metadata.JsonObject{
			"duplicate_a": referenceSpec("sample_data_a"),
			"self":        referenceSpec("sample_data_self"),
			"zeta":        referenceSpec("sample_data_z"),
		},
		"sample_member_b": metadata.JsonObject{
			"alpha": referenceSpec("sample_data_a"),
			"beta":  referenceSpec("sample_data_z"),
		},
	}
	root := metadata.LoadedPackRoot{
		Active: metadata.PackSelection{Packs: []string{"sample"}},
		Packs: metadata.PackMetadata{Manifests: []metadata.PackManifest{{
			Name: "sample", Data: metadata.JsonObject{"references": references},
		}}},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"sample_member_a": {
				Type: "sample_member_a", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
			"sample_member_b": {
				Type: "sample_member_b", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
			"sample_data_a": {
				Type: "sample_data_a", Provider: "sample",
				Registry: metadata.JsonObject{"data_referent": true},
			},
			"sample_data_self": {
				Type: "sample_data_self", Provider: "sample",
				Registry: metadata.JsonObject{"data_referent": true},
			},
			"sample_data_z": {
				Type: "sample_data_z", Provider: "sample",
				Registry: metadata.JsonObject{"data_referent": true},
			},
		},
	}
	resourceRoots := map[string]string{
		"sample_member_a":  "sample_managed",
		"sample_member_b":  "sample_managed",
		"sample_data_a":    "sample_a_data",
		"sample_data_self": "sample_managed",
		"sample_data_z":    "sample_z_data",
	}
	return root, resourceRoots
}

func TestResolveUnionsMultiMemberReferencesDeduplicatesSelfRootAndSortsEdges(t *testing.T) {
	root, resourceRoots := multiMemberFixture()
	got, err := refedges.Resolve(refedges.Options{
		Deployment:    deployment.Deployment{Overlay: "."},
		Root:          root,
		ResourceRoots: resourceRoots,
	})
	if err != nil {
		t.Fatalf("refedges.Resolve(multi-member fixture) error = %v, want nil", err)
	}
	wantEdges := []refedges.Edge{
		{Field: "duplicate_a", Referrer: "sample_member_a", ReferrerRoot: "sample_managed", Referent: "sample_data_a", ReferentRoot: "sample_a_data"},
		{Field: "zeta", Referrer: "sample_member_a", ReferrerRoot: "sample_managed", Referent: "sample_data_z", ReferentRoot: "sample_z_data"},
		{Field: "alpha", Referrer: "sample_member_b", ReferrerRoot: "sample_managed", Referent: "sample_data_a", ReferentRoot: "sample_a_data"},
		{Field: "beta", Referrer: "sample_member_b", ReferrerRoot: "sample_managed", Referent: "sample_data_z", ReferentRoot: "sample_z_data"},
	}
	if diff := cmp.Diff(wantEdges, got.Edges); diff != "" {
		t.Errorf("refedges.Resolve(multi-member fixture) edges mismatch (-want +got):\n%s", diff)
	}
	wantDependencies := map[string]map[string]bool{
		"sample_managed": {"sample_a_data": true, "sample_z_data": true},
	}
	if diff := cmp.Diff(wantDependencies, got.DependenciesByRoot); diff != "" {
		t.Errorf("refedges.Resolve(multi-member fixture) dependencies mismatch (-want +got):\n%s", diff)
	}
	wantOutputs := map[string]map[string]bool{
		"sample_a_data": {"sample_data_a": true},
		"sample_z_data": {"sample_data_z": true},
	}
	if diff := cmp.Diff(wantOutputs, got.OutputsByRoot); diff != "" {
		t.Errorf("refedges.Resolve(multi-member fixture) outputs mismatch (-want +got):\n%s", diff)
	}

	labels := make([]string, 0, len(got.DependenciesByRoot["sample_managed"]))
	for label := range got.DependenciesByRoot["sample_managed"] {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	if diff := cmp.Diff([]string{"sample_a_data", "sample_z_data"}, labels); diff != "" {
		t.Errorf("refedges.Resolve(multi-member fixture) dependency labels mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveExplicitFalseEmitsEmptyEdges(t *testing.T) {
	root, resourceRoots := multiMemberFixture()
	got, err := refedges.Resolve(refedges.Options{
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"sample": {HasCrossStateReferences: true, CrossStateReferences: false},
			},
		},
		Root:          root,
		ResourceRoots: resourceRoots,
	})
	if err != nil {
		t.Fatalf("refedges.Resolve(explicit false) error = %v, want nil", err)
	}
	if got.Edges == nil {
		t.Fatalf("refedges.Resolve(explicit false) edges = nil, want non-nil empty slice")
	}
	if len(got.Edges) != 0 {
		t.Errorf("refedges.Resolve(explicit false) edge count = %d, want 0", len(got.Edges))
	}
	if len(got.DependenciesByRoot) != 0 || len(got.OutputsByRoot) != 0 {
		t.Errorf("refedges.Resolve(explicit false) graph = %+v, want empty dependency/output maps", got)
	}
}

func TestResolveActiveManifestPrecedenceMatchesMergedTransformReferences(t *testing.T) {
	root := metadata.LoadedPackRoot{
		Active: metadata.PackSelection{Packs: []string{"alpha", "beta"}},
		Packs: metadata.PackMetadata{Manifests: []metadata.PackManifest{
			{
				Name: "alpha",
				Data: metadata.JsonObject{"references": metadata.JsonObject{
					"sample_referrer": metadata.JsonObject{
						"target": referenceSpec("sample_earlier"),
					},
				}},
			},
			{
				Name: "ignored",
				Data: metadata.JsonObject{"references": metadata.JsonObject{
					"sample_referrer": metadata.JsonObject{
						"target": referenceSpec("sample_ignored"),
					},
				}},
			},
			{
				Name: "beta",
				Data: metadata.JsonObject{"references": metadata.JsonObject{
					"sample_referrer": metadata.JsonObject{
						"target": referenceSpec("sample_later"),
					},
				}},
			},
		}},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"sample_referrer": {
				Type: "sample_referrer", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
			"sample_earlier": {
				Type: "sample_earlier", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
			"sample_later": {
				Type: "sample_later", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
			"sample_ignored": {
				Type: "sample_ignored", Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
		},
	}
	resourceRoots := map[string]string{
		"sample_referrer": "sample_referrer",
		"sample_earlier":  "sample_earlier",
		"sample_later":    "sample_later",
		"sample_ignored":  "sample_ignored",
	}
	merged := transform.MergedTransformReferences(root)
	mergedTarget := merged["sample_referrer"]["target"].(map[string]any)["referent"].(string)
	if mergedTarget != "sample_later" {
		t.Fatalf("transform.MergedTransformReferences target = %q, want sample_later", mergedTarget)
	}

	got, err := refedges.Resolve(refedges.Options{
		Deployment:    deployment.Deployment{Overlay: "."},
		Root:          root,
		ResourceRoots: resourceRoots,
	})
	if err != nil {
		t.Fatalf("refedges.Resolve(active manifest precedence) error = %v, want nil", err)
	}
	if len(got.Edges) != 1 || got.Edges[0].Referent != mergedTarget {
		t.Errorf("refedges.Resolve(active manifest precedence) edges = %+v, want one edge to %q", got.Edges, mergedTarget)
	}
}
