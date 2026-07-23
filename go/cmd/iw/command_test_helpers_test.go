package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeTransformDeployment(t *testing.T, dir, overlay string, crossStateReferences *bool) string {
	t.Helper()
	payloadObject := map[string]any{
		"overlay":    overlay,
		"module_dir": filepath.Join(overlay, "modules"),
	}
	if crossStateReferences != nil {
		roots := map[string]any{}
		for _, provider := range []string{"zcc", "zia", "zpa"} {
			roots[provider] = map[string]any{"cross_state_references": *crossStateReferences}
		}
		payloadObject["roots"] = roots
	}
	payload, err := json.Marshal(payloadObject)
	if err != nil {
		t.Fatal(err)
	}
	deploymentPath := filepath.Join(dir, "deployment.json")
	if err := os.WriteFile(deploymentPath, append(payload, '\n'), 0o666); err != nil {
		t.Fatal(err)
	}
	return deploymentPath
}

func treeBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	output := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		output[filepath.ToSlash(relative)] = content
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return output
}

func runBinaryWithEnv(
	t *testing.T,
	dir, argv0 string,
	args []string,
	extraEnv []string,
) runResult {
	t.Helper()
	command := exec.Command(argv0, args...)
	command.Dir = dir
	command.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}, extraEnv...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exit := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		exit = exitError.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %v: %v", argv0, args, err)
	}
	return runResult{exit: exit, stdout: stdout.Bytes(), stderr: stderr.Bytes()}
}
