package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// BoundFileDigest ports BoundFileDigest from
// node-src/domain/plan-evidence.ts.
type BoundFileDigest struct {
	Path string
	artifacts.StableFileDigest
}

// SavedPlanFingerprintFile ports SavedPlanFingerprintFile from
// node-src/domain/plan-evidence.ts.
type SavedPlanFingerprintFile struct {
	artifacts.StableFileDigest
	Fingerprint PlanFingerprintV2
}

// SavedPlanEvidence ports SavedPlanEvidence from
// node-src/domain/plan-evidence.ts. Values are active capabilities: only the
// exact pointer returned by PrepareSavedPlanEvidence may be rechecked or
// cleaned. A copied struct or caller-built value has no valid binding.
type SavedPlanEvidence struct {
	FingerprintInput  PlanFingerprintInput
	FingerprintPath   string
	FingerprintFile   SavedPlanFingerprintFile
	OriginalPlan      BoundFileDigest
	SnapshotDirectory string
	Snapshot          artifacts.StableFileSnapshot

	binding *savedPlanEvidenceBinding
}

// PrepareSavedPlanEvidenceOptions ports PrepareSavedPlanEvidenceOptions from
// node-src/domain/plan-evidence.ts.
type PrepareSavedPlanEvidenceOptions struct {
	SavedPlanPath     string
	FingerprintPath   string
	FingerprintInput  PlanFingerprintInput
	SnapshotDirectory string
	FingerprintBudget *artifacts.ReadBudget
	SavedPlanBudget   *artifacts.ReadBudget
}

// RecheckSavedPlanEvidenceOptions ports RecheckSavedPlanEvidenceOptions from
// node-src/domain/plan-evidence.ts.
type RecheckSavedPlanEvidenceOptions struct {
	Evidence          *SavedPlanEvidence
	FingerprintBudget *artifacts.ReadBudget
	SavedPlanBudget   *artifacts.ReadBudget
}

type savedPlanEvidenceState struct {
	fingerprintInput  PlanFingerprintInput
	fingerprintPath   string
	fingerprintFile   SavedPlanFingerprintFile
	originalPlan      BoundFileDigest
	snapshotDirectory string
	snapshot          artifacts.StableFileSnapshot
}

type savedPlanEvidenceBinding struct {
	mu sync.Mutex

	owner     *SavedPlanEvidence
	state     savedPlanEvidenceState
	directory artifacts.StableFileIdentity
	file      artifacts.StableFileIdentity
	cleaned   bool
}

// evidenceHooks are deterministic race seams for package tests. Production
// entry points always pass the zero value.
type evidenceHooks struct {
	afterSnapshotIdentity func(artifacts.StableFileSnapshot) error
}

// evidenceCleanupHooks expose the two path-swap boundaries needed to prove
// descriptor-bound cleanup. Production entry points always pass the zero
// value.
type evidenceCleanupHooks struct {
	afterDirectoryIdentity func() error
	afterOpen              func() error
}

func evidenceFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func evidenceDomainFailure(code, message string) *procerr.ProcessFailure {
	return evidenceFailure(code, message, procerr.CategoryDomain)
}

func evidenceUnsupportedPlatformFailure() *procerr.ProcessFailure {
	return evidenceFailure(
		"UNSUPPORTED_BOUNDED_FILE_PLATFORM",
		"bounded stable file operations are supported only on Linux and macOS amd64/arm64",
		procerr.CategoryIO,
	)
}

func requireEvidencePlatform() error {
	if evidencePlatformSupported {
		return nil
	}
	return evidenceUnsupportedPlatformFailure()
}

func sameEvidenceDigest(left, right artifacts.StableFileDigest) bool {
	return left.SHA256 == right.SHA256 && left.Size == right.Size
}

func sameEvidenceIdentity(left, right artifacts.StableFileIdentity) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}

// OpenSavedPlanSnapshot opens the active private snapshot as a read-only,
// descriptor-bound capability. The caller must close the returned file before
// CleanupSavedPlanEvidence. A supplied budget is charged for the exact bytes
// verified through the descriptor.
func OpenSavedPlanSnapshot(evidence *SavedPlanEvidence, budget *artifacts.ReadBudget) (*os.File, error) {
	if evidence == nil || evidence.binding == nil {
		return nil, evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	binding := evidence.binding
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.owner != evidence || binding.cleaned {
		return nil, evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	if err := requireEvidencePlatform(); err != nil {
		return nil, err
	}
	file, err := openEvidenceSnapshotFile(binding.state.snapshot.Path)
	if err != nil {
		return nil, evidenceDomainFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot changed before exact Apply")
	}
	if err := recheckOpenedSavedPlanSnapshot(binding, file, budget); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// RecheckSavedPlanSnapshot verifies that an already-open snapshot descriptor
// still names the exact inode and bytes prepared as saved-plan evidence.
func RecheckSavedPlanSnapshot(evidence *SavedPlanEvidence, file *os.File, budget *artifacts.ReadBudget) error {
	if evidence == nil || evidence.binding == nil {
		return evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	binding := evidence.binding
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.owner != evidence || binding.cleaned {
		return evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	if err := requireEvidencePlatform(); err != nil {
		return err
	}
	return recheckOpenedSavedPlanSnapshot(binding, file, budget)
}

func recheckOpenedSavedPlanSnapshot(binding *savedPlanEvidenceBinding, file *os.File, budget *artifacts.ReadBudget) error {
	if file == nil {
		return evidenceDomainFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot changed before exact Apply")
	}
	info, err := file.Stat()
	identity, identityOK := evidenceFileIdentity(info)
	if err != nil || info == nil || !info.Mode().IsRegular() || !identityOK || !sameEvidenceIdentity(binding.file, identity) {
		return evidenceDomainFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot changed before exact Apply")
	}
	digest, err := digestOpenedSavedPlanSnapshot(file, budget)
	if err != nil {
		return err
	}
	if !sameEvidenceDigest(digest, binding.state.snapshot.StableFileDigest) {
		return evidenceDomainFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot changed before exact Apply")
	}
	return nil
}

func digestOpenedSavedPlanSnapshot(file *os.File, budget *artifacts.ReadBudget) (artifacts.StableFileDigest, error) {
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return artifacts.StableFileDigest{}, errors.New("invalid snapshot descriptor")
	}
	if err := budget.Reserve(big.NewInt(info.Size())); err != nil {
		return artifacts.StableFileDigest{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return artifacts.StableFileDigest{}, err
	}
	buffer := make([]byte, 64*1024)
	defer clear(buffer)
	hash := sha256.New()
	var consumed int64
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			consumed += int64(count)
			if consumed > info.Size() {
				return artifacts.StableFileDigest{}, errors.New("snapshot grew while read")
			}
			_, _ = hash.Write(buffer[:count])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return artifacts.StableFileDigest{}, readErr
		}
		if count == 0 {
			break
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil || consumed != info.Size() {
		return artifacts.StableFileDigest{}, errors.New("snapshot changed while read")
	}
	sum := hash.Sum(nil)
	digest := artifacts.StableFileDigest{SHA256: hex.EncodeToString(sum), Size: consumed}
	clear(sum)
	return digest, nil
}

func samePlanFingerprint(left, right PlanFingerprintV2) bool {
	return left.Version == right.Version && left.SHA256 == right.SHA256
}

func requireAbsoluteEvidencePath(value string) error {
	if filepath.IsAbs(value) {
		return nil
	}
	return evidenceDomainFailure(
		"UNRESOLVED_EVIDENCE_PATH",
		"saved-plan evidence requires resolved absolute paths",
	)
}

func copyResolvedFingerprintInput(input PlanFingerprintInput) (PlanFingerprintInput, error) {
	if err := requireAbsoluteEvidencePath(input.EnvDir); err != nil {
		return PlanFingerprintInput{}, err
	}
	for _, varFile := range input.VarFiles {
		if err := requireAbsoluteEvidencePath(varFile); err != nil {
			return PlanFingerprintInput{}, err
		}
	}
	if input.BackendConfig != nil {
		if err := requireAbsoluteEvidencePath(*input.BackendConfig); err != nil {
			return PlanFingerprintInput{}, err
		}
	}
	return PlanFingerprintInput{
		EnvDir:        input.EnvDir,
		VarFiles:      append([]string{}, input.VarFiles...),
		MemberTypes:   append([]string{}, input.MemberTypes...),
		BackendConfig: cloneStringPointer(input.BackendConfig),
		BackendKey:    cloneStringPointer(input.BackendKey),
	}, nil
}

func cloneFingerprintInput(input PlanFingerprintInput) PlanFingerprintInput {
	return PlanFingerprintInput{
		EnvDir:        input.EnvDir,
		VarFiles:      append([]string{}, input.VarFiles...),
		MemberTypes:   append([]string{}, input.MemberTypes...),
		BackendConfig: cloneStringPointer(input.BackendConfig),
		BackendKey:    cloneStringPointer(input.BackendKey),
	}
}

func cloneEvidenceState(state savedPlanEvidenceState) savedPlanEvidenceState {
	state.fingerprintInput = cloneFingerprintInput(state.fingerprintInput)
	return state
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func fingerprintVersionTwo(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	return err == nil && parsed == float64(PlanFingerprintVersion)
}

func validateSavedPlanFingerprint(value any) (PlanFingerprintV2, error) {
	record, ok := value.(map[string]any)
	if !ok || len(record) != 2 {
		return PlanFingerprintV2{}, evidenceDomainFailure(
			"INVALID_PLAN_SOURCES",
			"saved-plan fingerprint does not match the version 2 contract",
		)
	}
	sha256Value, sha256OK := record["sha256"].(string)
	version, versionOK := record["version"]
	if !sha256OK || !versionOK || !isLowerSHA256(sha256Value) || !fingerprintVersionTwo(version) {
		return PlanFingerprintV2{}, evidenceDomainFailure(
			"INVALID_PLAN_SOURCES",
			"saved-plan fingerprint does not match the version 2 contract",
		)
	}
	return PlanFingerprintV2{Version: PlanFingerprintVersion, SHA256: sha256Value}, nil
}

// ReadSavedPlanFingerprint ports readSavedPlanFingerprint from
// node-src/domain/plan-evidence.ts. The returned digest binds the raw
// fingerprint-file bytes, not a re-encoded JSON value.
func ReadSavedPlanFingerprint(
	fingerprintPath string,
	budget *artifacts.ReadBudget,
) (SavedPlanFingerprintFile, error) {
	if err := requireEvidencePlatform(); err != nil {
		return SavedPlanFingerprintFile{}, err
	}
	if err := requireAbsoluteEvidencePath(fingerprintPath); err != nil {
		return SavedPlanFingerprintFile{}, err
	}
	file, err := artifacts.ReadBoundedUTF8File(
		fingerprintPath,
		budget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return SavedPlanFingerprintFile{}, err
	}
	parsed, err := canonjson.ParseControlJSON(file.Text)
	if err != nil {
		return SavedPlanFingerprintFile{}, evidenceDomainFailure(
			"INVALID_PLAN_SOURCES_JSON",
			"saved-plan fingerprint is not valid contract JSON",
		)
	}
	fingerprint, err := validateSavedPlanFingerprint(parsed)
	if err != nil {
		return SavedPlanFingerprintFile{}, err
	}
	return SavedPlanFingerprintFile{
		StableFileDigest: file.Digest,
		Fingerprint:      fingerprint,
	}, nil
}

func currentPlanFingerprint(
	input PlanFingerprintInput,
	budget *artifacts.ReadBudget,
) (fingerprint PlanFingerprintV2, err error) {
	defer func() {
		if recover() != nil {
			fingerprint = PlanFingerprintV2{}
			err = evidenceDomainFailure(
				"SOURCE_FINGERPRINT_FAILED",
				"unable to fingerprint current plan inputs",
			)
		}
	}()
	fingerprint, err = FingerprintPlanV2(input, budget)
	if err != nil {
		return PlanFingerprintV2{}, evidenceDomainFailure(
			"SOURCE_FINGERPRINT_FAILED",
			"unable to fingerprint current plan inputs",
		)
	}
	return fingerprint, nil
}

func requireCurrentPlanSources(declared, current PlanFingerprintV2) error {
	if samePlanFingerprint(declared, current) {
		return nil
	}
	return evidenceDomainFailure(
		"STALE_PLAN_SOURCES",
		"saved plan does not match the current plan inputs",
	)
}

func requireEvidenceDigest(
	actual artifacts.StableFileDigest,
	expected artifacts.StableFileDigest,
	code string,
	message string,
) error {
	if sameEvidenceDigest(actual, expected) {
		return nil
	}
	return evidenceDomainFailure(code, message)
}

func snapshotDirectoryIdentity(directory string) (artifacts.StableFileIdentity, error) {
	if err := requireEvidencePlatform(); err != nil {
		return artifacts.StableFileIdentity{}, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return artifacts.StableFileIdentity{}, evidenceDomainFailure(
			"UNSAFE_SNAPSHOT_DIRECTORY",
			"unable to bind the private snapshot directory",
		)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return artifacts.StableFileIdentity{}, evidenceDomainFailure(
			"UNSAFE_SNAPSHOT_DIRECTORY",
			"snapshot directory is not a stable private directory",
		)
	}
	identity, ok := evidenceFileIdentity(info)
	if !ok {
		return artifacts.StableFileIdentity{}, evidenceDomainFailure(
			"UNSAFE_SNAPSHOT_DIRECTORY",
			"unable to bind the private snapshot directory",
		)
	}
	return identity, nil
}

func snapshotFileIdentity(
	filePath string,
	expected *artifacts.StableFileIdentity,
) (artifacts.StableFileIdentity, error) {
	if err := requireEvidencePlatform(); err != nil {
		return artifacts.StableFileIdentity{}, err
	}
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return artifacts.StableFileIdentity{}, evidenceDomainFailure(
			"PLAN_SNAPSHOT_CHANGED",
			"saved-plan snapshot changed while evidence was prepared",
		)
	}
	identity, ok := evidenceFileIdentity(info)
	if !ok || (expected != nil && !sameEvidenceIdentity(*expected, identity)) {
		return artifacts.StableFileIdentity{}, evidenceDomainFailure(
			"PLAN_SNAPSHOT_CHANGED",
			"saved-plan snapshot changed while evidence was prepared",
		)
	}
	return identity, nil
}

// PrepareSavedPlanEvidence ports prepareSavedPlanEvidence from
// node-src/domain/plan-evidence.ts.
func PrepareSavedPlanEvidence(options PrepareSavedPlanEvidenceOptions) (*SavedPlanEvidence, error) {
	return prepareSavedPlanEvidence(options, evidenceHooks{})
}

func prepareSavedPlanEvidence(
	options PrepareSavedPlanEvidenceOptions,
	hooks evidenceHooks,
) (_ *SavedPlanEvidence, err error) {
	if err := requireEvidencePlatform(); err != nil {
		return nil, err
	}
	for _, value := range []string{
		options.SavedPlanPath,
		options.FingerprintPath,
		options.SnapshotDirectory,
	} {
		if pathErr := requireAbsoluteEvidencePath(value); pathErr != nil {
			return nil, pathErr
		}
	}
	fingerprintInput, err := copyResolvedFingerprintInput(options.FingerprintInput)
	if err != nil {
		return nil, err
	}
	directoryBefore, err := snapshotDirectoryIdentity(options.SnapshotDirectory)
	if err != nil {
		return nil, err
	}

	declaredBefore, err := ReadSavedPlanFingerprint(options.FingerprintPath, options.FingerprintBudget)
	if err != nil {
		return nil, err
	}
	currentBefore, err := currentPlanFingerprint(fingerprintInput, options.FingerprintBudget)
	if err != nil {
		return nil, err
	}
	if err := requireCurrentPlanSources(declaredBefore.Fingerprint, currentBefore); err != nil {
		return nil, err
	}

	var cleanupBinding *savedPlanEvidenceBinding
	defer func() {
		if err == nil || cleanupBinding == nil {
			return
		}
		cleanupErr := removeBoundSnapshot(cleanupBinding, evidenceCleanupHooks{})
		if cleanupErr == nil {
			return
		}
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) {
			details := append([]procerr.ErrorDetail(nil), failure.Details...)
			details = append(details, procerr.ErrorDetail{
				Path:    "$",
				Code:    "SNAPSHOT_CLEANUP_FAILED",
				Message: "private saved-plan snapshot cleanup also failed",
			})
			err = procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code:      failure.Code,
				Category:  failure.Category,
				Message:   failure.Message,
				Retryable: failure.Retryable,
				Details:   details,
			})
			return
		}
		err = evidenceFailure(
			"EVIDENCE_PREPARATION_AND_CLEANUP_FAILED",
			"saved-plan evidence preparation and private cleanup failed",
			procerr.CategoryIO,
		)
	}()

	snapshot, err := artifacts.SnapshotStableFile(artifacts.SnapshotStableFileOptions{
		SourcePath:       options.SavedPlanPath,
		PrivateDirectory: options.SnapshotDirectory,
		Budget:           options.SavedPlanBudget,
	})
	if err != nil {
		return nil, err
	}
	cleanupBinding = &savedPlanEvidenceBinding{
		state: savedPlanEvidenceState{
			snapshotDirectory: options.SnapshotDirectory,
			snapshot:          snapshot,
		},
		directory: directoryBefore,
	}

	wantSnapshotIdentity := snapshot.StableFileIdentity
	snapshotIdentity, err := snapshotFileIdentity(snapshot.Path, &wantSnapshotIdentity)
	if err != nil {
		return nil, err
	}
	cleanupBinding.file = snapshotIdentity
	if hooks.afterSnapshotIdentity != nil {
		if err := hooks.afterSnapshotIdentity(snapshot); err != nil {
			return nil, err
		}
	}
	directoryAfter, err := snapshotDirectoryIdentity(options.SnapshotDirectory)
	if err != nil {
		return nil, err
	}
	if !sameEvidenceIdentity(directoryBefore, directoryAfter) {
		return nil, evidenceDomainFailure(
			"SNAPSHOT_DIRECTORY_CHANGED",
			"private snapshot directory changed while evidence was prepared",
		)
	}

	snapshotCheck, err := artifacts.SHA256StableFile(
		snapshot.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return nil, err
	}
	if err := requireEvidenceDigest(
		snapshotCheck,
		snapshot.StableFileDigest,
		"PLAN_SNAPSHOT_CHANGED",
		"saved-plan snapshot changed while evidence was prepared",
	); err != nil {
		return nil, err
	}

	declaredAfter, err := ReadSavedPlanFingerprint(options.FingerprintPath, options.FingerprintBudget)
	if err != nil {
		return nil, err
	}
	if !sameEvidenceDigest(declaredBefore.StableFileDigest, declaredAfter.StableFileDigest) ||
		!samePlanFingerprint(declaredBefore.Fingerprint, declaredAfter.Fingerprint) {
		return nil, evidenceDomainFailure(
			"PLAN_SOURCES_CHANGED",
			"saved-plan fingerprint changed while evidence was prepared",
		)
	}
	currentAfter, err := currentPlanFingerprint(fingerprintInput, options.FingerprintBudget)
	if err != nil {
		return nil, err
	}
	if err := requireCurrentPlanSources(declaredBefore.Fingerprint, currentAfter); err != nil {
		return nil, err
	}

	originalCheck, err := artifacts.SHA256StableFile(
		options.SavedPlanPath,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return nil, err
	}
	if err := requireEvidenceDigest(
		originalCheck,
		snapshot.StableFileDigest,
		"SAVED_PLAN_CHANGED",
		"saved plan changed while evidence was prepared",
	); err != nil {
		return nil, err
	}
	finalSnapshotCheck, err := artifacts.SHA256StableFile(
		snapshot.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return nil, err
	}
	if err := requireEvidenceDigest(
		finalSnapshotCheck,
		snapshot.StableFileDigest,
		"PLAN_SNAPSHOT_CHANGED",
		"saved-plan snapshot changed while evidence was prepared",
	); err != nil {
		return nil, err
	}
	if _, err := snapshotFileIdentity(snapshot.Path, &snapshotIdentity); err != nil {
		return nil, err
	}

	state := savedPlanEvidenceState{
		fingerprintInput: cloneFingerprintInput(fingerprintInput),
		fingerprintPath:  options.FingerprintPath,
		fingerprintFile:  declaredBefore,
		originalPlan: BoundFileDigest{
			Path:             options.SavedPlanPath,
			StableFileDigest: snapshot.StableFileDigest,
		},
		snapshotDirectory: options.SnapshotDirectory,
		snapshot:          snapshot,
	}
	evidence := &SavedPlanEvidence{
		FingerprintInput:  cloneFingerprintInput(state.fingerprintInput),
		FingerprintPath:   state.fingerprintPath,
		FingerprintFile:   state.fingerprintFile,
		OriginalPlan:      state.originalPlan,
		SnapshotDirectory: state.snapshotDirectory,
		Snapshot:          state.snapshot,
	}
	binding := &savedPlanEvidenceBinding{
		owner:     evidence,
		state:     cloneEvidenceState(state),
		directory: directoryBefore,
		file:      snapshotIdentity,
	}
	evidence.binding = binding
	cleanupBinding = nil
	return evidence, nil
}

// RecheckSavedPlanEvidence ports recheckSavedPlanEvidence from
// node-src/domain/plan-evidence.ts.
func RecheckSavedPlanEvidence(options RecheckSavedPlanEvidenceOptions) error {
	evidence := options.Evidence
	if evidence == nil || evidence.binding == nil {
		return evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	binding := evidence.binding
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.owner != evidence || binding.cleaned {
		return evidenceDomainFailure("INVALID_EVIDENCE_BINDING", "saved-plan evidence is not active")
	}
	if err := requireEvidencePlatform(); err != nil {
		return err
	}
	state := binding.state

	if _, err := snapshotFileIdentity(state.snapshot.Path, &binding.file); err != nil {
		return err
	}
	originalBefore, err := artifacts.SHA256StableFile(
		state.originalPlan.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return err
	}
	if err := requireEvidenceDigest(
		originalBefore,
		state.originalPlan.StableFileDigest,
		"SAVED_PLAN_CHANGED",
		"saved plan changed after evidence was prepared",
	); err != nil {
		return err
	}

	declaredBefore, err := ReadSavedPlanFingerprint(state.fingerprintPath, options.FingerprintBudget)
	if err != nil {
		return err
	}
	if !sameEvidenceDigest(declaredBefore.StableFileDigest, state.fingerprintFile.StableFileDigest) ||
		!samePlanFingerprint(declaredBefore.Fingerprint, state.fingerprintFile.Fingerprint) {
		return evidenceDomainFailure(
			"PLAN_SOURCES_CHANGED",
			"saved-plan fingerprint changed after evidence was prepared",
		)
	}
	currentBefore, err := currentPlanFingerprint(state.fingerprintInput, options.FingerprintBudget)
	if err != nil {
		return err
	}
	if err := requireCurrentPlanSources(state.fingerprintFile.Fingerprint, currentBefore); err != nil {
		return err
	}

	snapshotCheck, err := artifacts.SHA256StableFile(
		state.snapshot.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return err
	}
	if err := requireEvidenceDigest(
		snapshotCheck,
		state.snapshot.StableFileDigest,
		"PLAN_SNAPSHOT_CHANGED",
		"saved-plan snapshot changed after evidence was prepared",
	); err != nil {
		return err
	}
	if _, err := snapshotFileIdentity(state.snapshot.Path, &binding.file); err != nil {
		return err
	}

	declaredAfter, err := ReadSavedPlanFingerprint(state.fingerprintPath, options.FingerprintBudget)
	if err != nil {
		return err
	}
	if !sameEvidenceDigest(declaredAfter.StableFileDigest, state.fingerprintFile.StableFileDigest) ||
		!samePlanFingerprint(declaredAfter.Fingerprint, state.fingerprintFile.Fingerprint) {
		return evidenceDomainFailure(
			"PLAN_SOURCES_CHANGED",
			"saved-plan fingerprint changed after evidence was prepared",
		)
	}
	currentAfter, err := currentPlanFingerprint(state.fingerprintInput, options.FingerprintBudget)
	if err != nil {
		return err
	}
	if err := requireCurrentPlanSources(state.fingerprintFile.Fingerprint, currentAfter); err != nil {
		return err
	}

	originalAfter, err := artifacts.SHA256StableFile(
		state.originalPlan.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return err
	}
	if err := requireEvidenceDigest(
		originalAfter,
		state.originalPlan.StableFileDigest,
		"SAVED_PLAN_CHANGED",
		"saved plan changed after evidence was prepared",
	); err != nil {
		return err
	}
	snapshotAfter, err := artifacts.SHA256StableFile(
		state.snapshot.Path,
		options.SavedPlanBudget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return err
	}
	if err := requireEvidenceDigest(
		snapshotAfter,
		state.snapshot.StableFileDigest,
		"PLAN_SNAPSHOT_CHANGED",
		"saved-plan snapshot changed after evidence was prepared",
	); err != nil {
		return err
	}
	_, err = snapshotFileIdentity(state.snapshot.Path, &binding.file)
	return err
}

// CleanupSavedPlanEvidence ports cleanupSavedPlanEvidence from
// node-src/domain/plan-evidence.ts. Cleanup scrubs the exact bound inode and
// deliberately leaves the zero-length snapshot directory entry in place.
func CleanupSavedPlanEvidence(evidence *SavedPlanEvidence) error {
	return cleanupSavedPlanEvidence(evidence, evidenceCleanupHooks{})
}

func cleanupSavedPlanEvidence(
	evidence *SavedPlanEvidence,
	hooks evidenceCleanupHooks,
) error {
	if evidence == nil || evidence.binding == nil {
		return evidenceDomainFailure(
			"INVALID_SNAPSHOT_BINDING",
			"saved-plan snapshot has no active cleanup binding",
		)
	}
	binding := evidence.binding
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.owner != evidence {
		return evidenceDomainFailure(
			"INVALID_SNAPSHOT_BINDING",
			"saved-plan snapshot has no active cleanup binding",
		)
	}
	return removeBoundSnapshot(binding, hooks)
}

func removeBoundSnapshot(
	binding *savedPlanEvidenceBinding,
	hooks evidenceCleanupHooks,
) error {
	if binding.cleaned {
		return nil
	}
	if err := requireEvidencePlatform(); err != nil {
		return err
	}
	directory, err := os.Lstat(binding.state.snapshotDirectory)
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return evidenceDomainFailure(
			"SNAPSHOT_CLEANUP_REFUSED",
			"private snapshot directory changed before cleanup",
		)
	}
	directoryIdentity, ok := evidenceFileIdentity(directory)
	if !ok || !sameEvidenceIdentity(binding.directory, directoryIdentity) {
		return evidenceDomainFailure(
			"SNAPSHOT_CLEANUP_REFUSED",
			"private snapshot directory changed before cleanup",
		)
	}
	if hooks.afterDirectoryIdentity != nil {
		if err := hooks.afterDirectoryIdentity(); err != nil {
			return evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
		}
	}
	return scrubBoundSnapshot(binding, hooks)
}

func scrubBoundSnapshot(
	binding *savedPlanEvidenceBinding,
	hooks evidenceCleanupHooks,
) (err error) {
	descriptor, openErr := openEvidenceCleanupFile(binding.state.snapshot.Path)
	if openErr != nil {
		return evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
	}
	defer func() {
		if closeErr := descriptor.Close(); closeErr != nil {
			err = evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
		}
	}()
	if hooks.afterOpen != nil {
		if hookErr := hooks.afterOpen(); hookErr != nil {
			return evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
		}
	}
	info, statErr := descriptor.Stat()
	identity, identityOK := evidenceFileIdentity(info)
	if statErr != nil || info == nil || !info.Mode().IsRegular() ||
		!identityOK || !sameEvidenceIdentity(binding.file, identity) {
		return evidenceDomainFailure(
			"SNAPSHOT_CLEANUP_REFUSED",
			"saved-plan snapshot changed before cleanup",
		)
	}
	if truncateErr := descriptor.Truncate(0); truncateErr != nil {
		return evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
	}
	if syncErr := descriptor.Sync(); syncErr != nil {
		return evidenceDomainFailure("SNAPSHOT_CLEANUP_FAILED", "unable to scrub saved-plan snapshot")
	}
	binding.cleaned = true
	return nil
}
