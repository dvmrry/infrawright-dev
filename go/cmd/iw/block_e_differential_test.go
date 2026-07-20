package main

import (
	"path/filepath"
	"testing"
)

type blockEFixture struct {
	workspace    string
	packs        string
	invalidPacks string
	profile      string
	catalog      string
	requirements string
	missing      string
	deployment   string
}

func prepareBlockEFixture(t *testing.T) blockEFixture {
	t.Helper()
	workspace := t.TempDir()
	fixture := blockEFixture{
		workspace:    workspace,
		packs:        filepath.Join(workspace, "packs"),
		invalidPacks: filepath.Join(workspace, "invalid-packs"),
		profile:      filepath.Join(workspace, "profile.json"),
		catalog:      filepath.Join(workspace, "catalog.json"),
		requirements: filepath.Join(workspace, "requirements.json"),
		missing:      filepath.Join(workspace, "requirements-missing.json"),
		deployment:   filepath.Join(workspace, "deployment.json"),
	}
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"vendor":            "example",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.invalidPacks, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"vendor":            "example",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.invalidPacks, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.invalidPacks, "sample", "overrides", "sample_resource.json"), map[string]any{
		"rename": map[string]any{"old": "new"},
	})
	writeBlockC4JSON(t, fixture.profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeBlockC4JSON(t, fixture.catalog, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"missing", "sample"}, "shared": []any{"missing_shared"},
	})
	writeBlockC4JSON(t, fixture.requirements, map[string]any{
		"kind": "infrawright.pack-requirements", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeBlockC4JSON(t, fixture.missing, map[string]any{
		"kind": "infrawright.pack-requirements", "version": 1,
		"packs": []any{"missing"}, "shared": []any{"missing_shared"},
	})
	writeBlockC4JSON(t, fixture.deployment, map[string]any{
		"overlay": "estate", "tfvars_format": "hcl",
	})
	return fixture
}

func runBlockESide(
	t *testing.T,
	runtime blockC4Runtime,
	fixture blockEFixture,
	oracle bool,
	arguments []string,
	environment []string,
) runResult {
	t.Helper()
	argv0 := runtime.candidate
	argv := append([]string(nil), arguments...)
	if oracle {
		argv0 = runtime.node
		argv = append([]string{runtime.oracleBundle}, argv...)
	}
	return runBinaryWithEnv(t, fixture.workspace, argv0, argv, environment)
}

func TestBlockEMetadataCommandsDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	fixture := prepareBlockEFixture(t)
	cleanEnvironment := []string{
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"INFRAWRIGHT_DEPLOYMENT=",
	}
	tests := []struct {
		name        string
		arguments   []string
		environment []string
	}{
		{
			name: "check-pack explicit root", arguments: []string{
				"check-pack", "--pack", "sample", "--root", fixture.packs,
			}, environment: cleanEnvironment,
		},
		{
			name: "check-pack resource validation failure", arguments: []string{
				"check-pack", "--pack", "sample", "--root", fixture.invalidPacks,
			}, environment: cleanEnvironment,
		},
		{name: "check-pack empty positional", arguments: []string{"check-pack", "PACK="}, environment: cleanEnvironment},
		{
			name: "check-pack nonempty environment root", arguments: []string{"check-pack", "--pack", "sample"},
			environment: []string{"INFRAWRIGHT_PACKS=" + fixture.packs, "INFRAWRIGHT_PACK_PROFILE=", "INFRAWRIGHT_DEPLOYMENT="},
		},
		{
			name: "check-pack empty environment falls back", arguments: []string{"check-pack", "--pack", "zia"},
			environment: cleanEnvironment,
		},
		{
			name: "check-pack-set explicit", arguments: []string{
				"check-pack-set", "--root", fixture.packs, "--profile", fixture.profile, "--catalog", fixture.catalog,
			}, environment: cleanEnvironment,
		},
		{
			name: "check-pack-set environment defaults", arguments: []string{"check-pack-set", "--catalog", fixture.catalog},
			environment: []string{
				"INFRAWRIGHT_PACKS=" + fixture.packs,
				"INFRAWRIGHT_PACK_PROFILE=" + fixture.profile,
				"INFRAWRIGHT_DEPLOYMENT=",
			},
		},
		{name: "check-pack-set empty environment falls back", arguments: []string{"check-pack-set"}, environment: cleanEnvironment},
		{
			name: "check-pack-set requirements satisfied", arguments: []string{
				"check-pack-set", "--root", fixture.packs, "--catalog", fixture.catalog,
				"--requirements", fixture.requirements,
			}, environment: cleanEnvironment,
		},
		{
			name: "check-pack-set requirements unavailable exit three", arguments: []string{
				"check-pack-set", "--root", fixture.packs, "--catalog", fixture.catalog,
				"--requirements", fixture.missing,
			}, environment: cleanEnvironment,
		},
		{name: "deployment overlay", arguments: []string{"deployment", "--deployment", fixture.deployment, "overlay"}, environment: cleanEnvironment},
		{name: "deployment tfvars format", arguments: []string{"deployment", "--deployment", fixture.deployment, "tfvars-format"}, environment: cleanEnvironment},
		{name: "deployment module dir", arguments: []string{"deployment", "--deployment", fixture.deployment, "module-dir"}, environment: cleanEnvironment},
		{name: "deployment tenant root", arguments: []string{"deployment", "--deployment", fixture.deployment, "tenant-root", "tenant-a"}, environment: cleanEnvironment},
		{name: "deployment config dir", arguments: []string{"deployment", "--deployment", fixture.deployment, "config-dir", "tenant-a"}, environment: cleanEnvironment},
		{name: "deployment imports dir", arguments: []string{"deployment", "--deployment", fixture.deployment, "imports-dir", "tenant-a"}, environment: cleanEnvironment},
		{name: "deployment envs dir", arguments: []string{"deployment", "--deployment", fixture.deployment, "envs-dir", "tenant-a"}, environment: cleanEnvironment},
		{
			name: "deployment nonempty environment path", arguments: []string{"deployment", "overlay"},
			environment: []string{"INFRAWRIGHT_PACKS=", "INFRAWRIGHT_PACK_PROFILE=", "INFRAWRIGHT_DEPLOYMENT=" + fixture.deployment},
		},
		{name: "deployment empty environment cwd fallback", arguments: []string{"deployment", "overlay"}, environment: cleanEnvironment},
		{name: "deployment missing verb", arguments: []string{"deployment"}, environment: cleanEnvironment},
		{name: "deployment missing tenant", arguments: []string{"deployment", "--deployment", fixture.deployment, "config-dir"}, environment: cleanEnvironment},
		{name: "deployment unknown quoted verb", arguments: []string{"deployment", "--deployment", fixture.deployment, "bad<&"}, environment: cleanEnvironment},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oracle := runBlockESide(t, runtime, fixture, true, test.arguments, test.environment)
			candidate := runBlockESide(t, runtime, fixture, false, test.arguments, test.environment)
			compareBlockC4RunResult(t, test.name, oracle, candidate)
		})
	}
}
