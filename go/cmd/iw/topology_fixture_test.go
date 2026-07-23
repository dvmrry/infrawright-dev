package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type blockC4Fixture struct {
	workspace  string
	packs      string
	profile    string
	deployment string
	terraform  string
	log        string
	envDir     string
	varFile    string
}

func writeBlockC4File(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", path, err)
	}
}

func writeBlockC4JSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%q) error: %v", path, err)
	}
	writeBlockC4File(t, path, append(content, '\n'), 0o600)
}

func prepareBlockC4Fixture(t *testing.T, workspace string) blockC4Fixture {
	t.Helper()
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("os.RemoveAll(%q) error: %v", workspace, err)
	}
	fixture := blockC4Fixture{
		workspace:  workspace,
		packs:      filepath.Join(workspace, "packs"),
		profile:    filepath.Join(workspace, "packs", "full.packset.json"),
		deployment: filepath.Join(workspace, "deployment.json"),
		terraform:  filepath.Join(workspace, "terraform-fake"),
		log:        filepath.Join(workspace, "terraform.log"),
		envDir:     filepath.Join(workspace, "envs", "tenant", "sample_resource"),
		varFile:    filepath.Join(workspace, "config", "tenant", "sample_resource.auto.tfvars.json"),
	}
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"vendor":            "sample",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, fixture.profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeBlockC4JSON(t, fixture.deployment, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
		"roots": map[string]any{"sample": map[string]any{}},
	})
	moduleDir := filepath.Join(workspace, "modules", "sample_resource")
	writeBlockC4File(t, filepath.Join(moduleDir, "main.tf"), []byte("# fixture module\n"), 0o600)
	relativeModule, err := filepath.Rel(fixture.envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", fixture.envDir, moduleDir, err)
	}
	writeBlockC4File(t, filepath.Join(fixture.envDir, "main.tf"), []byte(strings.Join([]string{
		`module "sample_resource" {`,
		`  source = "` + filepath.ToSlash(relativeModule) + `"`,
		"  items = var.sample_resource_items",
		"}",
		"",
	}, "\n")), 0o600)
	writeBlockC4File(t, fixture.varFile, []byte("{\"sample_resource_items\":{}}\n"), 0o600)
	writeBlockC4File(t, fixture.terraform, []byte(strings.Join([]string{
		"#!/bin/sh",
		`printf 'cwd=%s\n' "$PWD" >> "$FAKE_TF_LOG"`,
		`printf 'arg=%s\n' "$@" >> "$FAKE_TF_LOG"`,
		`if [ "$1" = "plan" ]; then`,
		`  for argument in "$@"; do`,
		`    case "$argument" in -out=*) printf '%s' 'opaque plan bytes' > "${argument#-out=}";; esac`,
		`  done`,
		`elif [ "$1" = "show" ]; then`,
		`  printf '%s' "$FAKE_TF_SHOW_JSON"`,
		`fi`,
		"exit 0",
		"",
	}, "\n")), 0o700)
	if err := os.MkdirAll(filepath.Join(workspace, "tmp"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(TMPDIR) error: %v", err)
	}
	return fixture
}
