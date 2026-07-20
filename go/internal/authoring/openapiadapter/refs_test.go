package openapiadapter

import (
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestSplitRefRejectsClosedBoundaryEscapes(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{
		"https://example.invalid/a.json", "//host/a.json", "/absolute.json",
		"a\\b.json", "a%2fb.json", "a.json?x=1", "./a.json", "../a.json",
		"dir//a.json", "a.json#bad~2pointer", "a.json#/%00", "a\x00.json",
	} {
		if _, _, err := splitRef("root.json", ref); err == nil {
			t.Errorf("splitRef(%q) error = nil, want non-nil", ref)
		}
	}
}

func TestSplitRefAndPointerLocalSuccess(t *testing.T) {
	t.Parallel()
	file, fragment, err := splitRef("root.json", "local.yaml#/a~1b/0")
	if err != nil || file != "local.yaml" || fragment != "/a~1b/0" {
		t.Errorf("splitRef(local pointer) = %q, %q, %v", file, fragment, err)
	}
	got, err := pointer(map[string]any{"a/b": []any{"ok"}}, fragment)
	if err != nil || got != "ok" {
		t.Errorf("pointer(local success) = %v, %v, want ok, nil", got, err)
	}
}

func TestCaptureFilesRejectsDuplicateAndMutation(t *testing.T) {
	t.Parallel()
	file := captured("root.json", []byte(`{}`))
	if _, _, err := captureFiles([]sourcebind.CapturedFile{file, file}); err == nil {
		t.Error("captureFiles(duplicate) error = nil, want non-nil")
	}
	file.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, _, err := captureFiles([]sourcebind.CapturedFile{file}); err == nil {
		t.Error("captureFiles(hash mismatch) error = nil, want non-nil")
	}
}
