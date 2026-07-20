//go:build (darwin && !ios && (amd64 || arm64)) || (linux && !android && (amd64 || arm64))

package controlevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestBindRequiredAssessmentControlTextBindsStableUTF8Source(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "control.json")
	content := []byte("\xef\xbb\xbf{\"secret\":\"value\"}\n")
	writeControlFile(t, filePath, content)

	bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}
	if bound.Text == nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q).Text = nil, want decoded text", filePath)
	}
	if got, want := *bound.Text, "{\"secret\":\"value\"}\n"; got != want {
		t.Errorf("BindRequiredAssessmentControlText(%q).Text = %q, want %q", filePath, got, want)
	}
	if got, want := bound.File.Path, filePath; got != want {
		t.Errorf("BindRequiredAssessmentControlText(%q).File.Path = %q, want %q", filePath, got, want)
	}
	if bound.File.Digest == nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q).File.Digest = nil, want stable digest", filePath)
	}
	wantHash := sha256.Sum256(content)
	if got, want := bound.File.Digest.SHA256, hex.EncodeToString(wantHash[:]); got != want {
		t.Errorf("BindRequiredAssessmentControlText(%q).File.Digest.SHA256 = %q, want %q", filePath, got, want)
	}
	if got, want := bound.File.Digest.Size, int64(len(content)); got != want {
		t.Errorf("BindRequiredAssessmentControlText(%q).File.Digest.Size = %d, want %d", filePath, got, want)
	}
	if bound.File.Identity == nil {
		t.Errorf("BindRequiredAssessmentControlText(%q).File.Identity = nil, want stable identity", filePath)
	}
	if bound.File.FollowSymlinks != nil {
		t.Errorf("BindRequiredAssessmentControlText(%q).File.FollowSymlinks = %v, want omitted default", filePath, *bound.File.FollowSymlinks)
	}
}

func TestBindRequiredAssessmentControlTextRejectsInvalidUTF8WithoutLeakingInput(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "credential-secret.json")
	writeControlFile(t, filePath, []byte{0xff, 's', 'e', 'c', 'r', 'e', 't'})

	_, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	failure := requireControlFailure(
		t,
		err,
		"INVALID_UTF8",
		procerr.CategoryDomain,
		"assessment control input is not valid UTF-8",
	)
	if strings.Contains(failure.Message, filePath) || strings.Contains(failure.Message, "secret") {
		t.Errorf("BindRequiredAssessmentControlText(%q) failure message = %q, want redacted", filePath, failure.Message)
	}
}

func TestBindRequiredAssessmentControlTextUsesResolvedAbsolutePaths(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
	}{
		{name: "relative", filePath: "control.json"},
		{name: "nul", filePath: filepath.Join(t.TempDir(), "control") + "\x00tail"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BindRequiredAssessmentControlText(test.filePath, BindOptions{})
			requireControlFailure(
				t,
				err,
				"UNRESOLVED_ASSESSMENT_CONTROL_PATH",
				procerr.CategoryDomain,
				"assessment control inputs require resolved absolute paths",
			)
		})
	}
}

func TestBindAssessmentControlTextSymlinkPolicy(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	link := filepath.Join(directory, "link.json")
	writeControlFile(t, target, []byte("target"))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", target, link, err)
	}

	bound, err := BindRequiredAssessmentControlText(link, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q, default follow) error = %v, want nil", link, err)
	}
	if bound.Text == nil || *bound.Text != "target" {
		t.Errorf("BindRequiredAssessmentControlText(%q, default follow).Text = %v, want %q", link, bound.Text, "target")
	}

	followSymlinks := false
	_, err = BindRequiredAssessmentControlText(link, BindOptions{FollowSymlinks: &followSymlinks})
	requireControlFailure(
		t,
		err,
		"SYMLINK_NOT_ALLOWED",
		procerr.CategoryIO,
		"input file must not be a symbolic link",
	)

	regular, err := BindRequiredAssessmentControlText(
		target,
		BindOptions{FollowSymlinks: &followSymlinks},
	)
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q, no follow) error = %v, want nil", target, err)
	}
	if regular.File.FollowSymlinks == nil || *regular.File.FollowSymlinks {
		t.Errorf("BindRequiredAssessmentControlText(%q, no follow).File.FollowSymlinks = %v, want false", target, regular.File.FollowSymlinks)
	}
}

func TestBindOptionalAssessmentControlTextBindsSelectedAbsence(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.json")

	bound, err := BindOptionalAssessmentControlText(missing, BindOptions{})
	if err != nil {
		t.Fatalf("BindOptionalAssessmentControlText(%q) error = %v, want nil", missing, err)
	}
	if bound.Text != nil || bound.File.Digest != nil || bound.File.Identity != nil {
		t.Errorf("BindOptionalAssessmentControlText(%q) = %+v, want absent binding", missing, bound)
	}
	if bound.File.Path != missing || bound.File.FollowSymlinks != nil {
		t.Errorf("BindOptionalAssessmentControlText(%q).File = %+v, want default-follow path binding", missing, bound.File)
	}

	dangling := filepath.Join(directory, "dangling.json")
	if err := os.Symlink(filepath.Join(directory, "absent-target"), dangling); err != nil {
		t.Fatalf("os.Symlink(dangling, %q) error = %v, want nil", dangling, err)
	}
	bound, err = BindOptionalAssessmentControlText(dangling, BindOptions{})
	if err != nil {
		t.Fatalf("BindOptionalAssessmentControlText(%q, default follow) error = %v, want nil", dangling, err)
	}
	if bound.Text != nil || bound.File.Digest != nil {
		t.Errorf("BindOptionalAssessmentControlText(%q, default follow) = %+v, want absent binding", dangling, bound)
	}

	followSymlinks := false
	_, err = BindOptionalAssessmentControlText(
		dangling,
		BindOptions{FollowSymlinks: &followSymlinks},
	)
	requireControlFailure(
		t,
		err,
		"SYMLINK_NOT_ALLOWED",
		procerr.CategoryIO,
		"input file must not be a symbolic link",
	)
}

func TestBindOptionalAssessmentControlTextPreservesNonENOENTReadFailure(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "unreadable.json")
	writeControlFile(t, filePath, []byte("content"))
	if err := os.Chmod(filePath, 0); err != nil {
		t.Fatalf("os.Chmod(%q, 0) error = %v, want nil", filePath, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(filePath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("cleanup os.Chmod(%q, 0600) error = %v, want nil", filePath, err)
		}
	})
	probe, probeErr := os.Open(filePath)
	if probeErr == nil {
		if err := probe.Close(); err != nil {
			t.Errorf("probe.Close(%q) error = %v, want nil", filePath, err)
		}
		t.Skip("current user can read mode-000 files")
	}

	_, err := BindOptionalAssessmentControlText(filePath, BindOptions{})
	requireControlFailure(
		t,
		err,
		"READ_FAILED",
		procerr.CategoryIO,
		"unable to open input file",
	)
}

func TestCopyAssessmentControlFilesPreservesValidationPrecedence(t *testing.T) {
	validDigest := artifacts.StableFileDigest{SHA256: strings.Repeat("a", 64), Size: 1}
	invalidDigest := artifacts.StableFileDigest{SHA256: "invalid", Size: -1}
	absolute := filepath.Join(t.TempDir(), "control.json")

	tooMany := make([]BoundAssessmentControlFile, maximumControlFiles+1)
	for index := range tooMany {
		tooMany[index] = BoundAssessmentControlFile{Path: filepath.Join(t.TempDir(), "control.json")}
	}
	tooMany[0].Path = "relative"
	_, err := CopyAssessmentControlFiles(tooMany)
	requireControlFailure(
		t,
		err,
		"TOO_MANY_ASSESSMENT_CONTROL_FILES",
		procerr.CategoryDomain,
		"saved-plan assessment exceeds the control-file limit",
	)

	_, err = CopyAssessmentControlFiles([]BoundAssessmentControlFile{
		{Path: absolute, Digest: &validDigest},
		{Path: absolute, Digest: &invalidDigest},
	})
	requireControlFailure(
		t,
		err,
		"DUPLICATE_ASSESSMENT_CONTROL_FILE",
		procerr.CategoryDomain,
		"saved-plan assessment control files must be unique",
	)
}

func TestCopyAssessmentControlFilesValidatesDigestContract(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "control.json")
	tests := []struct {
		name   string
		digest artifacts.StableFileDigest
	}{
		{name: "short_sha", digest: artifacts.StableFileDigest{SHA256: "abc", Size: 0}},
		{name: "uppercase_sha", digest: artifacts.StableFileDigest{SHA256: strings.Repeat("A", 64), Size: 0}},
		{name: "nonhex_sha", digest: artifacts.StableFileDigest{SHA256: strings.Repeat("g", 64), Size: 0}},
		{name: "negative_size", digest: artifacts.StableFileDigest{SHA256: strings.Repeat("a", 64), Size: -1}},
		{name: "oversize", digest: artifacts.StableFileDigest{SHA256: strings.Repeat("a", 64), Size: maximumControlFileSize + 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CopyAssessmentControlFiles([]BoundAssessmentControlFile{{
				Path:   absolute,
				Digest: &test.digest,
			}})
			requireControlFailure(
				t,
				err,
				"INVALID_ASSESSMENT_CONTROL_FILE",
				procerr.CategoryDomain,
				"saved-plan assessment control binding is invalid",
			)
		})
	}
}

func TestCopyAssessmentControlFilesDeepCopiesEvidence(t *testing.T) {
	digest := artifacts.StableFileDigest{
		SHA256: strings.Repeat("a", 64),
		Size:   maximumControlFileSize,
	}
	identity := artifacts.StableFileIdentity{Dev: 4, Ino: 8}
	followSymlinks := false
	original := []BoundAssessmentControlFile{{
		Path:           filepath.Join(t.TempDir(), "control.json"),
		Digest:         &digest,
		Identity:       &identity,
		FollowSymlinks: &followSymlinks,
	}}

	copied, err := CopyAssessmentControlFiles(original)
	if err != nil {
		t.Fatalf("CopyAssessmentControlFiles(%+v) error = %v, want nil", original, err)
	}
	original[0].Digest.SHA256 = strings.Repeat("b", 64)
	original[0].Identity.Ino = 9
	*original[0].FollowSymlinks = true
	if got, want := copied[0].Digest.SHA256, strings.Repeat("a", 64); got != want {
		t.Errorf("CopyAssessmentControlFiles digest after input mutation = %q, want %q", got, want)
	}
	if got, want := copied[0].Identity.Ino, uint64(8); got != want {
		t.Errorf("CopyAssessmentControlFiles identity after input mutation = %d, want %d", got, want)
	}
	if got := copied[0].FollowSymlinks; got == nil || *got {
		t.Errorf("CopyAssessmentControlFiles followSymlinks after input mutation = %v, want false", got)
	}
}

func TestRecheckAssessmentControlFilesAcceptsUnchangedAndStillAbsentInputs(t *testing.T) {
	directory := t.TempDir()
	present := filepath.Join(directory, "present.json")
	missing := filepath.Join(directory, "missing.json")
	writeControlFile(t, present, []byte("present"))

	boundPresent, err := BindRequiredAssessmentControlText(present, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", present, err)
	}
	boundMissing, err := BindOptionalAssessmentControlText(missing, BindOptions{})
	if err != nil {
		t.Fatalf("BindOptionalAssessmentControlText(%q) error = %v, want nil", missing, err)
	}
	if err := RecheckAssessmentControlFiles([]BoundAssessmentControlFile{
		boundPresent.File,
		boundMissing.File,
	}); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(unchanged, absent) error = %v, want nil", err)
	}
}

func TestRecheckAssessmentControlFilesRejectsContentChangeWithRedactedFailure(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "credential-secret.json")
	writeControlFile(t, filePath, []byte("original-secret"))
	bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}
	writeControlFile(t, filePath, []byte("replacement-secret"))

	err = RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound.File})
	failure := requireAssessmentControlChanged(t, err)
	if strings.Contains(failure.Message, filePath) || strings.Contains(failure.Message, "secret") {
		t.Errorf("RecheckAssessmentControlFiles(%q) failure message = %q, want redacted", filePath, failure.Message)
	}
}

func TestRecheckAssessmentControlFilesRejectsAbsentInputAppearance(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "optional.json")
	bound, err := BindOptionalAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindOptionalAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}
	writeControlFile(t, filePath, []byte("appeared"))
	requireAssessmentControlChanged(
		t,
		RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound.File}),
	)
}

func TestRecheckAssessmentControlFilesAbsenceHonorsNoFollowPolicy(t *testing.T) {
	directory := t.TempDir()
	filePath := filepath.Join(directory, "optional.json")
	followSymlinks := false
	bound := BoundAssessmentControlFile{
		Path:           filePath,
		FollowSymlinks: &followSymlinks,
	}
	if err := RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound}); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(absent no-follow input) error = %v, want nil", err)
	}
	if err := os.Symlink(filepath.Join(directory, "missing-target"), filePath); err != nil {
		t.Fatalf("os.Symlink(dangling, %q) error = %v, want nil", filePath, err)
	}
	requireAssessmentControlChanged(
		t,
		RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound}),
	)
}

func TestRecheckAssessmentControlFilesIdentityBindingControlsSameByteReplacement(t *testing.T) {
	directory := t.TempDir()
	filePath := filepath.Join(directory, "control.json")
	writeControlFile(t, filePath, []byte("same bytes"))
	bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}

	replaceControlFile(t, filePath, []byte("same bytes"))
	requireAssessmentControlChanged(
		t,
		RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound.File}),
	)

	withoutIdentity := copyControlFile(bound.File)
	withoutIdentity.Identity = nil
	if err := RecheckAssessmentControlFiles([]BoundAssessmentControlFile{withoutIdentity}); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(same bytes without bound identity) error = %v, want nil", err)
	}
}

func TestRecheckAssessmentControlFilesRejectsTOCTOUReplacement(t *testing.T) {
	tests := []struct {
		name  string
		hooks func(filePath string) recheckHooks
	}{
		{
			name: "between_initial_stat_and_hash",
			hooks: func(filePath string) recheckHooks {
				return recheckHooks{afterInitialStat: func(int) error {
					replaceControlFile(t, filePath, []byte("same bytes"))
					return nil
				}}
			},
		},
		{
			name: "between_hash_and_final_stat",
			hooks: func(filePath string) recheckHooks {
				return recheckHooks{afterHash: func(int) error {
					replaceControlFile(t, filePath, []byte("same bytes"))
					return nil
				}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filePath := filepath.Join(t.TempDir(), "control.json")
			writeControlFile(t, filePath, []byte("same bytes"))
			bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
			if err != nil {
				t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
			}
			bound.File.Identity = nil
			err = recheckAssessmentControlFiles(
				[]BoundAssessmentControlFile{bound.File},
				test.hooks(filePath),
			)
			requireAssessmentControlChanged(t, err)
		})
	}
}

func TestRecheckAssessmentControlFilesRejectsSameInodeMutationAfterHash(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "control.json")
	writeControlFile(t, filePath, []byte("original"))
	bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}
	if bound.File.Identity == nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q).File.Identity = nil, want stable identity", filePath)
	}
	wantIdentity := *bound.File.Identity

	err = recheckAssessmentControlFiles(
		[]BoundAssessmentControlFile{bound.File},
		recheckHooks{afterHash: func(int) error {
			writeControlFile(t, filePath, []byte("different bytes and size"))
			info, statErr := os.Stat(filePath)
			if statErr != nil {
				return statErr
			}
			gotIdentity, ok := stableIdentity(info)
			if !ok {
				return errors.New("stable identity unavailable")
			}
			if gotIdentity != wantIdentity {
				t.Errorf("same-inode mutation identity = %+v, want unchanged %+v", gotIdentity, wantIdentity)
			}
			return nil
		}},
	)
	requireAssessmentControlChanged(t, err)
}

func TestRecheckAssessmentControlFilesRejectsSymlinkSwap(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.json")
	second := filepath.Join(directory, "second.json")
	link := filepath.Join(directory, "control.json")
	writeControlFile(t, first, []byte("same"))
	writeControlFile(t, second, []byte("same"))
	if err := os.Symlink(first, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", first, link, err)
	}
	bound, err := BindRequiredAssessmentControlText(link, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", link, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", link, err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", second, link, err)
	}
	requireAssessmentControlChanged(
		t,
		RecheckAssessmentControlFiles([]BoundAssessmentControlFile{bound.File}),
	)
}

func TestRecheckAssessmentControlFilesMapsBudgetFailure(t *testing.T) {
	directory := t.TempDir()
	files := make([]BoundAssessmentControlFile, 0, maximumControlFiles+1)
	for index := 0; index < maximumControlFiles+1; index++ {
		filePath := filepath.Join(directory, "control-"+string(rune('a'+index))+".json")
		writeControlFile(t, filePath, []byte("x"))
		bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
		if err != nil {
			t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
		}
		bound.File.Identity = nil
		files = append(files, bound.File)
	}
	requireAssessmentControlChanged(t, RecheckAssessmentControlFiles(files))
}

func TestControlReadLimitsAcceptExactBoundariesAndRejectExcess(t *testing.T) {
	directory := t.TempDir()
	files := make([]BoundAssessmentControlFile, 0, 5)
	for index := 0; index < 4; index++ {
		filePath := filepath.Join(directory, "boundary-"+string(rune('a'+index))+".json")
		writeControlFile(t, filePath, nil)
		if err := os.Truncate(filePath, maximumControlFileSize); err != nil {
			t.Fatalf("os.Truncate(%q, %d) error = %v, want nil", filePath, maximumControlFileSize, err)
		}
		bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
		if err != nil {
			t.Fatalf("BindRequiredAssessmentControlText(%q, exact file limit) error = %v, want nil", filePath, err)
		}
		bound.File.Identity = nil
		files = append(files, bound.File)
	}
	if err := RecheckAssessmentControlFiles(files); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(exact 64-MiB aggregate) error = %v, want nil", err)
	}

	extraPath := filepath.Join(directory, "aggregate-excess.json")
	writeControlFile(t, extraPath, []byte("x"))
	extra, err := BindRequiredAssessmentControlText(extraPath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", extraPath, err)
	}
	extra.File.Identity = nil
	requireAssessmentControlChanged(
		t,
		RecheckAssessmentControlFiles(append(files, extra.File)),
	)

	oversizePath := filepath.Join(directory, "file-excess.json")
	writeControlFile(t, oversizePath, nil)
	if err := os.Truncate(oversizePath, maximumControlFileSize+1); err != nil {
		t.Fatalf("os.Truncate(%q, %d) error = %v, want nil", oversizePath, maximumControlFileSize+1, err)
	}
	_, err = BindRequiredAssessmentControlText(oversizePath, BindOptions{})
	requireControlFailure(
		t,
		err,
		"FILE_LIMIT_EXCEEDED",
		procerr.CategoryIO,
		"input file exceeds the configured size limit",
	)
}

func TestRecheckAssessmentControlFilesShortCircuitsInCallerOrder(t *testing.T) {
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.json")
	secondPath := filepath.Join(directory, "second.json")
	writeControlFile(t, firstPath, []byte("first"))
	writeControlFile(t, secondPath, []byte("second"))
	first, err := BindRequiredAssessmentControlText(firstPath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", firstPath, err)
	}
	second, err := BindRequiredAssessmentControlText(secondPath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", secondPath, err)
	}
	first.File.Identity = nil
	second.File.Identity = nil
	writeControlFile(t, firstPath, []byte("first changed"))

	var observed []int
	err = recheckAssessmentControlFiles(
		[]BoundAssessmentControlFile{first.File, second.File},
		recheckHooks{afterInitialStat: func(index int) error {
			observed = append(observed, index)
			return nil
		}},
	)
	requireAssessmentControlChanged(t, err)
	if len(observed) != 1 || observed[0] != 0 {
		t.Errorf("recheckAssessmentControlFiles caller-order observations = %v, want [0]", observed)
	}
}

func TestRecheckAssessmentControlFilesMapsHookFailureAndPanic(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "control.json")
	writeControlFile(t, filePath, []byte("content"))
	bound, err := BindRequiredAssessmentControlText(filePath, BindOptions{})
	if err != nil {
		t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", filePath, err)
	}
	bound.File.Identity = nil

	tests := []struct {
		name string
		hook func(int) error
	}{
		{name: "error", hook: func(int) error { return errors.New("secret diagnostic") }},
		{name: "panic", hook: func(int) error { panic("secret diagnostic") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := recheckAssessmentControlFiles(
				[]BoundAssessmentControlFile{bound.File},
				recheckHooks{afterInitialStat: test.hook},
			)
			failure := requireAssessmentControlChanged(t, err)
			if strings.Contains(failure.Message, "secret") {
				t.Errorf("recheckAssessmentControlFiles(%s hook) message = %q, want redacted", test.name, failure.Message)
			}
		})
	}
}

func requireControlFailure(
	t *testing.T,
	err error,
	code string,
	category procerr.Category,
	message string,
) *procerr.ProcessFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want *procerr.ProcessFailure code %q", code)
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v (%T), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if got := failure.Code; got != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", got, code)
	}
	if got := failure.Category; got != category {
		t.Errorf("ProcessFailure.Category = %q, want %q", got, category)
	}
	if got := failure.Message; got != message {
		t.Errorf("ProcessFailure.Message = %q, want %q", got, message)
	}
	return failure
}

func requireAssessmentControlChanged(t *testing.T, err error) *procerr.ProcessFailure {
	t.Helper()
	return requireControlFailure(
		t,
		err,
		"ASSESSMENT_CONTROL_CHANGED",
		procerr.CategoryDomain,
		"saved-plan assessment control input changed during assessment",
	)
}

func writeControlFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", filePath, err)
	}
}

func replaceControlFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	replacement := filepath.Join(filepath.Dir(filePath), ".replacement")
	writeControlFile(t, replacement, content)
	if err := os.Rename(replacement, filePath); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", replacement, filePath, err)
	}
}
