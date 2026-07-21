package main

// The frozen Node transform oracle remains authoritative only when the v2
// cross-state default is explicitly disabled. This corpus pins the Go-owned
// default: omitted roots enable cross-state binding across the complete
// committed demo pull set, with exact command transcripts and output bytes.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const v2TransformGoldenRoot = "testdata/v2_transform"

type v2TransformAuthorityRun struct {
	stdout []byte
	stderr []byte
	tree   map[string][]byte
}

func normalizeV2TransformPathPrefix(content []byte, path, placeholder string) []byte {
	prefix := []byte(filepath.ToSlash(filepath.Clean(path)))
	var normalized bytes.Buffer
	for remaining := content; len(remaining) > 0; {
		index := bytes.Index(remaining, prefix)
		if index < 0 {
			normalized.Write(remaining)
			break
		}
		next := index + len(prefix)
		if !isV2TransformPathLeftBoundary(remaining, index) ||
			(next < len(remaining) && !isV2TopologyPathBoundary(remaining[next])) {
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

// isV2TransformPathLeftBoundary rejects a path-shaped token embedded in a
// larger token while preserving paths at the start or after a delimiter.
func isV2TransformPathLeftBoundary(content []byte, index int) bool {
	return index == 0 || isV2TopologyPathBoundary(content[index-1])
}

// normalizeV2TransformPaths removes only the two exact path prefixes the
// command necessarily reports. It does not normalize JSON, diagnostics,
// ordering, artifact bytes, or unrelated absolute paths.
func normalizeV2TransformPaths(content []byte, repositoryRoot, workspace string) []byte {
	normalized := normalizeV2TransformPathPrefix(content, workspace, "<V2_TRANSFORM_WORKSPACE>")
	return normalizeV2TransformPathPrefix(normalized, repositoryRoot, "<REPOSITORY>")
}

func readV2TransformGolden(t *testing.T, root, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "go", "cmd", "iw", v2TransformGoldenRoot, name))
	if err != nil {
		t.Fatalf("reading V2 transform golden %s: %v", name, err)
	}
	return content
}

func readV2TransformTreeGolden(t *testing.T, root string) map[string][]byte {
	t.Helper()
	return treeBytes(t, filepath.Join(root, "go", "cmd", "iw", v2TransformGoldenRoot, "tree"))
}

func runV2TransformAuthority(t *testing.T, repositoryRoot, binary, name string) v2TransformAuthorityRun {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("create V2 transform workspace: %v", err)
	}
	overlay := filepath.Join(workspace, "out")
	deploymentPath := writeTransformDeployment(t, workspace, overlay, nil)
	temporary := filepath.Join(workspace, "tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatalf("create V2 transform temporary directory: %v", err)
	}
	demoInput := filepath.Join(repositoryRoot, "packs", "_shared", "zscaler", "demo")
	result := runBinaryWithEnv(t, repositoryRoot, binary, []string{
		"transform", "--in", demoInput, "--tenant", "demo",
		"--profile", "packsets/full.json", "--catalog", "packsets/full.json",
	}, []string{
		"INFRAWRIGHT_DEPLOYMENT=" + deploymentPath,
		"TMPDIR=" + temporary,
	})
	if result.exit != 0 {
		t.Fatalf("V2 transform authority %s exit = %d, want 0; stdout=%q stderr=%q", name, result.exit, result.stdout, result.stderr)
	}
	return v2TransformAuthorityRun{
		stdout: normalizeV2TransformPaths(result.stdout, repositoryRoot, workspace),
		stderr: normalizeV2TransformPaths(result.stderr, repositoryRoot, workspace),
		tree:   treeBytes(t, overlay),
	}
}

func TestV2TransformAuthorityNormalizationIsNarrow(t *testing.T) {
	const repository = "/repo/infrawright"
	const workspace = "/tmp/v2-transform-workspace"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid start and space delimiters",
			input: repository + "/packs " + workspace + "/out",
			want:  "<REPOSITORY>/packs <V2_TRANSFORM_WORKSPACE>/out",
		},
		{
			name:  "valid quote delimiter",
			input: `"` + repository + `/packs"`,
			want:  `"<REPOSITORY>/packs"`,
		},
		{
			name:  "invalid left-adjacent tokens",
			input: "token" + repository + "/packs token" + workspace + "/out",
			want:  "token" + repository + "/packs token" + workspace + "/out",
		},
		{
			name:  "invalid right-adjacent tokens",
			input: repository + "-other " + workspace + "-other",
			want:  repository + "-other " + workspace + "-other",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := normalizeV2TransformPaths([]byte(testCase.input), repository, workspace)
			if string(got) != testCase.want {
				t.Errorf("normalizeV2TransformPaths(%q, %q, %q) = %q, want %q", testCase.input, repository, workspace, got, testCase.want)
			}
		})
	}
}

func TestV2TransformDefaultCrossStateAuthority(t *testing.T) {
	root := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-transform")
	first := runV2TransformAuthority(t, root, binary, "first")
	second := runV2TransformAuthority(t, root, binary, "second")

	if !bytes.Equal(first.stdout, second.stdout) {
		t.Errorf("repeat V2 transform stdout differs\nfirst: %q\nsecond: %q", first.stdout, second.stdout)
	}
	if !bytes.Equal(first.stderr, second.stderr) {
		t.Errorf("repeat V2 transform stderr differs\nfirst: %q\nsecond: %q", first.stderr, second.stderr)
	}
	requireExactTree(t, "repeat V2 transform tree", second.tree, first.tree)

	if want := readV2TransformGolden(t, root, "transform.stdout"); !bytes.Equal(first.stdout, want) {
		t.Errorf("V2 transform stdout differs from authority golden\n got: %q\nwant: %q", first.stdout, want)
	}
	if want := readV2TransformGolden(t, root, "transform.stderr"); !bytes.Equal(first.stderr, want) {
		t.Errorf("V2 transform stderr differs from authority golden\n got: %q\nwant: %q", first.stderr, want)
	}
	requireExactTree(t, "V2 transform output tree", first.tree, readV2TransformTreeGolden(t, root))

	bindingsPath := "config/demo/zpa_server_group.generated.expressions.json"
	bindings, found := first.tree[bindingsPath]
	if !found {
		t.Fatalf("V2 transform output lacks cross-state artifact %q", bindingsPath)
	}
	if !bytes.Contains(bindings, []byte("data.terraform_remote_state.zpa_app_connector_group.outputs.infrawright_reference_ids.zpa_app_connector_group")) {
		t.Errorf("cross-state artifact %q lacks the expected remote-state expression: %s", bindingsPath, bindings)
	}
	for _, path := range []string{
		"config/demo/zpa_app_connector_group.lookup.json",
		"config/demo/zpa_application_server.lookup.json",
		"config/demo/zpa_server_group.lookup.json",
	} {
		if _, found := first.tree[path]; !found {
			t.Errorf("V2 transform output lacks inferred lookup %q", path)
		}
	}
}
