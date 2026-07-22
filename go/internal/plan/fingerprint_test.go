package plan

import (
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

func TestFingerprintV2PayloadAndDigestMatchFixedContract(t *testing.T) {
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
	backendSHA := "8dd324f94a0cba2bdddcabf4b5af10ca1938274116d8524dc1f061db45ba9cea"
	wantBackend := &BackendFingerprint{Key: &backendKey, Present: true, SHA256: &backendSHA}
	wantModules := []ModuleFingerprint{
		{
			Files: []FileFingerprint{
				{"linked-main.tf", "b949b9ff0951ebc9090bdfee5e3f6c2c810b7ad666cd8ad240ae6045b9024b41"},
				{"main.tf", "b949b9ff0951ebc9090bdfee5e3f6c2c810b7ad666cd8ad240ae6045b9024b41"},
				{"nested/binary.bin", "26a66b061e8f48f39927c312f25293959729eee95978e2892d49d3512a5cc092"},
			},
			Local: true, Present: true, ResourceType: firstType, Source: "../../../modules/segment-\x7f-é-😀",
		},
		{Files: []FileFingerprint{}, Local: true, Present: false, ResourceType: secondType, Source: "../../../modules/missing-server-group"},
	}
	wantPayload := PlanSourcesPayload{
		Backend:     wantBackend,
		MemberTypes: []string{firstType, secondType},
		Modules:     wantModules,
		RootTF: []FileFingerprint{
			{".terraform.lock.hcl", "718475cd179c5fce5f3cbaf68fb45b15017015ebeec359c040cf704e3f1c86b6"},
			{"a.auto.tfvars", "cb78bd8a17f7b751fe0d4663366dcbc257204033ef7ddd64b1f2969573b5b2e2"},
			{"b.auto.tfvars.json", "651b5768de252a9f4d2083046d83f81c31369beb73d14411492b20ea8fd1fcf5"},
			{"linked.tf", "dfc51e9bb51789b3470e435fa6268343f4983afbc4581bcc71d5115ce1723a33"},
			{"main.tf", "519a99a232e663b96326012ecfd66d4d7c9b51f7958549ea31878d37accf6b1b"},
			{"providers.tf", "6f908af107c30c64af46b456482e8c53e2afe3d528700d139a01fe8c4607d003"},
			{"terraform.tfvars", "5a1a948fb3fd8d68bee00abb4d6d05b6437033cf330d0b1b90f62c79622b41e6"},
			{"terraform.tfvars.json", "276b788e4bdad7cb58761dd279d04bae9f3768994de1cf4bef198a8e977d0782"},
			{"é-\x7f.tf.json", "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356"},
		},
		VarFiles: []FileFingerprint{
			{"shared.auto.tfvars.json", "651b5768de252a9f4d2083046d83f81c31369beb73d14411492b20ea8fd1fcf5"},
			{"shared.auto.tfvars.json", "e346432021b04179518d9614f3560ccd71354a4ee101ddcb893d6959a9d6301c"},
			{"vars-\x7f-é.auto.tfvars.json", "867e933df8d9ec57739dff826e4e3caecaf550fe521a266a1065f1218c77de65"},
		},
	}
	wantInitPayload := InitSourcesPayload{
		Backend: wantBackend,
		Modules: wantModules,
		RootConfig: []FileFingerprint{
			{"linked.tf", "dfc51e9bb51789b3470e435fa6268343f4983afbc4581bcc71d5115ce1723a33"},
			{"main.tf", "519a99a232e663b96326012ecfd66d4d7c9b51f7958549ea31878d37accf6b1b"},
			{"providers.tf", "6f908af107c30c64af46b456482e8c53e2afe3d528700d139a01fe8c4607d003"},
			{"é-\x7f.tf.json", "ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356"},
		},
	}
	if !reflect.DeepEqual(payload, wantPayload) {
		t.Errorf("CapturePlanSourcesPayload(%+v, nil) = %#v, want fixed %#v", input, payload, wantPayload)
	}
	if !reflect.DeepEqual(initPayload, wantInitPayload) {
		t.Errorf("CaptureInitSourcesPayload(%+v, nil) = %#v, want fixed %#v", input, initPayload, wantInitPayload)
	}
	wantCanonical := `{"backend":{"key":"tenant/zpa-\u007f-\u00e9-\ud83d\ude00.tfstate","present":true,"sha256":"8dd324f94a0cba2bdddcabf4b5af10ca1938274116d8524dc1f061db45ba9cea"},"member_types":["zpa_segment_group","zpa_server_group"],"modules":[{"files":[["linked-main.tf","b949b9ff0951ebc9090bdfee5e3f6c2c810b7ad666cd8ad240ae6045b9024b41"],["main.tf","b949b9ff0951ebc9090bdfee5e3f6c2c810b7ad666cd8ad240ae6045b9024b41"],["nested/binary.bin","26a66b061e8f48f39927c312f25293959729eee95978e2892d49d3512a5cc092"]],"local":true,"present":true,"resource_type":"zpa_segment_group","source":"../../../modules/segment-\u007f-\u00e9-\ud83d\ude00"},{"files":[],"local":true,"present":false,"resource_type":"zpa_server_group","source":"../../../modules/missing-server-group"}],"root_tf":[[".terraform.lock.hcl","718475cd179c5fce5f3cbaf68fb45b15017015ebeec359c040cf704e3f1c86b6"],["a.auto.tfvars","cb78bd8a17f7b751fe0d4663366dcbc257204033ef7ddd64b1f2969573b5b2e2"],["b.auto.tfvars.json","651b5768de252a9f4d2083046d83f81c31369beb73d14411492b20ea8fd1fcf5"],["linked.tf","dfc51e9bb51789b3470e435fa6268343f4983afbc4581bcc71d5115ce1723a33"],["main.tf","519a99a232e663b96326012ecfd66d4d7c9b51f7958549ea31878d37accf6b1b"],["providers.tf","6f908af107c30c64af46b456482e8c53e2afe3d528700d139a01fe8c4607d003"],["terraform.tfvars","5a1a948fb3fd8d68bee00abb4d6d05b6437033cf330d0b1b90f62c79622b41e6"],["terraform.tfvars.json","276b788e4bdad7cb58761dd279d04bae9f3768994de1cf4bef198a8e977d0782"],["\u00e9-\u007f.tf.json","ca3d163bab055381827226140568f3bef7eaac187cebd76878e0b63e9e442356"]],"var_files":[["shared.auto.tfvars.json","651b5768de252a9f4d2083046d83f81c31369beb73d14411492b20ea8fd1fcf5"],["shared.auto.tfvars.json","e346432021b04179518d9614f3560ccd71354a4ee101ddcb893d6959a9d6301c"],["vars-\u007f-\u00e9.auto.tfvars.json","867e933df8d9ec57739dff826e4e3caecaf550fe521a266a1065f1218c77de65"]]}`
	if got := CanonicalPlanSourcesJSON(payload); got != wantCanonical {
		t.Errorf("CanonicalPlanSourcesJSON(payload) = %q, want fixed %q", got, wantCanonical)
	}
	const wantDigest = "c601a941a2e1b868320b5a19da38cd1c97b883d5d31facfd5b1527771e74d676"
	if got := PlanSourcesSHA256(payload); got != wantDigest {
		t.Errorf("PlanSourcesSHA256(payload) = %q, want fixed %q", got, wantDigest)
	}
	const wantInitDigest = "7ee0107752451c2e207caa4d91a37234cfc88850886a4ad9c97c0444afc8449d"
	if got := InitSourcesSHA256(initPayload); got != wantInitDigest {
		t.Errorf("InitSourcesSHA256(initPayload) = %q, want fixed %q", got, wantInitDigest)
	}
	fingerprint, err := FingerprintPlanV2(input, nil)
	if err != nil {
		t.Fatalf("FingerprintPlanV2(%+v, nil) error: %v", input, err)
	}
	wantFingerprint := PlanFingerprintV2{Version: PlanFingerprintVersion, SHA256: wantDigest}
	if fingerprint != wantFingerprint {
		t.Errorf("FingerprintPlanV2(%+v, nil) = %#v, want %#v", input, fingerprint, wantFingerprint)
	}
	sources, err := RootModuleSources(envDir, nil)
	if err != nil {
		t.Fatalf("RootModuleSources(%q, nil) error: %v", envDir, err)
	}
	wantSources := map[string]string{
		firstType:  filepath.ToSlash(firstSource),
		secondType: filepath.ToSlash(secondSource),
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("RootModuleSources(%q, nil) = %#v, want %#v", envDir, sources, wantSources)
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
	want := []FileFingerprint{{"file.txt", "434728a410a78f56fc1b5899c3593436e61ab0c731e9072d95e96db290205e53"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TreeFingerprints(%q, nil) = %#v, want %#v", link, got, want)
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
	if got, want := payload.RootTF[1][0], "\ufeffroot.tf"; got != want {
		t.Errorf("CapturePlanSourcesPayload(%+v, nil).RootTF[1].path = %q, want %q", input, got, want)
	}
	if got, want := payload.Modules[0].Files[0][0], "\ufeffmodule.tf"; got != want {
		t.Errorf("CapturePlanSourcesPayload(%+v, nil).Modules[0].Files[0].path = %q, want %q", input, got, want)
	}
	initInput := InitFingerprintInput{
		EnvDir:      input.EnvDir,
		MemberTypes: input.MemberTypes,
	}
	initPayload, err := CaptureInitSourcesPayload(initInput, nil)
	if err != nil {
		t.Fatalf("CaptureInitSourcesPayload(%+v, nil) error: %v", initInput, err)
	}
	if !reflect.DeepEqual(initPayload.Modules, payload.Modules) || !reflect.DeepEqual(initPayload.RootConfig, payload.RootTF) {
		t.Errorf("CaptureInitSourcesPayload(%+v, nil) modules/root = %#v/%#v, want plan modules/root %#v/%#v", initInput, initPayload.Modules, initPayload.RootConfig, payload.Modules, payload.RootTF)
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

func TestGeneratedRootHCLScannerAcceptsCurrentRootGrammar(t *testing.T) {
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
	writeFingerprintText(t, filepath.Join(envDir, "unicode-whitespace.tf"), strings.Join([]string{
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
	want := map[string]string{"alpha": "../modules/alpha", "beta": "../modules/beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RootModuleSources(%q, nil) = %#v, want %#v", envDir, got, want)
	}
}

func TestGeneratedRootHCLScannerRejectsUnsupportedGrammar(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{name: "template source", text: fingerprintModuleBlock("alpha", "../modules/$${alpha}", "alpha"), want: "/main.tf:2 module alpha source uses HCL template syntax outside the generated-root contract; run make gen-env to regenerate the root"},
		{name: "heredoc", text: strings.Join([]string{"value = <<EOF", "text", "EOF", ""}, "\n"), want: "/main.tf:1 contains a heredoc outside the generated-root contract; run make gen-env to regenerate the root"},
		{name: "duplicate source", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  source = "../modules/beta"`,
			`  items = var.alpha_items`, `}`, "",
		}, "\n"), want: "/main.tf:3 module alpha has multiple source values"},
		{name: "duplicate items", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  items = var.alpha_items`,
			`  items = local.alpha_items`, `}`, "",
		}, "\n"), want: "/main.tf:4 module alpha has multiple items values"},
		{name: "unexpected module field", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `  count = 1`,
			`  items = var.alpha_items`, `}`, "",
		}, "\n"), want: "/main.tf:3 module alpha is outside the generated-root contract; run make gen-env to regenerate the root"},
		{name: "missing items", text: strings.Join([]string{
			`module "alpha" {`, `  source = "../modules/alpha"`, `}`, "",
		}, "\n"), want: "/main.tf module alpha is outside the generated-root contract; run make gen-env to regenerate the root"},
		{name: "unexpected closing brace", text: "}\n", want: "/main.tf:1 has an unexpected closing brace"},
		{name: "unbalanced braces", text: "terraform {\n", want: "/main.tf has unbalanced braces"},
		{name: "unterminated quote", text: "value = \"unterminated\n", want: "/main.tf:1 contains an unterminated quoted string"},
		{name: "unterminated block comment", text: "/* never closed\n", want: "/main.tf contains an unterminated block comment"},
		{name: "unicode line separator line number", text: "# first\u2028value = \"unterminated\n", want: "/main.tf:2 contains an unterminated quoted string"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			envDir := filepath.Join(t.TempDir(), "root")
			filePath := filepath.Join(envDir, "main.tf")
			writeFingerprintText(t, filePath, test.text)
			_, err := RootModuleSources(envDir, nil)
			want := envDir + test.want
			if err == nil || err.Error() != want {
				t.Errorf("RootModuleSources(%q, nil) error = %v, want %q", envDir, err, want)
			}
		})
	}
}

func TestDuplicateModulesAcrossRootFilesAreRejected(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "root")
	writeFingerprintText(t, filepath.Join(envDir, "a.tf"), fingerprintModuleBlock("alpha", "../modules/alpha", ""))
	writeFingerprintText(t, filepath.Join(envDir, "b.tf"), fingerprintModuleBlock("alpha", "../modules/alpha", ""))
	_, err := RootModuleSources(envDir, nil)
	want := envDir + " contains duplicate module alpha"
	if err == nil || err.Error() != want {
		t.Errorf("RootModuleSources(%q, nil) error = %v, want %q", envDir, err, want)
	}
}

func TestLinuxInvalidFilenameBytesFailClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permits non-UTF-8 directory entry bytes")
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
