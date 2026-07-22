// Package authority verifies the immutable Node-v1 inputs held during the Go
// authoring-port handoff. It is test-only: no production command reads this
// manifest or invokes the retained Node runtime.
package authority

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const (
	authorityManifestRelativePath = "tests/fixtures/authoring/node-v1/authority.json"
	authorityManifestSHA256       = "c9485be8b0c7a805247d54250c700c562ba8f32fa60f9e35ceb1b6c6e6671612"
	authorityManifestEntryCount   = 10
)

type authorityManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Entries       []authorityEntry `json:"entries"`
}

type authorityEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func TestNodeV1Authority(t *testing.T) {
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, filepath.FromSlash(authorityManifestRelativePath))
	if err := verifyAuthority(root, manifestPath); err != nil {
		t.Fatalf("verifyAuthority(%q, %q) error = %v, want nil", root, manifestPath, err)
	}
}

func TestVerifyEntriesRejectsMutation(t *testing.T) {
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, filepath.FromSlash(authorityManifestRelativePath))
	manifest := loadManifest(t, manifestPath)
	tempRoot := t.TempDir()
	for _, entry := range manifest.Entries {
		copyAuthorityEntry(t, root, tempRoot, entry)
	}
	entry := manifest.Entries[0]

	targetPath := filepath.Join(tempRoot, filepath.FromSlash(entry.Path))
	source, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", targetPath, err)
	}

	if err := verifyEntries(tempRoot, manifest, artifacts.NewDefaultReadBudget()); err != nil {
		t.Fatalf("verifyEntries(initial copy) error = %v, want nil", err)
	}

	mutated := append([]byte(nil), source...)
	mutated[0] ^= 0x01
	if err := os.WriteFile(targetPath, mutated, 0o600); err != nil {
		t.Fatalf("os.WriteFile(mutated %q) error = %v, want nil", targetPath, err)
	}
	if err := verifyEntries(
		tempRoot,
		manifest,
		artifacts.NewDefaultReadBudget(),
	); err == nil {
		t.Errorf("verifyEntries(mutated %q) error = nil, want digest failure", entry.Path)
	}
}

func TestAuthorityManifestDigestAndCardinalityRejectMutationAndRemoval(t *testing.T) {
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, filepath.FromSlash(authorityManifestRelativePath))
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", manifestPath, err)
	}
	manifest := loadManifest(t, manifestPath)

	tempRoot := t.TempDir()
	tempManifestPath := filepath.Join(tempRoot, filepath.FromSlash(authorityManifestRelativePath))
	if err := os.MkdirAll(filepath.Dir(tempManifestPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(tempManifestPath), err)
	}
	if err := os.WriteFile(tempManifestPath, append([]byte("\n"), manifestBytes...), 0o600); err != nil {
		t.Fatalf("os.WriteFile(mutated %q) error = %v, want nil", tempManifestPath, err)
	}
	if err := verifyAuthority(tempRoot, tempManifestPath); err == nil {
		t.Errorf("verifyAuthority(mutated manifest) error = nil, want manifest digest failure")
	}

	removed := authorityManifest{
		SchemaVersion: manifest.SchemaVersion,
		Entries:       append([]authorityEntry(nil), manifest.Entries[:len(manifest.Entries)-1]...),
	}
	if err := validateManifest(removed); err == nil {
		t.Errorf("validateManifest(entries=%d) error = nil, want exact-cardinality failure", len(removed.Entries))
	}
}

func TestDecodeManifestRejectsUnknownAndUnsafeFields(t *testing.T) {
	validSHA256 := strings.Repeat("a", 64)
	tests := []struct {
		name string
		json string
	}{
		{
			name: "unknown_field",
			json: `{"schema_version":1,"entries":[{"path":"fixture.json","size":0,"sha256":"` + validSHA256 + `"}],"extra":true}`,
		},
		{
			name: "duplicate_schema_version",
			json: `{"schema_version":1,"schema_version":1,"entries":[{"path":"fixture.json","size":0,"sha256":"` + validSHA256 + `"}]}`,
		},
		{
			name: "unsafe_control_integer",
			json: `{"schema_version":9007199254740993,"entries":[{"path":"fixture.json","size":0,"sha256":"` + validSHA256 + `"}]}`,
		},
		{
			name: "parent_escape",
			json: `{"schema_version":1,"entries":[{"path":"../fixture.json","size":0,"sha256":"` + validSHA256 + `"}]}`,
		},
		{
			name: "uppercase_digest",
			json: `{"schema_version":1,"entries":[{"path":"fixture.json","size":0,"sha256":"` + strings.ToUpper(validSHA256) + `"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeManifest([]byte(tt.json)); err == nil {
				t.Errorf("decodeManifest(%s) error = nil, want validation failure", tt.name)
			}
		})
	}
}

func verifyAuthority(repositoryRoot, manifestPath string) error {
	budget := artifacts.NewDefaultReadBudget()
	manifestBytes, err := artifacts.ReadBoundedFileBytes(manifestPath, budget, artifacts.StableReadOptions{})
	if err != nil {
		return fmt.Errorf("read authority manifest: %w", err)
	}
	computed := sha256.Sum256(manifestBytes.Bytes)
	computedSHA256 := hex.EncodeToString(computed[:])
	if manifestBytes.Digest.SHA256 != computedSHA256 {
		return fmt.Errorf("authority manifest stable digest = %q, independently rehashed %q", manifestBytes.Digest.SHA256, computedSHA256)
	}
	if computedSHA256 != authorityManifestSHA256 {
		return fmt.Errorf("authority manifest sha256 = %q, want %q", computedSHA256, authorityManifestSHA256)
	}
	manifest, err := decodeManifest(manifestBytes.Bytes)
	if err != nil {
		return fmt.Errorf("decode authority manifest: %w", err)
	}
	return verifyEntries(repositoryRoot, manifest, budget)
}

func verifyEntries(repositoryRoot string, manifest authorityManifest, budget *artifacts.ReadBudget) error {
	if err := validateManifest(manifest); err != nil {
		return fmt.Errorf("validate authority manifest: %w", err)
	}

	for _, entry := range manifest.Entries {
		filePath := filepath.Join(repositoryRoot, filepath.FromSlash(entry.Path))
		bound, err := artifacts.ReadBoundedFileBytes(filePath, budget, artifacts.StableReadOptions{})
		if err != nil {
			return fmt.Errorf("stable-read authority entry %q: %w", entry.Path, err)
		}
		computed := sha256.Sum256(bound.Bytes)
		computedSHA256 := hex.EncodeToString(computed[:])
		if bound.Digest.Size != int64(len(bound.Bytes)) {
			return fmt.Errorf("authority entry %q stable size = %d, byte length = %d", entry.Path, bound.Digest.Size, len(bound.Bytes))
		}
		if bound.Digest.Size != entry.Size {
			return fmt.Errorf("authority entry %q size = %d, want %d", entry.Path, bound.Digest.Size, entry.Size)
		}
		if bound.Digest.SHA256 != computedSHA256 {
			return fmt.Errorf("authority entry %q stable digest = %q, independently rehashed %q", entry.Path, bound.Digest.SHA256, computedSHA256)
		}
		if computedSHA256 != entry.SHA256 {
			return fmt.Errorf("authority entry %q sha256 = %q, want %q", entry.Path, computedSHA256, entry.SHA256)
		}
	}
	return nil
}

func loadManifest(t *testing.T, manifestPath string) authorityManifest {
	t.Helper()
	budget := artifacts.NewDefaultReadBudget()
	bound, err := artifacts.ReadBoundedFileBytes(manifestPath, budget, artifacts.StableReadOptions{})
	if err != nil {
		t.Fatalf("ReadBoundedFileBytes(%q) error = %v, want nil", manifestPath, err)
	}
	manifest, err := decodeManifest(bound.Bytes)
	if err != nil {
		t.Fatalf("decodeManifest(%q) error = %v, want nil", manifestPath, err)
	}
	return manifest
}

func decodeManifest(content []byte) (authorityManifest, error) {
	if _, err := canonjson.ParseControlJSON(string(content)); err != nil {
		return authorityManifest{}, fmt.Errorf("validate control JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()

	var manifest authorityManifest
	if err := decoder.Decode(&manifest); err != nil {
		return authorityManifest{}, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return authorityManifest{}, fmt.Errorf("decode JSON: authority manifest contains multiple values")
		}
		return authorityManifest{}, fmt.Errorf("decode JSON trailing data: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return authorityManifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest authorityManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if len(manifest.Entries) != authorityManifestEntryCount {
		return fmt.Errorf("entries length = %d, want %d", len(manifest.Entries), authorityManifestEntryCount)
	}
	previousPath := ""
	for _, entry := range manifest.Entries {
		if !safeRepositoryRelativePath(entry.Path) {
			return fmt.Errorf("entry path %q is not a safe repository-relative path", entry.Path)
		}
		if entry.Path <= previousPath {
			return fmt.Errorf("entry path %q is not strictly sorted after %q", entry.Path, previousPath)
		}
		if entry.Size < 0 {
			return fmt.Errorf("entry %q size = %d, want a non-negative value", entry.Path, entry.Size)
		}
		if !lowercaseSHA256(entry.SHA256) {
			return fmt.Errorf("entry %q sha256 = %q, want a lowercase SHA-256 digest", entry.Path, entry.SHA256)
		}
		previousPath = entry.Path
	}
	return nil
}

func copyAuthorityEntry(t *testing.T, sourceRoot, targetRoot string, entry authorityEntry) {
	t.Helper()
	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(entry.Path))
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", sourcePath, err)
	}
	targetPath := filepath.Join(targetRoot, filepath.FromSlash(entry.Path))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(targetPath), err)
	}
	if err := os.WriteFile(targetPath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", targetPath, err)
	}
}

func safeRepositoryRelativePath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return false
	}
	if path.IsAbs(value) || filepath.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func lowercaseSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	for directory := filepath.Dir(thisFile); ; directory = filepath.Dir(directory) {
		manifestPath := filepath.Join(directory, filepath.FromSlash(authorityManifestRelativePath))
		if _, err := os.Stat(manifestPath); err == nil {
			if _, err := os.Stat(filepath.Join(directory, "packs", "full.packset.json")); err == nil {
				return directory
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("walked to filesystem root from %q without locating %q", thisFile, authorityManifestRelativePath)
		}
	}
}
