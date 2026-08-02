package envgen

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/google/go-cmp/cmp"
)

func TestLoadedPlanRootsProjectionAgreesWithCrossStateTopology(t *testing.T) {
	root := syntheticRootWithDataReferent(t)
	dep := deployment.Deployment{Overlay: "."}
	tenant := "tenant"
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: dep, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(plan-roots parity fixture) error = %v, want nil", err)
	}

	workspace := t.TempDir()
	for _, topologyRoot := range topologyResult.Topology.Roots {
		path := filepath.Join(workspace, "envs", tenant, topologyRoot.Label)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", path, err)
		}
	}
	projected, err := roots.LoadedPlanRoots(roots.LoadedPlanRootsOptions{
		Workspace: workspace, Deployment: dep, Root: root, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedPlanRoots(plan-roots parity fixture) error = %v, want nil", err)
	}
	crossState, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: root, Topology: topologyResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology(plan-roots parity fixture) error = %v, want nil", err)
	}

	wantByRoot := map[string][]string{}
	for _, edge := range crossState.Edges {
		resource, ok := root.Resources[edge.Referent]
		dataReferent, isDataReferent := resource.Registry["data_referent"].(bool)
		if !ok || !isDataReferent || !dataReferent {
			continue
		}
		wantByRoot[edge.ReferrerRoot] = append(wantByRoot[edge.ReferrerRoot], edge.ReferentRoot)
	}
	for label, referents := range wantByRoot {
		sort.Strings(referents)
		unique := referents[:0]
		for _, referent := range referents {
			if len(unique) == 0 || unique[len(unique)-1] != referent {
				unique = append(unique, referent)
			}
		}
		wantByRoot[label] = unique
	}

	for _, materialized := range projected.Result.Roots {
		want := wantByRoot[materialized.Label]
		if want == nil {
			want = []string{}
		}
		if diff := cmp.Diff(want, materialized.DataReferents); diff != "" {
			t.Errorf("LoadedPlanRoots(%s).DataReferents mismatch (-want +got):\n%s", materialized.Label, diff)
		}
	}
}
