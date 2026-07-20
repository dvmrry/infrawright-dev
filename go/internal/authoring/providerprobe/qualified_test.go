package providerprobe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceoperation"
)

func TestQualifiedRootsResolveRecipeRelativePathsAndCopySDKRoots(t *testing.T) {
	openAPI := "openapi"
	recipe := loadedRecipe{
		directory: t.TempDir(),
		provenance: &sourceProvenance{
			manifest:     "manifest.json",
			providerRoot: "provider",
			schemaRoot:   "schema",
			openAPIRoot:  &openAPI,
			sdkRoots: map[string]string{
				"example.invalid/sdk": "sdk",
			},
		},
	}
	roots, err := qualifiedRoots(recipe)
	if err != nil {
		t.Fatalf("qualifiedRoots() error = %v", err)
	}
	for label, got := range map[string]string{
		"manifest": roots.ManifestPath,
		"provider": roots.ProviderRoot,
		"schema":   roots.SchemaRoot,
		"openapi":  roots.OpenAPIRoot,
	} {
		want := filepath.Join(recipe.directory, map[string]string{"manifest": "manifest.json", "provider": "provider", "schema": "schema", "openapi": "openapi"}[label])
		if got != want {
			t.Errorf("%s root = %q, want %q", label, got, want)
		}
	}
	if got, want := roots.SDKRoots["example.invalid/sdk"], filepath.Join(recipe.directory, "sdk"); got != want {
		t.Errorf("SDK root = %q, want %q", got, want)
	}
	recipe.provenance.sdkRoots["example.invalid/sdk"] = "mutated"
	if got, want := roots.SDKRoots["example.invalid/sdk"], filepath.Join(recipe.directory, "sdk"); got != want {
		t.Errorf("SDK roots were not detached: got %q, want %q", got, want)
	}
}

func TestQualifiedRootsRequireCompleteLocalProvenance(t *testing.T) {
	base := sourceProvenance{manifest: "manifest", providerRoot: "provider", schemaRoot: "schema", sdkRoots: map[string]string{"example.invalid/sdk": "sdk"}}
	for name, mutate := range map[string]func(*sourceProvenance){
		"manifest": func(p *sourceProvenance) { p.manifest = "" },
		"provider": func(p *sourceProvenance) { p.providerRoot = "" },
		"schema":   func(p *sourceProvenance) { p.schemaRoot = "" },
		"sdk key":  func(p *sourceProvenance) { p.sdkRoots = map[string]string{"": "sdk"} },
		"sdk path": func(p *sourceProvenance) { p.sdkRoots = map[string]string{"example.invalid/sdk": ""} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.sdkRoots = map[string]string{"example.invalid/sdk": "sdk"}
			mutate(&candidate)
			if _, err := qualifiedRoots(loadedRecipe{directory: t.TempDir(), provenance: &candidate}); err == nil {
				t.Fatal("qualifiedRoots() error = nil, want rejection")
			}
		})
	}
}

func TestQualifiedRootsAllowOmittedSDKRoots(t *testing.T) {
	roots, err := qualifiedRoots(loadedRecipe{
		directory: t.TempDir(),
		provenance: &sourceProvenance{
			manifest: "manifest", providerRoot: "provider", schemaRoot: "schema",
		},
	})
	if err != nil {
		t.Fatalf("qualifiedRoots() error = %v, want empty SDK root support", err)
	}
	if roots.SDKRoots == nil || len(roots.SDKRoots) != 0 {
		t.Fatalf("qualifiedRoots().SDKRoots = %#v, want detached empty map", roots.SDKRoots)
	}
}

func TestQualifiedRootsAcceptAbsoluteLocalPaths(t *testing.T) {
	root := t.TempDir()
	recipe := loadedRecipe{
		directory: filepath.Join(root, "recipe-directory"),
		provenance: &sourceProvenance{
			manifest:     filepath.Join(root, "manifest.json"),
			providerRoot: filepath.Join(root, "provider"),
			schemaRoot:   filepath.Join(root, "schema"),
			sdkRoots:     map[string]string{"example.invalid/sdk": filepath.Join(root, "sdk")},
		},
	}
	roots, err := qualifiedRoots(recipe)
	if err != nil {
		t.Fatalf("qualifiedRoots() error = %v", err)
	}
	if roots.ManifestPath != recipe.provenance.manifest || roots.ProviderRoot != recipe.provenance.providerRoot || roots.SchemaRoot != recipe.provenance.schemaRoot || roots.SDKRoots["example.invalid/sdk"] != recipe.provenance.sdkRoots["example.invalid/sdk"] {
		t.Fatalf("qualifiedRoots() changed absolute paths: %#v", roots)
	}
}

func TestQualifiedRejectsEveryPresentLegacyAlias(t *testing.T) {
	value := ""
	for name, set := range map[string]func(*loadedRecipe){
		"openapi.path":                  func(r *loadedRecipe) { r.openAPI.path = &value },
		"openapi.url":                   func(r *loadedRecipe) { r.openAPI.url = &value },
		"openapi.format":                func(r *loadedRecipe) { r.openAPI.format = &value },
		"source.path":                   func(r *loadedRecipe) { r.source.path = &value },
		"source.git":                    func(r *loadedRecipe) { r.source.git = &value },
		"source.ref":                    func(r *loadedRecipe) { r.source.ref = &value },
		"source.subdir":                 func(r *loadedRecipe) { r.source.subdir = &value },
		"terraform_schema.path":         func(r *loadedRecipe) { r.schema.path = &value },
		"terraform_provider.source":     func(r *loadedRecipe) { r.terraform.source = &value },
		"terraform_provider.version":    func(r *loadedRecipe) { r.terraform.version = &value },
		"terraform_provider.local_name": func(r *loadedRecipe) { r.terraform.localName = &value },
		"tools.terraform":               func(r *loadedRecipe) { r.tools.terraform = &value },
	} {
		t.Run(name, func(t *testing.T) {
			recipe := loadedRecipe{mode: QualifiedV2, provenance: &sourceProvenance{}}
			set(&recipe)
			err := rejectQualifiedLegacyFields(recipe)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("rejectQualifiedLegacyFields() error = %v, want field %q", err, name)
			}
		})
	}
}

func TestRunQualifiedRejectsLegacyAliasBeforeRootsOrHost(t *testing.T) {
	root := t.TempDir()
	recipePath := filepath.Join(root, "recipe.json")
	writeQualifiedRecipe(t, recipePath, map[string]any{
		"openapi": map[string]any{"url": ""},
		"source_provenance": map[string]any{
			"manifest": "missing-manifest.json", "provider_root": "missing-provider", "schema_root": "missing-schema",
			"sdk_roots": map[string]any{"example.invalid/sdk": "missing-sdk"},
		},
	})
	if _, err := Run(context.Background(), RunOptions{RecipePath: recipePath, LegacyHost: panicLegacyHost{t: t}}); err == nil || !strings.Contains(err.Error(), "openapi.url") {
		t.Fatalf("Run(qualified legacy alias) error = %v, want openapi.url rejection", err)
	}
}

func TestRunQualifiedRejectsEmptyNullAndUnknownLegacySectionsBeforeRootsOrHost(t *testing.T) {
	for name, section := range map[string]any{
		"empty":   map[string]any{},
		"null":    nil,
		"unknown": map[string]any{"future_control": true},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			recipePath := filepath.Join(root, "recipe.json")
			writeQualifiedRecipe(t, recipePath, map[string]any{
				"source": section,
				"source_provenance": map[string]any{
					"manifest": "missing-manifest.json", "provider_root": "missing-provider", "schema_root": "missing-schema",
				},
			})
			if _, err := Run(context.Background(), RunOptions{RecipePath: recipePath, LegacyHost: panicLegacyHost{t: t}}); err == nil || !strings.Contains(err.Error(), "legacy section source") {
				t.Fatalf("Run(qualified %s legacy section) error = %v, want source-section rejection", name, err)
			}
		})
	}
}

func TestRunRejectsNilAndCancelledContextBeforeRecipeLoad(t *testing.T) {
	if _, err := Run(nil, RunOptions{RecipePath: "does-not-exist"}); err == nil {
		t.Fatal("Run(nil) error = nil, want rejection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, RunOptions{RecipePath: "does-not-exist"}); err == nil {
		t.Fatal("Run(cancelled) error = nil, want rejection")
	}
}

func TestRunQualifiedSourceOnlyMatchesSealedComposition(t *testing.T) {
	recipePath, roots := materializeQualifiedSourceOnlyFixture(t)
	ctx := context.Background()
	verified, err := sourcebind.LoadVerified(ctx, roots)
	if err != nil {
		t.Fatalf("LoadVerified() error = %v", err)
	}
	inputs, err := sourcebind.RequireQualification(verified)
	if err != nil {
		t.Fatalf("RequireQualification() error = %v", err)
	}
	evidence, err := sourceanalysis.Analyze(ctx, inputs)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	direct, err := sourceoperation.CompileQualified(ctx, evidence, inputs)
	if err != nil {
		t.Fatalf("CompileQualified() error = %v", err)
	}

	result, err := Run(ctx, RunOptions{RecipePath: recipePath, LegacyHost: panicLegacyHost{t: t}})
	if err != nil {
		t.Fatalf("Run(qualified source-only fixture) error = %v", err)
	}
	if result.Mode() != QualifiedV2 {
		t.Errorf("Run().Mode() = %q, want %q", result.Mode(), QualifiedV2)
	}
	if result.WorkDirectory() != "" {
		t.Errorf("Run().WorkDirectory() = %q, want empty", result.WorkDirectory())
	}
	want := direct.Artifacts()
	got := result.Artifacts()
	if len(got) != 6 || len(got) != len(want) {
		t.Fatalf("Run().Artifacts() count = %d, want six direct artifacts", len(got))
	}
	for i := range want {
		if got[i].Name != want[i].Name || !bytes.Equal(got[i].Bytes, want[i].Bytes) {
			t.Fatalf("artifact %d = %q (%d bytes), want %q (%d direct bytes)", i, got[i].Name, len(got[i].Bytes), want[i].Name, len(want[i].Bytes))
		}
	}
	got[0].Bytes[0] ^= 0xff
	again := result.Artifacts()
	if !bytes.Equal(again[0].Bytes, want[0].Bytes) {
		t.Fatal("mutating returned artifact bytes changed Result.Artifacts()")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recipePath), "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("qualified Run created artifacts directory: %v", err)
	}
}

func TestRunQualifiedUsableOpenAPIAppendsOnlyDiagnosticMap(t *testing.T) {
	recipePath, roots := materializeQualifiedSourceOnlyFixture(t)
	addQualifiedOpenAPI(t, filepath.Dir(recipePath), []byte(`{"openapi":"3.0.3","info":{"title":"source-first","version":"1"},"paths":{}}`))
	writeQualifiedRecipe(t, recipePath, map[string]any{
		"provider_source": "registry.terraform.io/fixture/sourcefirst", "resource_prefix": "sourcefirst", "api_prefix": "/api/",
		"source_provenance": map[string]any{
			"manifest": "source-provenance-v1.json", "provider_root": "provider", "schema_root": ".", "openapi_root": ".",
			"sdk_roots": map[string]any{"example.invalid/sourcefirst-sdk": "sdk"},
		},
	})
	result, err := Run(context.Background(), RunOptions{RecipePath: recipePath, LegacyHost: panicLegacyHost{t: t}})
	if err != nil {
		t.Fatalf("Run(qualified usable OpenAPI fixture) error = %v", err)
	}
	got := result.Artifacts()
	if len(got) != 7 || got[6].Name != openAPIMapArtifactName || len(got[6].Bytes) == 0 {
		t.Fatalf("qualified usable OpenAPI artifacts = %#v, want six core artifacts plus %s", artifactNames(got), openAPIMapArtifactName)
	}
	verified, err := sourcebind.LoadVerified(context.Background(), rootsWithOpenAPIRoot(roots))
	if err != nil {
		t.Fatalf("LoadVerified(usable OpenAPI) error = %v", err)
	}
	inputs, err := sourcebind.RequireQualification(verified)
	if err != nil {
		t.Fatalf("RequireQualification(usable OpenAPI) error = %v", err)
	}
	evidence, err := sourceanalysis.Analyze(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Analyze(usable OpenAPI) error = %v", err)
	}
	direct, err := sourceoperation.CompileQualified(context.Background(), evidence, inputs)
	if err != nil {
		t.Fatalf("CompileQualified(usable OpenAPI) error = %v", err)
	}
	for i, artifact := range direct.Artifacts() {
		if got[i].Name != artifact.Name || !bytes.Equal(got[i].Bytes, artifact.Bytes) {
			t.Fatalf("usable OpenAPI core artifact %d differs from direct composition", i)
		}
	}
}

func TestRunQualifiedMapFailureRetainsSealedCore(t *testing.T) {
	recipePath, roots := materializeQualifiedSourceOnlyFixture(t)
	addQualifiedOpenAPI(t, filepath.Dir(recipePath), []byte(`{"openapi":"3.0.3","info":{"title":"source-first","version":"1"},"paths":{}}`))
	writeQualifiedRecipe(t, recipePath, map[string]any{
		// This value is recipe metadata for the optional generic map only. It
		// cannot select a provider from this fixture's captured schema.
		"provider_source": "registry.terraform.io/not-present", "resource_prefix": "sourcefirst", "api_prefix": "/api/",
		"source_provenance": map[string]any{
			"manifest": "source-provenance-v1.json", "provider_root": "provider", "schema_root": ".", "openapi_root": ".",
			"sdk_roots": map[string]any{"example.invalid/sourcefirst-sdk": "sdk"},
		},
	})
	result, err := Run(context.Background(), RunOptions{RecipePath: recipePath, LegacyHost: panicLegacyHost{t: t}})
	if err != nil {
		t.Fatalf("Run(qualified map-only failure) error = %v, want sealed core bundle", err)
	}
	got := result.Artifacts()
	if len(got) != 6 {
		t.Fatalf("map-only failure artifact names = %v, want six core artifacts", artifactNames(got))
	}
	verified, err := sourcebind.LoadVerified(context.Background(), rootsWithOpenAPIRoot(roots))
	if err != nil {
		t.Fatalf("LoadVerified(map-only failure) error = %v", err)
	}
	inputs, err := sourcebind.RequireQualification(verified)
	if err != nil {
		t.Fatalf("RequireQualification(map-only failure) error = %v", err)
	}
	evidence, err := sourceanalysis.Analyze(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Analyze(map-only failure) error = %v", err)
	}
	direct, err := sourceoperation.CompileQualified(context.Background(), evidence, inputs)
	if err != nil {
		t.Fatalf("CompileQualified(map-only failure) error = %v", err)
	}
	for i, artifact := range direct.Artifacts() {
		if got[i].Name != artifact.Name || !bytes.Equal(got[i].Bytes, artifact.Bytes) {
			t.Fatalf("map-only failure core artifact %d differs from direct composition", i)
		}
	}
}

func TestRunQualifiedUnavailableOpenAPIOmitsMap(t *testing.T) {
	recipePath, _ := materializeQualifiedSourceOnlyFixture(t)
	addQualifiedOpenAPI(t, filepath.Dir(recipePath), []byte(`{"not":"openapi"}`))
	writeQualifiedRecipe(t, recipePath, map[string]any{
		"source_provenance": map[string]any{
			"manifest": "source-provenance-v1.json", "provider_root": "provider", "schema_root": ".", "openapi_root": ".",
			"sdk_roots": map[string]any{"example.invalid/sourcefirst-sdk": "sdk"},
		},
	})
	result, err := Run(context.Background(), RunOptions{RecipePath: recipePath})
	if err != nil {
		t.Fatalf("Run(qualified unavailable OpenAPI fixture) error = %v", err)
	}
	if got := artifactNames(result.Artifacts()); len(got) != 6 {
		t.Fatalf("unavailable OpenAPI artifact names = %v, want six core artifacts", got)
	}
}

func TestRunQualifiedDegradedOpenAPIOmitsMap(t *testing.T) {
	recipePath, _ := materializeQualifiedSourceOnlyFixture(t)
	addQualifiedOpenAPI(t, filepath.Dir(recipePath), []byte(`{"openapi":"3.0.3","info":{"title":"source-first","version":"1"},"paths":{"/bad":7}}`))
	writeQualifiedRecipe(t, recipePath, map[string]any{
		"source_provenance": map[string]any{
			"manifest": "source-provenance-v1.json", "provider_root": "provider", "schema_root": ".", "openapi_root": ".",
			"sdk_roots": map[string]any{"example.invalid/sourcefirst-sdk": "sdk"},
		},
	})
	result, err := Run(context.Background(), RunOptions{RecipePath: recipePath})
	if err != nil {
		t.Fatalf("Run(qualified degraded OpenAPI fixture) error = %v", err)
	}
	if got := artifactNames(result.Artifacts()); len(got) != 6 {
		t.Fatalf("degraded OpenAPI artifact names = %v, want six core artifacts", got)
	}
}

func writeQualifiedRecipe(t *testing.T, path string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type panicLegacyHost struct{ t *testing.T }

func (h panicLegacyHost) Download(context.Context, DownloadRequest) error {
	h.t.Fatal("qualified recipe called LegacyHost.Download")
	return nil
}

func (h panicLegacyHost) Clone(context.Context, CloneRequest) error {
	h.t.Fatal("qualified recipe called LegacyHost.Clone")
	return nil
}

func (h panicLegacyHost) CaptureTerraformSchema(context.Context, TerraformSchemaRequest) ([]byte, error) {
	h.t.Fatal("qualified recipe called LegacyHost.CaptureTerraformSchema")
	return nil, nil
}

func materializeQualifiedSourceOnlyFixture(t *testing.T) (string, sourcebind.LocalRoots) {
	t.Helper()
	fixtureRoot := filepath.Join("..", "..", "..", "..", "tests", "fixtures", "authoring", "source-first-v2")
	root := filepath.Join(t.TempDir(), "source-first-v2")
	copyQualifiedFixtureTree(t, fixtureRoot, root)
	providerRoot := filepath.Join(root, "provider")
	commitQualifiedFixtureProvider(t, providerRoot)
	recipePath := filepath.Join(root, "recipe.json")
	writeQualifiedRecipe(t, recipePath, map[string]any{
		"source_provenance": map[string]any{
			"manifest": "source-provenance-v1.json", "provider_root": "provider", "schema_root": ".",
			"sdk_roots": map[string]any{"example.invalid/sourcefirst-sdk": "sdk"},
		},
	})
	return recipePath, sourcebind.LocalRoots{
		ManifestPath: filepath.Join(root, "source-provenance-v1.json"),
		ProviderRoot: providerRoot,
		SchemaRoot:   root,
		SDKRoots: map[string]string{
			"example.invalid/sourcefirst-sdk": filepath.Join(root, "sdk"),
		},
	}
}

func addQualifiedOpenAPI(t *testing.T, root string, document []byte) {
	t.Helper()
	path := filepath.Join(root, "openapi.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	localReference := []byte(`{"components":{}}`)
	if err := os.WriteFile(filepath.Join(root, "openapi-local.json"), localReference, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "source-provenance-v1.json")
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := contracts.DecodeSourceProvenance(bytes)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(document)
	localReferenceDigest := sha256.Sum256(localReference)
	manifest.OpenAPI = &contracts.OpenAPIInputBinding{
		Document:  contracts.FileBinding{Path: "openapi.json", SHA256: hex.EncodeToString(digest[:])},
		LocalRefs: []contracts.FileBinding{{Path: "openapi-local.json", SHA256: hex.EncodeToString(localReferenceDigest[:])}},
	}
	rendered, err := contracts.RenderSourceProvenance(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rootsWithOpenAPIRoot(roots sourcebind.LocalRoots) sourcebind.LocalRoots {
	result := roots
	result.OpenAPIRoot = roots.SchemaRoot
	return result
}

func artifactNames(artifacts []Artifact) []string {
	result := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		result[i] = artifact.Name
	}
	return result
}

func copyQualifiedFixtureTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy fixture tree: %v", err)
	}
}

func commitQualifiedFixtureProvider(t *testing.T, root string) {
	t.Helper()
	environment := qualifiedFixtureGitEnvironment()
	for _, arguments := range [][]string{
		{"init", "--quiet"}, {"add", "."},
		{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "source-first fixture provider"},
	} {
		command := exec.Command("git", arguments...)
		command.Dir, command.Env = root, environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
	}
}

func qualifiedFixtureGitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C", "TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=0",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=Infrawright Fixture", "GIT_AUTHOR_EMAIL=fixtures@infrawright.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00 +0000",
		"GIT_COMMITTER_NAME=Infrawright Fixture", "GIT_COMMITTER_EMAIL=fixtures@infrawright.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00 +0000",
	}
}

func TestQualifiedFixtureGitEnvironmentExcludesParentSecrets(t *testing.T) {
	const secretName = "QUALIFIED_PROBE_PARENT_SECRET"
	t.Setenv(secretName, "must-not-reach-local-git")
	for _, entry := range qualifiedFixtureGitEnvironment() {
		if strings.HasPrefix(entry, secretName+"=") {
			t.Fatalf("fixture Git environment inherited %s", secretName)
		}
	}
}
