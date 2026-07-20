package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func policyBudget(t *testing.T, maxFiles int) *artifacts.ReadBudget {
	t.Helper()
	budget, err := artifacts.NewReadBudget(artifacts.BoundedReadLimits{
		MaxFiles:            maxFiles,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       big.NewInt(1024 * 1024),
		MaxFileBytes:        big.NewInt(1024 * 1024),
	})
	if err != nil {
		t.Fatalf("artifacts.NewReadBudget(policy limits) error = %v, want nil", err)
	}
	return budget
}

func writePolicyFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", filePath, err)
	}
}

func requireAssessmentFailure(
	t *testing.T,
	err error,
	code string,
) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestLoadAndRecheckBoundDriftPolicy(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.json")
	content := []byte(`{"version":1,"resource_types":{"zpa_sample":{"plan_tolerate":[` +
		`{"path":"status","reason":"provider read normalization","approved_by":"owner"}]}}}`)
	writePolicyFile(t, policyPath, content)
	pathInput := policyPath
	loadBudget := policyBudget(t, 4)
	bound, err := LoadBoundDriftPolicy(&pathInput, loadBudget)
	if err != nil {
		t.Fatalf("LoadBoundDriftPolicy(%q) error = %v, want nil", policyPath, err)
	}
	pathInput = "mutated"
	if bound.Path == nil || *bound.Path != policyPath {
		t.Fatalf("LoadBoundDriftPolicy(%q).Path = %v, want detached %q", policyPath, bound.Path, policyPath)
	}
	wantHash := sha256.Sum256(content)
	wantDigest := artifacts.StableFileDigest{
		SHA256: hex.EncodeToString(wantHash[:]),
		Size:   int64(len(content)),
	}
	if bound.File == nil || *bound.File != wantDigest {
		t.Errorf("LoadBoundDriftPolicy(%q).File = %+v, want %+v", policyPath, bound.File, wantDigest)
	}
	if bound.Policy == nil || !bound.Policy.ToleratesPlanPath("zpa_sample", []any{"status"}, "update") {
		t.Errorf("LoadBoundDriftPolicy(%q).Policy does not tolerate zpa_sample.status update", policyPath)
	}
	if got, want := loadBudget.Files(), 1; got != want {
		t.Errorf("LoadBoundDriftPolicy(%q) budget files = %d, want %d", policyPath, got, want)
	}
	if err := RecheckBoundDriftPolicy(bound, policyBudget(t, 4)); err != nil {
		t.Fatalf("RecheckBoundDriftPolicy(unchanged %q) error = %v, want nil", policyPath, err)
	}

	replacement := filepath.Join(root, "replacement")
	writePolicyFile(t, replacement, []byte(`{"version":1,"resource_types":{}}`))
	if err := os.Rename(replacement, policyPath); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", replacement, policyPath, err)
	}
	failure := requireAssessmentFailure(
		t,
		RecheckBoundDriftPolicy(bound, policyBudget(t, 4)),
		"DRIFT_POLICY_CHANGED",
	)
	if failure.Category != procerr.CategoryDomain ||
		failure.Message != "saved-plan drift policy changed during assessment" {
		t.Errorf("RecheckBoundDriftPolicy(changed) failure = %+v, want redacted domain failure", failure)
	}
}

func TestLoadBoundDriftPolicyRejectsInvalidContentWithoutLeakage(t *testing.T) {
	root := t.TempDir()
	secret := "policy-secret-c329"
	tests := []struct {
		name        string
		content     []byte
		wantMessage string
	}{
		{
			name:        "invalid_policy",
			content:     []byte(`{"version":1,"` + secret + `":true}`),
			wantMessage: "saved-plan drift policy is invalid",
		},
		{
			name:        "invalid_utf8",
			content:     []byte{0xff, 's', 'e', 'c', 'r', 'e', 't'},
			wantMessage: "saved-plan drift policy is invalid",
		},
		{
			name:        "syntax",
			content:     []byte(`{"version":`),
			wantMessage: "Expecting value: line 1 column 12 (char 11)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policyPath := filepath.Join(root, test.name+".json")
			writePolicyFile(t, policyPath, test.content)
			_, err := LoadBoundDriftPolicy(&policyPath, policyBudget(t, 4))
			var loadFailure *DriftPolicyLoadFailure
			if !errors.As(err, &loadFailure) {
				t.Fatalf("LoadBoundDriftPolicy(%q) error = %T(%v), want *DriftPolicyLoadFailure", policyPath, err, err)
			}
			failure := requireAssessmentFailure(t, err, "INVALID_DRIFT_POLICY")
			if failure.Category != procerr.CategoryDomain || failure.Message != test.wantMessage {
				t.Errorf("LoadBoundDriftPolicy(%q) failure = %+v, want message %q", policyPath, failure, test.wantMessage)
			}
			if strings.Contains(failure.Message, secret) || strings.Contains(failure.Message, root) {
				t.Errorf("LoadBoundDriftPolicy(%q) failure message = %q, want secret/path redacted", policyPath, failure.Message)
			}
			wantHash := sha256.Sum256(test.content)
			if got, want := loadFailure.File, (artifacts.StableFileDigest{
				SHA256: hex.EncodeToString(wantHash[:]), Size: int64(len(test.content)),
			}); got != want {
				t.Errorf("LoadBoundDriftPolicy(%q) bound digest = %+v, want %+v", policyPath, got, want)
			}
		})
	}
}

func TestLoadBoundDriftPolicyPathAndSymlinkPolicy(t *testing.T) {
	_, err := LoadBoundDriftPolicy(stringPointer("relative/policy.json"), policyBudget(t, 4))
	failure := requireAssessmentFailure(t, err, "UNRESOLVED_POLICY_PATH")
	if failure.Message != "saved-plan policy requires a resolved absolute path" {
		t.Errorf("LoadBoundDriftPolicy(relative) message = %q, want resolved-path message", failure.Message)
	}

	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	link := filepath.Join(root, "link.json")
	writePolicyFile(t, target, []byte(`{"version":1,"resource_types":{}}`))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", target, link, err)
	}
	_, err = LoadBoundDriftPolicy(&link, policyBudget(t, 4))
	requireAssessmentFailure(t, err, "SYMLINK_NOT_ALLOWED")
}

func TestAbsentDriftPolicyHasNoFileEvidence(t *testing.T) {
	budget := policyBudget(t, 1)
	bound, err := LoadBoundDriftPolicy(nil, budget)
	if err != nil {
		t.Fatalf("LoadBoundDriftPolicy(nil) error = %v, want nil", err)
	}
	if bound.Path != nil || bound.File != nil || bound.Policy == nil {
		t.Errorf("LoadBoundDriftPolicy(nil) = %+v, want empty policy without file evidence", bound)
	}
	if got := bound.Policy.StaleEntries(metadata.StaleEntriesOptions{}); len(got) != 0 {
		t.Errorf("LoadBoundDriftPolicy(nil).Policy.StaleEntries() = %#v, want []", got)
	}
	if got := budget.Files(); got != 0 {
		t.Errorf("LoadBoundDriftPolicy(nil) budget files = %d, want 0", got)
	}
	if err := RecheckBoundDriftPolicy(bound, budget); err != nil {
		t.Errorf("RecheckBoundDriftPolicy(absent) error = %v, want nil", err)
	}
}

func TestRecheckBoundDriftPolicyUsesDigestAndSerialBudget(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.json")
	content := []byte(`{"version":1,"resource_types":{}}`)
	writePolicyFile(t, policyPath, content)
	bound, err := LoadBoundDriftPolicy(&policyPath, policyBudget(t, 4))
	if err != nil {
		t.Fatalf("LoadBoundDriftPolicy(%q) error = %v, want nil", policyPath, err)
	}
	replacement := filepath.Join(root, "replacement")
	writePolicyFile(t, replacement, content)
	if err := os.Rename(replacement, policyPath); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", replacement, policyPath, err)
	}
	if err := RecheckBoundDriftPolicy(bound, policyBudget(t, 4)); err != nil {
		t.Errorf("RecheckBoundDriftPolicy(same-byte replacement) error = %v, want nil", err)
	}

	serial := policyBudget(t, 1)
	if _, err := LoadBoundDriftPolicy(&policyPath, serial); err != nil {
		t.Fatalf("LoadBoundDriftPolicy(serial first read) error = %v, want nil", err)
	}
	err = RecheckBoundDriftPolicy(bound, serial)
	requireAssessmentFailure(t, err, "FILE_COUNT_EXCEEDED")
}

func stringPointer(value string) *string {
	return &value
}
