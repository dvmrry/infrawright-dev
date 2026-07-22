package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// TestV2FullSurfaceSevenEdgeCommandQualification qualifies the real Go
// gen-env command against the full committed pack profile. The fixture has
// local, resolvable values for every declared reference edge, so no
// credentials, backend, Terraform executable, or provider transport is used.
func TestV2FullSurfaceSevenEdgeCommandQualification(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-full-surface")

	omitted := runV2FullSurfaceGenEnv(t, root, binary, "omitted", nil)
	explicitTrue := true
	enabled := runV2FullSurfaceGenEnv(t, root, binary, "explicit-true", &explicitTrue)
	explicitFalse := false
	disabled := runV2FullSurfaceGenEnv(t, root, binary, "explicit-false", &explicitFalse)

	requireExactTree(t, "omitted versus explicit-true full-profile gen-env tree", omitted.tree, enabled.tree)
	resourceTypes := v2FullSurfaceResourceTypes(t, root)
	requireV2FullSurfaceSingletonRoots(t, "omitted", omitted.tree, resourceTypes)
	requireV2FullSurfaceSingletonRoots(t, "explicit-true", enabled.tree, resourceTypes)
	requireV2FullSurfaceSingletonRoots(t, "explicit-false", disabled.tree, resourceTypes)

	edges := []v2FullSurfaceEdge{
		{
			declaredField: "trusted_network_ids", referrer: "zcc_forwarding_profile",
			referent: "zcc_trusted_network", nameField: "network_name", key: "network_one",
		},
		{
			declaredField: "trusted_network_ids_selected", referrer: "zcc_forwarding_profile",
			referent: "zcc_trusted_network", nameField: "network_name", key: "network_one",
		},
		{
			declaredField: "url_categories", referrer: "zia_url_filtering_rules",
			referent: "zia_url_categories", nameField: "configured_name", key: "category_one",
		},
		{
			declaredField: "segment_group_id", referrer: "zpa_application_segment",
			referent: "zpa_segment_group", nameField: "name", key: "segment_one",
		},
		{
			declaredField: "server_groups.id", artifactField: "server_groups[0].id",
			referrer: "zpa_application_segment", referent: "zpa_server_group",
			nameField: "name", key: "server_one",
		},
		{
			declaredField: "app_connector_groups.id", artifactField: "app_connector_groups[0].id",
			referrer: "zpa_server_group", referent: "zpa_app_connector_group",
			nameField: "name", key: "connector_one",
		},
		{
			declaredField: "servers.id", artifactField: "servers[0].id",
			referrer: "zpa_server_group", referent: "zpa_application_server",
			nameField: "name", key: "application_server",
		},
	}
	requireV2FullSurfaceDeclaredEdges(t, root, edges)
	requireV2FullSurfaceEdges(t, "omitted", omitted.tree, edges)
	requireV2FullSurfaceEdges(t, "explicit-true", enabled.tree, edges)
	requireV2FullSurfaceDisabled(t, disabled.tree)

	wantDelta := []string{
		"zcc_forwarding_profile/expression_bindings.tf",
		"zcc_forwarding_profile/main.tf",
		"zcc_forwarding_profile/tests/smoke.tftest.hcl",
		"zcc_trusted_network/main.tf",
		"zia_url_categories/main.tf",
		"zia_url_filtering_rules/expression_bindings.tf",
		"zia_url_filtering_rules/main.tf",
		"zia_url_filtering_rules/tests/smoke.tftest.hcl",
		"zpa_app_connector_group/main.tf",
		"zpa_application_segment/expression_bindings.tf",
		"zpa_application_segment/main.tf",
		"zpa_application_segment/tests/smoke.tftest.hcl",
		"zpa_application_server/main.tf",
		"zpa_segment_group/main.tf",
		"zpa_server_group/expression_bindings.tf",
		"zpa_server_group/main.tf",
		"zpa_server_group/tests/smoke.tftest.hcl",
	}
	requireV2FullSurfaceDelta(t, omitted.tree, disabled.tree, wantDelta)
}

type v2FullSurfaceFixture struct {
	tree map[string][]byte
}

type v2FullSurfaceEdge struct {
	artifactField string
	declaredField string
	key           string
	nameField     string
	referrer      string
	referent      string
}

func (edge v2FullSurfaceEdge) expression() string {
	return `data.terraform_remote_state.` + edge.referent +
		`.outputs.infrawright_reference_ids.` + edge.referent + `["` + edge.key + `"]`
}

func (edge v2FullSurfaceEdge) concreteField() string {
	if edge.artifactField != "" {
		return edge.artifactField
	}
	return edge.declaredField
}

func (edge v2FullSurfaceEdge) declaration() string {
	return fmt.Sprintf("%s.%s -> %s (name_field=%s)", edge.referrer, edge.declaredField, edge.referent, edge.nameField)
}

func requireV2FullSurfaceDeclaredEdges(t *testing.T, repositoryRoot string, wantEdges []v2FullSurfaceEdge) {
	t.Helper()
	profile := filepath.Join(repositoryRoot, "packs", "full.packset.json")
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repositoryRoot, "packs"), ProfilePath: &profile, CatalogPath: &profile,
	})
	if err != nil {
		t.Fatalf("load full-profile metadata for reference qualification: %v", err)
	}
	references := transform.MergedTransformReferences(root)
	got := make([]string, 0, len(wantEdges))
	for referrer, fields := range references {
		for field, raw := range fields {
			specification, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("merged reference %s.%s = %#v, want object", referrer, field, raw)
			}
			referent, referentOK := specification["referent"].(string)
			nameField, nameFieldOK := specification["name_field"].(string)
			if !referentOK || !nameFieldOK {
				t.Fatalf("merged reference %s.%s = %#v, want string referent and name_field", referrer, field, raw)
			}
			got = append(got, fmt.Sprintf("%s.%s -> %s (name_field=%s)", referrer, field, referent, nameField))
		}
	}
	want := make([]string, len(wantEdges))
	for index, edge := range wantEdges {
		want[index] = edge.declaration()
	}
	sort.Strings(got)
	sort.Strings(want)
	if gotText, wantText := strings.Join(got, "\n"), strings.Join(want, "\n"); gotText != wantText {
		t.Errorf("merged full-profile reference declarations differ\n got:\n%s\nwant:\n%s", gotText, wantText)
	}
}

func v2FullSurfaceResourceTypes(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	profile := filepath.Join(repositoryRoot, "packs", "full.packset.json")
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repositoryRoot, "packs"), ProfilePath: &profile, CatalogPath: &profile,
	})
	if err != nil {
		t.Fatalf("load full-profile metadata for resource qualification: %v", err)
	}
	resourceTypes := make([]string, 0, len(root.Resources))
	for resourceType, resource := range root.Resources {
		generated, _ := resource.Registry["generate"].(bool)
		if !generated {
			t.Fatalf("full-profile resource %s is not generated", resourceType)
		}
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)
	if len(resourceTypes) != 151 {
		t.Fatalf("full-profile generated resource count = %d, want 151", len(resourceTypes))
	}
	return resourceTypes
}

func runV2FullSurfaceGenEnv(
	t *testing.T,
	repositoryRoot, binary, name string,
	crossStateReferences *bool,
) v2FullSurfaceFixture {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), name)
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeV2FullSurfaceInputs(t, workspace)
	for _, directory := range []string{filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create credential-free gen-env directory %q: %v", directory, err)
		}
	}

	roots := map[string]any{}
	if crossStateReferences != nil {
		for _, provider := range []string{"zcc", "zia", "zpa", "ztc"} {
			roots[provider] = map[string]any{"cross_state_references": *crossStateReferences}
		}
	}
	writeBlockC4JSON(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      roots,
	})

	result := runBinaryWithEnv(t, workspace, binary, []string{
		"gen-env", "--tenant", "qualification",
		"--root", filepath.Join(repositoryRoot, "packs"),
		"--profile", filepath.Join(repositoryRoot, "packs", "full.packset.json"),
		"--catalog", filepath.Join(repositoryRoot, "packs", "full.packset.json"),
		"--deployment", deploymentPath,
	}, []string{
		"HOME=" + filepath.Join(workspace, "home"),
		"INFRAWRIGHT_DEPLOYMENT=",
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"TMPDIR=" + filepath.Join(workspace, "tmp"),
	})
	if result.exit != 0 {
		t.Fatalf("gen-env (%s) exit = %d, want 0; stdout=%q stderr=%q", name, result.exit, result.stdout, result.stderr)
	}
	tree := treeBytes(t, filepath.Join(workspace, "envs", "qualification"))
	for path, content := range tree {
		if bytes.Contains(content, []byte(workspace)) {
			t.Errorf("gen-env (%s) artifact %q leaks its private fixture path", name, path)
		}
	}
	return v2FullSurfaceFixture{tree: tree}
}

func writeV2FullSurfaceInputs(t *testing.T, workspace string) {
	t.Helper()
	config := filepath.Join(workspace, "config", "qualification")
	writeBlockC4JSON(t, filepath.Join(config, "zcc_trusted_network.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"network_one": map[string]any{}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zcc_forwarding_profile.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"profile_one": map[string]any{
			"trusted_network_ids":          []any{"network-1"},
			"trusted_network_ids_selected": []any{"network-1"},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zia_url_categories.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"category_one": map[string]any{
			"configured_name": "Category One", "custom_category": true, "urls": []any{},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zia_url_filtering_rules.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"rule_one": map[string]any{"url_categories": []any{"category-1"}}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"segment_one": map[string]any{
			"description": "Segment", "enabled": true, "name": "Segment One",
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_app_connector_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"connector_one": map[string]any{
			"description": "Connector", "enabled": true, "name": "Connector One",
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_application_server.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"application_server": map[string]any{}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_server_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"server_one": map[string]any{
			"app_connector_groups": []any{map[string]any{"id": []any{"connector-1"}}},
			"description":          "Server",
			"enabled":              true,
			"name":                 "Server One",
			"servers":              []any{map[string]any{"id": []any{"application-server-1"}}},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "segment-1",
			"server_groups":    []any{map[string]any{"id": []any{"server-1"}}},
		}},
	})

	writeBlockC4JSON(t, filepath.Join(config, "zcc_forwarding_profile.generated.expressions.json"), map[string]any{
		"resources": map[string]any{"zcc_forwarding_profile.profile_one": map[string]any{
			"trusted_network_ids":          map[string]any{"expression": v2FullSurfaceCollectionExpression("zcc_trusted_network", "network_one")},
			"trusted_network_ids_selected": map[string]any{"expression": v2FullSurfaceCollectionExpression("zcc_trusted_network", "network_one")},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zia_url_filtering_rules.generated.expressions.json"), map[string]any{
		"resources": map[string]any{"zia_url_filtering_rules.rule_one": map[string]any{
			"url_categories": map[string]any{"expression": v2FullSurfaceCollectionExpression("zia_url_categories", "category_one")},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{"zpa_application_segment.app_one": map[string]any{
			"segment_group_id":    map[string]any{"expression": v2FullSurfaceExpression("zpa_segment_group", "segment_one")},
			"server_groups[0].id": map[string]any{"expression": v2FullSurfaceCollectionExpression("zpa_server_group", "server_one")},
		}},
	})
	writeBlockC4JSON(t, filepath.Join(config, "zpa_server_group.generated.expressions.json"), map[string]any{
		"resources": map[string]any{"zpa_server_group.server_one": map[string]any{
			"app_connector_groups[0].id": map[string]any{"expression": v2FullSurfaceCollectionExpression("zpa_app_connector_group", "connector_one")},
			"servers[0].id":              map[string]any{"expression": v2FullSurfaceCollectionExpression("zpa_application_server", "application_server")},
		}},
	})
}

func v2FullSurfaceExpression(referent, key string) string {
	return `data.terraform_remote_state.` + referent +
		`.outputs.infrawright_reference_ids.` + referent + `["` + key + `"]`
}

func v2FullSurfaceCollectionExpression(referent, key string) string {
	return "[" + v2FullSurfaceExpression(referent, key) + "]"
}

func requireV2FullSurfaceSingletonRoots(t *testing.T, name string, tree map[string][]byte, wantLabels []string) {
	t.Helper()
	roots := map[string][]byte{}
	for path, content := range tree {
		label, _, found := strings.Cut(path, "/")
		if !found || !containsV2FullSurfacePath(wantLabels, label) {
			t.Errorf("gen-env (%s) artifact %q is outside the exact full-profile root set", name, path)
		}
		if path != label+"/main.tf" {
			continue
		}
		roots[label] = content
	}
	if got := len(roots); got != 151 {
		t.Fatalf("gen-env (%s) singleton main.tf root count = %d, want 151", name, got)
	}
	gotLabels := make([]string, 0, len(roots))
	for label := range roots {
		gotLabels = append(gotLabels, label)
	}
	sort.Strings(gotLabels)
	if !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatalf("gen-env (%s) root labels differ from exact full-profile surface\n got: %v\nwant: %v", name, gotLabels, wantLabels)
	}
	for label, main := range roots {
		if got := strings.Count(string(main), "module \""); got != 1 {
			t.Errorf("gen-env (%s) root %q module count = %d, want 1 singleton module", name, label, got)
		}
		if !bytes.Contains(main, []byte(`module "`+label+`" {`)) {
			t.Errorf("gen-env (%s) root %q main.tf lacks its singleton module block", name, label)
		}
	}
}

var v2FullSurfaceRemoteExpression = regexp.MustCompile(
	`data\.terraform_remote_state\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.infrawright_reference_ids\.[A-Za-z_][A-Za-z0-9_]*\["[A-Za-z_][A-Za-z0-9_]*"\]`,
)

func requireV2FullSurfaceEdges(t *testing.T, name string, tree map[string][]byte, edges []v2FullSurfaceEdge) {
	t.Helper()
	var got []string
	var want []string
	for _, edge := range edges {
		bindingsPath := edge.referrer + "/expression_bindings.tf"
		if _, found := tree[bindingsPath]; !found {
			t.Errorf("gen-env (%s) lacks bindings for %s.%s at %q", name, edge.referrer, edge.concreteField(), bindingsPath)
		}
		want = append(want, edge.expression())
	}
	for path, content := range tree {
		if !strings.HasSuffix(path, "/expression_bindings.tf") {
			continue
		}
		got = append(got, v2FullSurfaceRemoteExpression.FindAllString(string(content), -1)...)
	}
	sort.Strings(got)
	sort.Strings(want)
	if gotText, wantText := strings.Join(got, "\n"), strings.Join(want, "\n"); gotText != wantText {
		t.Errorf("gen-env (%s) remote-state expressions differ from the committed seven-edge list\n got:\n%s\nwant:\n%s", name, gotText, wantText)
	}
	requireV2FullSurfaceBindingFiles(t, name, tree, edges)
}

func requireV2FullSurfaceBindingFiles(t *testing.T, name string, tree map[string][]byte, edges []v2FullSurfaceEdge) {
	t.Helper()
	wantByPath := map[string]string{
		"zcc_forwarding_profile/expression_bindings.tf":  v2FullSurfaceZCCBindings,
		"zia_url_filtering_rules/expression_bindings.tf": v2FullSurfaceZIABindings,
		"zpa_application_segment/expression_bindings.tf": v2FullSurfaceZPAApplicationBindings,
		"zpa_server_group/expression_bindings.tf":        v2FullSurfaceZPAServerBindings,
	}
	if len(wantByPath) != len(v2FullSurfaceReferrers(edges)) {
		t.Fatalf("literal binding oracle covers %d referrers, want %d", len(wantByPath), len(v2FullSurfaceReferrers(edges)))
	}
	for path, want := range wantByPath {
		got, found := tree[path]
		if !found {
			t.Errorf("gen-env (%s) lacks literal-oracle binding file %q", name, path)
			continue
		}
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("gen-env (%s) binding file %q does not match the independent literal field/path/shape oracle\n got: %q\nwant: %q", name, path, got, want)
		}
	}
}

func v2FullSurfaceReferrers(edges []v2FullSurfaceEdge) map[string]bool {
	referrers := make(map[string]bool)
	for _, edge := range edges {
		referrers[edge.referrer] = true
	}
	return referrers
}

const v2FullSurfaceZCCBindings = `# GENERATED by engine.gen_env from expression bindings — do not edit.
# Regenerate: make gen-env TENANT=<tenant>

locals {
  infrawright_expression_bound_items = merge(var.items, {
    "profile_one" = merge(var.items["profile_one"], {
      trusted_network_ids          = [data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network["network_one"]]
      trusted_network_ids_selected = [data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network["network_one"]]
    })
  })
}
`

const v2FullSurfaceZIABindings = `# GENERATED by engine.gen_env from expression bindings — do not edit.
# Regenerate: make gen-env TENANT=<tenant>

locals {
  infrawright_expression_bound_items = merge(var.items, {
    "rule_one" = merge(var.items["rule_one"], {
      url_categories = [data.terraform_remote_state.zia_url_categories.outputs.infrawright_reference_ids.zia_url_categories["category_one"]]
    })
  })
}
`

const v2FullSurfaceZPAApplicationBindings = `# GENERATED by engine.gen_env from expression bindings — do not edit.
# Regenerate: make gen-env TENANT=<tenant>

locals {
  infrawright_expression_bound_items = merge(var.items, {
    "app_one" = merge(var.items["app_one"], {
      segment_group_id = data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]
      server_groups = concat(slice(var.items["app_one"].server_groups, 0, 0), [merge(var.items["app_one"].server_groups[0], {
        id = [data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group["server_one"]]
      })], slice(var.items["app_one"].server_groups, 1, length(var.items["app_one"].server_groups)))
    })
  })
}
`

const v2FullSurfaceZPAServerBindings = `# GENERATED by engine.gen_env from expression bindings — do not edit.
# Regenerate: make gen-env TENANT=<tenant>

locals {
  infrawright_expression_bound_items = merge(var.items, {
    "server_one" = merge(var.items["server_one"], {
      app_connector_groups = concat(slice(var.items["server_one"].app_connector_groups, 0, 0), [merge(var.items["server_one"].app_connector_groups[0], {
        id = [data.terraform_remote_state.zpa_app_connector_group.outputs.infrawright_reference_ids.zpa_app_connector_group["connector_one"]]
      })], slice(var.items["server_one"].app_connector_groups, 1, length(var.items["server_one"].app_connector_groups)))
      servers = concat(slice(var.items["server_one"].servers, 0, 0), [merge(var.items["server_one"].servers[0], {
        id = [data.terraform_remote_state.zpa_application_server.outputs.infrawright_reference_ids.zpa_application_server["application_server"]]
      })], slice(var.items["server_one"].servers, 1, length(var.items["server_one"].servers)))
    })
  })
}
`

func requireV2FullSurfaceDisabled(t *testing.T, tree map[string][]byte) {
	t.Helper()
	for path, content := range tree {
		if strings.HasSuffix(path, "/expression_bindings.tf") {
			t.Errorf("explicit false gen-env emitted generated-expression artifact %q", path)
		}
		if bytes.Contains(content, []byte("terraform_remote_state")) {
			t.Errorf("explicit false gen-env emitted remote-state artifact %q", path)
		}
	}
}

func requireV2FullSurfaceDelta(t *testing.T, enabled, disabled map[string][]byte, want []string) {
	t.Helper()
	allPaths := map[string]bool{}
	for path := range enabled {
		allPaths[path] = true
	}
	for path := range disabled {
		allPaths[path] = true
	}
	got := make([]string, 0, len(want))
	for path := range allPaths {
		enabledBytes, enabledFound := enabled[path]
		disabledBytes, disabledFound := disabled[path]
		if enabledFound && disabledFound && !bytes.Equal(enabledBytes, disabledBytes) {
			if !containsV2FullSurfacePath(want, path) {
				t.Errorf("default-versus-false common artifact %q bytes differ outside the 17-path delta", path)
			}
		}
		if enabledFound != disabledFound || !bytes.Equal(enabledBytes, disabledBytes) {
			got = append(got, path)
		}
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if gotText, wantText := strings.Join(got, "\n"), strings.Join(want, "\n"); gotText != wantText {
		t.Errorf("default-versus-false artifact delta differs\n got:\n%s\nwant:\n%s", gotText, wantText)
	}
}

func containsV2FullSurfacePath(paths []string, value string) bool {
	for _, path := range paths {
		if path == value {
			return true
		}
	}
	return false
}
