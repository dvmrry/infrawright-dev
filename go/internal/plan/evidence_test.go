//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type evidenceFixture struct {
	root              string
	envDir            string
	moduleDir         string
	planPath          string
	fingerprintPath   string
	snapshotDirectory string
	fingerprintInput  PlanFingerprintInput
}

func evidenceSourceLimits() artifacts.BoundedReadLimits {
	return artifacts.BoundedReadLimits{
		MaxFiles:            1_000,
		MaxDirectories:      1_000,
		MaxDirectoryEntries: 10_000,
		MaxDepth:            64,
		MaxTotalBytes:       big.NewInt(64 * 1024 * 1024),
		MaxFileBytes:        big.NewInt(8 * 1024 * 1024),
	}
}

func evidencePlanLimits() artifacts.BoundedReadLimits {
	return artifacts.BoundedReadLimits{
		MaxFiles:            16,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       big.NewInt(64 * 1024 * 1024),
		MaxFileBytes:        big.NewInt(32 * 1024 * 1024),
	}
}

func newEvidenceBudget(t *testing.T, limits artifacts.BoundedReadLimits) *artifacts.ReadBudget {
	t.Helper()
	budget, err := artifacts.NewReadBudget(limits)
	if err != nil {
		t.Fatalf("artifacts.NewReadBudget(%+v) error = %v, want nil", limits, err)
	}
	return budget
}

func writeEvidenceFile(t *testing.T, filePath string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filePath, content, mode); err != nil {
		t.Fatalf("os.WriteFile(%q, %d bytes, %#o) error = %v, want nil", filePath, len(content), mode, err)
	}
}

func newEvidenceFixture(t *testing.T) *evidenceFixture {
	t.Helper()
	root := t.TempDir()
	envDir := filepath.Join(root, "envs", "tenant", "zpa_custom")
	moduleDir := filepath.Join(root, "modules", "zpa_segment_group")
	snapshotDirectory := filepath.Join(root, "snapshots")
	for _, directory := range []string{envDir, moduleDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
		}
	}
	if err := os.Mkdir(snapshotDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshotDirectory, err)
	}
	if err := os.Chmod(snapshotDirectory, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", snapshotDirectory, err)
	}

	relativeModule, err := filepath.Rel(envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v, want nil", envDir, moduleDir, err)
	}
	writeEvidenceFile(t, filepath.Join(envDir, "main.tf"), []byte(strings.Join([]string{
		`module "zpa_segment_group" {`,
		fmt.Sprintf(`  source = %q`, filepath.ToSlash(relativeModule)),
		"  items = var.zpa_segment_group_items",
		"}",
		"",
	}, "\n")), 0o600)
	writeEvidenceFile(t, filepath.Join(moduleDir, "main.tf"), []byte("# module\n"), 0o600)

	planPath := filepath.Join(envDir, "tfplan")
	fingerprintPath := filepath.Join(envDir, "tfplan.sources")
	writeEvidenceFile(t, planPath, []byte("opaque-plan-secret-bytes\n"), 0o600)
	fingerprintInput := PlanFingerprintInput{
		EnvDir:      envDir,
		MemberTypes: []string{"zpa_segment_group"},
		VarFiles:    []string{},
	}
	writeCurrentEvidenceFingerprint(t, fingerprintPath, fingerprintInput)
	return &evidenceFixture{
		root:              root,
		envDir:            envDir,
		moduleDir:         moduleDir,
		planPath:          planPath,
		fingerprintPath:   fingerprintPath,
		snapshotDirectory: snapshotDirectory,
		fingerprintInput:  fingerprintInput,
	}
}

func writeCurrentEvidenceFingerprint(
	t *testing.T,
	fingerprintPath string,
	input PlanFingerprintInput,
) {
	t.Helper()
	fingerprint, err := FingerprintPlanV2(input, nil)
	if err != nil {
		t.Fatalf("FingerprintPlanV2(%+v, nil) error = %v, want nil", input, err)
	}
	content := fmt.Sprintf("{\"version\":2,\"sha256\":%q}\n", fingerprint.SHA256)
	writeEvidenceFile(t, fingerprintPath, []byte(content), 0o600)
}

func evidencePrepareOptions(
	t *testing.T,
	fixture *evidenceFixture,
) PrepareSavedPlanEvidenceOptions {
	t.Helper()
	return PrepareSavedPlanEvidenceOptions{
		SavedPlanPath:     fixture.planPath,
		FingerprintPath:   fixture.fingerprintPath,
		FingerprintInput:  cloneFingerprintInput(fixture.fingerprintInput),
		SnapshotDirectory: fixture.snapshotDirectory,
		FingerprintBudget: newEvidenceBudget(t, evidenceSourceLimits()),
		SavedPlanBudget:   newEvidenceBudget(t, evidencePlanLimits()),
	}
}

func prepareEvidence(t *testing.T, fixture *evidenceFixture) *SavedPlanEvidence {
	t.Helper()
	evidence, err := PrepareSavedPlanEvidence(evidencePrepareOptions(t, fixture))
	if err != nil {
		t.Fatalf("PrepareSavedPlanEvidence(%q) error = %v, want nil", fixture.planPath, err)
	}
	return evidence
}

func recheckEvidence(t *testing.T, evidence *SavedPlanEvidence) error {
	t.Helper()
	return RecheckSavedPlanEvidence(RecheckSavedPlanEvidenceOptions{
		Evidence:          evidence,
		FingerprintBudget: newEvidenceBudget(t, evidenceSourceLimits()),
		SavedPlanBudget:   newEvidenceBudget(t, evidencePlanLimits()),
	})
}

func requireEvidenceFailure(
	t *testing.T,
	err error,
	code string,
	category procerr.Category,
	message string,
) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code || failure.Category != category || failure.Message != message {
		t.Errorf(
			"ProcessFailure = {Code:%q Category:%q Message:%q}, want {%q %q %q}",
			failure.Code,
			failure.Category,
			failure.Message,
			code,
			category,
			message,
		)
	}
	return failure
}

func requireEvidenceCode(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q (message %q)", failure.Code, code, failure.Message)
	}
	return failure
}

func evidenceSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func replaceEvidenceFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	replacement := filepath.Join(filepath.Dir(filePath), ".replacement-"+filepath.Base(filePath))
	writeEvidenceFile(t, replacement, content, 0o600)
	if err := os.Rename(replacement, filePath); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", replacement, filePath, err)
	}
}

func TestSavedPlanEvidencePrepareRecheckCleanupAndBudgets(t *testing.T) {
	fixture := newEvidenceFixture(t)
	options := evidencePrepareOptions(t, fixture)
	evidence, err := PrepareSavedPlanEvidence(options)
	if err != nil {
		t.Fatalf("PrepareSavedPlanEvidence(%q) error = %v, want nil", fixture.planPath, err)
	}
	planBytes, err := os.ReadFile(fixture.planPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.planPath, err)
	}
	if got, want := evidence.OriginalPlan.SHA256, evidenceSHA256(planBytes); got != want {
		t.Errorf("SavedPlanEvidence.OriginalPlan.SHA256 = %q, want %q", got, want)
	}
	if got, want := evidence.OriginalPlan.Size, int64(len(planBytes)); got != want {
		t.Errorf("SavedPlanEvidence.OriginalPlan.Size = %d, want %d", got, want)
	}
	if evidence.Snapshot.SHA256 != evidence.OriginalPlan.SHA256 ||
		evidence.Snapshot.Size != evidence.OriginalPlan.Size {
		t.Errorf("snapshot digest = %+v, want original digest %+v", evidence.Snapshot.StableFileDigest, evidence.OriginalPlan.StableFileDigest)
	}
	snapshotInfo, err := os.Lstat(evidence.Snapshot.Path)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error = %v, want nil", evidence.Snapshot.Path, err)
	}
	snapshotIdentity, ok := evidenceFileIdentity(snapshotInfo)
	if !ok || snapshotIdentity != evidence.Snapshot.StableFileIdentity {
		t.Errorf("SavedPlanEvidence.Snapshot identity = %+v, lstat identity = %+v (available %t)", evidence.Snapshot.StableFileIdentity, snapshotIdentity, ok)
	}
	if evidence.FingerprintInput.VarFiles == nil || evidence.FingerprintInput.MemberTypes == nil {
		t.Errorf("SavedPlanEvidence.FingerprintInput slices = %+v, want detached non-nil arrays", evidence.FingerprintInput)
	}
	snapshotBytes, err := os.ReadFile(evidence.Snapshot.Path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if string(snapshotBytes) != string(planBytes) {
		t.Errorf("snapshot bytes = %q, want %q", snapshotBytes, planBytes)
	}

	if got, want := options.SavedPlanBudget.Files(), 4; got != want {
		t.Errorf("PrepareSavedPlanEvidence saved-plan budget files = %d, want %d", got, want)
	}
	if got, want := options.FingerprintBudget.Files(), 8; got != want {
		t.Errorf("PrepareSavedPlanEvidence fingerprint budget files = %d, want %d", got, want)
	}
	if got, want := options.FingerprintBudget.Directories(), 6; got != want {
		t.Errorf("PrepareSavedPlanEvidence fingerprint budget directories = %d, want %d", got, want)
	}
	if got, want := options.FingerprintBudget.DirectoryEntries(), 14; got != want {
		t.Errorf("PrepareSavedPlanEvidence fingerprint budget entries = %d, want %d", got, want)
	}

	// The public view is detached from the private active binding. Mutating a
	// returned slice cannot redirect the bound recheck.
	evidence.FingerprintInput.MemberTypes[0] = "caller_mutation"
	if err := recheckEvidence(t, evidence); err != nil {
		t.Errorf("RecheckSavedPlanEvidence(active evidence) error = %v, want nil", err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Fatalf("CleanupSavedPlanEvidence(active evidence) error = %v, want nil", err)
	}
	info, err := os.Stat(evidence.Snapshot.Path)
	if err != nil {
		t.Fatalf("os.Stat(%q) after cleanup error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if info.Size() != 0 {
		t.Errorf("snapshot size after cleanup = %d, want 0", info.Size())
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("second CleanupSavedPlanEvidence(active evidence) error = %v, want nil", err)
	}
	requireEvidenceFailure(
		t,
		recheckEvidence(t, evidence),
		"INVALID_EVIDENCE_BINDING",
		procerr.CategoryDomain,
		"saved-plan evidence is not active",
	)
}

func TestReadSavedPlanFingerprintContractAndRawBinding(t *testing.T) {
	fixture := newEvidenceFixture(t)
	fingerprint, err := FingerprintPlanV2(fixture.fingerprintInput, nil)
	if err != nil {
		t.Fatalf("FingerprintPlanV2(%+v, nil) error = %v, want nil", fixture.fingerprintInput, err)
	}
	for _, version := range []string{"2", "2.0", "2e0"} {
		t.Run("version_"+strings.ReplaceAll(version, ".", "_"), func(t *testing.T) {
			content := fmt.Sprintf("{\"sha256\":%q,\"version\":%s}\n", fingerprint.SHA256, version)
			writeEvidenceFile(t, fixture.fingerprintPath, []byte(content), 0o600)
			got, err := ReadSavedPlanFingerprint(
				fixture.fingerprintPath,
				newEvidenceBudget(t, evidenceSourceLimits()),
			)
			if err != nil {
				t.Fatalf("ReadSavedPlanFingerprint(version %q) error = %v, want nil", version, err)
			}
			if got.Fingerprint != fingerprint {
				t.Errorf("ReadSavedPlanFingerprint(version %q).Fingerprint = %+v, want %+v", version, got.Fingerprint, fingerprint)
			}
			if got.SHA256 != evidenceSHA256([]byte(content)) || got.Size != int64(len(content)) {
				t.Errorf("raw fingerprint binding = %+v, want sha256 %q size %d", got.StableFileDigest, evidenceSHA256([]byte(content)), len(content))
			}
		})
	}

	cases := []struct {
		name    string
		content string
		code    string
		message string
	}{
		{
			name:    "malformed",
			content: "{not-json",
			code:    "INVALID_PLAN_SOURCES_JSON",
			message: "saved-plan fingerprint is not valid contract JSON",
		},
		{
			name:    "duplicate_key",
			content: fmt.Sprintf("{\"version\":2,\"version\":2,\"sha256\":%q}", fingerprint.SHA256),
			code:    "INVALID_PLAN_SOURCES_JSON",
			message: "saved-plan fingerprint is not valid contract JSON",
		},
		{
			name:    "extra_key",
			content: fmt.Sprintf("{\"version\":2,\"sha256\":%q,\"extra\":true}", fingerprint.SHA256),
			code:    "INVALID_PLAN_SOURCES",
			message: "saved-plan fingerprint does not match the version 2 contract",
		},
		{
			name:    "uppercase_digest",
			content: fmt.Sprintf("{\"version\":2,\"sha256\":%q}", strings.ToUpper(fingerprint.SHA256)),
			code:    "INVALID_PLAN_SOURCES",
			message: "saved-plan fingerprint does not match the version 2 contract",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			writeEvidenceFile(t, fixture.fingerprintPath, []byte(test.content), 0o600)
			_, err := ReadSavedPlanFingerprint(
				fixture.fingerprintPath,
				newEvidenceBudget(t, evidenceSourceLimits()),
			)
			requireEvidenceFailure(t, err, test.code, procerr.CategoryDomain, test.message)
		})
	}
}

func TestSavedPlanEvidenceInvalidationClasses(t *testing.T) {
	t.Run("stale_sources", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		writeEvidenceFile(
			t,
			fixture.fingerprintPath,
			[]byte(fmt.Sprintf("{\"version\":2,\"sha256\":%q}", strings.Repeat("0", 64))),
			0o600,
		)
		_, err := PrepareSavedPlanEvidence(evidencePrepareOptions(t, fixture))
		requireEvidenceFailure(
			t,
			err,
			"STALE_PLAN_SOURCES",
			procerr.CategoryDomain,
			"saved plan does not match the current plan inputs",
		)
	})

	t.Run("sources_file_changed", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		content, err := os.ReadFile(fixture.fingerprintPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.fingerprintPath, err)
		}
		replaceEvidenceFile(t, fixture.fingerprintPath, append([]byte(" \n"), content...))
		requireEvidenceFailure(
			t,
			recheckEvidence(t, evidence),
			"PLAN_SOURCES_CHANGED",
			procerr.CategoryDomain,
			"saved-plan fingerprint changed after evidence was prepared",
		)
	})

	t.Run("current_sources_changed", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		modulePath := filepath.Join(fixture.moduleDir, "main.tf")
		writeEvidenceFile(t, modulePath, []byte("# changed module\n"), 0o600)
		requireEvidenceFailure(
			t,
			recheckEvidence(t, evidence),
			"STALE_PLAN_SOURCES",
			procerr.CategoryDomain,
			"saved plan does not match the current plan inputs",
		)
	})

	for _, mode := range []string{"mutation", "replacement"} {
		t.Run("saved_plan_"+mode, func(t *testing.T) {
			fixture := newEvidenceFixture(t)
			evidence := prepareEvidence(t, fixture)
			if mode == "mutation" {
				writeEvidenceFile(t, fixture.planPath, []byte("changed plan bytes\n"), 0o600)
			} else {
				replaceEvidenceFile(t, fixture.planPath, []byte("replacement plan bytes\n"))
			}
			requireEvidenceFailure(
				t,
				recheckEvidence(t, evidence),
				"SAVED_PLAN_CHANGED",
				procerr.CategoryDomain,
				"saved plan changed after evidence was prepared",
			)
		})
	}

	t.Run("snapshot_changed", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		writeEvidenceFile(t, evidence.Snapshot.Path, []byte("changed snapshot bytes\n"), 0o600)
		requireEvidenceFailure(
			t,
			recheckEvidence(t, evidence),
			"PLAN_SNAPSHOT_CHANGED",
			procerr.CategoryDomain,
			"saved-plan snapshot changed after evidence was prepared",
		)
	})

	t.Run("snapshot_replaced_same_bytes", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		content, err := os.ReadFile(evidence.Snapshot.Path)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", evidence.Snapshot.Path, err)
		}
		replaceEvidenceFile(t, evidence.Snapshot.Path, content)
		requireEvidenceCode(t, recheckEvidence(t, evidence), "PLAN_SNAPSHOT_CHANGED")
	})

	t.Run("snapshot_directory_changed_during_prepare", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		moved := fixture.snapshotDirectory + "-moved"
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				if err := os.Rename(fixture.snapshotDirectory, moved); err != nil {
					return err
				}
				return os.Mkdir(fixture.snapshotDirectory, 0o700)
			},
		})
		failure := requireEvidenceFailure(
			t,
			err,
			"SNAPSHOT_DIRECTORY_CHANGED",
			procerr.CategoryDomain,
			"private snapshot directory changed while evidence was prepared",
		)
		if len(failure.Details) != 1 || failure.Details[0] != (procerr.ErrorDetail{
			Path:    "$",
			Code:    "SNAPSHOT_CLEANUP_FAILED",
			Message: "private saved-plan snapshot cleanup also failed",
		}) {
			t.Errorf("SNAPSHOT_DIRECTORY_CHANGED details = %+v, want one cleanup-failure detail", failure.Details)
		}
	})
}

func TestSavedPlanEvidenceSameByteOriginalAndFingerprintReplacementAccepted(t *testing.T) {
	t.Run("during_prepare", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		planBytes, err := os.ReadFile(fixture.planPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.planPath, err)
		}
		fingerprintBytes, err := os.ReadFile(fixture.fingerprintPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.fingerprintPath, err)
		}
		evidence, err := prepareSavedPlanEvidence(evidencePrepareOptions(t, fixture), evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				replaceEvidenceFile(t, fixture.planPath, planBytes)
				replaceEvidenceFile(t, fixture.fingerprintPath, fingerprintBytes)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("PrepareSavedPlanEvidence(same-byte replacements) error = %v, want nil", err)
		}
		if err := CleanupSavedPlanEvidence(evidence); err != nil {
			t.Errorf("CleanupSavedPlanEvidence(same-byte prepare replacements) error = %v, want nil", err)
		}
	})

	t.Run("during_recheck", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		planBytes, err := os.ReadFile(fixture.planPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.planPath, err)
		}
		fingerprintBytes, err := os.ReadFile(fixture.fingerprintPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.fingerprintPath, err)
		}
		replaceEvidenceFile(t, fixture.planPath, planBytes)
		replaceEvidenceFile(t, fixture.fingerprintPath, fingerprintBytes)
		if err := recheckEvidence(t, evidence); err != nil {
			t.Errorf("RecheckSavedPlanEvidence(same-byte replacements) error = %v, want nil", err)
		}
		if err := CleanupSavedPlanEvidence(evidence); err != nil {
			t.Errorf("CleanupSavedPlanEvidence(same-byte recheck replacements) error = %v, want nil", err)
		}
	})
}

func TestSavedPlanEvidenceExactObjectBinding(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	copyValue := *evidence
	copyPointer := &copyValue
	requireEvidenceFailure(
		t,
		recheckEvidence(t, copyPointer),
		"INVALID_EVIDENCE_BINDING",
		procerr.CategoryDomain,
		"saved-plan evidence is not active",
	)
	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(copyPointer),
		"INVALID_SNAPSHOT_BINDING",
		procerr.CategoryDomain,
		"saved-plan snapshot has no active cleanup binding",
	)
	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(&SavedPlanEvidence{Snapshot: evidence.Snapshot}),
		"INVALID_SNAPSHOT_BINDING",
		procerr.CategoryDomain,
		"saved-plan snapshot has no active cleanup binding",
	)
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(original exact object) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceAbsolutePathsAreLexical(t *testing.T) {
	fixture := newEvidenceFixture(t)
	backendEmpty := ""
	cases := []struct {
		name   string
		mutate func(*PrepareSavedPlanEvidenceOptions)
	}{
		{name: "saved_plan", mutate: func(options *PrepareSavedPlanEvidenceOptions) { options.SavedPlanPath = "relative.tfplan" }},
		{name: "fingerprint", mutate: func(options *PrepareSavedPlanEvidenceOptions) { options.FingerprintPath = "relative.sources" }},
		{name: "snapshot_directory", mutate: func(options *PrepareSavedPlanEvidenceOptions) { options.SnapshotDirectory = "snapshots" }},
		{name: "environment", mutate: func(options *PrepareSavedPlanEvidenceOptions) { options.FingerprintInput.EnvDir = "env" }},
		{name: "var_file", mutate: func(options *PrepareSavedPlanEvidenceOptions) {
			options.FingerprintInput.VarFiles = []string{"vars.tfvars"}
		}},
		{name: "backend_empty", mutate: func(options *PrepareSavedPlanEvidenceOptions) { options.FingerprintInput.BackendConfig = &backendEmpty }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := evidencePrepareOptions(t, fixture)
			test.mutate(&options)
			_, err := PrepareSavedPlanEvidence(options)
			requireEvidenceFailure(
				t,
				err,
				"UNRESOLVED_EVIDENCE_PATH",
				procerr.CategoryDomain,
				"saved-plan evidence requires resolved absolute paths",
			)
		})
	}

	options := evidencePrepareOptions(t, fixture)
	lexicalEnv := fixture.envDir + "/../" + filepath.Base(fixture.envDir)
	options.SavedPlanPath = lexicalEnv + "/tfplan"
	options.FingerprintPath = lexicalEnv + "/tfplan.sources"
	options.FingerprintInput.EnvDir = lexicalEnv
	options.SnapshotDirectory = fixture.snapshotDirectory + "/../snapshots"
	for name, value := range map[string]string{
		"saved plan":         options.SavedPlanPath,
		"fingerprint":        options.FingerprintPath,
		"environment":        options.FingerprintInput.EnvDir,
		"snapshot directory": options.SnapshotDirectory,
	} {
		if !strings.Contains(value, "/../") {
			t.Fatalf("%s test path = %q, want an absolute lexical path containing /../", name, value)
		}
	}
	evidence, err := PrepareSavedPlanEvidence(options)
	if err != nil {
		t.Fatalf("PrepareSavedPlanEvidence(absolute lexical .. paths) error = %v, want nil", err)
	}
	if evidence.OriginalPlan.Path != options.SavedPlanPath || evidence.FingerprintPath != options.FingerprintPath {
		t.Errorf("evidence paths = {%q %q}, want lexical spellings {%q %q}", evidence.OriginalPlan.Path, evidence.FingerprintPath, options.SavedPlanPath, options.FingerprintPath)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(lexical paths) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceDiagnosticsSanitizeSecretsAndPaths(t *testing.T) {
	fixture := newEvidenceFixture(t)
	plantedSecret := "super-secret-plan-token-7f6b"
	plantedPath := filepath.Join(fixture.root, "secret-tenant-name")
	content := fmt.Sprintf(
		"{\"version\":2,\"sha256\":%q,\"path\":%q}",
		plantedSecret,
		plantedPath,
	)
	writeEvidenceFile(t, fixture.fingerprintPath, []byte(content), 0o600)
	_, err := PrepareSavedPlanEvidence(evidencePrepareOptions(t, fixture))
	failure := requireEvidenceCode(t, err, "INVALID_PLAN_SOURCES")
	diagnostic := fmt.Sprintf("%s %s %+v", failure.Code, failure.Message, failure.Details)
	for _, forbidden := range []string{plantedSecret, plantedPath, fixture.root} {
		if strings.Contains(diagnostic, forbidden) {
			t.Errorf("invalid fingerprint diagnostic %q contains forbidden value %q", diagnostic, forbidden)
		}
	}

	writeCurrentEvidenceFingerprint(t, fixture.fingerprintPath, fixture.fingerprintInput)
	options := evidencePrepareOptions(t, fixture)
	options.FingerprintInput.MemberTypes = []string{"zpa_" + plantedSecret}
	_, err = PrepareSavedPlanEvidence(options)
	failure = requireEvidenceFailure(
		t,
		err,
		"SOURCE_FINGERPRINT_FAILED",
		procerr.CategoryDomain,
		"unable to fingerprint current plan inputs",
	)
	diagnostic = fmt.Sprintf("%s %s %+v", failure.Code, failure.Message, failure.Details)
	for _, forbidden := range []string{plantedSecret, fixture.root} {
		if strings.Contains(diagnostic, forbidden) {
			t.Errorf("current-source diagnostic %q contains forbidden value %q", diagnostic, forbidden)
		}
	}
}

func TestSavedPlanEvidenceRemainingExactFailureContracts(t *testing.T) {
	t.Run("missing_snapshot_directory", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		options.SnapshotDirectory = filepath.Join(fixture.root, "missing-snapshots")
		_, err := PrepareSavedPlanEvidence(options)
		requireEvidenceFailure(
			t,
			err,
			"UNSAFE_SNAPSHOT_DIRECTORY",
			procerr.CategoryDomain,
			"unable to bind the private snapshot directory",
		)
	})

	t.Run("snapshot_directory_is_file", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		path := filepath.Join(fixture.root, "not-a-directory")
		writeEvidenceFile(t, path, []byte("file\n"), 0o600)
		options := evidencePrepareOptions(t, fixture)
		options.SnapshotDirectory = path
		_, err := PrepareSavedPlanEvidence(options)
		requireEvidenceFailure(
			t,
			err,
			"UNSAFE_SNAPSHOT_DIRECTORY",
			procerr.CategoryDomain,
			"snapshot directory is not a stable private directory",
		)
	})

	t.Run("saved_plan_changed_during_prepare", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		_, err := prepareSavedPlanEvidence(evidencePrepareOptions(t, fixture), evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
				return nil
			},
		})
		requireEvidenceFailure(
			t,
			err,
			"SAVED_PLAN_CHANGED",
			procerr.CategoryDomain,
			"saved plan changed while evidence was prepared",
		)
	})

	t.Run("fingerprint_changed_during_prepare", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		_, err := prepareSavedPlanEvidence(evidencePrepareOptions(t, fixture), evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				content, readErr := os.ReadFile(fixture.fingerprintPath)
				if readErr != nil {
					return readErr
				}
				writeEvidenceFile(t, fixture.fingerprintPath, append([]byte(" \n"), content...), 0o600)
				return nil
			},
		})
		requireEvidenceFailure(
			t,
			err,
			"PLAN_SOURCES_CHANGED",
			procerr.CategoryDomain,
			"saved-plan fingerprint changed while evidence was prepared",
		)
	})
}

func TestSavedPlanEvidenceBudgetFailureOrderIsDeterministic(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		limits := evidencePlanLimits()
		limits.MaxFiles = 3
		options.SavedPlanBudget = newEvidenceBudget(t, limits)
		_, err := PrepareSavedPlanEvidence(options)
		requireEvidenceFailure(
			t,
			err,
			"FILE_COUNT_EXCEEDED",
			procerr.CategoryIO,
			"input exceeds the configured file-count limit",
		)
		if got, want := options.SavedPlanBudget.Files(), 3; got != want {
			t.Errorf("attempt %d saved-plan budget files = %d, want %d", attempt, got, want)
		}
	}
}

func TestSavedPlanEvidencePreparationCompositeCleanupFailures(t *testing.T) {
	t.Run("process_failure_preserved_with_detail", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		moved := fixture.snapshotDirectory + "-moved"
		primary := procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:      "PRIMARY_FAILURE",
			Category:  procerr.CategoryRequest,
			Message:   "primary safe message",
			Retryable: true,
			Details: []procerr.ErrorDetail{{
				Path:    "$.input",
				Code:    "PRIMARY_DETAIL",
				Message: "safe detail",
			}},
		})
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				if err := os.Rename(fixture.snapshotDirectory, moved); err != nil {
					return err
				}
				if err := os.Mkdir(fixture.snapshotDirectory, 0o700); err != nil {
					return err
				}
				return primary
			},
		})
		failure := requireEvidenceFailure(t, err, "PRIMARY_FAILURE", procerr.CategoryRequest, "primary safe message")
		if !failure.Retryable {
			t.Error("composite ProcessFailure.Retryable = false, want true")
		}
		wantDetails := []procerr.ErrorDetail{
			{Path: "$.input", Code: "PRIMARY_DETAIL", Message: "safe detail"},
			{Path: "$", Code: "SNAPSHOT_CLEANUP_FAILED", Message: "private saved-plan snapshot cleanup also failed"},
		}
		if !slices.Equal(failure.Details, wantDetails) {
			t.Errorf("composite ProcessFailure.Details = %+v, want %+v", failure.Details, wantDetails)
		}
	})

	t.Run("generic_dual_failure_collapses", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		moved := fixture.snapshotDirectory + "-moved"
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				if err := os.Rename(fixture.snapshotDirectory, moved); err != nil {
					return err
				}
				if err := os.Mkdir(fixture.snapshotDirectory, 0o700); err != nil {
					return err
				}
				return errors.New("generic planted secret")
			},
		})
		failure := requireEvidenceFailure(
			t,
			err,
			"EVIDENCE_PREPARATION_AND_CLEANUP_FAILED",
			procerr.CategoryIO,
			"saved-plan evidence preparation and private cleanup failed",
		)
		if strings.Contains(failure.Message, "planted") || len(failure.Details) != 0 {
			t.Errorf("generic dual failure = %+v, want sanitized empty-detail failure", failure)
		}
	})
}

func TestSavedPlanEvidencePrepareOrdering(t *testing.T) {
	t.Run("snapshot_before_fingerprint_and_original", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(snapshot artifacts.StableFileSnapshot) error {
				writeEvidenceFile(t, snapshot.Path, []byte("changed snapshot\n"), 0o600)
				writeEvidenceFile(t, fixture.fingerprintPath, []byte("{not-json"), 0o600)
				writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
				return nil
			},
		})
		requireEvidenceCode(t, err, "PLAN_SNAPSHOT_CHANGED")
	})

	t.Run("fingerprint_before_current_and_original", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				content, readErr := os.ReadFile(fixture.fingerprintPath)
				if readErr != nil {
					return readErr
				}
				writeEvidenceFile(t, fixture.fingerprintPath, append([]byte(" \n"), content...), 0o600)
				writeEvidenceFile(t, filepath.Join(fixture.moduleDir, "main.tf"), []byte("# changed source\n"), 0o600)
				writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
				return nil
			},
		})
		requireEvidenceCode(t, err, "PLAN_SOURCES_CHANGED")
	})

	t.Run("current_before_original", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		options := evidencePrepareOptions(t, fixture)
		_, err := prepareSavedPlanEvidence(options, evidenceHooks{
			afterSnapshotIdentity: func(artifacts.StableFileSnapshot) error {
				writeEvidenceFile(t, filepath.Join(fixture.moduleDir, "main.tf"), []byte("# changed source\n"), 0o600)
				writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
				return nil
			},
		})
		requireEvidenceCode(t, err, "STALE_PLAN_SOURCES")
	})
}

func TestSavedPlanEvidenceRecheckOrdering(t *testing.T) {
	t.Run("snapshot_identity_before_original", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		snapshotBytes, err := os.ReadFile(evidence.Snapshot.Path)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", evidence.Snapshot.Path, err)
		}
		replaceEvidenceFile(t, evidence.Snapshot.Path, snapshotBytes)
		writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
		requireEvidenceCode(t, recheckEvidence(t, evidence), "PLAN_SNAPSHOT_CHANGED")
	})

	t.Run("original_before_fingerprint", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		writeEvidenceFile(t, fixture.planPath, []byte("changed original\n"), 0o600)
		writeEvidenceFile(t, fixture.fingerprintPath, []byte("{not-json"), 0o600)
		requireEvidenceCode(t, recheckEvidence(t, evidence), "SAVED_PLAN_CHANGED")
	})

	t.Run("fingerprint_before_current", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		content, err := os.ReadFile(fixture.fingerprintPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", fixture.fingerprintPath, err)
		}
		writeEvidenceFile(t, fixture.fingerprintPath, append([]byte(" \n"), content...), 0o600)
		writeEvidenceFile(t, filepath.Join(fixture.moduleDir, "main.tf"), []byte("# changed source\n"), 0o600)
		requireEvidenceCode(t, recheckEvidence(t, evidence), "PLAN_SOURCES_CHANGED")
	})

	t.Run("current_before_snapshot_digest", func(t *testing.T) {
		fixture := newEvidenceFixture(t)
		evidence := prepareEvidence(t, fixture)
		writeEvidenceFile(t, filepath.Join(fixture.moduleDir, "main.tf"), []byte("# changed source\n"), 0o600)
		writeEvidenceFile(t, evidence.Snapshot.Path, []byte("changed snapshot\n"), 0o600)
		requireEvidenceCode(t, recheckEvidence(t, evidence), "STALE_PLAN_SOURCES")
	})
}
