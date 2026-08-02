package roots

import "testing"

func TestRenderLegacyPlanRootsPinsEmptyAndNonEmptyDataReferents(t *testing.T) {
	tenant := "tenant"
	provider := "sample"
	got, err := RenderLegacyPlanRoots(PlanRoots{
		Kind:          "infrawright.plan_roots",
		SchemaVersion: 2,
		Request:       PlanRootsRequest{Tenant: &tenant, Selectors: []string{}},
		Roots: []MaterializedPlanRoot{
			{
				Tenant:        tenant,
				Label:         "sample_rule",
				Provider:      &provider,
				Members:       []string{"sample_rule"},
				DataReferents: []string{"sample_groups_data"},
				EnvDir:        "envs/tenant/sample_rule",
				ArtifactState: ArtifactStateAbsent,
			},
			{
				Tenant:        tenant,
				Label:         "sample_clean",
				Provider:      &provider,
				Members:       []string{"sample_clean"},
				DataReferents: []string{},
				EnvDir:        "envs/tenant/sample_clean",
				ArtifactState: ArtifactStateAbsent,
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderLegacyPlanRoots(data_referents) error = %v, want nil", err)
	}
	want := `{
  "kind": "infrawright.plan_roots",
  "request": {
    "selectors": [],
    "tenant": "tenant"
  },
  "roots": [
    {
      "artifact_state": "absent",
      "artifacts": {
        "tfplan": {
          "exists": false,
          "path": ""
        },
        "tfplan_sources": {
          "exists": false,
          "path": ""
        }
      },
      "data_referents": [
        "sample_groups_data"
      ],
      "env_dir": "envs/tenant/sample_rule",
      "label": "sample_rule",
      "members": [
        "sample_rule"
      ],
      "provider": "sample",
      "tenant": "tenant"
    },
    {
      "artifact_state": "absent",
      "artifacts": {
        "tfplan": {
          "exists": false,
          "path": ""
        },
        "tfplan_sources": {
          "exists": false,
          "path": ""
        }
      },
      "data_referents": [],
      "env_dir": "envs/tenant/sample_clean",
      "label": "sample_clean",
      "members": [
        "sample_clean"
      ],
      "provider": "sample",
      "tenant": "tenant"
    }
  ],
  "schema_version": 2
}
`
	if got != want {
		t.Errorf("RenderLegacyPlanRoots(data_referents) bytes mismatch (-want +got):\n-want:\n%s\n+got:\n%s", want, got)
	}
}
