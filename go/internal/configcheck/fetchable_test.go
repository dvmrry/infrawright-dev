package configcheck

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func writeConfig(t *testing.T, workspace, tenant, name string) {
	t.Helper()
	directory := filepath.Join(workspace, "config", tenant)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
	}
	target := filepath.Join(directory, name)
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", target, err)
	}
}

func fetchableRoot(entries map[string]map[string]any) metadata.LoadedPackRoot {
	resources := make(map[string]metadata.LoadedResourceMetadata, len(entries))
	for resourceType, registry := range entries {
		resources[resourceType] = metadata.LoadedResourceMetadata{
			Type:     resourceType,
			Product:  "zia",
			Registry: registry,
		}
	}
	return metadata.LoadedPackRoot{Resources: resources}
}

func runCheck(t *testing.T, workspace string, root metadata.LoadedPackRoot) CheckFetchableResult {
	t.Helper()
	result, err := CheckFetchable(CheckFetchableOptions{
		Workspace:  workspace,
		Deployment: deployment.Deployment{Overlay: "."},
		Root:       root,
	})
	if err != nil {
		t.Fatalf("CheckFetchable() error = %v, want nil", err)
	}
	return result
}

// The reported incident in one test: a vendor refresh drops a fetch block, the
// registry entry stays valid JSON, every existing check stays green, and drift
// goes blind to a resource the consumer still manages.
func TestCheckFetchableCatchesCommittedConfigWithNoFetchBlock(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_url_categories.auto.tfvars.json")
	writeConfig(t, workspace, "zs2", "zia_tenant_restriction_profile.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
		// The dropped one: still a valid entry, simply no longer fetchable.
		"zia_tenant_restriction_profile": {"product": "zia"},
	})

	result := runCheck(t, workspace, root)
	if result.Checked != 2 || result.Skipped != 0 {
		t.Errorf("CheckFetchable() checked/skipped = %d/%d, want 2/0", result.Checked, result.Skipped)
	}
	want := []FetchableViolation{{
		Tenant: "zs2", Type: "zia_tenant_restriction_profile",
		Detail: "registry entry has no fetch block; " +
			"add one, or declare \"fetch\": false with a fetch_skip_reason",
	}}
	if !reflect.DeepEqual(result.Violations, want) {
		t.Errorf("CheckFetchable().Violations = %#v, want %#v", result.Violations, want)
	}
	var failure *procerr.ProcessFailure
	if err := FetchableFailure(result); !errors.As(err, &failure) ||
		failure.Code != "COMMITTED_CONFIG_NOT_FETCHABLE" {
		t.Errorf("FetchableFailure() = %v, want COMMITTED_CONFIG_NOT_FETCHABLE", err)
	}
}

// The companion is what makes the invariant landable: without a way to declare
// an intentional exception, the first legitimate gen-only commit is a false
// positive, and a nuisance gate gets disabled.
func TestCheckFetchableHonoursAnExplicitUnfetchableDeclaration(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_generated_only.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"zia_generated_only": {
			"product":           "zia",
			"fetch":             false,
			"fetch_skip_reason": "generate-only; the API exposes no read for this type",
		},
	})

	result := runCheck(t, workspace, root)
	if result.Checked != 1 || result.Skipped != 1 || len(result.Violations) != 0 {
		t.Errorf(
			"CheckFetchable(declared unfetchable) = %+v, want one checked, one skipped, no violations",
			result,
		)
	}
	if err := FetchableFailure(result); err != nil {
		t.Errorf("FetchableFailure(declared unfetchable) = %v, want nil", err)
	}
}

// A bare "fetch": false with no reason is not a declaration, it is the same
// silent gap wearing a different spelling.
func TestCheckFetchableRefusesAnUnexplainedSkip(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_quiet.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"zia_quiet": {"product": "zia", "fetch": false},
	})

	result := runCheck(t, workspace, root)
	if result.Skipped != 0 || len(result.Violations) != 1 {
		t.Errorf("CheckFetchable(unexplained skip) = %+v, want one violation and no skip", result)
	}
}

func TestCheckFetchableRangesOverEveryTenantAndConfigSpelling(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_url_categories.auto.tfvars.json")
	writeConfig(t, workspace, "zs3", "zia_orphan.auto.tfvars")
	// Sidecars and unrelated files are not committed config for a type.
	writeConfig(t, workspace, "zs2", "zia_url_categories.generated.expressions.json")
	writeConfig(t, workspace, "zs2", "README.md")
	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
	})

	result := runCheck(t, workspace, root)
	if !reflect.DeepEqual(result.Tenants, []string{"zs2", "zs3"}) {
		t.Errorf("CheckFetchable().Tenants = %#v, want [zs2 zs3]", result.Tenants)
	}
	if result.Checked != 2 {
		t.Errorf("CheckFetchable() checked = %d, want 2 (the sidecar and README are not config)", result.Checked)
	}
	// The HCL-spelled config names a type with no registry entry at all,
	// which is the same blindness by a different route.
	wantViolations := []FetchableViolation{{
		Tenant: "zs3", Type: "zia_orphan",
		Detail: "committed config has no registry entry",
	}}
	if !reflect.DeepEqual(result.Violations, wantViolations) {
		t.Errorf("CheckFetchable().Violations = %#v, want %#v", result.Violations, wantViolations)
	}
}

func TestCheckFetchableIsCleanForACorrectlyConfiguredConsumerAndEmptyTree(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_url_categories.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
	})
	if result := runCheck(t, workspace, root); len(result.Violations) != 0 {
		t.Errorf("CheckFetchable(correct consumer).Violations = %#v, want none", result.Violations)
	}

	empty := runCheck(t, t.TempDir(), root)
	if len(empty.Tenants) != 0 || empty.Checked != 0 || len(empty.Violations) != 0 {
		t.Errorf("CheckFetchable(no config tree) = %+v, want an empty clean result", empty)
	}
}

// A derived type's registry entry already states that its config comes from
// another resource, so it needs no written reason. A generate-only type with
// no derive block is the ambiguous case, and still does.
func TestCheckFetchableTreatsDeriveAsAStructuralDeclaration(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "demo", "zpa_policy_access_rule_reorder.auto.tfvars.json")
	writeConfig(t, workspace, "demo", "zia_generate_only.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"zpa_policy_access_rule_reorder": {
			"product":  "zpa",
			"generate": true,
			"derive":   map[string]any{"from": "zpa_policy_access_rule"},
		},
		"zia_generate_only": {"product": "zia", "generate": true},
	})

	result := runCheck(t, workspace, root)
	if result.Skipped != 1 {
		t.Errorf("CheckFetchable() skipped = %d, want the derived type skipped", result.Skipped)
	}
	if len(result.Violations) != 1 || result.Violations[0].Type != "zia_generate_only" {
		t.Errorf(
			"CheckFetchable().Violations = %#v, want only the undeclared generate-only type",
			result.Violations,
		)
	}
}
