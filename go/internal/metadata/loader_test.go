package metadata

// loader_test.go ports the library-level tests from
// node-tests/metadata-loader.test.ts (there is no CLI-subprocess test in
// that file to skip -- every test there exercises this package's library
// surface directly).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestLoadPackRootExposesGenericResourceSurface ports "committed pack
// metadata exposes the complete generic resource surface".
func TestLoadPackRootExposesGenericResourceSurface(t *testing.T) {
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	catalogPath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := LoadPackRoot(LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	metadata := loaded.Packs

	var names []string
	for _, manifest := range metadata.Manifests {
		names = append(names, manifest.Name)
	}
	wantNames := []string{"aws", "cloudflare", "google", "netbox", "zcc", "zia", "zpa", "ztc"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("manifest names = %v, want %v", names, wantNames)
	}

	wantPrefixes := map[string]string{
		"aws_": "aws", "cloudflare_": "cloudflare", "google_": "google", "netbox_": "netbox",
		"zcc_": "zcc", "zia_": "zia", "zpa_": "zpa", "ztc_": "ztc",
	}
	if !reflect.DeepEqual(metadata.ProviderPrefixes, wantPrefixes) {
		t.Fatalf("providerPrefixes = %v, want %v", metadata.ProviderPrefixes, wantPrefixes)
	}

	registry := loaded.Registry
	overrides := loaded.Overrides
	if len(registry.Entries) != 151 {
		t.Fatalf("registry entries = %d, want 151", len(registry.Entries))
	}
	if len(overrides.Entries) != 74 {
		t.Fatalf("override entries = %d, want 74", len(overrides.Entries))
	}
	if product, _ := registry.Entries["zia_url_categories"]["product"].(string); product != "zia" {
		t.Fatalf("zia_url_categories product = %q, want zia", product)
	}
	if keyField, _ := overrides.Entries["zia_url_categories"]["key_field"].(string); keyField != "configured_name" {
		t.Fatalf("zia_url_categories key_field = %q, want configured_name", keyField)
	}

	resource, ok := loaded.Resources["zia_url_categories"]
	if !ok {
		t.Fatal("zia_url_categories missing from loaded.Resources")
	}
	if resource.Type != "zia_url_categories" || resource.Product != "zia" || resource.Provider != "zia" {
		t.Fatalf("unexpected resource shape: %+v", resource)
	}
	if resource.Pack == nil || *resource.Pack != "zia" {
		t.Fatalf("resource.Pack = %v, want zia", resource.Pack)
	}
	if !reflect.DeepEqual(resource.Registry, registry.Entries["zia_url_categories"]) {
		t.Fatalf("resource.Registry mismatch")
	}
	if !reflect.DeepEqual(resource.Override, overrides.Entries["zia_url_categories"]) {
		t.Fatalf("resource.Override mismatch")
	}

	schema, err := loaded.LoadResourceSchema("zia_url_categories")
	if err != nil {
		t.Fatalf("LoadResourceSchema: %v", err)
	}
	if _, ok := schema["block"].(JsonObject); !ok {
		t.Fatalf("zia_url_categories schema block is not an object: %T", schema["block"])
	}
}

// TestProviderSchemasResolveThroughPackOwnership ports "provider schemas
// resolve through pack ownership and fail on misspellings".
func TestProviderSchemasResolveThroughPackOwnership(t *testing.T) {
	root := repoRoot(t)
	metadata, err := LoadPackMetadata(filepath.Join(root, "packs"))
	if err != nil {
		t.Fatalf("LoadPackMetadata: %v", err)
	}
	counts := make(map[string]int)
	for _, provider := range []string{"zcc", "zia", "zpa", "ztc"} {
		schema, err := LoadProviderSchema(metadata, provider)
		if err != nil {
			t.Fatalf("LoadProviderSchema(%s): %v", provider, err)
		}
		counts[provider] = len(schema.ResourceSchemas)
	}
	want := map[string]int{"zcc": 7, "zia": 74, "zpa": 55, "ztc": 16}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("resourceSchemas counts = %v, want %v", counts, want)
	}

	category, err := LoadResourceSchema(metadata, "zia_url_categories")
	if err != nil {
		t.Fatalf("LoadResourceSchema: %v", err)
	}
	if _, ok := category["block"].(JsonObject); !ok {
		t.Fatalf("category.block is not an object: %T", category["block"])
	}

	if _, err := LoadResourceSchema(metadata, "zia_url_categoriess"); err == nil || !strings.Contains(err.Error(), "not in zia schema") {
		t.Fatalf("expected 'not in zia schema' error, got %v", err)
	}

	if got := ProviderForResource(metadata, "zia_url_categories"); got != "zia" {
		t.Fatalf("ProviderForResource = %q, want zia", got)
	}
}

// TestPackSetValidationCountsManifestlessDirectoriesFailClosed ports
// "pack-set validation counts manifestless runtime directories
// fail-closed".
func TestPackSetValidationCountsManifestlessDirectoriesFailClosed(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "ghost"), 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}
	profile := filepath.Join(directory, "profile.json")
	writeJSONFile(t, profile, JsonObject{
		"kind": PackSetKind, "version": float64(1), "packs": []string{}, "shared": []string{},
	})
	_, err := ValidateActivePackSet(ValidateActivePackSetOptions{ProfilePath: profile, Root: directory})
	if err == nil || !strings.Contains(err.Error(), "undeclared packs: ghost") {
		t.Fatalf("expected undeclared packs error, got %v", err)
	}
}

// TestPackOwnershipAndSharedComponentsHardFailures ports "pack ownership
// and required shared components remain hard failures".
func TestPackOwnershipAndSharedComponentsHardFailures(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "one", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"one_": "same"},
		"requires_shared":   []string{"common"},
	})
	writeJSONFile(t, filepath.Join(directory, "two", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"two_": "same"},
	})
	if _, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Root: directory}); err == nil ||
		!strings.Contains(err.Error(), `provider "same" is declared by multiple packs: one, two`) {
		t.Fatalf("expected multiple-packs error, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(directory, "two")); err != nil {
		t.Fatalf("remove two: %v", err)
	}
	if _, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Root: directory}); err == nil ||
		!strings.Contains(err.Error(), "pack one requires missing shared component common") {
		t.Fatalf("expected missing shared component error, got %v", err)
	}
}

// TestPackAuthoringIgnoresRetainedPythonCollectorFilenames ports "Node
// pack authoring ignores retained Python collector filenames".
func TestPackAuthoringIgnoresRetainedPythonCollectorFilenames(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "bad-name", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
		"provider_sources":  map[string]string{"sample": "example/sample"},
	})
	writeRawFile(t, filepath.Join(directory, "bad-name", "collector.py"), "raise RuntimeError('must not be imported')\n")
	pack := "bad-name"
	result, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Pack: &pack, Root: directory})
	if err != nil {
		t.Fatalf("ValidatePackAuthoring: %v", err)
	}
	if !reflect.DeepEqual(result.Names, []string{"bad-name"}) {
		t.Fatalf("names = %v, want [bad-name]", result.Names)
	}
}

// TestStrictVocabulariesRejectSilentTypos ports "strict profile, registry,
// and override vocabularies reject silent typos".
func TestStrictVocabulariesRejectSilentTypos(t *testing.T) {
	if _, err := ValidatePackSetDocument(JsonObject{
		"kind": PackSetKind, "version": float64(1), "packs": []any{"two", "one"}, "shared": []any{},
	}, "profile.json", PackSetKind); err == nil || !strings.Contains(err.Error(), "packs must be sorted") {
		t.Fatalf("expected packs-must-be-sorted error, got %v", err)
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"product": "sample",
			"fetch":   JsonObject{"path": "/items", "pagination": "singel"},
		},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), `unsupported value "singel"`) {
		t.Fatalf("expected unsupported pagination error, got %v", err)
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{"product": "sample", "slug_group": "false"},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), "slug_group has been removed; see docs/singleton-state-topology-v2.md") {
		t.Fatalf("expected slug_group retirement error, got %v", err)
	}

	if _, err := ValidateOverride(JsonObject{"rename": JsonObject{"one": "two"}}, "override.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown override key rename") {
		t.Fatalf("expected unknown override key error, got %v", err)
	}
}

func TestRegistryResourceKeysRequireCanonicalTerraformTypes(t *testing.T) {
	valid, err := ValidateRegistry(JsonObject{
		"sample_resource_2": JsonObject{"product": "sample"},
	}, "registry.json")
	if err != nil {
		t.Fatalf("ValidateRegistry(valid key) error = %v, want nil", err)
	}
	if _, ok := valid["sample_resource_2"]; !ok {
		t.Error("ValidateRegistry(valid key) omitted sample_resource_2")
	}

	invalid := []string{
		"sample_Foo",
		"sample-foo",
		"2sample_resource",
		"sample_caf\u00e9",
		"sample_cafe\u0301",
	}
	for _, resourceType := range invalid {
		t.Run(resourceType, func(t *testing.T) {
			_, err := ValidateRegistry(JsonObject{
				resourceType: JsonObject{"product": "sample"},
			}, "registry.json")
			if err == nil || !strings.Contains(err.Error(), "must match ^[a-z][a-z0-9_]*$") {
				t.Errorf("ValidateRegistry(resourceType=%q) error = %v, want canonical-resource-type error", resourceType, err)
			}
		})
	}
}

func cloneRule(rule JsonObject, overrides JsonObject) JsonObject {
	out := make(JsonObject, len(rule)+len(overrides))
	for k, v := range rule {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// TestUnsupportedAdoptionMetadataClosedVersionScopedForbiddenOnDerived
// ports "unsupported adoption metadata is closed, version-scoped, and
// forbidden on derived resources".
func TestUnsupportedAdoptionMetadataClosedVersionScopedForbiddenOnDerived(t *testing.T) {
	rule := JsonObject{
		"evidence": []any{"https://example.invalid/provider-source"},
		"match":    JsonObject{"action": "ISOLATE"},
		"provider": JsonObject{"source": "example/sample", "version": "1.2.3"},
		"reason":   "provider cannot round-trip this object",
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"product": "sample",
		},
	}, "registry.json"); err != nil {
		t.Fatalf("expected valid unsupported_if rule to pass, got %v", err)
	}

	cases := []any{
		JsonObject{},
		[]any{},
		[]any{cloneRule(rule, JsonObject{"evidence": []any{}})},
		[]any{cloneRule(rule, JsonObject{"evidence": []any{"same", "same"}})},
		[]any{cloneRule(rule, JsonObject{"match": JsonObject{}})},
		[]any{cloneRule(rule, JsonObject{"match": JsonObject{"nested": JsonObject{"value": true}}})},
		[]any{cloneRule(rule, JsonObject{"provider": JsonObject{"source": "example/sample"}})},
		[]any{cloneRule(rule, JsonObject{"reason": ""})},
		[]any{cloneRule(rule, JsonObject{"unexpected": true})},
		[]any{rule, rule},
	}
	keywords := regexp.MustCompile(`unsupported_if|evidence|match|provider|reason|unknown`)
	for i, unsupportedIf := range cases {
		_, err := ValidateRegistry(JsonObject{
			"sample_resource": JsonObject{
				"adopt":   JsonObject{"unsupported_if": unsupportedIf},
				"product": "sample",
			},
		}, "registry.json")
		if err == nil {
			t.Fatalf("case %d: expected error, got none", i)
		}
		if !keywords.MatchString(err.Error()) {
			t.Fatalf("case %d: error %q does not match expected keywords", i, err.Error())
		}
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"derive":  JsonObject{"from": "sample_source"},
			"product": "sample",
		},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), "not valid for a derived resource") {
		t.Fatalf("expected derived-resource error, got %v", err)
	}

	if _, err := ValidateOverride(JsonObject{"unsupported_if": []any{rule}}, "override.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown override key unsupported_if") {
		t.Fatalf("expected unknown override key error, got %v", err)
	}

	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"pin":               "1.2.3",
		"provider_prefixes": map[string]string{"sample_": "sample"},
		"provider_sources":  map[string]string{"sample": "example/sample"},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"product": "sample",
		},
	})
	if _, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory}); err != nil {
		t.Fatalf("expected LoadPackRoot to succeed, got %v", err)
	}

	for _, mutation := range []struct{ field, value string }{
		{"source", "example/other"},
		{"version", "9.9.9"},
	} {
		mutatedProvider := JsonObject{"source": "example/sample", "version": "1.2.3"}
		mutatedProvider[mutation.field] = mutation.value
		writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
			"sample_resource": JsonObject{
				"adopt": JsonObject{"unsupported_if": []any{JsonObject{
					"evidence": []any{"https://example.invalid/provider-source"},
					"match":    JsonObject{"action": "ISOLATE"},
					"provider": mutatedProvider,
					"reason":   "provider cannot round-trip this object",
				}}},
				"product": "sample",
			},
		})
		_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
		pattern := regexp.MustCompile(`provider\.` + mutation.field + `.*does not match active provider`)
		if err == nil || !pattern.MatchString(err.Error()) {
			t.Fatalf("mutation %s: expected mismatch error, got %v", mutation.field, err)
		}
	}
}

// TestRegistryFetchPathsRejectUnsafeInputs ports "registry fetch paths
// reject inputs that WHATWG URLs would silently normalize".
func TestRegistryFetchPathsRejectUnsafeInputs(t *testing.T) {
	registryFor := func(pathValue string, expansion *string) JsonObject {
		fetch := JsonObject{"pagination": "single", "path": pathValue}
		if expansion != nil {
			fetch["expand"] = JsonObject{"item": []any{*expansion}}
		}
		return JsonObject{"sample_resource": JsonObject{"product": "sample", "fetch": fetch}}
	}

	mustContainPattern := regexp.MustCompile(`fetch\.path must not contain`)
	for _, value := range []string{
		`items\admin`, "items?scope=admin", "items#admin", "items/../admin",
		"items/.%2E/admin", "items/%2e./admin",
	} {
		_, err := ValidateRegistry(registryFor(value, nil), "registry.json")
		if err == nil || !mustContainPattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected fetch.path violation, got %v", value, err)
		}
	}

	rfcPattern := regexp.MustCompile(`RFC 3986 path characters`)
	for _, value := range []string{"items admin", "items/é", "items/%zz"} {
		_, err := ValidateRegistry(registryFor(value, nil), "registry.json")
		if err == nil || !rfcPattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected RFC 3986 violation, got %v", value, err)
		}
	}

	bracePattern := regexp.MustCompile(`undeclared expansion braces`)
	for _, value := range []string{"items/{literal}", "items/{item}/{other}"} {
		var expansion *string
		if strings.Contains(value, "{item}") {
			safe := "safe"
			expansion = &safe
		}
		_, err := ValidateRegistry(registryFor(value, expansion), "registry.json")
		if err == nil || !bracePattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected undeclared braces violation, got %v", value, err)
		}
	}

	dotPattern := regexp.MustCompile(`fetch\.expand\.item\[0\] must not be`)
	for _, value := range []string{".", ".."} {
		value := value
		_, err := ValidateRegistry(registryFor("items/{item}", &value), "registry.json")
		if err == nil || !dotPattern.MatchString(err.Error()) {
			t.Fatalf("expansion %q: expected dot-segment violation, got %v", value, err)
		}
	}

	okCases := []struct {
		path      string
		expansion string
	}{
		{"items/{item}", "slash/value"},
		{"items/{item}/{item}", "safe"},
		{"items/{item}", "nested/../value?#\\"},
		{"items/{item}", "%2e"},
	}
	for _, c := range okCases {
		expansion := c.expansion
		if _, err := ValidateRegistry(registryFor(c.path, &expansion), "registry.json"); err != nil {
			t.Fatalf("path %q expansion %q: expected success, got %v", c.path, c.expansion, err)
		}
	}
}

// TestMetadataLoadingPreservesFetchQueryNumberTokens ports "metadata
// loading preserves fetch query number tokens and wide integers".
func TestMetadataLoadingPreservesFetchQueryNumberTokens(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
	})
	if err := os.MkdirAll(filepath.Join(directory, "sample", "overrides"), 0o755); err != nil {
		t.Fatalf("mkdir overrides: %v", err)
	}
	writeRawFile(t, filepath.Join(directory, "sample", "registry.json"),
		`{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":{"safe":9007199254740991,"wide":9007199254740993,"decimal":1.0,"exponent":1e0,"negative_zero":-0.0}}}}`,
	)
	writeRawFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource.json"),
		`{"defaults":{"wide":9007199254740993}}`,
	)
	profile := filepath.Join(directory, "profile.json")
	writeRawFile(t, profile, `{"kind":"infrawright.pack-set","version":1,"packs":["sample"],"shared":[]}`)

	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	resource, ok := loaded.Resources["sample_resource"]
	if !ok {
		t.Fatal("sample_resource missing")
	}
	fetch, ok := resource.Registry["fetch"].(JsonObject)
	if !ok {
		t.Fatalf("fetch is not an object: %T", resource.Registry["fetch"])
	}
	query, ok := fetch["query"].(JsonObject)
	if !ok {
		t.Fatalf("query is not an object: %T", fetch["query"])
	}
	checkToken := func(key, want string) {
		t.Helper()
		n, ok := query[key].(json.Number)
		if !ok {
			t.Fatalf("query[%s] is not a json.Number: %T (%v)", key, query[key], query[key])
		}
		if n.String() != want {
			t.Fatalf("query[%s] = %s, want %s", key, n.String(), want)
		}
	}
	checkToken("safe", "9007199254740991")
	checkToken("wide", "9007199254740993")
	checkToken("decimal", "1.0")
	checkToken("exponent", "1e0")
	checkToken("negative_zero", "-0.0")

	if resource.Override == nil {
		t.Fatal("override missing")
	}
	defaults, ok := resource.Override["defaults"].(JsonObject)
	if !ok {
		t.Fatalf("defaults is not an object: %T", resource.Override["defaults"])
	}
	wideDefault, ok := defaults["wide"].(json.Number)
	if !ok {
		t.Fatalf("defaults.wide is not a json.Number: %T", defaults["wide"])
	}
	if wideDefault.String() != "9007199254740993" {
		t.Fatalf("defaults.wide = %s, want 9007199254740993", wideDefault.String())
	}

	writeRawFile(t, profile, `{"kind":"infrawright.pack-set","version":1.0,"packs":["sample"],"shared":[]}`)
	if _, err := ValidateActivePackSet(ValidateActivePackSetOptions{ProfilePath: profile, Root: directory}); err == nil ||
		!strings.Contains(err.Error(), "version must be 1") {
		t.Fatalf("expected version-must-be-1 error, got %v", err)
	}

	writeRawFile(t, filepath.Join(directory, "sample", "registry.json"),
		`{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":9007199254740993}}}`,
	)
	if _, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory}); err == nil ||
		!strings.Contains(err.Error(), "fetch.query must be an object") {
		t.Fatalf("expected fetch.query object error, got %v", err)
	}
}

// TestAllCommittedPackProfilesLoadFromReducedRoots ports "all committed
// pack profiles load from physically reduced roots".
func TestAllCommittedPackProfilesLoadFromReducedRoots(t *testing.T) {
	root := repoRoot(t)
	packsetNames := []string{
		"empty", "aws", "cloudflare", "google", "netbox", "zcc", "zia", "zpa", "ztc", "zscaler", "full",
	}
	fullCatalogPath := filepath.Join(root, "packs", "full.packset.json")
	for _, name := range packsetNames {
		name := name
		t.Run(name, func(t *testing.T) {
			profilePath := filepath.Join(root, "packs", name+".packset.json")
			raw, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatalf("reading %s: %v", profilePath, err)
			}
			var profile struct {
				Packs  []string `json:"packs"`
				Shared []string `json:"shared"`
			}
			if err := json.Unmarshal(raw, &profile); err != nil {
				t.Fatalf("unmarshal %s: %v", profilePath, err)
			}

			directory := t.TempDir()
			for _, packName := range profile.Packs {
				if err := copyDir(filepath.Join(root, "packs", packName), filepath.Join(directory, packName)); err != nil {
					t.Fatalf("copy pack %s: %v", packName, err)
				}
			}
			for _, sharedName := range profile.Shared {
				if err := copyDir(
					filepath.Join(root, "packs", "_shared", sharedName),
					filepath.Join(directory, "_shared", sharedName),
				); err != nil {
					t.Fatalf("copy shared %s: %v", sharedName, err)
				}
			}

			loaded, err := LoadPackRoot(LoadPackRootOptions{
				PacksRoot:   directory,
				ProfilePath: &profilePath,
				CatalogPath: &fullCatalogPath,
			})
			if err != nil {
				t.Fatalf("LoadPackRoot: %v", err)
			}
			wantActive := PackSelection{Packs: profile.Packs, Shared: profile.Shared}
			if !reflect.DeepEqual(loaded.Active, wantActive) {
				t.Fatalf("loaded.Active = %+v, want %+v", loaded.Active, wantActive)
			}
			active, err := ActivePackSelection(directory)
			if err != nil {
				t.Fatalf("ActivePackSelection: %v", err)
			}
			if !reflect.DeepEqual(active, loaded.Active) {
				t.Fatalf("ActivePackSelection(directory) = %+v, want %+v (loaded.Active)", active, loaded.Active)
			}
		})
	}
}

// TestCommittedPackProfilesAreDerivable proves the current checked-in profile
// inventory contains no selection knowledge beyond pack identity, vendor, and
// requires_shared metadata. The full profile remains independently valuable as
// an exact distribution lock: deriving it from an already damaged installed
// root could not detect an accidentally deleted pack.
func TestCommittedPackProfilesAreDerivable(t *testing.T) {
	root := repoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	metadata, err := LoadPackMetadata(packsRoot)
	if err != nil {
		t.Fatalf("LoadPackMetadata(%q) error = %v, want nil", packsRoot, err)
	}

	manifestByName := make(map[string]PackManifest, len(metadata.Manifests))
	for _, manifest := range metadata.Manifests {
		manifestByName[manifest.Name] = manifest
	}
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", packsRoot, err)
	}
	var profileNames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".packset.json") {
			profileNames = append(profileNames, strings.TrimSuffix(entry.Name(), ".packset.json"))
		}
	}
	if len(profileNames) == 0 {
		t.Fatalf("os.ReadDir(%q) found no *.packset.json profiles", packsRoot)
	}
	sort.Strings(profileNames)
	for _, profileName := range profileNames {
		t.Run(profileName, func(t *testing.T) {
			selectedPacks := []string{}
			switch profileName {
			case "empty":
			case "full":
				for _, manifest := range metadata.Manifests {
					selectedPacks = append(selectedPacks, manifest.Name)
				}
			case "zscaler":
				for _, manifest := range metadata.Manifests {
					if manifest.Data["vendor"] == "zscaler" {
						selectedPacks = append(selectedPacks, manifest.Name)
					}
				}
			default:
				if _, ok := manifestByName[profileName]; !ok {
					t.Fatalf("profile %q has no matching pack manifest", profileName)
				}
				selectedPacks = []string{profileName}
			}
			sort.Strings(selectedPacks)
			sharedSet := make(map[string]struct{})
			for _, packName := range selectedPacks {
				for _, dependency := range manifestByName[packName].RequiresShared {
					sharedSet[dependency] = struct{}{}
				}
			}
			selectedShared := make([]string, 0, len(sharedSet))
			for name := range sharedSet {
				selectedShared = append(selectedShared, name)
			}
			sort.Strings(selectedShared)

			profilePath := filepath.Join(packsRoot, profileName+".packset.json")
			profile, loadErr := LoadPackSetDocument(profilePath, PackSetKind)
			if loadErr != nil {
				t.Fatalf("LoadPackSetDocument(%q) error = %v, want nil", profilePath, loadErr)
			}
			want := PackSelection{Packs: selectedPacks, Shared: selectedShared}
			if !reflect.DeepEqual(profile.PackSelection, want) {
				t.Errorf("LoadPackSetDocument(%q).PackSelection = %+v, want derived selection %+v", profilePath, profile.PackSelection, want)
			}
		})
	}
}

// TestFrozenNodePackSetsMatchFlatProfiles keeps the temporary Node-v1
// compatibility layout byte-identical to the current Go profile layout.
func TestFrozenNodePackSetsMatchFlatProfiles(t *testing.T) {
	root := repoRoot(t)
	flatRoot := filepath.Join(root, "packs")
	compatibilityRoot := filepath.Join(root, "packsets")
	flatEntries, err := os.ReadDir(flatRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", flatRoot, err)
	}
	compatibilityEntries, err := os.ReadDir(compatibilityRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", compatibilityRoot, err)
	}

	flatNames := make(map[string]struct{})
	for _, entry := range flatEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".packset.json") {
			flatNames[strings.TrimSuffix(entry.Name(), ".packset.json")] = struct{}{}
		}
	}
	compatibilityNames := make(map[string]struct{})
	for _, entry := range compatibilityEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			compatibilityNames[strings.TrimSuffix(entry.Name(), ".json")] = struct{}{}
		}
	}
	if !reflect.DeepEqual(compatibilityNames, flatNames) {
		t.Fatalf("compatibility profile names = %v, want flat profile names %v", compatibilityNames, flatNames)
	}

	for name := range flatNames {
		flatPath := filepath.Join(flatRoot, name+".packset.json")
		flat, readErr := os.ReadFile(flatPath)
		if readErr != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", flatPath, readErr)
		}
		compatibilityPath := filepath.Join(compatibilityRoot, name+".json")
		compatibility, readErr := os.ReadFile(compatibilityPath)
		if readErr != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", compatibilityPath, readErr)
		}
		if !bytes.Equal(compatibility, flat) {
			t.Errorf("os.ReadFile(%q) differs from current profile %q", compatibilityPath, flatPath)
		}
	}
}
