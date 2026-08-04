package envgen

// referent_self_describing_tokens_test.go pins the Phase 7 design revision
// (see docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md):
// reference tokens self-describe their id space, and a committed token
// whose suffix disagrees with the space its resolved binding actually
// covers must never plan -- it is refused the same loud way a stranded
// token is today.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// newReferentSpaceMismatchEnvironmentFixture is
// newReferentAlternateSpaceEnvironmentFixture's twin, except the committed
// config's canonical-space field ("group_id") carries an explicit ".val"
// suffix it has no business naming: the covering edge declares no
// referent_id_field at all, and its committed generated-bindings cache
// resolves through the canonical iw_reference_ids output, exactly as an
// operator hand-editing (or a stale re-transform racing a pack change)
// could produce.
func newReferentSpaceMismatchEnvironmentFixture(t *testing.T) referentAlternateSpaceEnvironmentFixture {
	t.Helper()
	fixture := newReferentAlternateSpaceEnvironmentFixture(t)
	configDirectory := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(configDirectory, "sample_rule.auto.tfvars.json"), metadata.JsonObject{
		"items": metadata.JsonObject{
			"rule_one": metadata.JsonObject{
				// group_id's edge declares no referent_id_field -- this
				// committed value's ".val" suffix disagrees with that.
				"group_id":  "sample_groups.group_one.val",
				"group_val": "sample_groups.group_two.val",
			},
		},
	})
	return fixture
}

// TestReferentSpaceMismatchTokenRefusesToPlan is the fail-closed control:
// invariant 4 requires that a committed token whose suffix disagrees with
// the space its covering edge actually resolves through is never silently
// planned through either space. GenerateEnvironmentRoots must refuse
// outright, through the same committed-token totality gate an ordinary
// stranded/orphaned token hits ("has no binding that resolves it").
func TestReferentSpaceMismatchTokenRefusesToPlan(t *testing.T) {
	fixture := newReferentSpaceMismatchEnvironmentFixture(t)
	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       fixture.root,
		Selectors:  []string{"sample_rule"},
		StateAware: false,
		Tenant:     "tenant",
	})
	if err == nil {
		t.Fatal("GenerateEnvironmentRoots(space-mismatch fixture) error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "has no binding that resolves it") {
		t.Errorf("GenerateEnvironmentRoots(space-mismatch fixture) error = %q, want the committed-token totality-gate refusal", err.Error())
	}
	if !strings.Contains(err.Error(), "sample_groups.group_one.val") {
		t.Errorf("GenerateEnvironmentRoots(space-mismatch fixture) error = %q, want it to name the mismatched token", err.Error())
	}
}
