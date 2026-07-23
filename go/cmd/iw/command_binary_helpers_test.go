package main

// Shared helpers for command-level tests that execute a disposable iw binary.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		packs, err := os.Stat(filepath.Join(current, "packs", "full.packset.json"))
		if err == nil && packs.Mode().IsRegular() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("unable to locate the repository root (packs/full.packset.json)")
		}
		current = parent
	}
}

type runResult struct {
	exit   int
	stdout []byte
	stderr []byte
}

// buildGoV2AuthorityCLI builds a disposable CLI next to the runtime data so
// packageRoot resolves the checked-in packs.
func buildGoV2AuthorityCLI(t *testing.T, root, prefix string) string {
	t.Helper()
	dist := filepath.Join(root, "dist")
	distInfo, statErr := os.Stat(dist)
	createdDist := os.IsNotExist(statErr)
	if statErr != nil && !createdDist {
		t.Fatalf("stat %s: %v", dist, statErr)
	}
	if statErr == nil && !distInfo.IsDir() {
		t.Fatalf("runtime directory %s is not a directory", dist)
	}
	if createdDist {
		if err := os.MkdirAll(dist, 0o755); err != nil {
			t.Fatalf("creating %s for disposable Go CLI: %v", dist, err)
		}
		// The test owns this directory only when it was absent on entry. Remove
		// it only if it is still empty after removing our own binary; this never
		// removes a checked-in oracle or an artifact created by another test.
		t.Cleanup(func() { _ = os.Remove(dist) })
	}
	candidateFile, err := os.CreateTemp(dist, prefix+"-*")
	if err != nil {
		t.Fatalf("os.CreateTemp(%s): %v", prefix, err)
	}
	candidate := candidateFile.Name()
	if err := candidateFile.Close(); err != nil {
		t.Fatalf("closing %s: %v", candidate, err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatalf("removing build placeholder %s: %v", candidate, err)
	}
	t.Cleanup(func() { _ = os.Remove(candidate) })
	build := exec.Command("go", "build", "-o", candidate, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	return candidate
}
