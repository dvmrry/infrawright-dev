package providerprobe

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLegacyReusableWorkspaceRejectsStaticAliasesWithoutOutsideMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	cases := []struct {
		name      string
		prepare   func(t *testing.T, work, outside string)
		wantError string
	}{
		{
			name: "inputs directory symlink",
			prepare: func(t *testing.T, work, outside string) {
				symlinkWorkspaceTest(t, outside, filepath.Join(work, "inputs"))
			},
			wantError: `alias "inputs"`,
		},
		{
			name: "inputs regular file",
			prepare: func(t *testing.T, work, _ string) {
				if err := os.WriteFile(filepath.Join(work, "inputs"), []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantError: `alias "inputs"`,
		},
		{
			name: "terraform directory symlink",
			prepare: func(t *testing.T, work, outside string) {
				symlinkWorkspaceTest(t, outside, filepath.Join(work, "terraform-schema"))
			},
			wantError: `alias "terraform-schema"`,
		},
		{
			name: "schema output symlink",
			prepare: func(t *testing.T, work, outside string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "inputs"))
				symlinkWorkspaceTest(t, filepath.Join(outside, "sentinel"), filepath.Join(work, "inputs", "provider-schema.json"))
			},
			wantError: `alias "inputs/provider-schema.json"`,
		},
		{
			name: "schema output directory",
			prepare: func(t *testing.T, work, _ string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "inputs", "provider-schema.json"))
			},
			wantError: `alias "inputs/provider-schema.json"`,
		},
		{
			name: "OpenAPI output symlink",
			prepare: func(t *testing.T, work, outside string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "inputs"))
				symlinkWorkspaceTest(t, filepath.Join(outside, "sentinel"), filepath.Join(work, "inputs", "openapi.json"))
			},
			wantError: `alias "inputs/openapi.json"`,
		},
		{
			name: "download output symlink",
			prepare: func(t *testing.T, work, outside string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "inputs"))
				symlinkWorkspaceTest(t, filepath.Join(outside, "sentinel"), filepath.Join(work, "inputs", "openapi.raw"))
			},
			wantError: `alias "inputs/openapi.raw"`,
		},
		{
			name: "Terraform main symlink",
			prepare: func(t *testing.T, work, outside string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "terraform-schema"))
				symlinkWorkspaceTest(t, filepath.Join(outside, "sentinel"), filepath.Join(work, "terraform-schema", "main.tf"))
			},
			wantError: `alias "terraform-schema/main.tf"`,
		},
		{
			name: "eventual public output symlink",
			prepare: func(t *testing.T, work, outside string) {
				mkdirWorkspaceTest(t, filepath.Join(work, "artifacts"))
				symlinkWorkspaceTest(t, filepath.Join(outside, "sentinel"), filepath.Join(work, "artifacts", "summary.json"))
			},
			wantError: `alias "artifacts/summary.json"`,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			work := filepath.Join(base, "work")
			if _, err := legacyWorkDirectory(loadedRecipe{}, work); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside")
			mkdirWorkspaceTest(t, outside)
			if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside remains untouched"), 0o600); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, work, outside)

			if _, err := legacyWorkDirectory(loadedRecipe{}, work); err == nil || !containsWorkspaceError(err, test.wantError) {
				t.Fatalf("legacyWorkDirectory() error = %v, want %q", err, test.wantError)
			}
			if got, err := os.ReadFile(filepath.Join(outside, "sentinel")); err != nil || string(got) != "outside remains untouched" {
				t.Fatalf("outside sentinel = %q, %v; workspace validation followed or wrote through alias", got, err)
			}
		})
	}
}

func TestDefaultLegacyHostBoundDownloadReplacesFinalSymlinkWithoutFollowingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	base := t.TempDir()
	work := filepath.Join(base, "work")
	if _, err := legacyWorkDirectory(loadedRecipe{}, work); err != nil {
		t.Fatal(err)
	}
	binding, inputs, _, inputsPath, err := legacyInputsRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	defer inputs.Close()
	outside := filepath.Join(base, "outside")
	mkdirWorkspaceTest(t, outside)
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside remains untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(inputsPath, "openapi.raw")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "openapi.json")
	if err := os.WriteFile(source, []byte(`{"openapi":"3.0.3","paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newDefaultLegacyHost(nil)
	fileURL := (&url.URL{Scheme: "file", Path: source}).String()
	if err := host.Download(context.Background(), DownloadRequest{URL: fileURL, Destination: filepath.Join(inputsPath, "openapi.raw"), legacyDestinationRoot: inputs, legacyDestinationName: "openapi.raw"}); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "outside remains untouched" {
		t.Fatalf("outside sentinel = %q, %v; download followed final symlink", got, err)
	}
	if got, err := legacyReadRegular(inputs, "openapi.raw"); err != nil || !bytes.Equal(got, []byte(`{"openapi":"3.0.3","paths":{}}`)) {
		t.Fatalf("bound raw output = %q, %v", got, err)
	}
}

func TestDefaultLegacyHostTerraformReplacesMainSymlinkWithoutFollowingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is unsupported on Windows")
	}
	base := t.TempDir()
	work := filepath.Join(base, "work")
	if _, err := legacyWorkDirectory(loadedRecipe{}, work); err != nil {
		t.Fatal(err)
	}
	binding, err := bindLegacyWorkspace(work)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	directory, info, err := binding.directory("terraform-schema")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	outside := filepath.Join(base, "outside")
	mkdirWorkspaceTest(t, outside)
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside remains untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(work, "terraform-schema", "main.tf")); err != nil {
		t.Fatal(err)
	}
	writeHostExecutable(t, base, "terraform", `
if [ "$1" = init ]; then exit 0; fi
printf '%s' '{"provider_schemas":{}}'
`)
	host := newDefaultLegacyHost(nil)
	request := TerraformSchemaRequest{
		TerraformExecutable: "terraform",
		Directory:           filepath.Join(work, "terraform-schema"),
		MainHCL:             []byte("terraform {}\n"),
		Environment:         map[string]string{"PATH": base},
		legacyWorkspace:     binding,
		legacyDirectory:     "terraform-schema",
		legacyDirectoryInfo: info,
		legacyDirectoryRoot: directory,
	}
	if _, err := host.CaptureTerraformSchema(context.Background(), request); err != nil {
		t.Fatalf("CaptureTerraformSchema() error = %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "outside remains untouched" {
		t.Fatalf("outside sentinel = %q, %v; Terraform write followed main.tf symlink", got, err)
	}
	if got, err := legacyReadRegular(directory, "main.tf"); err != nil || string(got) != "terraform {}\n" {
		t.Fatalf("bound main.tf = %q, %v", got, err)
	}
}

func mkdirWorkspaceTest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func symlinkWorkspaceTest(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func containsWorkspaceError(err error, want string) bool {
	return err != nil && len(want) > 0 && bytes.Contains([]byte(err.Error()), []byte(want))
}
