package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const fingerprintAuthoritySHA256 = "69ebf724f468e72c37ffaac33f78055e37cc944397fa923a31ff08331030a1b6"

type fingerprintScannerResult struct {
	OK      bool              `json:"ok"`
	Sources map[string]string `json:"sources"`
	Message string            `json:"message"`
}

type fingerprintAuthorityMetadata struct {
	Implementation string `json:"implementation"`
	Platform       string `json:"platform"`
	Python         string `json:"python"`
	Unicode        string `json:"unicode"`
}

type invalidFilenameAuthorityResult struct {
	AfterDigest  string `json:"after_digest"`
	BeforeDigest string `json:"before_digest"`
	Kind         string `json:"kind"`
}

type fingerprintAuthority struct {
	Main struct {
		Canonical   string             `json:"canonical"`
		Digest      string             `json:"digest"`
		InitDigest  string             `json:"init_digest"`
		InitPayload InitSourcesPayload `json:"init_payload"`
		ModulePaths map[string]string  `json:"module_sources"`
		Payload     PlanSourcesPayload `json:"payload"`
	} `json:"main"`
	LeadingFEFF struct {
		Canonical   string             `json:"canonical"`
		Digest      string             `json:"digest"`
		InitDigest  string             `json:"init_digest"`
		InitPayload InitSourcesPayload `json:"init_payload"`
		Payload     PlanSourcesPayload `json:"payload"`
	} `json:"leading_feff"`
	LinuxInvalidFilename struct {
		Authority fingerprintAuthorityMetadata     `json:"authority"`
		Results   []invalidFilenameAuthorityResult `json:"results"`
	} `json:"linux_invalid_filename"`
	Scanner struct {
		Accepted fingerprintScannerResult `json:"accepted"`
		Failures []struct {
			Name   string                   `json:"name"`
			Result fingerprintScannerResult `json:"result"`
		} `json:"failures"`
		DuplicateModules fingerprintScannerResult `json:"duplicate_modules"`
	} `json:"scanner"`
	TopLevelSymlinkTree []FileFingerprint `json:"top_level_symlink_tree"`
}

func loadFingerprintAuthority(t *testing.T) fingerprintAuthority {
	t.Helper()
	filePath := filepath.Join("..", "..", "..", "node-tests", "fixtures", "python-plan-fingerprint-v1.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", filePath, err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != fingerprintAuthoritySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", filePath, got, fingerprintAuthoritySHA256)
	}
	var authority fingerprintAuthority
	if err := json.Unmarshal(data, &authority); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", filePath, err)
	}
	return authority
}

func writeFingerprintFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", filePath, err)
	}
}

func writeFingerprintText(t *testing.T, filePath, content string) {
	t.Helper()
	writeFingerprintFile(t, filePath, []byte(content))
}

func fingerprintModuleBlock(resourceType, source, itemName string) string {
	if itemName == "" {
		itemName = resourceType
	}
	return strings.Join([]string{
		`module "` + resourceType + `" {`,
		`  source = "` + source + `"`,
		`  items = var.` + itemName + `_items`,
		"}",
		"",
	}, "\n")
}

func stringPointer(value string) *string {
	return &value
}

func newFingerprintTestBudget(
	t *testing.T,
	configure func(*artifacts.BoundedReadLimits),
) *artifacts.ReadBudget {
	t.Helper()
	limits := artifacts.DefaultBoundedReadLimits()
	configure(&limits)
	budget, err := artifacts.NewReadBudget(limits)
	if err != nil {
		t.Fatalf("artifacts.NewReadBudget(%+v) error: %v", limits, err)
	}
	return budget
}

func requireFingerprintFailureCode(t *testing.T, operation string, err error, want string) {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("%s error = %v, want *procerr.ProcessFailure code %q", operation, err, want)
	}
	if failure.Code != want {
		t.Errorf("%s error code = %q, want %q (error: %v)", operation, failure.Code, want, err)
	}
}

func TestFingerprintV2PayloadAndDigestMatchFrozenPythonAuthority(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	temp := t.TempDir()
	envDir := filepath.Join(temp, "envs", "tenant", "zpa_custom")
	firstType := "zpa_segment_group"
	secondType := "zpa_server_group"
	firstModule := filepath.Join(temp, "modules", "segment-\x7f-é-😀")
	missingModule := filepath.Join(temp, "modules", "missing-server-group")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", envDir, err)
	}
	if err := os.MkdirAll(firstModule, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", firstModule, err)
	}

	firstSource, err := filepath.Rel(envDir, firstModule)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, firstModule, err)
	}
	secondSource, err := filepath.Rel(envDir, missingModule)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, missingModule, err)
	}
	writeFingerprintText(t, filepath.Join(envDir, "main.tf"), strings.Join([]string{
		"# module text in comments is not configuration",
		`/* module "ignored" { source = "remote/example" } */`,
		fingerprintModuleBlock(firstType, firstSource, ""),
		fingerprintModuleBlock(secondType, secondSource, ""),
	}, "\n"))
	writeFingerprintText(t, filepath.Join(envDir, "providers.tf"), "terraform { required_version = \">= 1.8\" }\n")
	writeFingerprintText(t, filepath.Join(envDir, "é-\x7f.tf.json"), "{}\n")
	writeFingerprintText(t, filepath.Join(envDir, ".terraform.lock.hcl"), "# lock\n")
	writeFingerprintText(t, filepath.Join(envDir, "terraform.tfvars"), "root = true\n")
	writeFingerprintText(t, filepath.Join(envDir, "terraform.tfvars.json"), "{\"root\":true}\n")
	writeFingerprintText(t, filepath.Join(envDir, "a.auto.tfvars"), "a = 1\n")
	writeFingerprintText(t, filepath.Join(envDir, "b.auto.tfvars.json"), "{\"b\":2}\n")
	writeFingerprintText(t, filepath.Join(envDir, "manual.tfvars"), "ignored = true\n")
	writeFingerprintText(t, filepath.Join(envDir, "tfplan"), "ignored plan bytes")
	writeFingerprintText(t, filepath.Join(envDir, "tfplan.sources"), "ignored fingerprint")
	if err := os.Mkdir(filepath.Join(envDir, "directory.tf"), 0o755); err != nil {
		t.Fatalf("os.Mkdir(directory.tf) error: %v", err)
	}

	writeFingerprintText(t, filepath.Join(firstModule, "main.tf"), "# module main\n")
	writeFingerprintFile(t, filepath.Join(firstModule, "nested", "binary.bin"), []byte{0, 1, 255})
	writeFingerprintText(t, filepath.Join(firstModule, ".terraform", "ignored.bin"), "ignored\n")
	writeFingerprintText(t, filepath.Join(firstModule, "__pycache__", "ignored.pyc"), "ignored\n")
	if err := os.Symlink(filepath.Join(firstModule, "main.tf"), filepath.Join(firstModule, "linked-main.tf")); err != nil {
		t.Fatalf("os.Symlink(linked-main.tf) error: %v", err)
	}
	outsideDir := filepath.Join(temp, "outside-tree")
	writeFingerprintText(t, filepath.Join(outsideDir, "must-not-appear.txt"), "outside\n")
	if err := os.Symlink(outsideDir, filepath.Join(firstModule, "linked-directory")); err != nil {
		t.Fatalf("os.Symlink(linked-directory) error: %v", err)
	}

	linkedRootTarget := filepath.Join(temp, "linked-root.tf")
	writeFingerprintText(t, linkedRootTarget, "# linked root\n")
	if err := os.Symlink(linkedRootTarget, filepath.Join(envDir, "linked.tf")); err != nil {
		t.Fatalf("os.Symlink(linked.tf) error: %v", err)
	}

	configA := filepath.Join(temp, "config-a", "shared.auto.tfvars.json")
	configB := filepath.Join(temp, "config-b", "shared.auto.tfvars.json")
	configUnicode := filepath.Join(temp, "config", "vars-\x7f-é.auto.tfvars.json")
	writeFingerprintText(t, configA, "{\"a\":1}\n")
	writeFingerprintText(t, configB, "{\"b\":2}\n")
	writeFingerprintText(t, configUnicode, "{\"unicode\":true}\n")

	backendTarget := filepath.Join(temp, "backend-target.hcl")
	backendLink := filepath.Join(temp, "backend.hcl")
	writeFingerprintText(t, backendTarget, "bucket = \"example\"\n")
	if err := os.Symlink(backendTarget, backendLink); err != nil {
		t.Fatalf("os.Symlink(backend.hcl) error: %v", err)
	}
	backendKey := "tenant/zpa-\x7f-é-😀.tfstate"
	input := PlanFingerprintInput{
		BackendConfig: &backendLink,
		BackendKey:    &backendKey,
		EnvDir:        envDir,
		MemberTypes:   []string{secondType, firstType},
		VarFiles: []string{
			configB,
			filepath.Join(temp, "missing.auto.tfvars.json"),
			configA,
			configUnicode,
		},
	}

	payload, err := CapturePlanSourcesPayload(input, nil)
	if err != nil {
		t.Fatalf("CapturePlanSourcesPayload(%+v, nil) error: %v", input, err)
	}
	initPayload, err := CaptureInitSourcesPayload(InitFingerprintInput{
		BackendConfig: input.BackendConfig,
		BackendKey:    input.BackendKey,
		EnvDir:        input.EnvDir,
		MemberTypes:   input.MemberTypes,
	}, nil)
	if err != nil {
		t.Fatalf("CaptureInitSourcesPayload(%+v, nil) error: %v", input, err)
	}
	if !reflect.DeepEqual(payload, authority.Main.Payload) {
		t.Errorf("CapturePlanSourcesPayload(%+v, nil) = %#v, want frozen %#v", input, payload, authority.Main.Payload)
	}
	if !reflect.DeepEqual(initPayload, authority.Main.InitPayload) {
		t.Errorf("CaptureInitSourcesPayload(%+v, nil) = %#v, want frozen %#v", input, initPayload, authority.Main.InitPayload)
	}
	if got := CanonicalPlanSourcesJSON(payload); got != authority.Main.Canonical {
		t.Errorf("CanonicalPlanSourcesJSON(payload) = %q, want frozen %q", got, authority.Main.Canonical)
	}
	if got := PlanSourcesSHA256(payload); got != authority.Main.Digest {
		t.Errorf("PlanSourcesSHA256(payload) = %q, want %q", got, authority.Main.Digest)
	}
	if got := InitSourcesSHA256(initPayload); got != authority.Main.InitDigest {
		t.Errorf("InitSourcesSHA256(initPayload) = %q, want %q", got, authority.Main.InitDigest)
	}
	fingerprint, err := FingerprintPlanV2(input, nil)
	if err != nil {
		t.Fatalf("FingerprintPlanV2(%+v, nil) error: %v", input, err)
	}
	wantFingerprint := PlanFingerprintV2{Version: 2, SHA256: authority.Main.Digest}
	if fingerprint != wantFingerprint {
		t.Errorf("FingerprintPlanV2(%+v, nil) = %#v, want %#v", input, fingerprint, wantFingerprint)
	}
	sources, err := RootModuleSources(envDir, nil)
	if err != nil {
		t.Fatalf("RootModuleSources(%q, nil) error: %v", envDir, err)
	}
	if !reflect.DeepEqual(sources, authority.Main.ModulePaths) {
		t.Errorf("RootModuleSources(%q, nil) = %#v, want frozen %#v", envDir, sources, authority.Main.ModulePaths)
	}
}

func TestCanonicalPlanSourcesJSONCompactEnsureASCIIContract(t *testing.T) {
	key := "key-\x7f-é-😀"
	payload := PlanSourcesPayload{
		Backend:     &BackendFingerprint{Key: &key, Present: false},
		MemberTypes: []string{},
		Modules:     []ModuleFingerprint{},
		RootTF:      []FileFingerprint{},
		VarFiles:    []FileFingerprint{},
	}
	want := `{"backend":{"key":"key-\u007f-\u00e9-\ud83d\ude00","present":false},"member_types":[],"modules":[],"root_tf":[],"var_files":[]}`
	got := CanonicalPlanSourcesJSON(payload)
	if got != want {
		t.Errorf("CanonicalPlanSourcesJSON(%#v) = %q, want %q", payload, got, want)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("CanonicalPlanSourcesJSON(%#v) has a trailing newline; want compact JSON without one", payload)
	}
}

func TestTreeFingerprintsFollowsTopLevelSymlinkOnly(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	temp := t.TempDir()
	target := filepath.Join(temp, "target")
	link := filepath.Join(temp, "link")
	writeFingerprintText(t, filepath.Join(target, "file.txt"), "content\n")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error: %v", target, link, err)
	}
	got, err := TreeFingerprints(link, nil)
	if err != nil {
		t.Fatalf("TreeFingerprints(%q, nil) error: %v", link, err)
	}
	if !reflect.DeepEqual(got, authority.TopLevelSymlinkTree) {
		t.Errorf("TreeFingerprints(%q, nil) = %#v, want frozen %#v", link, got, authority.TopLevelSymlinkTree)
	}
}

func TestTreeFingerprintsIgnoresEverySourceDirectory(t *testing.T) {
	root := t.TempDir()
	wantIgnored := []string{
		".git", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".terraform", "__pycache__",
	}
	if got := ModuleFingerprintIgnoredDirs(); !reflect.DeepEqual(got, wantIgnored) {
		t.Fatalf("ModuleFingerprintIgnoredDirs() = %#v, want %#v", got, wantIgnored)
	}
	for _, directory := range wantIgnored {
		writeFingerprintText(t, filepath.Join(root, directory, "ignored.txt"), directory)
	}
	writeFingerprintText(t, filepath.Join(root, "kept", "file.txt"), "kept")
	got, err := TreeFingerprints(root, nil)
	if err != nil {
		t.Fatalf("TreeFingerprints(%q, nil) error: %v", root, err)
	}
	if len(got) != 1 || got[0][0] != "kept/file.txt" {
		t.Errorf("TreeFingerprints(%q, nil) = %#v, want only kept/file.txt", root, got)
	}
}

func TestLeadingFEFFFilenameBytesArePreserved(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	temp := t.TempDir()
	envDir := filepath.Join(temp, "env")
	moduleDir := filepath.Join(temp, "module")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", envDir, err)
	}
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", moduleDir, err)
	}
	source, err := filepath.Rel(envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, moduleDir, err)
	}
	writeFingerprintText(t, filepath.Join(envDir, "main.tf"), fingerprintModuleBlock("zpa_sample", source, ""))
	writeFingerprintText(t, filepath.Join(envDir, "\ufeffroot.tf"), "# leading FEFF root\n")
	writeFingerprintText(t, filepath.Join(moduleDir, "\ufeffmodule.tf"), "# leading FEFF module\n")
	input := PlanFingerprintInput{EnvDir: envDir, MemberTypes: []string{"zpa_sample"}, VarFiles: []string{}}
	payload, err := CapturePlanSourcesPayload(input, nil)
	if err != nil {
		t.Fatalf("CapturePlanSourcesPayload(%+v, nil) error: %v", input, err)
	}
	if !reflect.DeepEqual(payload, authority.LeadingFEFF.Payload) {
		t.Errorf("CapturePlanSourcesPayload(%+v, nil) = %#v, want frozen %#v", input, payload, authority.LeadingFEFF.Payload)
	}
	if got := CanonicalPlanSourcesJSON(payload); got != authority.LeadingFEFF.Canonical {
		t.Errorf("CanonicalPlanSourcesJSON(payload) = %q, want frozen %q", got, authority.LeadingFEFF.Canonical)
	}
	if got := PlanSourcesSHA256(payload); got != authority.LeadingFEFF.Digest {
		t.Errorf("PlanSourcesSHA256(payload) = %q, want %q", got, authority.LeadingFEFF.Digest)
	}
	initInput := InitFingerprintInput{
		EnvDir:      input.EnvDir,
		MemberTypes: input.MemberTypes,
	}
	initPayload, err := CaptureInitSourcesPayload(initInput, nil)
	if err != nil {
		t.Fatalf("CaptureInitSourcesPayload(%+v, nil) error: %v", initInput, err)
	}
	if !reflect.DeepEqual(initPayload, authority.LeadingFEFF.InitPayload) {
		t.Errorf("CaptureInitSourcesPayload(%+v, nil) = %#v, want frozen %#v", initInput, initPayload, authority.LeadingFEFF.InitPayload)
	}
	if got := InitSourcesSHA256(initPayload); got != authority.LeadingFEFF.InitDigest {
		t.Errorf("InitSourcesSHA256(initPayload) = %q, want %q", got, authority.LeadingFEFF.InitDigest)
	}
}

func TestBackendVarFileAndLocalPathEdgeSemantics(t *testing.T) {
	temp := t.TempDir()
	backend, err := BackendConfigFingerprint(nil, nil, nil)
	if err != nil {
		t.Fatalf("BackendConfigFingerprint(nil, nil, nil) error: %v", err)
	}
	if backend != nil {
		t.Errorf("BackendConfigFingerprint(nil, nil, nil) = %#v, want nil", backend)
	}
	empty := ""
	backend, err = BackendConfigFingerprint(&empty, stringPointer("ignored"), nil)
	if err != nil {
		t.Fatalf("BackendConfigFingerprint(empty, ignored, nil) error: %v", err)
	}
	if backend != nil {
		t.Errorf("BackendConfigFingerprint(empty, ignored, nil) = %#v, want nil", backend)
	}
	missing := filepath.Join(temp, "missing-backend.hcl")
	backend, err = BackendConfigFingerprint(&missing, nil, nil)
	if err != nil {
		t.Fatalf("BackendConfigFingerprint(%q, nil, nil) error: %v", missing, err)
	}
	wantBackend := &BackendFingerprint{Present: false}
	if !reflect.DeepEqual(backend, wantBackend) {
		t.Errorf("BackendConfigFingerprint(%q, nil, nil) = %#v, want %#v", missing, backend, wantBackend)
	}
	varFiles, err := VarFileFingerprints([]string{filepath.Join(temp, "missing.tfvars")}, nil)
	if err != nil {
		t.Fatalf("VarFileFingerprints(missing, nil) error: %v", err)
	}
	if len(varFiles) != 0 {
		t.Errorf("VarFileFingerprints(missing, nil) = %#v, want empty", varFiles)
	}
	tests := []struct {
		source string
		want   string
		local  bool
	}{
		{source: "registry/module/provider", want: "", local: false},
		{source: "", want: "", local: false},
		{source: "./module", want: filepath.Join(temp, "module"), local: true},
		{source: "../module", want: filepath.Join(temp, "..", "module"), local: true},
	}
	for _, test := range tests {
		got, local := LocalModulePath(temp, test.source)
		if got != test.want || local != test.local {
			t.Errorf("LocalModulePath(%q, %q) = (%q, %t), want (%q, %t)", temp, test.source, got, local, test.want, test.local)
		}
	}
}

func TestNilBudgetUsesDefaultForFileReads(t *testing.T) {
	envDir := t.TempDir()
	writeFingerprintText(t, filepath.Join(envDir, "main.tf"), "# root\n")
	got, err := RootTFFingerprints(envDir, nil)
	if err != nil {
		t.Fatalf("RootTFFingerprints(%q, nil) error: %v", envDir, err)
	}
	if len(got) != 1 || got[0][0] != "main.tf" {
		t.Errorf("RootTFFingerprints(%q, nil) = %#v, want one main.tf fingerprint", envDir, got)
	}
}

func TestDirectoryReadFailureHasExactProcessFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := directoryNames(missing, artifacts.NewDefaultReadBudget(), 0)
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("directoryNames(%q, default budget, 0) error = %v, want *procerr.ProcessFailure", missing, err)
	}
	if failure.Code != "DIRECTORY_READ_FAILED" ||
		failure.Category != procerr.CategoryIO ||
		failure.Message != "unable to enumerate fingerprint inputs" {
		t.Errorf("directoryNames(%q, default budget, 0) failure = %#v, want exact DIRECTORY_READ_FAILED/io contract", missing, failure)
	}
}

func TestModuleFingerprintErrorsMatchSourceText(t *testing.T) {
	t.Run("missing_source", func(t *testing.T) {
		envDir := t.TempDir()
		got, err := ModuleFingerprints(envDir, []string{"alpha"}, nil)
		want := envDir + " member alpha has no module source; run make gen-env to regenerate the root"
		if got != nil || err == nil || err.Error() != want {
			t.Errorf("ModuleFingerprints(%q, [alpha], nil) = (%#v, %v), want (nil, %q)", envDir, got, err, want)
		}
	})

	t.Run("remote_source", func(t *testing.T) {
		envDir := t.TempDir()
		writeFingerprintText(t, filepath.Join(envDir, "main.tf"), fingerprintModuleBlock("alpha", "registry/é/alpha", ""))
		got, err := ModuleFingerprints(envDir, []string{"alpha"}, nil)
		want := envDir + ` member alpha module source "registry/é/alpha" is not local; generated roots must use local module sources`
		if got != nil || err == nil || err.Error() != want {
			t.Errorf("ModuleFingerprints(%q, [alpha], nil) = (%#v, %v), want (nil, %q)", envDir, got, err, want)
		}
	})
}

func TestCapturePlanSourcesUsesOneSerialBudget(t *testing.T) {
	temp := t.TempDir()
	envDir := filepath.Join(temp, "env")
	moduleDir := filepath.Join(temp, "module")
	source, err := filepath.Rel(envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, moduleDir, err)
	}
	writeFingerprintText(t, filepath.Join(envDir, "main.tf"), fingerprintModuleBlock("alpha", source, ""))
	writeFingerprintText(t, filepath.Join(moduleDir, "main.tf"), "# module\n")
	limits := artifacts.DefaultBoundedReadLimits()
	limits.MaxFiles = 2
	limits.MaxDirectories = 10
	limits.MaxDirectoryEntries = 100
	limits.MaxDepth = 8
	limits.MaxFileBytes.SetInt64(1024)
	limits.MaxTotalBytes.SetInt64(2048)
	budget, err := artifacts.NewReadBudget(limits)
	if err != nil {
		t.Fatalf("artifacts.NewReadBudget(%+v) error: %v", limits, err)
	}
	_, err = CapturePlanSourcesPayload(PlanFingerprintInput{
		EnvDir:      envDir,
		MemberTypes: []string{"alpha"},
		VarFiles:    []string{},
	}, budget)
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != "FILE_COUNT_EXCEEDED" {
		t.Errorf("CapturePlanSourcesPayload(..., budget) error = %v, want ProcessFailure code FILE_COUNT_EXCEEDED", err)
	}
}

func TestRootTFFingerprintsChargesDirectoryEntriesBeforeFiltering(t *testing.T) {
	envDir := t.TempDir()
	writeFingerprintText(t, filepath.Join(envDir, "ignored-a.txt"), "a")
	writeFingerprintText(t, filepath.Join(envDir, "ignored-b.txt"), "b")
	budget := newFingerprintTestBudget(t, func(limits *artifacts.BoundedReadLimits) {
		limits.MaxDirectoryEntries = 1
	})
	_, err := RootTFFingerprints(envDir, budget)
	requireFingerprintFailureCode(
		t,
		"RootTFFingerprints(envDir with two irrelevant entries, one-entry budget)",
		err,
		"DIRECTORY_ENTRY_LIMIT_EXCEEDED",
	)
	if got := budget.DirectoryEntries(); got != 1 {
		t.Errorf("budget.DirectoryEntries() = %d, want 1 successful pre-filter charge", got)
	}
	if got := budget.Files(); got != 0 {
		t.Errorf("budget.Files() = %d, want 0 when irrelevant entries fail before file filtering", got)
	}
}

func TestRootConfigFingerprintsChargesAllRootInputsBeforeFiltering(t *testing.T) {
	envDir := t.TempDir()
	files := map[string]string{
		".terraform.lock.hcl": "# lock\n",
		"main.tf":             "# config\n",
		"terraform.tfvars":    "value = true\n",
	}
	wantBytes := int64(0)
	for name, content := range files {
		writeFingerprintText(t, filepath.Join(envDir, name), content)
		wantBytes += int64(len(content))
	}

	t.Run("success_charges_filtered_files", func(t *testing.T) {
		budget := artifacts.NewDefaultReadBudget()
		got, err := RootConfigFingerprints(envDir, budget)
		if err != nil {
			t.Fatalf("RootConfigFingerprints(%q, default budget) error: %v", envDir, err)
		}
		if len(got) != 1 || got[0][0] != "main.tf" {
			t.Errorf("RootConfigFingerprints(%q, default budget) = %#v, want only main.tf", envDir, got)
		}
		if got := budget.Files(); got != 3 {
			t.Errorf("budget.Files() = %d, want 3 root-input charges before config filtering", got)
		}
		if got := budget.Bytes().Int64(); got != wantBytes {
			t.Errorf("budget.Bytes().Int64() = %d, want %d bytes hashed before config filtering", got, wantBytes)
		}
	})

	t.Run("tfvars_limit_failure_precedes_filter", func(t *testing.T) {
		budget := newFingerprintTestBudget(t, func(limits *artifacts.BoundedReadLimits) {
			limits.MaxFiles = 2
		})
		_, err := RootConfigFingerprints(envDir, budget)
		requireFingerprintFailureCode(
			t,
			"RootConfigFingerprints(lock, main.tf, tfvars; two-file budget)",
			err,
			"FILE_COUNT_EXCEEDED",
		)
		if got := budget.Files(); got != 2 {
			t.Errorf("budget.Files() = %d, want 2 successful charges before tfvars limit failure", got)
		}
	})
}

func TestTreeFingerprintDirectoryBudgetSemantics(t *testing.T) {
	t.Run("ignored_and_symlink_directories_do_not_enter", func(t *testing.T) {
		root := t.TempDir()
		writeFingerprintText(t, filepath.Join(root, ".git", "ignored.txt"), "ignored")
		target := filepath.Join(t.TempDir(), "target")
		writeFingerprintText(t, filepath.Join(target, "outside.txt"), "outside")
		if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
			t.Fatalf("os.Symlink(%q, linked) error: %v", target, err)
		}
		budget := newFingerprintTestBudget(t, func(limits *artifacts.BoundedReadLimits) {
			limits.MaxDirectories = 1
			limits.MaxDepth = 0
		})
		got, err := TreeFingerprints(root, budget)
		if err != nil {
			t.Fatalf("TreeFingerprints(%q, one-directory/depth-zero budget) error: %v", root, err)
		}
		if len(got) != 0 {
			t.Errorf("TreeFingerprints(%q, one-directory/depth-zero budget) = %#v, want empty", root, got)
		}
		if got := budget.Directories(); got != 1 {
			t.Errorf("budget.Directories() = %d, want only the root directory charged", got)
		}
	})

	t.Run("depth_failure_precedes_directory_count", func(t *testing.T) {
		root := t.TempDir()
		writeFingerprintText(t, filepath.Join(root, "nested", "file.txt"), "nested")
		budget := newFingerprintTestBudget(t, func(limits *artifacts.BoundedReadLimits) {
			limits.MaxDirectories = 1
			limits.MaxDepth = 0
		})
		_, err := TreeFingerprints(root, budget)
		requireFingerprintFailureCode(
			t,
			"TreeFingerprints(real child, one-directory/depth-zero budget)",
			err,
			"DIRECTORY_DEPTH_EXCEEDED",
		)
		if got := budget.Directories(); got != 1 {
			t.Errorf("budget.Directories() = %d, want failed depth charge not to increment the root-only count", got)
		}
	})

	t.Run("directory_count_after_valid_depth", func(t *testing.T) {
		root := t.TempDir()
		writeFingerprintText(t, filepath.Join(root, "nested", "file.txt"), "nested")
		budget := newFingerprintTestBudget(t, func(limits *artifacts.BoundedReadLimits) {
			limits.MaxDirectories = 1
			limits.MaxDepth = 1
		})
		_, err := TreeFingerprints(root, budget)
		requireFingerprintFailureCode(
			t,
			"TreeFingerprints(real child, one-directory/depth-one budget)",
			err,
			"DIRECTORY_COUNT_EXCEEDED",
		)
		if got := budget.Directories(); got != 1 {
			t.Errorf("budget.Directories() = %d, want failed count charge not to increment the root-only count", got)
		}
	})
}

func TestGeneratedRootHCLScannerAcceptanceMatchesFrozenAuthority(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	temp := t.TempDir()
	envDir := filepath.Join(temp, "root")
	writeFingerprintText(t, filepath.Join(envDir, "main.tf"), strings.Join([]string{
		`# module "commented" {`,
		`terraform { required_version = ">= 1.8" }`,
		`module "alpha" {`,
		`  /* source = "remote/ignored" */`,
		`  source = "../modules/alpha" // trailing comment`,
		`  items = local.alpha_items # trailing comment`,
		`}`,
		"",
	}, "\r\n"))
	writeFingerprintText(t, filepath.Join(envDir, "ignored.tf.json"), "not HCL")
	writeFingerprintText(t, filepath.Join(envDir, "python-whitespace.tf"), strings.Join([]string{
		"\u001fmodule\u001f\"beta\"\u001f{",
		"\u001fsource\u001f=\u001f\"../modules/beta\"",
		"\u001fitems\u001f=\u001flocal.beta_items",
		"\u001f}",
		"",
	}, "\n"))
	writeFingerprintText(t, filepath.Join(envDir, "utf8-bom.tf"),
		"\ufeffmodule \"bom_ignored\" {\n  source = \"../modules/bom\"\n  items = var.bom_items\n}\n")
	got, err := RootModuleSources(envDir, nil)
	if err != nil {
		t.Fatalf("RootModuleSources(%q, nil) error: %v", envDir, err)
	}
	if !reflect.DeepEqual(got, authority.Scanner.Accepted.Sources) {
		t.Errorf("RootModuleSources(%q, nil) = %#v, want frozen %#v", envDir, got, authority.Scanner.Accepted.Sources)
	}
}

func TestGeneratedRootHCLScannerFailuresMatchFrozenAuthority(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	cases := []struct {
		name string
		text string
	}{
		{name: "template source", text: fingerprintModuleBlock("alpha", "../modules/$${alpha}", "alpha")},
		{name: "heredoc", text: strings.Join([]string{"value = <<EOF", "text", "EOF", ""}, "\n")},
		{name: "duplicate source", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  source = "../modules/beta"`,
			`  items = var.alpha_items`, `}`, "",
		}, "\n")},
		{name: "duplicate items", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  items = var.alpha_items`,
			`  items = local.alpha_items`, `}`, "",
		}, "\n")},
		{name: "unexpected module field", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  count = 1`,
			`  items = var.alpha_items`, `}`, "",
		}, "\n")},
		{name: "missing items", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `}`, "",
		}, "\n")},
		{name: "unexpected closing brace", text: "}\n"},
		{name: "unbalanced braces", text: "terraform {\n"},
		{name: "unterminated quote", text: "value = \"unterminated\n"},
		{name: "unterminated block comment", text: "/* never closed\n"},
		{name: "unicode line separator line number", text: "# first\u2028value = \"unterminated\n"},
	}
	if len(cases) != len(authority.Scanner.Failures) {
		t.Fatalf("scanner case count = %d, frozen authority count = %d", len(cases), len(authority.Scanner.Failures))
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			frozen := authority.Scanner.Failures[index]
			if frozen.Name != test.name {
				t.Fatalf("scanner frozen case %d name = %q, want %q", index, frozen.Name, test.name)
			}
			envDir := filepath.Join(t.TempDir(), "root")
			filePath := filepath.Join(envDir, "main.tf")
			writeFingerprintText(t, filePath, test.text)
			_, err := RootModuleSources(envDir, nil)
			want := strings.ReplaceAll(frozen.Result.Message, "{env_dir}", envDir)
			if err == nil || err.Error() != want {
				t.Errorf("RootModuleSources(%q, nil) error = %v, want %q", envDir, err, want)
			}
		})
	}
}

func TestDuplicateModulesAcrossRootFilesMatchFrozenAuthority(t *testing.T) {
	authority := loadFingerprintAuthority(t)
	envDir := filepath.Join(t.TempDir(), "root")
	writeFingerprintText(t, filepath.Join(envDir, "a.tf"), fingerprintModuleBlock("alpha", "../modules/alpha", ""))
	writeFingerprintText(t, filepath.Join(envDir, "b.tf"), fingerprintModuleBlock("alpha", "../modules/alpha", ""))
	_, err := RootModuleSources(envDir, nil)
	want := strings.ReplaceAll(authority.Scanner.DuplicateModules.Message, "{env_dir}", envDir)
	if err == nil || err.Error() != want {
		t.Errorf("RootModuleSources(%q, nil) error = %v, want %q", envDir, err, want)
	}
}

func TestLinuxInvalidFilenameBytesFailClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permits non-UTF-8 directory entry bytes")
	}
	authority := loadFingerprintAuthority(t).LinuxInvalidFilename
	wantAuthority := fingerprintAuthorityMetadata{
		Implementation: "cpython",
		Platform:       "linux",
		Python:         "3.13.13",
		Unicode:        "15.1.0",
	}
	if !reflect.DeepEqual(authority.Authority, wantAuthority) {
		t.Fatalf("linux_invalid_filename.authority = %#v, want %#v", authority.Authority, wantAuthority)
	}
	wantResults := []invalidFilenameAuthorityResult{
		{
			AfterDigest:  "f55bfc8f268b952751975428560aa040426782e42c72fd85576163451981b4f5",
			BeforeDigest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
			Kind:         "root file",
		},
		{
			AfterDigest:  "6c29bcfd5f334e3e039a8b9d1865a36c4a8e97b728a842dca8fa7a387a65756f",
			BeforeDigest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
			Kind:         "module file",
		},
		{
			AfterDigest:  "655895ac143b2de15c777d38ab61ce910b72cd37e756b802413cdabacc988212",
			BeforeDigest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
			Kind:         "module directory",
		},
	}
	if !reflect.DeepEqual(authority.Results, wantResults) {
		t.Fatalf("linux_invalid_filename.results = %#v, want %#v", authority.Results, wantResults)
	}
	tests := []struct {
		kind      string
		rootInput bool
		directory bool
	}{
		{kind: "root file", rootInput: true},
		{kind: "module file"},
		{kind: "module directory", directory: true},
	}
	for _, test := range tests {
		t.Run(strings.ReplaceAll(test.kind, " ", "_"), func(t *testing.T) {
			temp := t.TempDir()
			envDir := filepath.Join(temp, "env")
			moduleDir := filepath.Join(temp, "module")
			if err := os.MkdirAll(envDir, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error: %v", envDir, err)
			}
			if err := os.MkdirAll(moduleDir, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error: %v", moduleDir, err)
			}
			writeFingerprintText(t, filepath.Join(moduleDir, "main.tf"), "# module\n")
			source, err := filepath.Rel(envDir, moduleDir)
			if err != nil {
				t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, moduleDir, err)
			}
			writeFingerprintText(t, filepath.Join(envDir, "main.tf"), fingerprintModuleBlock("zpa_sample", source, ""))
			parent := moduleDir
			if test.rootInput {
				parent = envDir
			}
			rawName := append([]byte(parent+"/bad-"), 0xff)
			if !test.directory {
				rawName = append(rawName, []byte(".tf")...)
			}
			if test.directory {
				if err := os.Mkdir(string(rawName), 0o755); err != nil {
					t.Fatalf("os.Mkdir(raw name) error: %v", err)
				}
				writeFingerprintText(t, string(append(append([]byte(nil), rawName...), []byte("/child.tf")...)), "# nested raw path\n")
			} else {
				writeFingerprintText(t, string(rawName), "# raw filename\n")
			}
			var fingerprintErr error
			if test.rootInput {
				_, fingerprintErr = RootTFFingerprints(envDir, nil)
			} else {
				_, fingerprintErr = FingerprintPlanV2(PlanFingerprintInput{
					EnvDir: envDir, MemberTypes: []string{"zpa_sample"}, VarFiles: []string{},
				}, nil)
			}
			var failure *procerr.ProcessFailure
			if !errors.As(fingerprintErr, &failure) || failure.Code != "INVALID_FILENAME_ENCODING" {
				t.Errorf("fingerprint with invalid %s name error = %v, want ProcessFailure code INVALID_FILENAME_ENCODING", test.kind, fingerprintErr)
			}
		})
	}
}
