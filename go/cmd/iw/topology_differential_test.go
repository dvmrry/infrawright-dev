package main

// Slice-4 differential corpus: the topology command family (resources,
// roots, scope-paths, plan-roots) compared on stdout/stderr/exit against
// the Node oracle over the committed pack set and demo deployment, plus
// full output-tree comparisons for gen-env and modules generate.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTopologyDifferentialAgainstNodeOracle(t *testing.T) {
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s)", oracleBundle)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	goBinary := filepath.Join(root, "dist", "iw-go-diff-topology")
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	t.Cleanup(func() { os.Remove(goBinary) })

	demoDeployment := []string{"INFRAWRIGHT_DEPLOYMENT=demo/deployment.json"}

	scopeList := filepath.Join(t.TempDir(), "paths.json")
	if err := os.WriteFile(scopeList, []byte(`["demo/config/demo/zcc_web_privacy.auto.tfvars.json", "unrelated/file.txt"]`+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	badList := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badList, []byte("{ nope\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "resources", args: []string{"resources"}},
		{name: "resources-reference-order", args: []string{"resources", "--order=references"}},
		{name: "resources-selector", args: []string{"resources", "--resource", "zcc"}},
		{name: "roots-default", args: []string{"roots"}, env: demoDeployment},
		{name: "roots-tenant", args: []string{"roots", "--tenant", "demo"}, env: demoDeployment},
		{name: "roots-member-selector", args: []string{"roots", "--resource", "zia_dlp_dictionaries"}, env: demoDeployment},
		{name: "roots-invalid-tenant", args: []string{"roots", "--tenant", "bad tenant"}, env: demoDeployment},
		{name: "scope-paths-flags", args: []string{"scope-paths", "--path", "demo/config/demo/zcc_web_privacy.auto.tfvars.json", "--path", "demo/deployment.json"}, env: demoDeployment},
		{name: "scope-paths-json-file", args: []string{"scope-paths", "--paths-json", scopeList}, env: demoDeployment},
		{name: "scope-paths-bad-json", args: []string{"scope-paths", "--paths-json", badList}, env: demoDeployment},
		{name: "plan-roots-default", args: []string{"plan-roots"}, env: demoDeployment},
		{name: "plan-roots-tenant", args: []string{"plan-roots", "--tenant", "demo"}, env: demoDeployment},
		{name: "plan-roots-selector", args: []string{"plan-roots", "--resource", "zcc_web_privacy"}, env: demoDeployment},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			oracle := runBinaryWithEnv(t, root, nodeBinary,
				append([]string{oracleBundle}, testCase.args...), testCase.env)
			candidate := runBinaryWithEnv(t, root, goBinary, testCase.args, testCase.env)
			if oracle.exit != candidate.exit {
				t.Errorf("exit: node=%d go=%d\nnode stderr:\n%s\ngo stderr:\n%s",
					oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
			}
			if !equalAfterA6Usage(oracle.stdout, candidate.stdout) {
				t.Errorf("stdout diverges\nnode:\n%s\ngo:\n%s", oracle.stdout, candidate.stdout)
			}
			scrubbedNode := strings.ReplaceAll(string(oracle.stderr), scopeList, "<paths>")
			scrubbedNode = strings.ReplaceAll(scrubbedNode, badList, "<paths>")
			scrubbedGo := strings.ReplaceAll(string(candidate.stderr), scopeList, "<paths>")
			scrubbedGo = strings.ReplaceAll(scrubbedGo, badList, "<paths>")
			if scrubbedNode != scrubbedGo {
				t.Errorf("stderr diverges\nnode:\n%s\ngo:\n%s", scrubbedNode, scrubbedGo)
			}
		})
	}
}

func TestGenerationDifferentialAgainstNodeOracle(t *testing.T) {
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s)", oracleBundle)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH; gen-env and module generation format through terraform fmt")
	}
	goBinary := filepath.Join(root, "dist", "iw-go-diff-generation")
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	t.Cleanup(func() { os.Remove(goBinary) })

	t.Run("modules-generate-and-validate", func(t *testing.T) {
		nodeOut := filepath.Join(t.TempDir(), "modules")
		goOut := filepath.Join(t.TempDir(), "modules")
		missingTerraform := filepath.Join(t.TempDir(), "missing-terraform")
		for _, side := range []struct {
			argv0 string
			args  []string
			out   string
		}{
			{nodeBinary, []string{oracleBundle}, nodeOut},
			{goBinary, nil, goOut},
		} {
			generateArguments := append(append([]string{}, side.args...),
				"modules", "generate", "--out", side.out, "--resource", "zcc_web_privacy", "--resource", "zia_rule_labels")
			if side.argv0 == goBinary {
				// The flag remains accepted for Node CLI compatibility, but the Go
				// formatter must not execute the supplied Terraform path.
				generateArguments = append(generateArguments, "--terraform", missingTerraform)
			}
			result := runBinaryWithEnv(t, root, side.argv0, generateArguments, nil)
			if result.exit != 0 {
				t.Fatalf("%s modules generate failed: %s", side.argv0, result.stderr)
			}
			validateArguments := append(append([]string{}, side.args...),
				"modules", "validate", "--out", side.out, "--resource", "zcc_web_privacy", "--resource", "zia_rule_labels")
			validated := runBinaryWithEnv(t, root, side.argv0, validateArguments, nil)
			if validated.exit != 0 {
				t.Fatalf("%s modules validate failed: %s", side.argv0, validated.stderr)
			}
		}
		nodeTree := treeBytes(t, nodeOut)
		goTree := treeBytes(t, goOut)
		if len(nodeTree) == 0 {
			t.Fatal("node generated no module files")
		}
		for relative, nodeContent := range nodeTree {
			goContent, ok := goTree[relative]
			if !ok {
				t.Errorf("go output missing %s", relative)
				continue
			}
			if !bytes.Equal(nodeContent, goContent) {
				t.Errorf("module file %s diverges", relative)
			}
		}
		for relative := range goTree {
			if _, ok := nodeTree[relative]; !ok {
				t.Errorf("go output has extra module file %s", relative)
			}
		}
	})

	t.Run("gen-env-tree", func(t *testing.T) {
		nodeDir, goDir := t.TempDir(), t.TempDir()
		nodeDeployment := writeTransformDeployment(t, nodeDir, filepath.Join(nodeDir, "out"))
		goDeployment := writeTransformDeployment(t, goDir, filepath.Join(goDir, "out"))
		arguments := []string{"gen-env", "--tenant", "demo", "--resource", "zcc_web_privacy", "--resource", "zia_rule_labels"}
		oracle := runBinaryWithEnv(t, root, nodeBinary,
			append([]string{oracleBundle}, arguments...),
			[]string{"INFRAWRIGHT_DEPLOYMENT=" + nodeDeployment})
		candidateArguments := append(append([]string{}, arguments...),
			"--terraform", filepath.Join(t.TempDir(), "missing-terraform"))
		candidate := runBinaryWithEnv(t, root, goBinary, candidateArguments,
			[]string{"INFRAWRIGHT_DEPLOYMENT=" + goDeployment})
		if oracle.exit != candidate.exit {
			t.Errorf("exit: node=%d go=%d\nnode stderr:\n%s\ngo stderr:\n%s",
				oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
		}
		scrubbedNode := strings.ReplaceAll(string(oracle.stderr), filepath.Join(nodeDir, "out"), "<overlay>")
		scrubbedGo := strings.ReplaceAll(string(candidate.stderr), filepath.Join(goDir, "out"), "<overlay>")
		if scrubbedNode != scrubbedGo {
			t.Errorf("stderr diverges\nnode:\n%s\ngo:\n%s", scrubbedNode, scrubbedGo)
		}
		nodeTree := treeBytes(t, filepath.Join(nodeDir, "out"))
		goTree := treeBytes(t, filepath.Join(goDir, "out"))
		if len(nodeTree) == 0 {
			t.Fatal("node generated no env files")
		}
		for relative, nodeContent := range nodeTree {
			goContent, ok := goTree[relative]
			if !ok {
				t.Errorf("go output missing %s", relative)
				continue
			}
			if !bytes.Equal(nodeContent, goContent) {
				t.Errorf("env file %s diverges\nnode:\n%s\ngo:\n%s", relative, nodeContent, goContent)
			}
		}
		for relative := range goTree {
			if _, ok := nodeTree[relative]; !ok {
				t.Errorf("go output has extra env file %s", relative)
			}
		}
	})
}
