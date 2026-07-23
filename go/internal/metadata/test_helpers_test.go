package metadata

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	for directory := filepath.Dir(thisFile); ; directory = filepath.Dir(directory) {
		marker := filepath.Join(directory, "packs", "full.packset.json")
		if info, err := os.Stat(marker); err == nil && info.Mode().IsRegular() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("unable to find packs/full.packset.json above %s", thisFile)
		}
	}
}
