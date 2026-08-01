package envgen

// reference_topology_test.go ports the original topology corpus against the
// synthetic pack universe in pack_scope_test.go so the engine contracts do not
// require any committed provider pack.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

// repoRoot walks up from this test file's directory until it finds the
// committed full pack profile.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, packsErr := os.Stat(filepath.Join(dir, "packs", "full.packset.json"))
		if packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding packs/full.packset.json", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func TestCrossStateTopologyDefaultsToAllDeclaredSingletonEdges(t *testing.T) {
	root := syntheticRootForTopology(t)
	tenant := "tenant"

	singletonDeployment := deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}}
	singletonResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: singletonDeployment, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	singleton, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: singletonDeployment, Root: root, Topology: singletonResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology: %v", err)
	}
	wantEdges := []CrossStateReferenceEdge{
		{Field: "trusted_network_ids", Referent: "zcc_trusted_network", ReferentRoot: "zcc_trusted_network", Referrer: "zcc_forwarding_profile", ReferrerRoot: "zcc_forwarding_profile"},
		{Field: "trusted_network_ids_selected", Referent: "zcc_trusted_network", ReferentRoot: "zcc_trusted_network", Referrer: "zcc_forwarding_profile", ReferrerRoot: "zcc_forwarding_profile"},
		{Field: "url_categories", Referent: "zia_url_categories", ReferentRoot: "zia_url_categories", Referrer: "zia_url_filtering_rules", ReferrerRoot: "zia_url_filtering_rules"},
		{Field: "segment_group_id", Referent: "zpa_segment_group", ReferentRoot: "zpa_segment_group", Referrer: "zpa_application_segment", ReferrerRoot: "zpa_application_segment"},
		{Field: "server_groups.id", Referent: "zpa_server_group", ReferentRoot: "zpa_server_group", Referrer: "zpa_application_segment", ReferrerRoot: "zpa_application_segment"},
		{Field: "app_connector_groups.id", Referent: "zpa_app_connector_group", ReferentRoot: "zpa_app_connector_group", Referrer: "zpa_server_group", ReferrerRoot: "zpa_server_group"},
		{Field: "servers.id", Referent: "zpa_application_server", ReferentRoot: "zpa_application_server", Referrer: "zpa_server_group", ReferrerRoot: "zpa_server_group"},
	}
	if !reflect.DeepEqual(singleton.Edges, wantEdges) {
		t.Fatalf("Edges = %+v, want %+v", singleton.Edges, wantEdges)
	}

	explicitDeployment := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zcc": {HasCrossStateReferences: true, CrossStateReferences: true},
			"zia": {HasCrossStateReferences: true, CrossStateReferences: true},
			"zpa": {HasCrossStateReferences: true, CrossStateReferences: true},
		},
	}
	explicit, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: explicitDeployment, Root: root, Topology: singletonResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology(explicit true): %v", err)
	}
	if !reflect.DeepEqual(explicit, singleton) {
		t.Fatalf("explicit true topology = %+v, want absent-setting topology %+v", explicit, singleton)
	}

	gotDeps := setKeysSorted(singleton.DependenciesByRoot["zpa_application_segment"])
	wantDeps := []string{"zpa_segment_group", "zpa_server_group"}
	if !reflect.DeepEqual(gotDeps, wantDeps) {
		t.Fatalf("dependenciesByRoot[zpa_application_segment] = %v, want %v", gotDeps, wantDeps)
	}

	closure := CrossStateDependencyClosure([]string{"zpa_application_segment"}, singleton.DependenciesByRoot)
	wantClosure := []string{
		"zpa_app_connector_group",
		"zpa_application_segment",
		"zpa_application_server",
		"zpa_segment_group",
		"zpa_server_group",
	}
	if !reflect.DeepEqual(closure, wantClosure) {
		t.Fatalf("closure = %v, want %v", closure, wantClosure)
	}
}

func TestCrossStateTopologyExplicitFalseFiltersOnlyThatProvider(t *testing.T) {
	root := syntheticRootForTopology(t)
	tenant := "tenant"
	dep := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zia": {HasCrossStateReferences: true, CrossStateReferences: false},
		},
	}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: dep, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	got, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: root, Topology: topologyResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology: %v", err)
	}
	for _, edge := range got.Edges {
		if edge.Referrer == "zia_url_filtering_rules" {
			t.Fatalf("explicit zia false retained edge %+v", edge)
		}
	}
	if len(got.Edges) != 6 {
		t.Fatalf("cross-state edges after explicit zia false = %d, want 6", len(got.Edges))
	}
}

func TestCrossStateTopologyAcceptsDataReferent(t *testing.T) {
	root := syntheticRootWithDataReferent(t)
	tenant := "tenant"
	dep := deployment.Deployment{Overlay: "."}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: dep, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(data referent fixture) error = %v, want nil", err)
	}
	got, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: root, Topology: topologyResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology(data referent fixture) error = %v, want nil", err)
	}

	want := []CrossStateReferenceEdge{{
		Field:        "location_groups.id",
		Referrer:     "zia_url_filtering_rules",
		ReferrerRoot: "zia_url_filtering_rules",
		Referent:     "zia_location_groups",
		ReferentRoot: "zia_location_groups",
	}}
	var dataReferentEdges []CrossStateReferenceEdge
	for _, edge := range got.Edges {
		if edge.Referent == "zia_location_groups" {
			dataReferentEdges = append(dataReferentEdges, edge)
		}
	}
	if !reflect.DeepEqual(dataReferentEdges, want) {
		t.Errorf("ResolveCrossStateReferenceTopology(data referent fixture) edges mismatch:\n got: %#v\nwant: %#v", dataReferentEdges, want)
	}
}

func TestCrossStateTopologySkipsDataReferentReferrer(t *testing.T) {
	root := syntheticRootWithDataReferent(t)
	dep := deployment.Deployment{Overlay: "."}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: dep, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(data referent fixture) error = %v, want nil", err)
	}
	got, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: root, Topology: topologyResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology(data referent referrer) error = %v, want nil", err)
	}
	for _, edge := range got.Edges {
		if edge.Referrer == "zia_location_groups" {
			t.Errorf("ResolveCrossStateReferenceTopology(data referent referrer) emitted edge %#v, want no data-referent referrer edges", edge)
		}
	}
}

func TestCrossStateTopologyRejectsNonGeneratedNonDataReferent(t *testing.T) {
	root := syntheticRootForTopology(t)
	const unknownReferent = "zia_known_only"
	root.Resources[unknownReferent] = metadata.LoadedResourceMetadata{
		Type:     unknownReferent,
		Product:  "zia",
		Provider: "zia",
		Registry: metadata.JsonObject{"product": "zia"},
	}

	var manifests []metadata.PackManifest
	for _, manifest := range root.Packs.Manifests {
		if manifest.Name != "zia" {
			manifests = append(manifests, manifest)
			continue
		}
		references, _ := manifest.Data["references"].(map[string]any)
		newReferences := make(map[string]any, len(references))
		for resourceType, fieldsValue := range references {
			newReferences[resourceType] = fieldsValue
		}
		fields, _ := newReferences["zia_url_filtering_rules"].(map[string]any)
		newFields := make(map[string]any, len(fields)+1)
		for field, specification := range fields {
			newFields[field] = specification
		}
		newFields["known_only_id"] = metadata.JsonObject{
			"name_field": "name",
			"referent":   unknownReferent,
		}
		newReferences["zia_url_filtering_rules"] = newFields
		newData := make(metadata.JsonObject, len(manifest.Data))
		for key, value := range manifest.Data {
			newData[key] = value
		}
		newData["references"] = newReferences
		manifests = append(manifests, metadata.PackManifest{
			Name: manifest.Name, Directory: manifest.Directory, Path: manifest.Path,
			Data: newData, ProviderPrefixes: manifest.ProviderPrefixes,
			ProviderSources: manifest.ProviderSources, RequiresShared: manifest.RequiresShared,
		})
	}
	root.Packs.Manifests = manifests

	dep := deployment.Deployment{Overlay: "."}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: dep, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(unknown referent fixture) error = %v, want nil", err)
	}
	_, err = ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: root, Topology: topologyResult.Topology,
	})
	want := "cross-state reference zia_url_filtering_rules.known_only_id targets zia_known_only, which is not a generated non-derived resource or data referent"
	if err == nil || err.Error() != want {
		t.Errorf("ResolveCrossStateReferenceTopology(unknown referent) error = %v, want exact refusal %q", err, want)
	}
}

func setKeysSorted(set map[string]bool) []string {
	var out []string
	for key := range set {
		out = append(out, key)
	}
	// Match Node's `[...set]` insertion-order iteration for a set built by
	// this port's own addToSet in ascending-sorted addition order: the Go
	// side re-sorts explicitly here since map iteration order is undefined,
	// but the two roots this fixture ever adds ("zpa_segment_group" then
	// "zpa_server_group" by referrer/field-sorted edge order) already sort
	// lexicographically, so a plain sort reproduces the Node assertion's
	// literal expected order.
	sortStringsInPlace(out)
	return out
}

func sortStringsInPlace(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func TestCrossStateTopologyRejectsDeclaredRootCycles(t *testing.T) {
	root := syntheticRootForTopology(t)
	tenant := "tenant"

	var manifests []metadata.PackManifest
	for _, manifest := range root.Packs.Manifests {
		if manifest.Name != "zpa" {
			manifests = append(manifests, manifest)
			continue
		}
		references, _ := manifest.Data["references"].(map[string]any)
		newReferences := map[string]any{}
		for k, v := range references {
			newReferences[k] = v
		}
		newReferences["zpa_segment_group"] = map[string]any{
			"application_id": map[string]any{"name_field": "name", "referent": "zpa_application_segment"},
		}
		newData := map[string]any{}
		for k, v := range manifest.Data {
			newData[k] = v
		}
		newData["references"] = newReferences
		manifests = append(manifests, metadata.PackManifest{
			Name: manifest.Name, Directory: manifest.Directory, Path: manifest.Path,
			Data: newData, ProviderPrefixes: manifest.ProviderPrefixes,
			ProviderSources: manifest.ProviderSources, RequiresShared: manifest.RequiresShared,
		})
	}
	cyclicRoot := root
	cyclicRoot.Packs.Manifests = manifests

	dep := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {HasCrossStateReferences: true, CrossStateReferences: true},
		},
	}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: cyclicRoot, Deployment: dep, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	_, err = ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: cyclicRoot, Topology: topologyResult.Topology,
	})
	if err == nil {
		t.Fatal("expected a cross-state reference cycle error")
	}
	mustMatch(t, err.Error(), `cross-state reference cycle detected.*resolve one direction via a literal ID or operator expression`)
}
