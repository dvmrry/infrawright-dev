package main

// Singleton topology and generated artifacts are pinned to reviewed goldens.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const v2TopologyGoldenRoot = "testdata/v2_topology"

func runV2TopologyCommand(t *testing.T, binary string, fixture blockC4Fixture, arguments []string) runResult {
	t.Helper()
	return runBinaryWithEnv(t, fixture.workspace, binary, arguments, []string{
		"TMPDIR=" + filepath.Join(fixture.workspace, "tmp"),
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"INFRAWRIGHT_DEPLOYMENT=",
	})
}

func topologyFixtureArguments(fixture blockC4Fixture) []string {
	return []string{
		"--root", fixture.packs,
		"--profile", fixture.profile,
		"--deployment", fixture.deployment,
	}
}

// normalizeV2TopologyFixturePaths substitutes only complete fixture-root path
// prefixes. The V2 command fixtures intentionally contain absolute paths, so
// this narrowly removes the test-run temporary directory without normalizing
// JSON, ordering, diagnostics, or any generated artifact content.
func normalizeV2TopologyFixturePaths(content []byte, fixtureRoot string) []byte {
	root := filepath.ToSlash(filepath.Clean(fixtureRoot))
	const placeholder = "<V2_TOPOLOGY_FIXTURE>"
	var normalized bytes.Buffer
	for remaining := content; len(remaining) > 0; {
		index := bytes.Index(remaining, []byte(root))
		if index < 0 {
			normalized.Write(remaining)
			break
		}
		next := index + len(root)
		if next < len(remaining) && !isV2TopologyPathBoundary(remaining[next]) {
			normalized.Write(remaining[:next])
			remaining = remaining[next:]
			continue
		}
		normalized.Write(remaining[:index])
		normalized.WriteString(placeholder)
		remaining = remaining[next:]
	}
	return normalized.Bytes()
}

func isV2TopologyPathBoundary(value byte) bool {
	switch value {
	case '/', '\\', '"', '\'', ')', ']', '}', ',', ':', ';', ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func readV2TopologyGolden(t *testing.T, root, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "go", "cmd", "iw", v2TopologyGoldenRoot, name))
	if err != nil {
		t.Fatalf("reading V2 topology golden %s: %v", name, err)
	}
	return content
}

func requireV2TopologyRunGolden(t *testing.T, root, fixtureRoot, name string, result runResult) {
	t.Helper()
	if result.exit != 0 {
		t.Errorf("%s exit = %d, want 0; stdout=%q stderr=%q", name, result.exit, result.stdout, result.stderr)
	}
	if got, want := normalizeV2TopologyFixturePaths(result.stdout, fixtureRoot), readV2TopologyGolden(t, root, name+".stdout"); !bytes.Equal(got, want) {
		t.Errorf("%s stdout bytes differ from V2 authority golden\n got: %q\nwant: %q", name, got, want)
	}
	if got, want := normalizeV2TopologyFixturePaths(result.stderr, fixtureRoot), readV2TopologyGolden(t, root, name+".stderr"); !bytes.Equal(got, want) {
		t.Errorf("%s stderr bytes differ from V2 authority golden\n got: %q\nwant: %q", name, got, want)
	}
}

func requireExactTree(t *testing.T, name string, got, want map[string][]byte) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	for path, wantBytes := range want {
		gotBytes, found := got[path]
		if !found {
			t.Errorf("%s missing artifact %q", name, path)
			continue
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Errorf("%s artifact %q bytes differ\n got: %q\nwant: %q", name, path, gotBytes, wantBytes)
		}
	}
	for path := range got {
		if _, found := want[path]; !found {
			t.Errorf("%s has unexpected artifact %q with bytes %q", name, path, got[path])
		}
	}
}

func readV2TopologyTreeGolden(t *testing.T, root string) map[string][]byte {
	t.Helper()
	return treeBytes(t, filepath.Join(root, "go", "cmd", "iw", v2TopologyGoldenRoot, "gen-env.tree"))
}

func TestV2TopologyAuthorityNormalizationIsNarrow(t *testing.T) {
	const root = "/tmp/v2-topology-workspace"
	got := normalizeV2TopologyFixturePaths([]byte(root+"/one "+root+"-other "+root), root)
	const want = "<V2_TOPOLOGY_FIXTURE>/one /tmp/v2-topology-workspace-other <V2_TOPOLOGY_FIXTURE>"
	if string(got) != want {
		t.Errorf("normalization = %q, want %q", got, want)
	}
}

func TestV2TopologyAuthority(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-topology")
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))

	cases := []struct {
		name string
		args []string
	}{
		{"roots.default", append([]string{"roots"}, topologyFixtureArguments(fixture)...)},
		{"roots.selector", append([]string{"roots", "--resource", "sample_resource"}, topologyFixtureArguments(fixture)...)},
		{"scope-paths", append([]string{
			"scope-paths",
			"--path", filepath.ToSlash(filepath.Join("config", "tenant", "sample_resource.auto.tfvars.json")),
			"--path", "deployment.json",
		}, topologyFixtureArguments(fixture)...)},
		{"plan-roots", append([]string{"plan-roots", "--tenant", "tenant"}, topologyFixtureArguments(fixture)...)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requireV2TopologyRunGolden(t, root, fixture.workspace, testCase.name, runV2TopologyCommand(t, binary, fixture, testCase.args))
		})
	}

	// Keep the semantic singleton checks secondary to the byte goldens: they
	// make the reason for the fixture concrete without weakening its authority.
	rootResult := runV2TopologyCommand(t, binary, fixture, cases[0].args)
	if !bytes.Contains(rootResult.stdout, []byte(`"label": "sample_resource"`)) ||
		!bytes.Contains(rootResult.stdout, []byte(`"members": [`)) {
		t.Errorf("roots default no longer represents sample_resource as one singleton state unit: %s", rootResult.stdout)
	}
}

func TestV2MetadataCommandAuthority(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-metadata")
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))

	resourceCases := []struct {
		name string
		args []string
	}{
		{name: "default", args: []string{"resources"}},
		{name: "reference order", args: []string{"resources", "--order", "references"}},
		{name: "provider selector", args: []string{"resources", "--resource", "sample"}},
	}
	for _, testCase := range resourceCases {
		t.Run("resources/"+testCase.name, func(t *testing.T) {
			arguments := append(append([]string(nil), testCase.args...),
				"--root", fixture.packs,
				"--profile", fixture.profile,
			)
			requireRunResult(t, runV2TopologyCommand(t, binary, fixture, arguments), 0, "sample_resource\n", "")
		})
	}

	deploymentCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "overlay", args: []string{"overlay"}, want: fixture.workspace},
		{name: "tfvars format", args: []string{"tfvars-format"}, want: "json"},
		{name: "module dir", args: []string{"module-dir"}, want: filepath.Join(fixture.workspace, "modules")},
		{name: "tenant root", args: []string{"tenant-root", "tenant"}, want: fixture.workspace},
		{name: "config dir", args: []string{"config-dir", "tenant"}, want: filepath.Join(fixture.workspace, "config", "tenant")},
		{name: "imports dir", args: []string{"imports-dir", "tenant"}, want: filepath.Join(fixture.workspace, "imports", "tenant")},
		{name: "envs dir", args: []string{"envs-dir", "tenant"}, want: filepath.Join(fixture.workspace, "envs", "tenant")},
	}
	for _, testCase := range deploymentCases {
		t.Run("deployment/"+testCase.name, func(t *testing.T) {
			arguments := append([]string{"deployment", "--deployment", fixture.deployment}, testCase.args...)
			requireRunResult(t, runV2TopologyCommand(t, binary, fixture, arguments), 0, filepath.ToSlash(testCase.want)+"\n", "")
		})
	}
}

func TestV2GenerationAuthority(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-generation")
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
	arguments := append([]string{
		"gen-env", "--tenant", "tenant", "--terraform", filepath.Join(fixture.workspace, "missing-terraform"),
	}, topologyFixtureArguments(fixture)...)
	result := runV2TopologyCommand(t, binary, fixture, arguments)
	requireV2TopologyRunGolden(t, root, fixture.workspace, "gen-env", result)
	requireExactTree(t, "V2 singleton gen-env tree", treeBytes(t, fixture.envDir), readV2TopologyTreeGolden(t, root))

	main, found := treeBytes(t, fixture.envDir)["main.tf"]
	if !found || !bytes.Contains(main, []byte(`module "sample_resource"`)) {
		t.Errorf("V2 singleton gen-env main.tf = %q, want singleton sample_resource module", main)
	}
	if _, found := treeBytes(t, fixture.envDir)["sample_resource.expressions.tf"]; found {
		t.Error("V2 singleton gen-env wrote bindings for a fixture with no declared expressions")
	}
}
