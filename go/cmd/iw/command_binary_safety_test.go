package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func runCompiledSafetyCommand(
	t *testing.T,
	binary string,
	fixture blockC4Fixture,
	arguments []string,
	extraEnvironment ...string,
) runResult {
	t.Helper()
	environment := []string{
		"FAKE_TF_LOG=" + fixture.log,
		"INFRAWRIGHT_DEPLOYMENT=",
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"TMPDIR=" + filepath.Join(fixture.workspace, "tmp"),
	}
	environment = append(environment, extraEnvironment...)
	return runBinaryWithEnv(t, fixture.workspace, binary, arguments, environment)
}

func compiledSafetyMetadataArguments(fixture blockC4Fixture) []string {
	return []string{
		"--root", fixture.packs,
		"--profile", fixture.profile,
		"--deployment", fixture.deployment,
	}
}

func writeCompiledShowTerraform(t *testing.T, fixture blockC4Fixture, showJSON string) {
	t.Helper()
	writeBlockC4File(t, fixture.terraform, []byte("#!/bin/sh\n"+
		"if [ \"$2\" = \"show\" ]; then\n"+
		"  printf '%s' "+assessmentCommandShellLiteral(showJSON)+"\n"+
		"fi\n"+
		"exit 0\n"), 0o700)
}

func createCompiledSavedPlan(t *testing.T, binary string, fixture blockC4Fixture) {
	t.Helper()
	arguments := append([]string{
		"plan", "--tenant", "tenant", "--resource", "sample_resource",
		"--save", "--terraform", fixture.terraform,
	}, compiledSafetyMetadataArguments(fixture)...)
	result := runCompiledSafetyCommand(t, binary, fixture, arguments)
	requireRunResult(t, result, 0, "", "== plan sample_resource\n")
	planPath := filepath.Join(fixture.envDir, "tfplan")
	if got, err := os.ReadFile(planPath); err != nil || string(got) != "opaque plan bytes" {
		t.Errorf("compiled plan artifact %q = %q, %v, want exact opaque plan bytes", planPath, got, err)
	}
	sourcesPath := filepath.Join(fixture.envDir, "tfplan.sources")
	sources, err := os.ReadFile(sourcesPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", sourcesPath, err)
	}
	if !regexp.MustCompile(`^\{"sha256": "[0-9a-f]{64}", "version": 2\}\n$`).Match(sources) {
		t.Errorf("compiled plan sources = %q, want exact v2 fingerprint shape", sources)
	}
}

func TestCompiledSafetyCommandMatrix(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-safety-matrix")

	t.Run("plan and assessments", func(t *testing.T) {
		fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
		createCompiledSavedPlan(t, binary, fixture)
		metadataArguments := compiledSafetyMetadataArguments(fixture)

		noOp := assessmentCommandPlan(map[string]any{
			"actions": []any{"no-op"}, "after": map[string]any{}, "before": map[string]any{},
		})
		writeCompiledShowTerraform(t, fixture, noOp)
		cleanArguments := append([]string{
			"assert-clean", "--tenant", "tenant", "--resource", "sample_resource",
			"--terraform", fixture.terraform,
		}, metadataArguments...)
		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, cleanArguments),
			0, "", "all 1 saved plan(s) clean (no-op/imports only)\n",
		)

		blocked := assessmentCommandPlan(map[string]any{
			"actions": []any{"create"}, "after": map[string]any{"name": "new"}, "before": nil,
		})
		writeCompiledShowTerraform(t, fixture, blocked)
		adoptableArguments := append([]string{
			"assert-adoptable", "--tenant", "tenant", "--resource", "sample_resource",
			"--terraform", fixture.terraform,
		}, metadataArguments...)
		result := runCompiledSafetyCommand(t, binary, fixture, adoptableArguments)
		requireRunResult(t, result, 1, "",
			"BLOCKED: tenant/sample_resource\n"+
				"  sample_resource.this[\"one\"] create blocked\n"+
				"    - <create>\n"+
				"error: 1 saved plan(s) blocked by untolerated changes\n"+
				"  code: PLAN_NOT_ADOPTABLE\n"+
				"  category: domain\n"+
				"  retryable: no\n")
	})

	t.Run("stage imports", func(t *testing.T) {
		fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
		arguments := append([]string{
			"stage-imports", "--tenant", "tenant", "--resource", "sample_resource",
		}, compiledSafetyMetadataArguments(fixture)...)
		missing := runCompiledSafetyCommand(t, binary, fixture, arguments)
		requireRunResult(t, missing, 1, "",
			"error: nothing to stage for TENANT=tenant (run make transform or make adopt first)\n"+
				"  code: NO_IMPORT_ARTIFACTS\n  category: domain\n  retryable: no\n")

		const imports = "import {\n  to = sample_resource.this[\"one\"]\n  id = \"one\"\n}\n"
		source := filepath.Join(fixture.workspace, "imports", "tenant", "sample_resource_imports.tf")
		writeBlockC4File(t, source, []byte(imports), 0o600)
		destination := filepath.Join(fixture.envDir, filepath.Base(source))
		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, arguments),
			0, "", "staged "+destination+"\n",
		)
		if got, err := os.ReadFile(destination); err != nil || string(got) != imports {
			t.Errorf("compiled stage-imports artifact %q = %q, %v, want exact source bytes", destination, got, err)
		}
	})

	t.Run("adopt", func(t *testing.T) {
		fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
		input := filepath.Join(fixture.workspace, "pulls", "tenant")
		if err := os.MkdirAll(input, 0o700); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v, want nil", input, err)
		}
		arguments := append([]string{
			"adopt", "--in", input, "--tenant", "tenant", "--resource", "sample_resource",
		}, compiledSafetyMetadataArguments(fixture)...)
		missingSource := filepath.Join(input, "sample_resource.json")
		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, arguments),
			0, "", "skip sample_resource (no "+missingSource+")\n",
		)

		writeBlockC4File(t, missingSource, []byte("{}\n"), 0o600)
		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, arguments),
			1, "", "error: sample_resource: "+missingSource+" must be a JSON LIST of items\n\n"+
				"adopt FAILED for: sample_resource\n",
		)
	})

	t.Run("apply", func(t *testing.T) {
		fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
		createCompiledSavedPlan(t, binary, fixture)
		noOp := assessmentCommandPlan(map[string]any{
			"actions": []any{"no-op"}, "after": map[string]any{}, "before": map[string]any{},
		})
		writeCompiledShowTerraform(t, fixture, noOp)
		arguments := append([]string{
			"apply", "--tenant", "tenant", "--resource", "sample_resource",
			"--terraform", fixture.terraform,
		}, compiledSafetyMetadataArguments(fixture)...)
		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, arguments,
				"BUILD_SOURCEBRANCH=refs/heads/feature/test"),
			1, "", "error: apply refused from 'feature/test' - only merged main config gets applied (use ALLOW_NON_MAIN=1 for an intentional exception)\n"+
				"  code: APPLY_BRANCH_REFUSED\n  category: domain\n  retryable: no\n",
		)

		requireRunResult(t,
			runCompiledSafetyCommand(t, binary, fixture, arguments,
				"BUILD_SOURCEBRANCH=refs/heads/main"),
			0, "", "== apply tenant/sample_resource\n",
		)
		for _, name := range []string{"tfplan", "tfplan.sources"} {
			path := filepath.Join(fixture.envDir, name)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("os.Stat(%q) error = %v, want os.ErrNotExist after compiled apply", path, err)
			}
		}
	})
}
