package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const transformFailureCompatibilitySHA256 = "7968270dda180294cdc4c5c4e0991fc0a7a177ea2ee3e068cf5b939efc5d0476"

type transformFailureCompatibilityFixture struct {
	SchemaVersion int                                 `json:"schema_version"`
	Cases         []transformFailureCompatibilityCase `json:"cases"`
}

type transformFailureCompatibilityCase struct {
	Name   string                              `json:"name"`
	Exit   int                                 `json:"exit"`
	Stdout string                              `json:"stdout"`
	Stderr string                              `json:"stderr"`
	Tree   []transformFailureCompatibilityFile `json:"tree"`
}

type transformFailureCompatibilityFile struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

type transformFailureInput struct {
	Selectors   []string
	DropCheck   bool
	MissingFile string
	Mutations   map[string]func([]byte) []byte
}

func copyTransformInputs(t *testing.T, source, target string, mutations map[string]func([]byte) []byte) {
	t.Helper()
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", target, err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", source, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error: %v", entry.Name(), err)
		}
		if mutate := mutations[entry.Name()]; mutate != nil {
			content = mutate(content)
		}
		if err := os.WriteFile(filepath.Join(target, entry.Name()), content, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", entry.Name(), err)
		}
	}
}

func transformInjectField(name string) func([]byte) []byte {
	return func(content []byte) []byte {
		var items []map[string]any
		if err := json.Unmarshal(content, &items); err != nil {
			panic(err)
		}
		for _, item := range items {
			item[name] = "compatibility-probe"
		}
		mutated, err := json.Marshal(items)
		if err != nil {
			panic(err)
		}
		return mutated
	}
}

func TestTransformFailureCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "transform_failure_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	fixtureDigest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(fixtureDigest[:]); got != transformFailureCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, transformFailureCompatibilitySHA256)
	}
	var fixture transformFailureCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 || len(fixture.Cases) != 7 {
		t.Fatalf("%s schema/cases = %d/%d, want 1/7", fixturePath, fixture.SchemaVersion, len(fixture.Cases))
	}

	inputs := map[string]transformFailureInput{
		"drops-diagnostics":  {Selectors: []string{"zcc_web_privacy"}, Mutations: map[string]func([]byte) []byte{"zcc_web_privacy.json": transformInjectField("reviewUnexpectedField")}},
		"drops-check-exit":   {Selectors: []string{"zcc_web_privacy"}, DropCheck: true, Mutations: map[string]func([]byte) []byte{"zcc_web_privacy.json": transformInjectField("reviewUnexpectedField")}},
		"invalid-input-json": {Selectors: []string{"zcc_web_privacy"}, Mutations: map[string]func([]byte) []byte{"zcc_web_privacy.json": func([]byte) []byte { return []byte("{ not json\n") }}},
		"envelope-not-list":  {Selectors: []string{"zcc_web_privacy"}, Mutations: map[string]func([]byte) []byte{"zcc_web_privacy.json": func([]byte) []byte { return []byte("{\"items\": []}\n") }}},
		"missing-input-file": {Selectors: []string{"zcc_web_privacy"}, MissingFile: "zcc_web_privacy.json"},
		"mixed-drop-and-invalid": {Selectors: []string{"zcc_web_privacy", "zia_rule_labels"}, DropCheck: true, Mutations: map[string]func([]byte) []byte{
			"zcc_web_privacy.json": transformInjectField("reviewUnexpectedField"),
			"zia_rule_labels.json": func([]byte) []byte { return []byte("{ not json\n") },
		}},
		"interpolation-literals": {Selectors: []string{"zia_rule_labels"}, Mutations: map[string]func([]byte) []byte{
			"zia_rule_labels.json": func(content []byte) []byte {
				return bytes.Replace(content, []byte(`"Test Description for VCR"`), []byte(`"$${TRANSACTION_ID} raw ${RAW} directive %{d}"`), 1)
			},
		}},
	}
	if len(inputs) != len(fixture.Cases) {
		t.Fatalf("Transform compatibility inputs = %d, want %d", len(inputs), len(fixture.Cases))
	}

	repository := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repository, "iw-go-transform-failure")
	demoInput := filepath.Join(repository, "packs", "_shared", "zscaler", "demo")
	for _, expected := range fixture.Cases {
		inputConfig, ok := inputs[expected.Name]
		if !ok {
			t.Fatalf("no Transform compatibility input for %q", expected.Name)
		}
		t.Run(expected.Name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), expected.Name)
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatalf("os.MkdirAll(%q) error: %v", workspace, err)
			}
			input := filepath.Join(workspace, "input")
			copyTransformInputs(t, demoInput, input, inputConfig.Mutations)
			if inputConfig.MissingFile != "" {
				if err := os.Remove(filepath.Join(input, inputConfig.MissingFile)); err != nil {
					t.Fatalf("os.Remove(missing input probe) error: %v", err)
				}
			}
			overlay := filepath.Join(workspace, "out")
			disableCrossState := false
			deploymentPath := writeTransformDeployment(t, workspace, overlay, &disableCrossState)
			temporary := filepath.Join(workspace, "tmp")
			if err := os.Mkdir(temporary, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q) error: %v", temporary, err)
			}
			arguments := []string{"transform", "--in", input, "--tenant", "demo", "--profile", "packs/full.packset.json"}
			for _, selector := range inputConfig.Selectors {
				arguments = append(arguments, "--resource", selector)
			}
			environment := []string{"INFRAWRIGHT_DEPLOYMENT=" + deploymentPath, "TMPDIR=" + temporary}
			if inputConfig.DropCheck {
				environment = append(environment, "DROPS_CHECK=1")
			}
			result := runBinaryWithEnv(t, repository, binary, arguments, environment)
			stdout := string(normalizeV2TransformPaths(result.stdout, repository, workspace))
			stderr := string(normalizeV2TransformPaths(result.stderr, repository, workspace))
			if result.exit != expected.Exit || stdout != expected.Stdout || stderr != expected.Stderr {
				t.Errorf("command exit/stdout/stderr = %d/%q/%q, want %d/%q/%q", result.exit, stdout, stderr, expected.Exit, expected.Stdout, expected.Stderr)
			}
			tree := treeBytes(t, overlay)
			if len(tree) != len(expected.Tree) {
				t.Errorf("output tree files = %d, want %d", len(tree), len(expected.Tree))
			}
			expectedPaths := map[string]bool{}
			for _, expectedFile := range expected.Tree {
				expectedPaths[expectedFile.Path] = true
				content, present := tree[expectedFile.Path]
				if !present {
					t.Errorf("output tree omitted %s", expectedFile.Path)
					continue
				}
				digest := sha256.Sum256(content)
				gotSHA256 := hex.EncodeToString(digest[:])
				if len(content) != expectedFile.Length || gotSHA256 != expectedFile.SHA256 {
					t.Errorf("output %s length/SHA256 = %d/%s, want %d/%s", expectedFile.Path, len(content), gotSHA256, expectedFile.Length, expectedFile.SHA256)
				}
			}
			for path := range tree {
				if !expectedPaths[path] {
					t.Errorf("output tree has unexpected path %s", path)
				}
			}
			if expected.Name == "interpolation-literals" {
				content := tree["config/demo/zia_rule_labels.auto.tfvars.json"]
				want := []byte(`$${TRANSACTION_ID} raw ${RAW} directive %{d}`)
				if !bytes.Contains(content, want) || bytes.Contains(content, []byte(`$$$`)) || bytes.Contains(content, []byte(`%%{`)) {
					t.Errorf("interpolation literal was modified in JSON tfvars: %s", content)
				}
			}
		})
	}
}
