// Package zpacorpus keeps the frozen ZPA v4.4.6 evidence matrix bound to its
// reviewed metadata and optional provider checkout. It is a test-only corpus:
// passing it never qualifies or infers a provider-to-SDK-to-HTTP endpoint.
package zpacorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const (
	matrixKind    = "infrawright.zpa_provider_evidence"
	matrixVersion = 1
	// matrixSHA256 is the exact authority for the reviewed import, identity,
	// state-shape, sensitivity, exception, and runtime-gate claims. This gate
	// deliberately does not recreate the retired version-specific validator.
	matrixSHA256   = "5694623da0f3d5e1871b6c62a28649d47e902093cc4a33da8069bb0f6ba97140"
	providerCommit = "dcf12469a9a8f648be0691c74e9816fc94ec7ddc"
	providerRef    = "v4.4.6"
	providerRepo   = "https://github.com/zscaler/terraform-provider-zpa"
	runtimeGate    = "terraform_runtime_evidence_required"

	wantResources   = 16
	wantLocalInputs = 12
	wantSourceFiles = 17
	wantAnchors     = 45
)

type corpusReport struct {
	Kind          string          `json:"kind"`
	SchemaVersion int             `json:"schema_version"`
	LocalInputs   []fileBinding   `json:"local_inputs"`
	Provider      providerBinding `json:"provider"`
	Resources     []resourceClaim `json:"resources"`
	Summary       corpusSummary   `json:"summary"`
}

type fileBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type providerBinding struct {
	Commit      string        `json:"commit"`
	Ref         string        `json:"ref"`
	Repository  string        `json:"repository"`
	SourceFiles []fileBinding `json:"source_files"`
}

type resourceClaim struct {
	Exceptions      []string             `json:"exceptions"`
	Fetch           map[string]any       `json:"fetch"`
	GeneratedConfig generatedConfigClaim `json:"generated_config"`
	Import          map[string]any       `json:"import"`
	ReadIdentity    map[string]any       `json:"read_identity"`
	ResourceType    string               `json:"resource_type"`
	SourceEvidence  sourceEvidence       `json:"source_evidence"`
	StateShape      stateShape           `json:"state_shape"`
}

type generatedConfigClaim struct {
	Qualification string `json:"qualification"`
}

type sourceEvidence struct {
	Exceptions   map[string]sourceAnchor `json:"exceptions"`
	Importer     sourceAnchor            `json:"importer"`
	ReadIdentity sourceAnchor            `json:"read_identity"`
}

type sourceAnchor struct {
	EndLine   int    `json:"end_line"`
	Function  string `json:"function"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	StartLine int    `json:"start_line"`
	URL       string `json:"url"`
}

type stateShape struct {
	SensitiveInputPaths []string `json:"sensitive_input_paths"`
}

type corpusSummary struct {
	FetchBackedResources         int `json:"fetch_backed_resources"`
	GeneratedConfigRuntimeGates  int `json:"generated_config_runtime_gates"`
	NumericOrAlternateImporters  int `json:"numeric_or_alternate_importers"`
	PassthroughImporters         int `json:"passthrough_importers"`
	ResourcesWithSensitiveInputs int `json:"resources_with_sensitive_inputs"`
	SchemaIDNotSourcePopulated   int `json:"schema_id_not_source_populated"`
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "docs", "evidence", "zpa-provider-v4.4.6.json")); err != nil {
		t.Fatalf("repositoryRoot() = %q does not contain the ZPA matrix: %v", root, err)
	}
	return root
}

func readMatrix(t *testing.T) ([]byte, corpusReport) {
	t.Helper()
	filename := filepath.Join(repositoryRoot(t), "docs", "evidence", "zpa-provider-v4.4.6.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", filename, err)
	}
	report, err := decodeReport(data)
	if err != nil {
		t.Fatalf("decodeReport(%q) error = %v", filename, err)
	}
	return data, report
}

func decodeReport(data []byte) (corpusReport, error) {
	value, err := canonjson.ParseControlJSON(string(data))
	if err != nil {
		return corpusReport{}, fmt.Errorf("validate corpus JSON: %w", err)
	}
	root, err := exactObject(value, "report", "kind", "local_inputs", "provider", "resources", "schema_version", "summary")
	if err != nil {
		return corpusReport{}, err
	}
	if _, err := exactObject(root["provider"], "report.provider", "commit", "ref", "repository", "source_files"); err != nil {
		return corpusReport{}, err
	}
	resources, ok := root["resources"].([]any)
	if !ok {
		return corpusReport{}, fmt.Errorf("report.resources must be a list")
	}
	for index, raw := range resources {
		label := fmt.Sprintf("report.resources[%d]", index)
		resource, err := exactObject(raw, label,
			"exceptions", "fetch", "generated_config", "import", "read_identity",
			"resource_type", "source_evidence", "state_shape",
		)
		if err != nil {
			return corpusReport{}, err
		}
		if _, err := exactObject(resource["source_evidence"], label+".source_evidence", "exceptions", "importer", "read_identity"); err != nil {
			return corpusReport{}, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var report corpusReport
	if err := decoder.Decode(&report); err != nil {
		return corpusReport{}, fmt.Errorf("decode corpus matrix: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return corpusReport{}, fmt.Errorf("decode corpus matrix: trailing content")
	}
	return report, nil
}

func exactObject(value any, label string, keys ...string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	if len(object) != len(keys) {
		return nil, fmt.Errorf("%s keys differ", label)
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return nil, fmt.Errorf("%s keys differ", label)
		}
	}
	return object, nil
}

func validateLocalCorpus(report corpusReport, repository, packsRoot string) error {
	if report.Kind != matrixKind || report.SchemaVersion != matrixVersion {
		return fmt.Errorf("unsupported corpus kind/version")
	}
	if report.Provider.Repository != providerRepo || report.Provider.Ref != providerRef || report.Provider.Commit != providerCommit {
		return fmt.Errorf("provider pin is unsupported")
	}
	if len(report.LocalInputs) != wantLocalInputs || len(report.Provider.SourceFiles) != wantSourceFiles || len(report.Resources) != wantResources {
		return fmt.Errorf("corpus cardinality differs")
	}
	if err := validateBindings(report.LocalInputs); err != nil {
		return fmt.Errorf("local inputs: %w", err)
	}
	if err := validateBindings(report.Provider.SourceFiles); err != nil {
		return fmt.Errorf("provider source files: %w", err)
	}
	boundSourcePaths := make(map[string]struct{}, len(report.Provider.SourceFiles))
	for _, binding := range report.Provider.SourceFiles {
		boundSourcePaths[binding.Path] = struct{}{}
	}
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot})
	if err != nil {
		return fmt.Errorf("load effective pack root: %w", err)
	}
	wantTypes := fetchResourceTypes(root)
	if gotTypes := claimTypes(report.Resources); !reflect.DeepEqual(gotTypes, wantTypes) {
		return fmt.Errorf("fetch-backed resource set/order = %v, want %v", gotTypes, wantTypes)
	}
	if err := validateInputBindings(report.LocalInputs, repository, packsRoot); err != nil {
		return err
	}

	anchorCount := 0
	for _, resource := range report.Resources {
		if resource.GeneratedConfig.Qualification != runtimeGate {
			return fmt.Errorf("%s generated-config claim is not fail-closed", resource.ResourceType)
		}
		if !sort.StringsAreSorted(resource.Exceptions) || hasDuplicate(resource.Exceptions) {
			return fmt.Errorf("%s exceptions are not sorted and unique", resource.ResourceType)
		}
		if len(resource.Exceptions) != len(resource.SourceEvidence.Exceptions) {
			return fmt.Errorf("%s exception anchors are incomplete", resource.ResourceType)
		}
		for _, code := range resource.Exceptions {
			if _, exists := resource.SourceEvidence.Exceptions[code]; !exists {
				return fmt.Errorf("%s exception anchor %q is missing", resource.ResourceType, code)
			}
		}
		for _, anchor := range claimAnchors(resource) {
			if err := validateAnchor(anchor); err != nil {
				return fmt.Errorf("%s: %w", resource.ResourceType, err)
			}
			if _, exists := boundSourcePaths[anchor.Path]; !exists {
				return fmt.Errorf("%s anchor path %q lacks a whole-file binding", resource.ResourceType, anchor.Path)
			}
		}
		anchorCount += 2 + len(resource.Exceptions)
	}
	if anchorCount != wantAnchors {
		return fmt.Errorf("source anchor count = %d, want %d", anchorCount, wantAnchors)
	}
	wantSummary := corpusSummary{
		FetchBackedResources:         16,
		GeneratedConfigRuntimeGates:  16,
		NumericOrAlternateImporters:  14,
		PassthroughImporters:         2,
		ResourcesWithSensitiveInputs: 1,
		SchemaIDNotSourcePopulated:   3,
	}
	if report.Summary != wantSummary {
		return fmt.Errorf("corpus summary = %+v, want %+v", report.Summary, wantSummary)
	}
	return nil
}

func validateInputBindings(bindings []fileBinding, repository, packsRoot string) error {
	canonicalPacks := filepath.Join(repository, "packs")
	for _, binding := range bindings {
		canonical := filepath.Join(repository, filepath.FromSlash(binding.Path))
		relative, err := filepath.Rel(canonicalPacks, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("local input %q is outside packs", binding.Path)
		}
		data, err := os.ReadFile(filepath.Join(packsRoot, relative))
		if err != nil || digest(data) != binding.SHA256 {
			return fmt.Errorf("effective pack input %q is stale", binding.Path)
		}
	}
	return nil
}

func validateBindings(bindings []fileBinding) error {
	prior := ""
	for _, binding := range bindings {
		if err := validateRelativePath(binding.Path); err != nil || !validSHA256(binding.SHA256) {
			return fmt.Errorf("binding is invalid")
		}
		if prior != "" && binding.Path <= prior {
			return fmt.Errorf("bindings are not sorted and unique")
		}
		prior = binding.Path
	}
	return nil
}

func fetchResourceTypes(root metadata.LoadedPackRoot) []string {
	var result []string
	for resourceType, resource := range root.Resources {
		if resource.Product == "zpa" {
			if _, exists := resource.Registry["fetch"]; exists {
				result = append(result, resourceType)
			}
		}
	}
	sort.Strings(result)
	return result
}

func claimTypes(resources []resourceClaim) []string {
	result := make([]string, len(resources))
	for index := range resources {
		result[index] = resources[index].ResourceType
	}
	return result
}

func claimAnchors(claim resourceClaim) []sourceAnchor {
	result := []sourceAnchor{claim.SourceEvidence.Importer, claim.SourceEvidence.ReadIdentity}
	for _, code := range claim.Exceptions {
		result = append(result, claim.SourceEvidence.Exceptions[code])
	}
	return result
}

func validateAnchor(anchor sourceAnchor) error {
	if err := validateRelativePath(anchor.Path); err != nil {
		return fmt.Errorf("source anchor path is invalid")
	}
	if anchor.Function == "" || anchor.StartLine < 1 || anchor.EndLine < anchor.StartLine || !validSHA256(anchor.SHA256) {
		return fmt.Errorf("source anchor is invalid")
	}
	wantURL := fmt.Sprintf("%s/blob/%s/%s#L%d-L%d", providerRepo, providerRef, anchor.Path, anchor.StartLine, anchor.EndLine)
	if anchor.URL != wantURL {
		return fmt.Errorf("source anchor URL is not pinned")
	}
	return nil
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func validateRelativePath(value string) error {
	windowsDrivePath := len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/'
	if value == "" || windowsDrivePath || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || filepath.IsAbs(value) {
		return fmt.Errorf("must be a portable relative path")
	}
	if clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))); clean != value || value == "." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("must be a portable relative path")
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && len(value) == sha256.Size*2 && value == strings.ToLower(value)
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func TestFrozenZPAMatrixIsCurrentAndFailClosed(t *testing.T) {
	data, report := readMatrix(t)
	if got := digest(data); got != matrixSHA256 {
		t.Fatalf("digest(ZPA matrix) = %q, want frozen %q", got, matrixSHA256)
	}
	repository := repositoryRoot(t)
	if err := validateLocalCorpus(report, repository, filepath.Join(repository, "packs")); err != nil {
		t.Fatalf("validateLocalCorpus(committed matrix) error = %v", err)
	}
}

func TestEffectivePackInputsRemainBound(t *testing.T) {
	_, report := readMatrix(t)
	repository := repositoryRoot(t)
	for _, path := range []string{
		"zpa/registry.json",
		"zpa/schemas/provider/zpa.json",
		"zpa/overrides/zpa_segment_group.json",
	} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			packsRoot := copyZPAPacks(t, repository)
			filename := filepath.Join(packsRoot, filepath.FromSlash(path))
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error = %v", filename, err)
			}
			if err := os.WriteFile(filename, append(data, '\n'), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q mutation) error = %v", filename, err)
			}
			if err := validateLocalCorpus(report, repository, packsRoot); err == nil {
				t.Errorf("validateLocalCorpus(%q mutation) error = nil, want rejection", path)
			}
		})
	}
}

func copyZPAPacks(t *testing.T, repository string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	if err := os.MkdirAll(filepath.Join(root, "_shared"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(pack root) error = %v", err)
	}
	if err := os.CopyFS(filepath.Join(root, "zpa"), os.DirFS(filepath.Join(repository, "packs", "zpa"))); err != nil {
		t.Fatalf("os.CopyFS(zpa) error = %v", err)
	}
	if err := os.CopyFS(filepath.Join(root, "_shared", "zscaler"), os.DirFS(filepath.Join(repository, "packs", "_shared", "zscaler"))); err != nil {
		t.Fatalf("os.CopyFS(shared zscaler) error = %v", err)
	}
	return root
}

func TestCorpusNeverAcceptsEndpointQualificationField(t *testing.T) {
	data, _ := readMatrix(t)
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("json.Unmarshal(matrix) error = %v", err)
	}
	document["endpoint_qualification"] = "observed_http"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal(endpoint mutation) error = %v", err)
	}
	if _, err := decodeReport(mutated); err == nil {
		t.Error("decodeReport(endpoint qualification) error = nil, want rejection")
	}
}
