package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
					"expression": `data.terraform_remote_state.sample_referent.outputs.iw_reference_ids.sample_referent["referent_one"]`,
				},
			},
		},
	})
	if err := os.MkdirAll(fixture.temporaryDir, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", fixture.temporaryDir, err)
	}
	return fixture
}

// fakeTerraformForProbe writes a terraform stand-in that reports every root
// unapplied: exit 0 with no state document, which is what an initialized but
// never-applied root produces. Passing it via --terraform keeps this test
// hermetic -- it asserts the CLI wiring and the fallback, not that a terraform
// binary is installed on the runner.
func fakeTerraformForProbe(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o777); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}
	return path
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
		result := runStateAwareGenEnvCLI(t, binary, fixture, "--state-aware", "--terraform", fakeTerraformForProbe(t))
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

// TestGenEnvStateAwareBackendDispatch pins the CLI's backend-aware probe
// wiring: local tenants probe files and need no terraform at all, azurerm
// tenants must be given the backend config plan already uses, and anything
// else is refused.
func TestGenEnvStateAwareBackendDispatch(t *testing.T) {
	repositoryRoot := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repositoryRoot, "iw-state-aware-dispatch")

	// A local tenant probes state files, so it must resolve no terraform
	// executable at all. Pointing --terraform at a path that does not exist
	// proves that: resolution would fail loudly, so success can only mean the
	// local branch never attempted it. Passing nothing would prove nothing,
	// since the runner usually does have a terraform on PATH.
	for _, testCase := range []struct {
		name      string
		arguments []string
	}{
		{name: "implicit local", arguments: []string{"--state-aware", "--terraform", "/nonexistent/terraform"}},
		{name: "explicit local", arguments: []string{"--state-aware", "--backend", "local", "--terraform", "/nonexistent/terraform"}},
	} {
		t.Run(testCase.name+" backend needs no terraform", func(t *testing.T) {
			fixture := prepareStateAwareCLIFixture(t)
			result := runStateAwareGenEnvCLI(t, binary, fixture, testCase.arguments...)
			if result.exit != 0 {
				t.Fatalf("gen-env %v exit = %d, want 0 without resolving any terraform; stderr=%q", testCase.arguments, result.exit, result.stderr)
			}
			if !bytes.Contains(result.stderr, []byte("fell back to literal")) {
				t.Errorf("stderr = %q, want the local prober's fallback note", result.stderr)
			}
		})
	}

	t.Run("azurerm requires backend config", func(t *testing.T) {
		fixture := prepareStateAwareCLIFixture(t)
		result := runStateAwareGenEnvCLI(t, binary, fixture,
			"--state-aware", "--backend", "azurerm", "--terraform", fakeTerraformForProbe(t))
		if result.exit == 0 {
			t.Fatalf("gen-env --state-aware --backend azurerm exit = 0, want a loud usage error without --backend-config; stderr=%q", result.stderr)
		}
		if !bytes.Contains(result.stderr, []byte("requires --backend-config")) {
			t.Errorf("stderr = %q, want it to name the missing --backend-config", result.stderr)
		}
	})

	t.Run("azurerm probes through backend config", func(t *testing.T) {
		fixture := prepareStateAwareCLIFixture(t)
		backendConfig := filepath.Join(fixture.workspace, "backend.azurerm.json")
		writeBlockC4JSON(t, backendConfig, map[string]any{
			"container_name": "tfstate", "resource_group_name": "rg",
			"storage_account_name": "sa", "use_azuread_auth": true,
		})
		// The fake terraform answers init with exit 0 and state pull with no
		// document, so the probe reaches the backend and reads every root as
		// unapplied: fallback, loudly reported, exit 0.
		result := runStateAwareGenEnvCLI(t, binary, fixture,
			"--state-aware", "--backend", "azurerm",
			"--backend-config", backendConfig, "--terraform", fakeTerraformForProbe(t))
		if result.exit != 0 {
			t.Fatalf("gen-env --state-aware --backend azurerm exit = %d, want 0; stderr=%q", result.exit, result.stderr)
		}
		if !bytes.Contains(result.stderr, []byte("fell back to literal")) {
			t.Errorf("stderr = %q, want the fallback note for an unapplied referent", result.stderr)
		}
		main := readStateAwareCLIArtifact(t, fixture, "main.tf")
		if bytes.Contains(main, []byte(`data "terraform_remote_state"`)) {
			t.Errorf("main.tf contains a remote-state block for absent state:\n%s", main)
		}
	})

	t.Run("unsupported backend is refused", func(t *testing.T) {
		fixture := prepareStateAwareCLIFixture(t)
		result := runStateAwareGenEnvCLI(t, binary, fixture,
			"--state-aware", "--backend", "s3", "--terraform", fakeTerraformForProbe(t))
		if result.exit == 0 {
			t.Fatalf("gen-env --state-aware --backend s3 exit = 0, want a refusal; stderr=%q", result.stderr)
		}
		if !bytes.Contains(result.stderr, []byte("azurerm")) {
			t.Errorf("stderr = %q, want the refusal to name the supported backends", result.stderr)
		}
	})
}

// recordingFakeTerraform writes a terraform stand-in that appends each
// invocation's argv to logPath and refuses any -backend-config=<file> naming
// a path that does not resolve from its own working directory. The probe runs
// Terraform in a scratch directory unrelated to the caller's, so a relative
// --backend-config that was never resolved fails here exactly as real
// terraform would. `state pull` answers with no document, so every referent
// reads unapplied and generation falls back.
func recordingFakeTerraform(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + logPath + "'\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    -backend-config=key=*) ;;\n" +
		"    -backend-config=*)\n" +
		"      file=${arg#-backend-config=}\n" +
		"      if [ ! -f \"$file\" ]; then\n" +
		"        echo \"fake terraform: backend config not found from $(pwd): $file\" >&2\n" +
		"        exit 1\n" +
		"      fi\n" +
		"      ;;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o777); err != nil {
		t.Fatalf("write recording fake terraform: %v", err)
	}
	return path
}

// TestGenEnvStateAwareResolvesRelativeBackendConfig pins the path contract the
// Makefile and every documented example actually use: BACKEND_CONFIG is
// relative to where the operator invoked iw, while the probe runs Terraform in
// a scratch directory. Unless gen-env resolves the path the way iw plan and
// stage-imports already do, terraform init cannot find the file and
// state-aware generation fails for every realistic invocation.
//
// Asserting on the recorded argv (not merely the exit code) is what makes this
// test observe the probe at all: a wiring regression that skipped Terraform
// entirely would still exit 0 and still emit the fallback note.
func TestGenEnvStateAwareResolvesRelativeBackendConfig(t *testing.T) {
	repositoryRoot := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repositoryRoot, "iw-state-aware-relative")
	fixture := prepareStateAwareCLIFixture(t)

	absoluteBackendConfig := filepath.Join(fixture.workspace, "cfg", "backend.azurerm.json")
	writeBlockC4JSON(t, absoluteBackendConfig, map[string]any{
		"container_name": "tfstate", "resource_group_name": "rg",
		"storage_account_name": "sa", "use_azuread_auth": true,
	})
	logPath := filepath.Join(t.TempDir(), "argv.log")

	result := runStateAwareGenEnvCLI(t, binary, fixture,
		"--state-aware", "--backend", "azurerm",
		"--backend-config", filepath.Join("cfg", "backend.azurerm.json"),
		"--terraform", recordingFakeTerraform(t, logPath))
	if result.exit != 0 {
		t.Fatalf("gen-env --state-aware with a relative --backend-config exit = %d, want 0; stderr=%q", result.exit, result.stderr)
	}

	recorded, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("os.ReadFile(argv log) error = %v, want nil: the probe never invoked terraform", err)
	}
	argv := string(recorded)
	// The child resolves the relative path against its own working directory,
	// which the OS reports physically (on macOS /var is a symlink to
	// /private/var), so the expectation is compared physically too.
	physicalBackendConfig, err := filepath.EvalSymlinks(absoluteBackendConfig)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(%q) error = %v, want nil", absoluteBackendConfig, err)
	}
	wantInit := "init -input=false -reconfigure -backend-config=" + physicalBackendConfig +
		" -backend-config=key=tenant/sample_referent.tfstate"
	if !strings.Contains(argv, wantInit) {
		t.Errorf("argv log = %q, want an init resolving the backend config to %q", argv, absoluteBackendConfig)
	}
	if !strings.Contains(argv, "state pull") {
		t.Errorf("argv log = %q, want a state pull", argv)
	}
}
