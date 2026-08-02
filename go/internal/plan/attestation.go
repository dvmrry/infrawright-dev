package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const (
	// PlanCreationAttestationVersion is the schema version of tfplan.attestation.
	PlanCreationAttestationVersion = 1

	// PlanCreationAttestationSuffix is appended to a saved plan path to find
	// the sibling creation attestation.
	PlanCreationAttestationSuffix = ".attestation"
)

// qualifiedTerraformVersions enumerates the exact Terraform releases the
// committed capture matrix has been run and promoted under. Widening this
// set REQUIRES re-running make regen-plan-captures
// and the focused contract suite under the new release first (see that
// directory's README); patch releases are NOT assumed equivalent.
var qualifiedTerraformVersions = map[string]bool{"1.15.4": true}

// PlanCreationAttestation records engine-created plan provenance as defense in
// depth against accidental plan/sidecar drift and version qualification.
// It does NOT authenticate against a writer who can forge both plan and sidecar — the same trust class as tfplan.sources.
// PlanArgv excludes the Terraform executable path and contains the exact
// argument vector passed to that executable. Refresh records the invocation's
// refresh flag; it is not a standalone stale-data claim.
type PlanCreationAttestation struct {
	FormatVersion    int      `json:"format_version"`
	TerraformVersion string   `json:"terraform_version"`
	PlanArgv         []string `json:"argv"`
	Refresh          bool     `json:"refresh"`
	PlanSHA256       string   `json:"plan_sha256"`
}

// SavedPlanAttestationFile binds a parsed attestation to the exact sidecar
// bytes that were read. The byte digest is rechecked with saved-plan evidence
// so a sidecar cannot be replaced during assessment.
type SavedPlanAttestationFile struct {
	Path string
	artifacts.StableFileDigest
	Attestation PlanCreationAttestation
}

// SavedPlanAttestationPath returns the sibling path used by saved-plan
// lifecycle operations.
func SavedPlanAttestationPath(savedPlanPath string) string {
	return savedPlanPath + PlanCreationAttestationSuffix
}

func clonePlanCreationAttestation(attestation *PlanCreationAttestation) *PlanCreationAttestation {
	if attestation == nil {
		return nil
	}
	cloned := *attestation
	cloned.PlanArgv = append([]string(nil), attestation.PlanArgv...)
	return &cloned
}

func samePlanCreationAttestation(left, right PlanCreationAttestation) bool {
	if left.FormatVersion != right.FormatVersion ||
		left.TerraformVersion != right.TerraformVersion ||
		left.Refresh != right.Refresh ||
		left.PlanSHA256 != right.PlanSHA256 ||
		len(left.PlanArgv) != len(right.PlanArgv) {
		return false
	}
	for index := range left.PlanArgv {
		if left.PlanArgv[index] != right.PlanArgv[index] {
			return false
		}
	}
	return true
}

func isLowerSHA256Attestation(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func parseAttestationVersion(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	version, err := strconv.Atoi(number.String())
	return version, err == nil
}

func parsePlanCreationAttestation(value any) (PlanCreationAttestation, error) {
	record, ok := value.(map[string]any)
	if !ok || len(record) != 5 {
		return PlanCreationAttestation{}, errors.New("attestation must contain exactly five fields")
	}
	formatVersion, ok := parseAttestationVersion(record["format_version"])
	if !ok {
		return PlanCreationAttestation{}, errors.New("attestation format_version must be an integer")
	}
	terraformVersion, ok := record["terraform_version"].(string)
	if !ok {
		return PlanCreationAttestation{}, errors.New("attestation terraform_version must be a string")
	}
	rawArgv, ok := record["argv"].([]any)
	if !ok || len(rawArgv) == 0 {
		return PlanCreationAttestation{}, errors.New("attestation argv must be a non-empty string array")
	}
	argv := make([]string, len(rawArgv))
	for index, rawArgument := range rawArgv {
		argument, stringOK := rawArgument.(string)
		if !stringOK || argument == "" {
			return PlanCreationAttestation{}, errors.New("attestation argv must be a non-empty string array")
		}
		argv[index] = argument
	}
	refresh, ok := record["refresh"].(bool)
	if !ok {
		return PlanCreationAttestation{}, errors.New("attestation refresh must be a boolean")
	}
	planSHA256, ok := record["plan_sha256"].(string)
	if !ok {
		return PlanCreationAttestation{}, errors.New("attestation plan_sha256 must be a string")
	}
	return PlanCreationAttestation{
		FormatVersion:    formatVersion,
		TerraformVersion: terraformVersion,
		PlanArgv:         argv,
		Refresh:          refresh,
		PlanSHA256:       planSHA256,
	}, nil
}

func validatePlanCreationAttestation(attestation PlanCreationAttestation, expectedPlanSHA256 string) error {
	if attestation.FormatVersion != PlanCreationAttestationVersion {
		return fmt.Errorf("attestation format_version must be %d", PlanCreationAttestationVersion)
	}
	if !qualifiedTerraformVersions[attestation.TerraformVersion] {
		return fmt.Errorf("attestation terraform_version %q is not capture-qualified (qualified: 1.15.4)", attestation.TerraformVersion)
	}
	if len(attestation.PlanArgv) == 0 || attestation.PlanArgv[0] != "plan" {
		return errors.New("attestation argv must begin with plan")
	}
	hasRefreshFlag := false
	for _, argument := range attestation.PlanArgv[1:] {
		if argument == "-refresh=false" || argument == "-refresh=true" {
			hasRefreshFlag = true
		}
	}
	if !hasRefreshFlag {
		return errors.New("attestation argv must contain an explicit refresh flag")
	}
	if !isLowerSHA256Attestation(attestation.PlanSHA256) {
		return errors.New("attestation plan_sha256 must be a lowercase SHA-256 digest")
	}
	if expectedPlanSHA256 != "" && attestation.PlanSHA256 != expectedPlanSHA256 {
		return errors.New("attestation plan_sha256 does not match the saved plan")
	}
	return nil
}

// ReadSavedPlanAttestation reads and validates one stable creation-attestation
// sidecar. A non-empty expectedPlanSHA256 binds the sidecar to the saved plan
// bytes. Missing-file handling belongs to the caller because managed plans
// preserve the pre-attestation behavior when no sidecar exists.
func ReadSavedPlanAttestation(
	attestationPath string,
	budget *artifacts.ReadBudget,
	expectedPlanSHA256 string,
) (SavedPlanAttestationFile, error) {
	if !filepath.IsAbs(attestationPath) {
		return SavedPlanAttestationFile{}, evidenceDomainFailure(
			"UNRESOLVED_EVIDENCE_PATH",
			"saved-plan attestation requires a resolved absolute path",
		)
	}
	file, err := artifacts.ReadBoundedUTF8File(
		attestationPath,
		budget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return SavedPlanAttestationFile{}, err
	}
	parsed, err := canonjson.ParseControlJSON(file.Text)
	if err != nil {
		return SavedPlanAttestationFile{}, evidenceDomainFailure(
			"INVALID_PLAN_ATTESTATION",
			"saved-plan attestation is not valid contract JSON",
		)
	}
	attestation, err := parsePlanCreationAttestation(parsed)
	if err != nil {
		return SavedPlanAttestationFile{}, evidenceDomainFailure(
			"INVALID_PLAN_ATTESTATION",
			err.Error(),
		)
	}
	if err := validatePlanCreationAttestation(attestation, expectedPlanSHA256); err != nil {
		code := "INVALID_PLAN_ATTESTATION"
		if expectedPlanSHA256 != "" && strings.Contains(err.Error(), "does not match") {
			code = "PLAN_ATTESTATION_MISMATCH"
		}
		return SavedPlanAttestationFile{}, evidenceDomainFailure(code, err.Error())
	}
	return SavedPlanAttestationFile{
		Path:             attestationPath,
		StableFileDigest: file.Digest,
		Attestation:      attestation,
	}, nil
}

// WritePlanCreationAttestation writes one validated creation-attestation
// sidecar. Exactly Terraform 1.15.4 is the qualified evidence boundary (the
// only release the capture matrix has been promoted under); plans made
// by another version remain assessable only as managed plans when no sidecar
// is present, and are refused for data authorization when this sidecar exists.
func WritePlanCreationAttestation(path string, attestation PlanCreationAttestation) error {
	if err := validatePlanCreationAttestation(attestation, ""); err != nil {
		return err
	}
	encoded, err := json.Marshal(attestation)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o600)
}

func planFileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("saved plan %q is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
