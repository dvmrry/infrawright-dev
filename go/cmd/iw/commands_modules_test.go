package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDataOnlyModuleValidationFixture(t *testing.T) (packsRoot, profilePath, moduleRoot, deploymentPath string) {
	t.Helper()
	workspace := t.TempDir()
	packsRoot = filepath.Join(workspace, "packs")
	packRoot := filepath.Join(packsRoot, "zia")
	moduleRoot = filepath.Join(workspace, "modules")
	if err := os.MkdirAll(filepath.Join(packRoot, "schemas", "provider"), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", packRoot, err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	write(filepath.Join(packRoot, "pack.json"), `{
  "lookup_sources": {
    "zia_location_groups": {"name_field": "name"}
  },
  "pin": "1.0.0",
  "provider_prefixes": {"zia_": "zia"},
  "provider_sources": {"zia": "zscaler/zia"}
}
`)
	write(filepath.Join(packRoot, "registry.json"), `{
  "zia_location_groups": {
    "data_referent": true,
    "fetch": {"pagination": "zia", "path": "locations/groups"},
    "product": "zia"
  }
}
`)
	write(filepath.Join(packRoot, "schemas", "provider", "zia.json"), `{
  "data_source_schemas": {
    "zia_location_groups": {
      "block": {
        "attributes": {
          "id": {"computed": true, "type": "string"},
          "name": {"optional": true, "type": "string"}
        }
      }
    }
  },
  "resource_schemas": {}
}
`)
	profilePath = filepath.Join(workspace, "data-only.packset.json")
	write(profilePath, `{
  "kind": "infrawright.pack-set",
  "version": 1,
  "packs": ["zia"],
  "shared": []
}
`)
	deploymentPath = filepath.Join(workspace, "deployment.json")
	write(deploymentPath, "{\n  \"module_dir\": "+quoteJSON(moduleRoot)+",\n  \"overlay\": "+quoteJSON(workspace)+"\n}\n")

	for _, relative := range []string{
		"variables.tf", "outputs.tf", "versions.tf", "README.md",
		"tests/defaults.tftest.hcl", "tests/sample.auto.tfvars.json",
	} {
		write(filepath.Join(moduleRoot, "zia_location_groups", relative), "fixture\n")
	}
	return packsRoot, profilePath, moduleRoot, deploymentPath
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func TestModulesValidateDefaultSelectionIncludesDataReferentModules(t *testing.T) {
	packsRoot, profilePath, moduleRoot, deploymentPath := writeDataOnlyModuleValidationFixture(t)
	t.Setenv("INFRAWRIGHT_PACKAGE_ROOT", t.TempDir())

	status, err := run([]string{
		"modules", "validate",
		"--root", packsRoot,
		"--profile", profilePath,
		"--out", moduleRoot,
		"--deployment", deploymentPath,
	})
	if err == nil {
		t.Fatalf("run(modules validate data-only pack) = (%d, nil), want missing %s/main.tf error", status, "zia_location_groups")
	}
	missingPath := filepath.Join("zia_location_groups", "main.tf")
	if !strings.Contains(err.Error(), missingPath) {
		t.Errorf("run(modules validate data-only pack) error = %q, want missing path %q", err, missingPath)
	}
}
