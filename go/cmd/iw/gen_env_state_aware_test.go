package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type stateAwareCLIFixture struct {
	workspace    string
	packs        string
	profile      string
	deployment   string
	temporaryDir string
	referrerDir  string
}

func prepareStateAwareCLIFixture(t *testing.T) stateAwareCLIFixture {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	fixture := stateAwareCLIFixture{
		workspace:    workspace,
		packs:        filepath.Join(workspace, "packs"),
		profile:      filepath.Join(workspace, "packs", "full.packset.json"),
		deployment:   filepath.Join(workspace, "deployment.json"),
		temporaryDir: filepath.Join(workspace, "tmp"),
		referrerDir:  filepath.Join(workspace, "envs", "tenant", "sample_referrer"),
	}

	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"references": map[string]any{
			"sample_referrer": map[string]any{
				"referent_id": map[string]any{
					"name_field": "name",
					"referent":   "sample_referent",
				},
			},
		},
		"vendor": "sample",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_referent": map[string]any{"generate": true, "product": "sample"},
		"sample_referrer": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "schemas", "provider", "sample.json"), map[string]any{
		"resource_schemas": map[string]any{
			"sample_referent": map[string]any{
				"block": map[string]any{
					"attributes": map[string]any{
						"id":   map[string]any{"computed": true, "type": "string"},
						"name": map[string]any{"optional": true, "type": "string"},
					},
				},
			},
			"sample_referrer": map[string]any{
				"block": map[string]any{
					"attributes": map[string]any{
						"id":          map[string]any{"computed": true, "type": "string"},
						"name":        map[string]any{"optional": true, "type": "string"},
						"referent_id": map[string]any{"optional": true, "type": "string"},
					},
				},
			},
		},
	})
	writeBlockC4JSON(t, fixture.profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeBlockC4JSON(t, fixture.deployment, map[string]any{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots": map[string]any{
			"sample": map[string]any{"cross_state_references": true},
		},
	})

	config := filepath.Join(workspace, "config", "tenant")
	writeBlockC4JSON(t, filepath.Join(config, "sample_referent.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"referent_one": map[string]any{"name": "Referent One"},
		},
	})
	writeBlockC4JSON(t, filepath.Join(config, "sample_referrer.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"referrer_one": map[string]any{"name": "Referrer One", "referent_id": "literal-id"},
		},
	})
	writeBlockC4JSON(t, filepath.Join(config, "sample_referrer.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"sample_referrer.referrer_one": map[string]any{
				"referent_id": map[string]any{
					"expression": `data.terraform_remote_state.sample_referent.outputs.infrawright_reference_ids.sample_referent["referent_one"]`,
				},
			},
		},
	})
	if err := os.MkdirAll(fixture.temporaryDir, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", fixture.temporaryDir, err)
	}
	return fixture
}

func runStateAwareGenEnvCLI(
	t *testing.T,
	binary string,
	fixture stateAwareCLIFixture,
	extraArguments ...string,
) runResult {
	t.Helper()
	arguments := []string{
		"gen-env",
		"--tenant", "tenant",
		"--root", fixture.packs,
		"--profile", fixture.profile,
		"--deployment", fixture.deployment,
		"--resource", "sample_referrer",
	}
	arguments = append(arguments, extraArguments...)
	return runBinaryWithEnv(t, fixture.workspace, binary, arguments, []string{
		"HOME=" + filepath.Join(fixture.workspace, "home"),
		"INFRAWRIGHT_DEPLOYMENT=",
		"INFRAWRIGHT_PACKAGE_ROOT=" + fixture.workspace,
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"TMPDIR=" + fixture.temporaryDir,
	})
}

func readStateAwareCLIArtifact(t *testing.T, fixture stateAwareCLIFixture, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.referrerDir, name))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v, want nil", name, err)
	}
	return content
}

func TestGenEnvStateAwareCLIControlsFallback(t *testing.T) {
	repositoryRoot := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repositoryRoot, "iw-state-aware-gen-env")

	t.Run("enabled falls back and reports", func(t *testing.T) {
		fixture := prepareStateAwareCLIFixture(t)
		result := runStateAwareGenEnvCLI(t, binary, fixture, "--state-aware")
		if result.exit != 0 {
			t.Fatalf("gen-env --state-aware exit = %d, want 0; stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
		}
		if !bytes.Contains(result.stderr, []byte("fell back to literal")) ||
			!bytes.Contains(result.stderr, []byte("sample_referent")) {
			t.Errorf("gen-env --state-aware stderr = %q, want fallback note naming sample_referent", result.stderr)
		}
		main := readStateAwareCLIArtifact(t, fixture, "main.tf")
		if bytes.Contains(main, []byte(`data "terraform_remote_state"`)) {
			t.Errorf("gen-env --state-aware main.tf contains a remote-state block for absent state:\n%s", main)
		}
		if _, err := os.Stat(filepath.Join(fixture.referrerDir, "expression_bindings.tf")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("gen-env --state-aware expression_bindings.tf stat error = %v, want os.ErrNotExist", err)
		}
	})

	for _, testCase := range []struct {
		name      string
		arguments []string
	}{
		{name: "omitted keeps binding"},
		{name: "explicit false keeps binding", arguments: []string{"--state-aware=false"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := prepareStateAwareCLIFixture(t)
			result := runStateAwareGenEnvCLI(t, binary, fixture, testCase.arguments...)
			if result.exit != 0 {
				t.Fatalf("gen-env %v exit = %d, want 0; stdout=%q stderr=%q", testCase.arguments, result.exit, result.stdout, result.stderr)
			}
			main := readStateAwareCLIArtifact(t, fixture, "main.tf")
			if !bytes.Contains(main, []byte(`data "terraform_remote_state"`)) {
				t.Errorf("gen-env %v main.tf has no remote-state block, want the generated binding retained:\n%s", testCase.arguments, main)
			}
			bindings := readStateAwareCLIArtifact(t, fixture, "expression_bindings.tf")
			if !bytes.Contains(bindings, []byte(`data.terraform_remote_state.sample_referent`)) {
				t.Errorf("gen-env %v expression_bindings.tf does not contain the generated binding:\n%s", testCase.arguments, bindings)
			}
		})
	}

}
