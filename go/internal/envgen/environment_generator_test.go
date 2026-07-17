package envgen

// environment_generator_test.go ports node-tests/environment-generator.test.ts.
//
// Every test that does NOT depend on a live Python oracle is ported
// verbatim (same fixtures, same assertions), driven against the real
// committed pack root (packs/ + packsets/full.json, exactly as the Node
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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
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
	executable := os.Getenv("TF")
	if executable == "" {
		executable = "terraform"
	}
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
	data, err := os.ReadFile(filepath.Join(repo, "packsets", profile))
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
	destination := filepath.Join(parent, "packs-"+strings.TrimSuffix(profile, ".json"))
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

func rootLabels(result EnvironmentGenerationResult) []string {
	labels := make([]string, len(result.Roots))
	for i, r := range result.Roots {
		labels[i] = r.Label
	}
	return labels
}

func TestOperatorDataSelectorsDoNotActivateCrossStateWithoutOptIn(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-operator-data-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	outputRoot := filepath.Join(workspace, "generated")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots":      map[string]any{},
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
// ungrouped, grouped/bound, singleton HCL, and slug roots" in
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

	t.Run("grouped", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-grouped-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
			"roots": map[string]any{
				"zpa": map[string]any{
					"bind_references": true,
					"groups":          map[string]any{"zpa_custom": []any{"zpa_application_segment", "zpa_segment_group"}},
				},
			},
		})
		config := filepath.Join(workspace, "config", "tenant")
		writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
			"zpa_application_segment_items": map[string]any{"app": map[string]any{"segment_group_id": "sg-1"}},
		})
		writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
			"zpa_segment_group_items": map[string]any{"group": map[string]any{"description": "Group", "enabled": true, "name": "Group"}},
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
		mustMatch(t, tree["tenant/zpa_custom/expression_bindings.tf"], `operator`)
		mustNotMatch(t, tree["tenant/zpa_custom/expression_bindings.tf"], `generated`)
	})

	t.Run("hcl_singleton", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-hcl-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "tfvars_format": "hcl",
			"roots": map[string]any{"zpa": map[string]any{"groups": map[string]any{"zpa_solo": []any{"zpa_segment_group"}}}},
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
		mustNotMatch(t, tree["tenant/zpa_solo/tests/smoke.tftest.hcl"], `config_plan`)
	})

	t.Run("slug", func(t *testing.T) {
		workspace := temporaryDirectory(t, "infrawright-gen-env-parity-slug-")
		deploymentPath := filepath.Join(workspace, "deployment.json")
		writeJSONFile(t, deploymentPath, map[string]any{
			"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
			"roots": map[string]any{"zia": map[string]any{"strategy": "slug"}},
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
		mustMatch(t, tree["tenant/zia_url/main.tf"], `module "zia_url_filtering_rules"`)
		mustNotMatch(t, tree["tenant/zia_url/main.tf"], `module "zia_url_categories_predefined"`)
	})
}

// TestFullProfileTreeGeneratesAllRoots ports the Go-reachable half of "the
// complete full-profile generated root tree is byte-identical to Python"
// (151 generated roots, 151*3 files); see this file's package doc comment
// for why the Python byte-comparison itself is dropped.
func TestFullProfileTreeGeneratesAllRoots(t *testing.T) {
	if testing.Short() {
		t.Skip("full-profile generation shells out to terraform fmt for every root; skipped under -short")
	}
	root := committedRootForTopology(t)
	workspace := temporaryDirectory(t, "infrawright-gen-env-full-profile-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{}})
	dep := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	result, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: terraformFmtFormatter(t), OutputRoot: &outputRoot, Root: root,
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
}

func TestOperatorPrecedenceStaleFilteringRemovalAndCyclesFailClosed(t *testing.T) {
	workspace := temporaryDirectory(t, "infrawright-gen-env-bindings-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace,
		"roots": map[string]any{
			"zpa": map[string]any{
				"bind_references": true,
				"groups":          map[string]any{"zpa_custom": []any{"zpa_application_segment", "zpa_server_group"}},
			},
		},
	})
	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"zpa_application_segment_items": map[string]any{"app": map[string]any{"description": "literal", "segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app": map[string]any{
				"description":      map[string]any{"expression": `module.zpa_server_group.items["server"].id`},
				"segment_group_id": map[string]any{"expression": `module.zpa_segment_group.items["stale"].id`},
			},
		},
	})
	output := filepath.Join(workspace, "generated")
	root := committedRootForTopology(t)
	var diagnostics []string
	onDiagnostic := func(m string) { diagnostics = append(diagnostics, m) }
	dep := loadDeploymentFile(t, deploymentPath)
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: identityFormatter, OnDiagnostic: onDiagnostic, OutputRoot: &output, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	overlay := readFileString(t, filepath.Join(output, "tenant", "zpa_custom", "expression_bindings.tf"))
	mustMatch(t, overlay, `module\.zpa_server_group`)
	mustNotMatch(t, overlay, `module\.zpa_segment_group`)
	foundStaleNonmember := false
	for _, message := range diagnostics {
		if strings.Contains(message, "target zpa_segment_group not in root members") {
			foundStaleNonmember = true
		}
	}
	if !foundStaleNonmember {
		t.Fatal("expected a stale-nonmember diagnostic")
	}

	deploymentRaw, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var deploymentDoc map[string]any
	if err := json.Unmarshal(deploymentRaw, &deploymentDoc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	zpaConfig := deploymentDoc["roots"].(map[string]any)["zpa"].(map[string]any)
	zpaConfig["bind_references"] = false
	writeJSONFile(t, deploymentPath, deploymentDoc)
	dep = loadDeploymentFile(t, deploymentPath)
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: dep, FormatHcl: identityFormatter, OnDiagnostic: onDiagnostic, OutputRoot: &output, Root: root,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots: %v", err)
	}
	if fileExists(filepath.Join(output, "tenant", "zpa_custom", "expression_bindings.tf")) {
		t.Fatal("expression_bindings.tf should have been removed")
	}
	foundDisabledNote := false
	for _, message := range diagnostics {
		if strings.Contains(message, "bind_references disabled") {
			foundDisabledNote = true
		}
	}
	if !foundDisabledNote {
		t.Fatal("expected a bind_references-disabled diagnostic")
	}

	zpaConfig["bind_references"] = true
	zpaConfig["groups"] = map[string]any{"zpa_cycle": []any{"zpa_application_segment", "zpa_segment_group"}}
	writeJSONFile(t, deploymentPath, deploymentDoc)
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
		"zpa_segment_group_items": map[string]any{"group": map[string]any{"description": "literal"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app": map[string]any{"description": map[string]any{"expression": `module.zpa_segment_group.items["group"].id`}},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_segment_group.group": map[string]any{"description": map[string]any{"expression": `module.zpa_application_segment.items["app"].id`}},
		},
	})
	cycleDeployment := loadDeploymentFile(t, deploymentPath)
	cycleRoot := committedRootForTopology(t)
	_, err = GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: cycleDeployment, FormatHcl: identityFormatter, OutputRoot: &output, Root: cycleRoot,
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	mustMatch(t, err.Error(), `expression binding cycle detected.*resolve one direction`)
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
		"roots":      map[string]any{"zia": map[string]any{"bind_references": true}},
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
		{"full.json", "zia_url_categories"},
		{"empty.json", ""},
		{"aws.json", ""},
		{"cloudflare.json", ""},
		{"google.json", ""},
		{"netbox.json", ""},
		{"zia.json", "zia_url_categories"},
		{"zpa.json", "zpa_segment_group"},
		{"zcc.json", "zcc_failopen_policy"},
		{"ztc.json", "ztc_account_groups"},
		{"zscaler.json", "zia_url_categories"},
	}
	for _, testCase := range cases {
		packsRoot := reducedPackRootForProfile(t, repo, workspace, testCase.profile)
		selectedRoot := committedRootFor(t, packsRoot, filepath.Join(repo, "packsets", testCase.profile), filepath.Join(repo, "packsets", "full.json"))
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
	reducedRoot := committedRootFor(t, reduced, filepath.Join(repo, "packsets", "zcc.json"), filepath.Join(repo, "packsets", "full.json"))
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
