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
	target := filepath.Join(workspace, "config", tenant, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", target, err)
	}
}

// TestCheckFetchableEnumeratesSymlinkedTenants pins that a tenant reached
// through a symlink is checked rather than silently skipped. Checking fewer
// tenants than the consumer has is the failure mode this check claims immunity
// to, and it would report a clean result while doing it.
func TestCheckFetchableEnumeratesSymlinkedTenants(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "direct", "zia_url_categories.auto.tfvars.json")

	// The link target stays inside the config root, so what the check reads
	// does not depend on the machine. An escaping link is refused instead --
	// see TestCheckFetchableRefusesATenantLinkLeavingTheConfigRoot.
	writeConfig(t, workspace, "real_linked", "zia_generate_only.auto.tfvars.json")
	target := filepath.Join(workspace, "config", "real_linked", "zia_generate_only.auto.tfvars.json")
	link := filepath.Join(workspace, "config", "linked")
	if err := os.Symlink(filepath.Join(workspace, "config", "real_linked"), link); err != nil {
		t.Fatalf("os.Symlink(%q) error = %v, want nil", link, err)
	}

	// A symlink to a file is not a tenant. Resolving the link is not enough
	// on its own -- what it resolves to still has to be a directory.
	fileLink := filepath.Join(workspace, "config", "not_a_tenant")
	if err := os.Symlink(target, fileLink); err != nil {
		t.Fatalf("os.Symlink(%q) error = %v, want nil", fileLink, err)
	}

	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
		"zia_generate_only":  {"product": "zia", "generate": true},
	})
	result := runCheck(t, workspace, root)

	for _, tenant := range result.Tenants {
		if tenant == "not_a_tenant" {
			t.Errorf("CheckFetchable().Tenants = %#v, want no symlink-to-a-file entry", result.Tenants)
		}
	}
	if len(result.Tenants) != 3 {
		t.Errorf("CheckFetchable().Tenants = %#v, want the direct, real and linked tenants", result.Tenants)
	}
	// The violation is what proves the linked tenant was read, not merely
	// counted: a skipped tenant reports clean. Both the link and its target
	// report, since each is a tenant directory in its own right.
	if len(result.Violations) != 2 {
		t.Errorf(
			"CheckFetchable().Violations = %#v, want the undeclared type through both the link and its target",
			result.Violations,
		)
	}
}

// TestCheckFetchableRefusesATenantLinkLeavingTheConfigRoot pins the gate's
// hermeticity. Following a link to an arbitrary path would make the same
// commit pass where the target exists and fail, or read different config,
// elsewhere -- so the claim that this check reads only the consumer's own tree
// would stop being true. Refused, not skipped: skipping is the silent
// narrowing the link handling exists to prevent.
func TestCheckFetchableRefusesATenantLinkLeavingTheConfigRoot(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "direct", "zia_url_categories.auto.tfvars.json")

	external := t.TempDir()
	outside := filepath.Join(external, "prod")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", outside, err)
	}
	link := filepath.Join(workspace, "config", "prod")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("os.Symlink(%q) error = %v, want nil", link, err)
	}

	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
	})
	_, err := CheckFetchable(CheckFetchableOptions{
		Workspace:  workspace,
		Deployment: deployment.Deployment{Overlay: "."},
		Root:       root,
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("CheckFetchable(escaping tenant link) error = %v, want a ProcessFailure", err)
	}
	if failure.Code != "CONFIG_TENANT_OUTSIDE_ROOT" {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, "CONFIG_TENANT_OUTSIDE_ROOT")
	}
}

// TestCheckFetchableRefusesABrokenTenantLink pins that a dangling symlink in
// the config root is a refusal rather than a skip or a panic. A link named
// like a tenant says the consumer meant a tenant to be there; treating it as
// absent is the silent narrowing this check must not do.
func TestCheckFetchableRefusesABrokenTenantLink(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "direct", "zia_url_categories.auto.tfvars.json")
	link := filepath.Join(workspace, "config", "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), link); err != nil {
		t.Fatalf("os.Symlink(%q) error = %v, want nil", link, err)
	}

	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories": {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
	})
	_, err := CheckFetchable(CheckFetchableOptions{
		Workspace:  workspace,
		Deployment: deployment.Deployment{Overlay: "."},
		Root:       root,
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("CheckFetchable(broken tenant link) error = %v, want a ProcessFailure", err)
	}
	if failure.Code != "CONFIG_ROOT_UNREADABLE" {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, "CONFIG_ROOT_UNREADABLE")
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

// The data-referent lane publishes committed config one directory down, in
// config/<tenant>/data/. The same if-we-manage-it-we-must-see-it contract
// covers it: a nested config whose registry entry loses its fetch block must
// be reported, not silently skipped. The faithful mutation this kills is the
// pre-split enumeration that discarded directory entries and never read
// data/.
func TestCheckFetchableCoversNestedDataReferentConfig(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_url_categories.auto.tfvars.json")
	writeConfig(t, workspace, "zs2", "data/zia_location_groups.auto.tfvars.json")
	// The migration window: the superseded flat file still on disk beside
	// the nested one, in the other tfvars format. One type, one check --
	// duplicated sightings must not inflate counts or duplicate violations.
	writeConfig(t, workspace, "zs2", "zia_location_groups.auto.tfvars")
	root := fetchableRoot(map[string]map[string]any{
		"zia_url_categories":  {"product": "zia", "fetch": map[string]any{"path": "/urlCategories"}},
		"zia_location_groups": {"product": "zia", "data_referent": true},
	})

	result := runCheck(t, workspace, root)
	if result.Checked != 2 || result.Skipped != 0 {
		t.Errorf("CheckFetchable() checked/skipped = %d/%d, want 2/0", result.Checked, result.Skipped)
	}
	want := []FetchableViolation{{
		Tenant: "zs2", Type: "zia_location_groups",
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
	if result.Checked != 1 {
		t.Errorf("CheckFetchable() checked = %d, want 1 in-profile type", result.Checked)
	}
	// The HCL-spelled config names a type the active profile does not claim.
	// It is reported, not refused: consumers run reduced profiles on purpose,
	// and there is no fetch declaration to hold an absent entry to.
	if !reflect.DeepEqual(result.OutOfProfile, []string{"zia_orphan"}) {
		t.Errorf("CheckFetchable().OutOfProfile = %#v, want [zia_orphan]", result.OutOfProfile)
	}
	if len(result.Violations) != 0 {
		t.Errorf("CheckFetchable().Violations = %#v, want none", result.Violations)
	}
	// The HCL spelling still has to be recognised as config; being
	// out-of-profile rather than a violation must not come from failing to
	// see the file at all.
	if !reflect.DeepEqual(result.Tenants, []string{"zs2", "zs3"}) {
		t.Errorf("CheckFetchable().Tenants = %#v, want both tenants ranged over", result.Tenants)
	}
}

// TestCheckFetchableStillCatchesADroppedFetchBlockInProfile pins the incident
// this check exists for against the profile-scoping change. A vendor refresh
// that drops a fetch block leaves the registry entry in place, so the type
// stays in profile and stays checked -- the narrowing must not reach it.
func TestCheckFetchableStillCatchesADroppedFetchBlockInProfile(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "zs2", "zia_tenant_restriction_profile.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		// The entry survives the refresh; only its fetch block is gone.
		"zia_tenant_restriction_profile": {"product": "zia"},
	})

	result := runCheck(t, workspace, root)
	if len(result.OutOfProfile) != 0 {
		t.Errorf("CheckFetchable().OutOfProfile = %#v, want none; the entry is in profile", result.OutOfProfile)
	}
	if len(result.Violations) != 1 || result.Violations[0].Type != "zia_tenant_restriction_profile" {
		t.Fatalf("CheckFetchable().Violations = %#v, want the dropped fetch block refused", result.Violations)
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
		// The source has to be present and fetchable for the exemption to
		// mean anything; without it the fixture proved only that a derive
		// key was there.
		"zpa_policy_access_rule": {
			"product": "zpa",
			"fetch":   map[string]any{"path": "/policySet"},
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

// TestCheckFetchableLetsDirectFetchOutrankDerive pins the precedence runtime
// selection uses: SelectFetchResources tests fetchableSet before it looks at a
// derive block, and metadata permits an entry carrying both. Checking derive
// first would refuse a type whose own fetch block works -- a refusal with no
// operational counterpart, which is the kind that gets a gate switched off.
func TestCheckFetchableLetsDirectFetchOutrankDerive(t *testing.T) {
	workspace := t.TempDir()
	writeConfig(t, workspace, "demo", "sample_resource.auto.tfvars.json")
	root := fetchableRoot(map[string]map[string]any{
		"sample_resource": {
			"product": "zia",
			"fetch":   map[string]any{"path": "/sample"},
			"derive":  map[string]any{"from": "missing_source"},
		},
	})

	result := runCheck(t, workspace, root)
	if len(result.Violations) != 0 {
		t.Errorf(
			"CheckFetchable(direct fetch + broken derive).Violations = %#v, want none; runtime selects it",
			result.Violations,
		)
	}
	if result.Skipped != 0 {
		t.Errorf("CheckFetchable() skipped = %d, want it counted as fetchable, not declared", result.Skipped)
	}
}

// TestCheckFetchableRequiresTheDeriveExemptionToResolve pins that a derive
// block is a declaration only when it names a source the engine can actually
// fetch. Runtime selection maps the derived type to derive.from and requires
// that source to carry a fetch entry, so a block that does not resolve leaves
// the check passing while make fetch fails.
func TestCheckFetchableRequiresTheDeriveExemptionToResolve(t *testing.T) {
	tests := []struct {
		name   string
		derive map[string]any
		source map[string]any
		detail string
	}{
		{
			name:   "no_from",
			derive: map[string]any{"policy_type": "ACCESS_POLICY"},
			detail: "derive block has no from; it cannot resolve to a fetchable source",
		},
		{
			name:   "from_is_not_a_string",
			derive: map[string]any{"from": 7},
			detail: "derive block has no from; it cannot resolve to a fetchable source",
		},
		{
			// Falling through to the lookup would report "derives from ,
			// which has no registry entry" -- accurate and useless.
			name:   "from_is_empty",
			derive: map[string]any{"from": ""},
			detail: "derive block has no from; it cannot resolve to a fetchable source",
		},
		{
			name:   "from_names_an_absent_type",
			derive: map[string]any{"from": "zpa_policy_access_rule"},
			detail: "derives from zpa_policy_access_rule, which has no registry entry",
		},
		{
			name:   "from_names_an_unfetchable_type",
			derive: map[string]any{"from": "zpa_policy_access_rule"},
			source: map[string]any{"product": "zpa", "generate": true},
			detail: "derives from zpa_policy_access_rule, which has no fetch block of its own",
		},
		{
			name:   "source_declared_unfetchable_on_purpose",
			derive: map[string]any{"from": "zpa_policy_access_rule"},
			source: map[string]any{
				"product": "zpa", "fetch": false,
				"fetch_skip_reason": "no list endpoint",
			},
			detail: "derives from zpa_policy_access_rule, which has no fetch block of its own",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeConfig(t, workspace, "demo", "zpa_policy_access_rule_reorder.auto.tfvars.json")
			entries := map[string]map[string]any{
				"zpa_policy_access_rule_reorder": {
					"product": "zpa", "generate": true, "derive": test.derive,
				},
			}
			if test.source != nil {
				entries["zpa_policy_access_rule"] = test.source
			}

			result := runCheck(t, workspace, fetchableRoot(entries))
			if result.Skipped != 0 {
				t.Errorf("CheckFetchable() skipped = %d, want 0 on an unresolved derive", result.Skipped)
			}
			if len(result.Violations) != 1 {
				t.Fatalf("CheckFetchable().Violations = %#v, want exactly one", result.Violations)
			}
			// The detail has to name why it failed to resolve: the generic
			// no-fetch-block message would send a consumer to add a fetch
			// block to a type that must not have one.
			if got := result.Violations[0].Detail; got != test.detail {
				t.Errorf("violation detail = %q, want %q", got, test.detail)
			}
		})
	}
}
