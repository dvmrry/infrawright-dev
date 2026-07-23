package envgen

// environment_generator_test.go ports node-tests/environment-generator.test.ts.
//
// Every test that does NOT depend on a live Python oracle is ported
// verbatim (same fixtures, same assertions), driven against the real
// committed pack root (packs/ + packs/full.packset.json, exactly as the Node
// test's committedRoot() helper does) and, where the Node test used a real
// `terraform fmt` subprocess (terraformHclFormatter), an equivalent local
// Go helper (terraformFmtFormatter below) that shells out to the same
// `terraform fmt -` command -- this environment has a working `terraform`
// binary (see this package's port report).
//
// Three Node tests are adapted rather than ported verbatim, because Python
// is out of scope for this Go-only wave (docs/go-runtime-plan.md: the
// Python archive is a precondition of this port, not a dependency of it):
//   - "complete generated root trees match Python for ungrouped,
//     grouped/bound, singleton HCL, and slug roots" and "the complete
//     full-profile generated root tree is byte-identical to Python" each
//     spawn a Python `engine.gen_env` oracle via PYTHON_ORACLE and diff its
//     output tree against the Go/Node candidate byte-for-byte. This port
//     keeps every one of the Node test's OWN structural assertions (the
//     `assert.match`/`assert.equal` calls on the generated tree, e.g. the
//     grouped-root backend marker, the HCL-format "validation reads json
//     only" diagnostic, the slug root's module selection) but drops the
//     line comparing against a live Python run, since no Python oracle is
//     available or in scope here. See TestPythonParityScenariosMatchStructurally
//     and TestFullProfileTreeGeneratesAllRoots.
//   - "dangling artifact paths retain Python existence and stale-file
//     semantics" spawns Python only to produce a "known good" starting
//     tree; the actual behavior under test (a dangling symlink's exists()
//     semantics, and that regeneration leaves an untouched dangling
//     symlink alone rather than resolving/replacing it) is pure
//     Go-generator behavior. See TestDanglingArtifactPathsPreserveSymlinks,
//     which seeds the same dangling symlinks and asserts they survive
//     generation, without a Python-produced comparison tree.
//
// One Node test is not ported at all: "make gen-env is Python-disabled and
// writes a real formatted root" drives the `make gen-env` CLI target
// end-to-end (spawning `make`, which invokes the Node CLI's `gen-env`
// adapter). No Go CLI wiring for `gen-env` exists yet -- that is a later
// slice (docs/go-runtime-plan.md's roots/scope-paths/plan-roots/
// environment-generation slice covers this envgen package; the CLI-shell
// slice that would give `iw gen-env` a Make target is still ahead) -- so
// there is nothing in this package's own contract for that test to drive.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

func strPtr(s string) *string { return &s }

func temporaryDirectory(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeJSONFile(t *testing.T, file string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(file, append(data, '\n'), 0o666); err != nil {
		t.Fatalf("WriteFile %s: %v", file, err)
	}
}

func readFileString(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", file, err)
	}
	return string(data)
}

func loadDeploymentFile(t *testing.T, path string) deployment.Deployment {
	t.Helper()
	dep, err := deployment.LoadDeployment(path)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	return dep
}

// identityFormatter is the Go analogue of the Node tests' `async (source)
// => source` inline HclFormatter, used wherever the Node test does not
// need real Terraform formatting.
func identityFormatter(source string) (string, error) { return source, nil }

// terraformFmtFormatter is the Go analogue of terraformHclFormatter from
// node-src/modules/generator.ts (see this package's port report for why it
// is reproduced locally rather than imported: that TS file is owned by the
// sibling modulesgen port, out of reach here). It shells out to `terraform
// fmt -`, exactly as the TS source's child_process.spawn call does.
func terraformFmtFormatter(t *testing.T) HclFormatter {
	t.Helper()
	executable := terraformTestExecutable(t)
	return func(source string) (string, error) {
		cmd := exec.Command(executable, "fmt", "-")
		cmd.Stdin = strings.NewReader(source)
		output, err := cmd.Output()
		if err != nil {
			detail := ""
			if exitErr, ok := err.(*exec.ExitError); ok {
				detail = strings.TrimSpace(string(exitErr.Stderr))
			}
			return "", fmt.Errorf("%s fmt failed: %v: %s", executable, err, detail)
		}
		return string(output), nil
	}
}

func terraformTestExecutable(t *testing.T) string {
	t.Helper()
	if executable := strings.TrimSpace(os.Getenv("TF")); executable != "" {
		return executable
	}
	if executable, err := exec.LookPath("terraform"); err == nil {
		return executable
	}
	t.Skip("no terraform executable on PATH; set TF to enable this cross-check")
	return ""
}

// snapshotTree ports the `snapshotTree` test helper from
// node-tests/environment-generator.test.ts, including its symlink handling:
// os.DirEntry (like Node's Dirent from readdir(withFileTypes)) reports its
// own entry type without following the link, so a symlink is neither
// IsDir() nor Type().IsRegular() and is silently skipped here exactly as
// entry.isFile()/entry.isDirectory() both being false skips it there.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	output := map[string]string{}
	var visit func(current string)
	visit = func(current string) {
		entries, err := os.ReadDir(current)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", current, err)
		}
		for _, entry := range entries {
			candidate := filepath.Join(current, entry.Name())
			switch {
			case entry.IsDir():
				visit(candidate)
			case entry.Type().IsRegular():
				data, err := os.ReadFile(candidate)
				if err != nil {
					t.Fatalf("ReadFile %s: %v", candidate, err)
				}
				rel, err := filepath.Rel(dir, candidate)
				if err != nil {
					t.Fatalf("Rel: %v", err)
				}
				output[rel] = string(data)
			}
		}
	}
	visit(dir)
	return output
}

func copyDirRecursive(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("Stat %s: %v", src, err)
	}
	if !info.IsDir() {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", src, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(dst, data, info.Mode()); err != nil {
			t.Fatalf("WriteFile %s: %v", dst, err)
		}
		return
	}
	if err := os.MkdirAll(dst, 0o777); err != nil {
		t.Fatalf("MkdirAll %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", src, err)
	}
	for _, entry := range entries {
		copyDirRecursive(t, filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
	}
}

// reducedPackRootForProfile ports the `reducedPackRootForProfile` test
// helper from node-tests/environment-generator.test.ts.
func reducedPackRootForProfile(t *testing.T, repo, parent, profile string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, "packs", profile))
	if err != nil {
		t.Fatalf("ReadFile packset %s: %v", profile, err)
	}
	var document struct {
		Packs  []string `json:"packs"`
		Shared []string `json:"shared"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("Unmarshal packset %s: %v", profile, err)
	}
	destination := filepath.Join(parent, "packs-"+strings.TrimSuffix(profile, ".packset.json"))
	if err := os.MkdirAll(destination, 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range document.Packs {
		copyDirRecursive(t, filepath.Join(repo, "packs", name), filepath.Join(destination, name))
	}
	for _, name := range document.Shared {
		if err := os.MkdirAll(filepath.Join(destination, "_shared"), 0o777); err != nil {
			t.Fatalf("MkdirAll _shared: %v", err)
		}
		copyDirRecursive(t, filepath.Join(repo, "packs", "_shared", name), filepath.Join(destination, "_shared", name))
	}
	return destination
}

func committedRootFor(t *testing.T, packsRoot, profilePath, catalogPath string) metadata.LoadedPackRoot {
	t.Helper()
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath, CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded
}

func TestUngroupedMainRenderingByteIdenticalToLegacyGolden(t *testing.T) {
	root := committedRootForTopology(t)
	dep := deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}}
	tenant := "zs2"
	result, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Deployment: dep, Root: root, Selectors: []string{"zpa_segment_group"}, Tenant: &tenant,
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	actual, err := RenderEnvironmentMain(RenderEnvironmentMainOptions{
		Deployment:           dep,
		EnvironmentDirectory: "envs/zs2/zpa_segment_group",
		Label:                "zpa_segment_group",
		Members:              []string{"zpa_segment_group"},
		Root:                 root,
		Tenant:               "zs2",
		Topology:             result.Topology,
	})
	if err != nil {
		t.Fatalf("RenderEnvironmentMain: %v", err)
	}
	want := strings.Join([]string{
		"# GENERATED by engine.gen_env for tenant 'zs2' — do not edit.",
		"# Regenerate: make gen-env TENANT=zs2",
		"",
		"terraform {",
		`  required_version = ">= 1.5"`,
		"  required_providers {",
		"    zpa = {",
		`      source = "zscaler/zpa"`,
		"    }",
		"  }",
		"  # local state — opt into remote state with",
		"  # make gen-env TENANT=zs2 BACKEND=azurerm",
		"}",
		"",
		`provider "zpa" {`,
		"  # credentials via provider environment variables",
		"}",
		"",
		`variable "items" {`,
		"  # opaque at the root; the module enforces the strict type.",
		"  type = any",
		"}",
		"",
		`module "zpa_segment_group" {`,
		`  source = "../../../modules/zpa_segment_group"`,
		"  items = var.items",
		"}",
		"",
	}, "\n")
	if actual != want {
		t.Fatalf("actual =\n%s\nwant =\n%s", actual, want)
	}
}

func TestCrossStateModeEmitsSingletonOutputsAndRemoteStateConsumers(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-cross-state-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{"zpa": map[string]any{"cross_state_references": true}},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"segment_one": map[string]any{"description": "Segment", "enabled": true, "name": "Segment One"},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})
	root := committedRootForTopology(t)
	dep := loadDeploymentFile(t, deploymentPath)
	formatHcl := terraformFmtFormatter(t)
	wantLabels := []string{
		"zpa_app_connector_group", "zpa_application_segment", "zpa_application_server",
		"zpa_segment_group", "zpa_server_group",
	}

	generated, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: formatHcl, OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	if got := rootLabels(generated); !reflect.DeepEqual(got, wantLabels) {
		t.Fatalf("labels = %v, want %v", got, wantLabels)
	}

	referentMain := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_segment_group", "main.tf"))
	mustMatch(t, referentMain, `output "infrawright_reference_ids"`)
	mustMatch(t, referentMain, `zpa_segment_group = \{ for key, item in module\.zpa_segment_group\.items : key => item\.id \}`)
	referrerMain := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_application_segment", "main.tf"))
	mustMatch(t, referrerMain, `data "terraform_remote_state" "zpa_segment_group"`)
	mustMatch(t, referrerMain, `path = "\.\./zpa_segment_group/terraform\.tfstate"`)
	mustNotMatch(t, referrerMain, `module "zpa_segment_group"`)
	overlay := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_application_segment", "expression_bindings.tf"))
	mustMatch(t, overlay, `data\.terraform_remote_state\.zpa_segment_group`)
	smoke := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_application_segment", "tests", "smoke.tftest.hcl"))
	mustMatch(t, smoke, `override_data`)
	mustMatch(t, smoke, `infrawright-test-reference-id`)

	remoteOutputRoot := filepath.Join(workspace, "generated-azurerm")
	generatedRemote, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Backend: strPtr("azurerm"), Deployment: dep, FormatHcl: formatHcl, OutputRoot: &remoteOutputRoot, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots (azurerm): %v", err)
	}
	if got := rootLabels(generatedRemote); !reflect.DeepEqual(got, wantLabels) {
		t.Fatalf("remote labels = %v, want %v", got, wantLabels)
	}
	remoteMain := readFileString(t, filepath.Join(remoteOutputRoot, "tenant", "zpa_application_segment", "main.tf"))
	mustMatch(t, remoteMain, `variable "infrawright_remote_state_backend_config"`)
	mustMatch(t, remoteMain, `sensitive\s+= true`)
	mustMatch(t, remoteMain, `config = merge\(var\.infrawright_remote_state_backend_config`)
	mustMatch(t, remoteMain, `key = "tenant/zpa_segment_group\.tfstate"`)
	remoteSmoke := readFileString(t, filepath.Join(remoteOutputRoot, "tenant", "zpa_application_segment", "tests", "smoke.tftest.hcl"))
	mustMatch(t, remoteSmoke, `infrawright_remote_state_backend_config = \{`)
	mustMatch(t, remoteSmoke, `use_azuread_auth\s+= true`)
}

func TestAbsentAndExplicitCrossStateProduceIdenticalDeclaredBindingArtifacts(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-default-cross-state-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	config := filepath.Join(workspace, "config", "tenant")
	writeFixture := func() {
		writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"segment_one": map[string]any{"description": "Segment", "enabled": true, "name": "Segment One"}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_server_group.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"server_one": map[string]any{
				"description":          "Server",
				"enabled":              true,
				"name":                 "Server One",
				"app_connector_groups": []any{map[string]any{"id": []any{"connector-1"}}},
				"servers":              []any{map[string]any{"id": []any{"application-server-1"}}},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_app_connector_group.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"connector_one": map[string]any{"description": "Connector", "enabled": true, "name": "Connector One"}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_application_server.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"application_server": map[string]any{}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"app_one": map[string]any{
				"segment_group_id": "segment-1",
				"server_groups":    []any{map[string]any{"id": []any{"server-1"}}},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zia_url_categories.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"category_one": map[string]any{"configured_name": "Category One", "custom_category": true, "urls": []any{}}},
		})
		writeJSONFile(t, filepath.Join(config, "zia_url_filtering_rules.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"rule_one": map[string]any{"url_categories": []any{"category-1"}}},
		})
		writeJSONFile(t, filepath.Join(config, "zcc_trusted_network.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"network_one": map[string]any{}},
		})
		writeJSONFile(t, filepath.Join(config, "zcc_forwarding_profile.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"profile_one": map[string]any{
				"trusted_network_ids":          []any{"network-1"},
				"trusted_network_ids_selected": []any{"network-1"},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
			"resources": map[string]any{"zpa_application_segment.app_one": map[string]any{
				"segment_group_id":    map[string]any{"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`},
				"server_groups[0].id": map[string]any{"expression": `[data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group["server_one"]]`},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_server_group.generated.expressions.json"), map[string]any{
			"resources": map[string]any{"zpa_server_group.server_one": map[string]any{
				"app_connector_groups[0].id": map[string]any{"expression": `[data.terraform_remote_state.zpa_app_connector_group.outputs.infrawright_reference_ids.zpa_app_connector_group["connector_one"]]`},
				"servers[0].id":              map[string]any{"expression": `[data.terraform_remote_state.zpa_application_server.outputs.infrawright_reference_ids.zpa_application_server["application_server"]]`},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zia_url_filtering_rules.generated.expressions.json"), map[string]any{
			"resources": map[string]any{"zia_url_filtering_rules.rule_one": map[string]any{
				"url_categories": map[string]any{"expression": `[data.terraform_remote_state.zia_url_categories.outputs.infrawright_reference_ids.zia_url_categories["category_one"]]`},
			}},
		})
		writeJSONFile(t, filepath.Join(config, "zcc_forwarding_profile.generated.expressions.json"), map[string]any{
			"resources": map[string]any{"zcc_forwarding_profile.profile_one": map[string]any{
				"trusted_network_ids":          map[string]any{"expression": `[data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network["network_one"]]`},
				"trusted_network_ids_selected": map[string]any{"expression": `[data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network["network_one"]]`},
			}},
		})
	}
	writeFixture()
	run := func(rootsConfig map[string]any) map[string]string {
		writeJSONFile(t, deploymentPath, map[string]any{
			"module_dir": filepath.Join(workspace, "modules"),
			"overlay":    workspace,
			"roots":      rootsConfig,
		})
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: loadDeploymentFile(t, deploymentPath), FormatHcl: identityFormatter,
			OutputRoot: &outputRoot, Root: committedRootForTopology(t),
			Selectors: []string{"zcc_forwarding_profile", "zia_url_filtering_rules", "zpa_application_segment", "zpa_server_group"},
			Tenant:    "tenant",
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots(%#v): %v", rootsConfig, err)
		}
		return snapshotTree(t, outputRoot)
	}
	absent := run(map[string]any{})
	explicit := run(map[string]any{
		"zcc": map[string]any{"cross_state_references": true},
		"zia": map[string]any{"cross_state_references": true},
		"zpa": map[string]any{"cross_state_references": true},
	})
	if !reflect.DeepEqual(explicit, absent) {
		t.Fatalf("explicit true artifact tree differs from absent default (-want +got):\nwant=%#v\ngot=%#v", absent, explicit)
	}
	for _, expression := range []string{
		`data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group`,
		`data.terraform_remote_state.zia_url_categories.outputs.infrawright_reference_ids.zia_url_categories`,
		`trusted_network_ids_selected`,
		`data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network`,
	} {
		if !strings.Contains(strings.Join([]string{
			explicit["tenant/zpa_application_segment/expression_bindings.tf"],
			explicit["tenant/zia_url_filtering_rules/expression_bindings.tf"],
			explicit["tenant/zcc_forwarding_profile/expression_bindings.tf"],
		}, "\n"), expression) {
			t.Errorf("generated binding artifacts do not contain %q", expression)
		}
	}
	zccBindings := explicit["tenant/zcc_forwarding_profile/expression_bindings.tf"]
	if got := strings.Count(zccBindings, "data.terraform_remote_state.zcc_trusted_network.outputs.infrawright_reference_ids.zcc_trusted_network"); got != 2 {
		t.Errorf("zcc shared referent output count = %d, want 2", got)
	}
}

func rootLabels(result EnvironmentGenerationResult) []string {
	labels := make([]string, len(result.Roots))
	for i, r := range result.Roots {
		labels[i] = r.Label
	}
	return labels
}

func TestValidateRemoteStateReferencesEmptySetNeedsNoIndex(t *testing.T) {
	if err := validateRemoteStateReferences(
		remoteStateReferenceValidationIndex{},
		"current",
		nil,
	); err != nil {
		t.Errorf("validateRemoteStateReferences(empty) error = %v, want nil", err)
	}
}

func BenchmarkValidateRemoteStateReferencesSharedIndex(b *testing.B) {
	const rootCount = 151
	rootsByLabel := make(map[string]roots.RootTopologyRoot, rootCount)
	for i := range rootCount {
		label := fmt.Sprintf("root_%03d", i)
		root := roots.RootTopologyRoot{Label: label, Members: []string{label + "_resource"}}
		rootsByLabel[label] = root
	}
	crossState := CrossStateReferenceTopology{Edges: []CrossStateReferenceEdge{{
		Field:        "target_id",
		Referrer:     "root_001_resource",
		ReferrerRoot: "root_001",
		Referent:     "root_000_resource",
		ReferentRoot: "root_000",
	}}}
	index := newRemoteStateReferenceValidationIndex(crossState, rootsByLabel)
	references := []boundRemoteStateReference{{
		RemoteStateReference: RemoteStateReference{
			Key:          "target",
			ResourceType: "root_000_resource",
			Root:         "root_000",
		},
		Field:    "target_id",
		Referrer: "root_001_resource",
	}}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := validateRemoteStateReferences(index, "root_001", references); err != nil {
			b.Fatalf("validateRemoteStateReferences(shared index) error = %v, want nil", err)
		}
	}
}

func TestExplicitCrossStateDisableDoesNotActivateOperatorDataSelectors(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-operator-data-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{"zpa": map[string]any{"cross_state_references": false}},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})
	root := committedRootForTopology(t)
	dep := loadDeploymentFile(t, deploymentPath)
	generated, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	if got := rootLabels(generated); !reflect.DeepEqual(got, []string{"zpa_application_segment"}) {
		t.Fatalf("labels = %v", got)
	}
	main := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_application_segment", "main.tf"))
	mustNotMatch(t, main, `data "terraform_remote_state"`)
	if fileExists(filepath.Join(outputRoot, "tenant", "zpa_segment_group", "main.tf")) {
		t.Fatal("zpa_segment_group root should not have been generated")
	}
}

func TestCrossStateOperatorSelectorsMustTargetDeclaredEdge(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-operator-edge-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{"zpa": map[string]any{"cross_state_references": true}},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_app_connector_group.outputs.infrawright_reference_ids.zpa_app_connector_group["group_one"]`,
				},
			},
		},
	})
	root := committedRootForTopology(t)
	dep := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	mustMatch(t, err.Error(), `is not declared by pack reference metadata`)
}

func TestNativeHclBindingsRequireExactIndexesForListBlocks(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-hcl-list-binding-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir":    filepath.Join(workspace, "modules"),
		"overlay":       workspace,
		"roots":         map[string]any{},
		"tfvars_format": "hcl",
	})
	config := filepath.Join(workspace, "config", "tenant")
	if err := os.MkdirAll(config, 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(config, "zia_url_filtering_rules.auto.tfvars"),
		[]byte("items = { isolate = { cbi_profile = [{ id = \"old\" }] } }\n"),
		0o666,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bindingPath := filepath.Join(config, "zia_url_filtering_rules.expressions.json")
	writeBinding := func(targetPath string) {
		writeJSONFile(t, bindingPath, map[string]any{
			"resources": map[string]any{
				"zia_url_filtering_rules.isolate": map[string]any{
					targetPath: map[string]any{"expression": "var.cbi_profile_id"},
				},
			},
		})
	}
	root := committedRootForTopology(t)
	generate := func() (EnvironmentGenerationResult, error) {
		dep := loadDeploymentFile(t, deploymentPath)
		return GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: dep, FormatHcl: identityFormatter, OutputRoot: &outputRoot, Root: root,
			Selectors: []string{"zia_url_filtering_rules"}, Tenant: "tenant",
		})
	}

	writeBinding("cbi_profile.id")
	if _, err := generate(); err == nil {
		t.Fatal("expected error")
	} else {
		mustMatch(t, err.Error(), `cbi_profile\.id traverses list block zia_url_filtering_rules\.cbi_profile without an exact numeric selector`)
	}

	writeBinding("cbi_profile[0].id")
	if _, err := generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	overlay := readFileString(t, filepath.Join(outputRoot, "tenant", "zia_url_filtering_rules", "expression_bindings.tf"))
	mustMatch(t, overlay, `cbi_profile = concat\(slice\(`)
	mustMatch(t, overlay, `cbi_profile\[0\]`)
	mustMatch(t, overlay, `id = var\.cbi_profile_id`)
}

func TestPackDeclaredNestedZpaReferencesValidateIndexedPathsAndDependencyRoots(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-zpa-nested-cross-state-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{"zpa": map[string]any{"cross_state_references": true}},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app": map[string]any{"server_groups": []any{map[string]any{"id": []any{"server-id"}}}}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app": map[string]any{
				"server_groups[0].id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group["server"]`,
				},
			},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_server_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"server": map[string]any{
				"app_connector_groups": []any{map[string]any{"id": []any{"connector-id"}}},
				"servers":              []any{map[string]any{"id": []any{"application-server-id"}}},
			},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_server_group.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_server_group.server": map[string]any{
				"app_connector_groups[0].id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_app_connector_group.outputs.infrawright_reference_ids.zpa_app_connector_group["connector"]`,
				},
				"servers[0].id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_application_server.outputs.infrawright_reference_ids.zpa_application_server["application_server"]`,
				},
			},
		},
	})
	root := committedRootForTopology(t)
	dep := loadDeploymentFile(t, deploymentPath)
	generated, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	wantSet := map[string]bool{
		"zpa_app_connector_group": true, "zpa_application_segment": true, "zpa_application_server": true,
		"zpa_segment_group": true, "zpa_server_group": true,
	}
	gotSet := map[string]bool{}
	for _, label := range rootLabels(generated) {
		gotSet[label] = true
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("labels = %v, want %v", gotSet, wantSet)
	}
	applicationOverlay := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_application_segment", "expression_bindings.tf"))
	mustMatch(t, applicationOverlay, `server_groups = concat\(slice\(`)
	mustMatch(t, applicationOverlay, `terraform_remote_state\.zpa_server_group`)
	serverOverlay := readFileString(t, filepath.Join(outputRoot, "tenant", "zpa_server_group", "expression_bindings.tf"))
	mustMatch(t, serverOverlay, `app_connector_groups = concat\(slice\(`)
	mustMatch(t, serverOverlay, `terraform_remote_state\.zpa_app_connector_group`)
	mustMatch(t, serverOverlay, `terraform_remote_state\.zpa_application_server`)
}

// TestPythonParityScenariosMatchStructurally ports the Go-reachable
// assertions from "complete generated root trees match Python for
// ungrouped, cross-state, singleton HCL, and slug roots" in
// node-tests/environment-generator.test.ts -- everything except the
// Python-oracle byte comparison itself; see this file's package doc
// comment.
func TestPythonParityScenariosMatchStructurally(t *testing.T) {
	root := committedRootForTopology(t)
	formatHcl := terraformFmtFormatter(t)

	t.Run("ungrouped", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-ungrouped-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{}})
		writeJSONFile(t, filepath.Join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"example": map[string]any{"configured_name": "Example", "custom_category": true, "urls": []any{}}},
		})
		dep := loadDeploymentFile(t, deploymentPath)
		outputRoot := filepath.Join(workspace, "generated")
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: dep, FormatHcl: formatHcl, OutputRoot: &outputRoot, Root: root,
			Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots: %v", err)
		}
	})

	t.Run("cross_state_singleton", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-cross-state-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
			"roots": map[string]any{
				"zpa": map[string]any{
					"cross_state_references": true,
				},
			},
		})
		config := filepath.Join(workspace, "config", "tenant")
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"app": map[string]any{"segment_group_id": "sg-1"}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
			"items": map[string]any{"group": map[string]any{"description": "Group", "enabled": true, "name": "Group"}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
			"resources": map[string]any{
				"zpa_application_segment.app": map[string]any{
					"segment_group_id": map[string]any{"expression": `module.zpa_segment_group.items["generated"].id`},
				},
			},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
			"resources": map[string]any{
				"zpa_application_segment.app": map[string]any{
					"segment_group_id": map[string]any{"expression": `module.zpa_segment_group.items["operator"].id`},
				},
			},
		})
		dep := loadDeploymentFile(t, deploymentPath)
		outputRoot := filepath.Join(workspace, "generated")
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Backend: strPtr("azurerm"), Deployment: dep, FormatHcl: formatHcl, OutputRoot: &outputRoot, Root: root,
			Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots: %v", err)
		}
		tree := snapshotTree(t, outputRoot)
		if tree["tenant/.backend"] != "azurerm\n" {
			t.Fatalf("tenant/.backend = %q", tree["tenant/.backend"])
		}
		if _, ok := tree["tenant/zpa_application_segment/main.tf"]; !ok {
			t.Fatalf("generated singleton tree = %#v, want zpa_application_segment root", tree)
		}
	})

	t.Run("hcl_singleton", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-hcl-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "tfvars_format": "hcl",
			"roots": map[string]any{},
		})
		config := filepath.Join(workspace, "config", "tenant")
		if err := os.MkdirAll(config, 0o777); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(config, "zpa_segment_group.auto.tfvars"), []byte("zpa_segment_group_items = {}\n"), 0o666); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		writeJSONFile(t, filepath.Join(config, "zpa_segment_group.expressions.json"), map[string]any{
			"resources": map[string]any{"zpa_segment_group.group": map[string]any{"description": map[string]any{"expression": "var.description"}}},
		})
		dep := loadDeploymentFile(t, deploymentPath)
		outputRoot := filepath.Join(workspace, "generated")
		var diagnostics []string
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: dep, FormatHcl: formatHcl, OnDiagnostic: func(m string) { diagnostics = append(diagnostics, m) },
			OutputRoot: &outputRoot, Root: root, Selectors: []string{"zpa_segment_group"}, Tenant: "tenant",
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots: %v", err)
		}
		foundHclNote := false
		for _, message := range diagnostics {
			if strings.Contains(message, "hcl tfvars; validation reads json only") {
				foundHclNote = true
			}
		}
		if !foundHclNote {
			t.Fatal("expected an hcl-tfvars-validation diagnostic")
		}
		tree := snapshotTree(t, outputRoot)
		mustNotMatch(t, tree["tenant/zpa_segment_group/tests/smoke.tftest.hcl"], `config_plan`)
	})

	t.Run("slug", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-slug-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
			"roots": map[string]any{},
		})
		dep := loadDeploymentFile(t, deploymentPath)
		outputRoot := filepath.Join(workspace, "generated")
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: dep, FormatHcl: formatHcl, OutputRoot: &outputRoot, Root: root,
			Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots: %v", err)
		}
		tree := snapshotTree(t, outputRoot)
		mustMatch(t, tree["tenant/zia_url_categories/main.tf"], `module "zia_url_categories"`)
		mustNotMatch(t, tree["tenant/zia_url_categories/main.tf"], `module "zia_url_filtering_rules"`)
	})
}

// TestFullProfileTreeGeneratesAllRoots ports the Go-reachable half of "the
// complete full-profile generated root tree is byte-identical to Python"
// (151 generated roots, 151*3 files). It also proves the production in-process
// formatter byte-identical to a single recursive Terraform oracle pass.
func TestFullProfileTreeGeneratesAllRoots(t *testing.T) {
	if testing.Short() {
		t.Skip("full-profile Terraform differential skipped under -short")
	}
	root := committedRootForTopology(t)
	workspace := temporaryDirectory(t, "infrawright-gen-env-full-profile-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{}})
	dep := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	formatter := modulesgen.NewHCLFormatter()
	result, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: formatter.FormatHCL, OutputRoot: &outputRoot, Root: root,
		Selectors: []string{}, Tenant: "full-profile-parity",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	if len(result.Roots) != 151 {
		t.Fatalf("len(result.Roots) = %d, want 151", len(result.Roots))
	}
	tree := snapshotTree(t, outputRoot)
	if len(tree) != 151*3 {
		t.Fatalf("len(tree) = %d, want %d", len(tree), 151*3)
	}

	oracleRoot := filepath.Join(workspace, "terraform-oracle")
	oracleResult, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: identityFormatter, OutputRoot: &oracleRoot, Root: root,
		Selectors: []string{}, Tenant: "full-profile-parity",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots (raw Terraform oracle tree): %v", err)
	}
	if len(oracleResult.Roots) != 151 {
		t.Fatalf("len(oracleResult.Roots) = %d, want 151", len(oracleResult.Roots))
	}
	command := exec.Command(terraformTestExecutable(t), "fmt", "-recursive", oracleRoot)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("terraform fmt -recursive %s: %v\n%s", oracleRoot, err, output)
	}
	oracleTree := snapshotTree(t, oracleRoot)
	if !reflect.DeepEqual(tree, oracleTree) {
		for path, got := range tree {
			if want, ok := oracleTree[path]; !ok || got != want {
				t.Errorf("in-process and Terraform-formatted environment trees differ at %s", path)
			}
		}
		for path := range oracleTree {
			if _, ok := tree[path]; !ok {
				t.Errorf("Terraform-formatted environment tree has extra path %s", path)
			}
		}
	}
}

func TestSingletonSelectionDoesNotGenerateUnselectedRoot(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-singleton-selection-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace,
		"roots":   map[string]any{"zpa": map[string]any{"cross_state_references": false}},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app": map[string]any{"description": "literal", "segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_server_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"server": map[string]any{"description": "unselected", "enabled": true, "name": "Server"}},
	})
	output := filepath.Join(workspace, "generated")
	var diagnostics []string
	generated, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, deploymentPath), FormatHcl: identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &output, Root: committedRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots(singleton selection) error: %v", err)
	}
	if got, want := rootLabels(generated), []string{"zpa_application_segment"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GenerateEnvironmentRoots(singleton selection) labels = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(output, "tenant", "zpa_server_group")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("GenerateEnvironmentRoots(singleton selection) generated unselected root: os.Stat error = %v, want os.ErrNotExist", err)
	}
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, "WHOLE_ROOT_SELECTION") || strings.Contains(diagnostic, "selects whole root") {
			t.Errorf("GenerateEnvironmentRoots(singleton selection) diagnostic = %q, want no whole-root selection diagnostic", diagnostic)
		}
	}
}

func TestSingletonCrossStateDisableRemovesStaleGeneratedBindings(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-stale-singleton-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeDeployment := func(crossState bool) {
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace,
			"roots": map[string]any{
				"zpa": map[string]any{"cross_state_references": crossState},
			},
		})
	}
	writeDeployment(true)

	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"segment_one": map[string]any{"description": "Segment", "enabled": true, "name": "Segment One"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	output := filepath.Join(workspace, "generated")
	expressionPath := filepath.Join(output, "tenant", "zpa_application_segment", "expression_bindings.tf")
	root := committedRootForTopology(t)
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, deploymentPath), FormatHcl: identityFormatter,
		OutputRoot: &output, Root: root, Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(cross-state enabled) error: %v", err)
	}
	if _, err := os.Stat(expressionPath); err != nil {
		t.Fatalf("os.Stat(%q) after cross-state generation error = %v, want expression bindings", expressionPath, err)
	}

	writeDeployment(false)
	diagnostics := make([]string, 0)
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, deploymentPath), FormatHcl: identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &output, Root: root, Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(cross-state disabled) error: %v", err)
	}
	if _, err := os.Stat(expressionPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(%q) after cross-state disable error = %v, want os.ErrNotExist", expressionPath, err)
	}
	foundStaleDisabled := false
	foundRemoval := false
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, "stale generated bindings ignored") {
			foundStaleDisabled = true
		}
		if diagnostic == "removed stale "+expressionPath {
			foundRemoval = true
		}
	}
	if !foundStaleDisabled || !foundRemoval {
		t.Errorf("GenerateEnvironmentRoots(cross-state disabled) diagnostics = %#v, want stale-disabled and stale-removal diagnostics", diagnostics)
	}
}

// TestDanglingArtifactPathsPreserveSymlinks ports the Go-reachable core of
// "dangling artifact paths retain Python existence and stale-file
// semantics" from node-tests/environment-generator.test.ts: dangling
// symlinks at exactly the paths generateEnvironmentRoots would otherwise
// consider stale (the root's expression_bindings.tf when there are no
// bindings, and the tenant's .backend marker when no backend is
// requested) must survive generation completely untouched, because
// fileExists (this file's exists() port) follows symlinks and reports a
// dangling one as absent -- so neither the removeIfPresent(...) stale-file
// path nor the backend-marker read/write path ever touches it. See this
// file's package doc comment for why the Python-produced comparison tree
// itself is dropped.
func TestDanglingArtifactPathsPreserveSymlinks(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-dangling-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{},
	})
	configDirectory := filepath.Join(workspace, "config", "tenant")
	if err := os.MkdirAll(configDirectory, 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{
		"zia_url_categories.auto.tfvars.json",
		"zia_url_categories.expressions.json",
		"zia_url_categories.generated.expressions.json",
	} {
		if err := os.Symlink("missing-"+name, filepath.Join(configDirectory, name)); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
	}
	expressionPath := filepath.Join(outputRoot, "tenant", "zia_url_categories", "expression_bindings.tf")
	backendPath := filepath.Join(outputRoot, "tenant", ".backend")
	seedDanglingOutputs := func() {
		if err := os.MkdirAll(filepath.Dir(expressionPath), 0o777); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.Symlink("missing-expression-bindings.tf", expressionPath); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(backendPath), 0o777); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.Symlink("missing-backend", backendPath); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
	}
	seedDanglingOutputs()

	root := committedRootForTopology(t)
	dep := loadDeploymentFile(t, deploymentPath)
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	mustBeSymlink(t, expressionPath)
	mustBeSymlink(t, backendPath)
	tree := snapshotTree(t, outputRoot)
	mustNotMatch(t, tree["tenant/zia_url_categories/main.tf"], `backend "`)
	mustNotMatch(t, tree["tenant/zia_url_categories/tests/smoke.tftest.hcl"], `config_plan`)

	if err := os.RemoveAll(filepath.Join(outputRoot, "tenant", "zia_url_categories")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.Remove(backendPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	seedDanglingOutputs()
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots (rerun): %v", err)
	}
	tree2 := snapshotTree(t, outputRoot)
	if !reflect.DeepEqual(tree2, tree) {
		t.Fatalf("regeneration is not idempotent:\nfirst  = %v\nsecond = %v", tree, tree2)
	}
	mustBeSymlink(t, expressionPath)
	mustBeSymlink(t, backendPath)
}

func mustBeSymlink(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", path)
	}
}

func TestInvalidPythonIncompatibleWhitespaceCannotPartiallyRewriteRoot(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-whitespace-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{"overlay": workspace, "roots": map[string]any{}})
	writeJSONFile(t, filepath.Join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"example": map[string]any{"configured_name": "Example"}},
	})
	writeJSONFile(t, filepath.Join(workspace, "config", "tenant", "zia_url_categories.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zia_url_categories.example": map[string]any{"configured_name": map[string]any{"expression": "[\uFEFF]"}},
		},
	})
	rootDirectory := filepath.Join(workspace, "envs", "tenant", "zia_url_categories")
	mainPath := filepath.Join(rootDirectory, "main.tf")
	if err := os.MkdirAll(rootDirectory, 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(mainPath, []byte("preexisting root\n"), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	invalidDeployment := loadDeploymentFile(t, deploymentPath)
	invalidRoot := committedRootForTopology(t)
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: invalidDeployment, FormatHcl: identityFormatter, Root: invalidRoot,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	mustMatch(t, err.Error(), `outside the v1 allowlist`)
	if got := readFileString(t, mainPath); got != "preexisting root\n" {
		t.Fatalf("main.tf was rewritten: %q", got)
	}
}

func TestBackendMarkerSurvivesRegenerationAndProfileVariantsGenerateWithoutPython(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-profiles-")
	dep := deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{}}
	output := filepath.Join(workspace, "generated")
	root := committedRootForTopology(t)

	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Backend: strPtr("azurerm"), Deployment: dep, FormatHcl: identityFormatter, OutputRoot: &output, Root: root,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots (azurerm): %v", err)
	}
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: identityFormatter, OutputRoot: &output, Root: root,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots (marker reuse): %v", err)
	}
	mustMatch(t, readFileString(t, filepath.Join(output, "tenant", "zia_url_categories", "main.tf")), `backend "azurerm"`)

	explicitEmpty := filepath.Join(workspace, "empty-backend")
	emptyBackendResult, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Backend: strPtr(""), Deployment: dep, FormatHcl: identityFormatter, OutputRoot: &explicitEmpty, Root: root,
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots (empty backend): %v", err)
	}
	if emptyBackendResult.Backend != nil {
		t.Fatalf("Backend = %v, want nil", *emptyBackendResult.Backend)
	}
	mustNotMatch(t, readFileString(t, filepath.Join(explicitEmpty, "tenant", "zia_url_categories", "main.tf")), `backend "`)

	repo := repoRoot(t)
	cases := []struct {
		profile  string
		selector string
	}{
		{"full.packset.json", "zia_url_categories"},
		{"empty.packset.json", ""},
		{"aws.packset.json", ""},
		{"cloudflare.packset.json", ""},
		{"google.packset.json", ""},
		{"netbox.packset.json", ""},
		{"zia.packset.json", "zia_url_categories"},
		{"zpa.packset.json", "zpa_segment_group"},
		{"zcc.packset.json", "zcc_failopen_policy"},
		{"ztc.packset.json", "ztc_account_groups"},
		{"zscaler.packset.json", "zia_url_categories"},
	}
	for _, testCase := range cases {
		packsRoot := reducedPackRootForProfile(t, repo, workspace, testCase.profile)
		selectedRoot := committedRootFor(t, packsRoot, filepath.Join(repo, "packs", testCase.profile), filepath.Join(repo, "packs", "full.packset.json"))
		target := filepath.Join(workspace, testCase.profile)
		selectors := []string{}
		if testCase.selector != "" {
			selectors = []string{testCase.selector}
		}
		result, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment: deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{}},
			FormatHcl:  identityFormatter, OutputRoot: &target, Root: selectedRoot,
			Selectors: selectors, Tenant: "profile",
		})
		if err != nil {
			t.Fatalf("%s: GenerateEnvironmentRoots: %v", testCase.profile, err)
		}
		wantCount := 1
		if testCase.selector == "" {
			wantCount = 0
		}
		if len(result.Roots) != wantCount {
			t.Fatalf("%s: len(result.Roots) = %d, want %d", testCase.profile, len(result.Roots), wantCount)
		}
	}

	reduced := filepath.Join(workspace, "reduced-packs")
	if err := os.MkdirAll(filepath.Join(reduced, "_shared"), 0o777); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	copyDirRecursive(t, filepath.Join(repo, "packs", "zcc"), filepath.Join(reduced, "zcc"))
	copyDirRecursive(t, filepath.Join(repo, "packs", "_shared", "zscaler"), filepath.Join(reduced, "_shared", "zscaler"))
	reducedRoot := committedRootFor(t, reduced, filepath.Join(repo, "packs", "zcc.packset.json"), filepath.Join(repo, "packs", "full.packset.json"))
	reducedOutput := filepath.Join(workspace, "reduced-output")
	reducedResult, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{}},
		FormatHcl:  identityFormatter, OutputRoot: &reducedOutput, Root: reducedRoot,
		Selectors: []string{"zcc_failopen_policy"}, Tenant: "reduced",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots (reduced): %v", err)
	}
	if len(reducedResult.Roots) != 1 {
		t.Fatalf("len(reducedResult.Roots) = %d, want 1", len(reducedResult.Roots))
	}
}
