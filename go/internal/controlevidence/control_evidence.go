package controlevidence

import (
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	maximumControlFiles    = 8
	maximumControlFileSize = 16 * 1024 * 1024
	maximumControlBytes    = 64 * 1024 * 1024
)

// BindOptions controls whether a final symbolic link may be followed. A nil
// FollowSymlinks value defaults to true, matching the Node contract.
type BindOptions struct {
	FollowSymlinks *bool
}

// BoundAssessmentControlFile records the stable evidence for one assessment
// control input. A nil Digest binds the path's absence. Identity is optional:
// nil covers both the absent and explicit-null source shapes, which have the
// same recheck semantics. A nil FollowSymlinks value means true.
type BoundAssessmentControlFile struct {
	Path           string
	Digest         *artifacts.StableFileDigest
	Identity       *artifacts.StableFileIdentity
	FollowSymlinks *bool
}

// BoundAssessmentControlText pairs decoded control text with its stable file
// evidence. Text is nil when an optional input was absent.
type BoundAssessmentControlText struct {
	Text *string
	File BoundAssessmentControlFile
}

// BindRequiredAssessmentControlText reads and binds one required UTF-8 input.
func BindRequiredAssessmentControlText(
	filePath string,
	options BindOptions,
) (BoundAssessmentControlText, error) {
	if err := validatePath(filePath); err != nil {
		return BoundAssessmentControlText{}, err
	}
	budget, err := newControlReadBudget()
	if err != nil {
		return BoundAssessmentControlText{}, err
	}
	followSymlinks := shouldFollowSymlinks(options.FollowSymlinks)
	source, err := artifacts.ReadBoundedFileBytes(
		filePath,
		budget,
		artifacts.StableReadOptions{FollowSymlinks: followSymlinks},
	)
	if err != nil {
		return BoundAssessmentControlText{}, err
	}
	defer clear(source.Bytes)
	if !utf8.Valid(source.Bytes) {
		return BoundAssessmentControlText{}, domainFailure(
			"INVALID_UTF8",
			"assessment control input is not valid UTF-8",
		)
	}

	text := strings.TrimPrefix(string(source.Bytes), "\ufeff")
	digest := source.Digest
	identity := source.Identity
	return BoundAssessmentControlText{
		Text: &text,
		File: BoundAssessmentControlFile{
			Path:           filePath,
			Digest:         &digest,
			Identity:       &identity,
			FollowSymlinks: boundFollowSymlinks(followSymlinks),
		},
	}, nil
}

// BindOptionalAssessmentControlText binds one UTF-8 input or, when the
// selected path observation reports ENOENT, binds its absence.
func BindOptionalAssessmentControlText(
	filePath string,
	options BindOptions,
) (BoundAssessmentControlText, error) {
	if err := validatePath(filePath); err != nil {
		return BoundAssessmentControlText{}, err
	}
	bound, err := BindRequiredAssessmentControlText(filePath, options)
	if err == nil {
		return bound, nil
	}
	failure, ok := err.(*procerr.ProcessFailure)
	if !ok || failure.Code != "READ_FAILED" {
		return BoundAssessmentControlText{}, err
	}

	followSymlinks := shouldFollowSymlinks(options.FollowSymlinks)
	_, metadataErr := pathInfo(filePath, followSymlinks)
	if errors.Is(metadataErr, fs.ErrNotExist) {
		return BoundAssessmentControlText{
			File: BoundAssessmentControlFile{
				Path:           filePath,
				FollowSymlinks: boundFollowSymlinks(followSymlinks),
			},
		}, nil
	}
	return BoundAssessmentControlText{}, err
}

// CopyAssessmentControlFiles validates and defensively copies control-file
// evidence before it crosses into an assessment or exact-apply operation.
func CopyAssessmentControlFiles(
	files []BoundAssessmentControlFile,
) ([]BoundAssessmentControlFile, error) {
	if len(files) > maximumControlFiles {
		return nil, domainFailure(
			"TOO_MANY_ASSESSMENT_CONTROL_FILES",
			"saved-plan assessment exceeds the control-file limit",
		)
	}

	seen := make(map[string]struct{}, len(files))
	copied := make([]BoundAssessmentControlFile, 0, len(files))
	for _, file := range files {
		if err := validatePath(file.Path); err != nil {
			return nil, err
		}
		if _, ok := seen[file.Path]; ok {
			return nil, domainFailure(
				"DUPLICATE_ASSESSMENT_CONTROL_FILE",
				"saved-plan assessment control files must be unique",
			)
		}
		seen[file.Path] = struct{}{}
		if file.Digest != nil && !validDigest(*file.Digest) {
			return nil, domainFailure(
				"INVALID_ASSESSMENT_CONTROL_FILE",
				"saved-plan assessment control binding is invalid",
			)
		}
		copied = append(copied, copyControlFile(file))
	}
	return copied, nil
}

// RecheckAssessmentControlFiles verifies each bound control input serially in
// caller order. Every metadata, read, digest, or identity failure is mapped to
// one redacted domain failure so source bytes and paths cannot escape.
func RecheckAssessmentControlFiles(files []BoundAssessmentControlFile) error {
	return recheckAssessmentControlFiles(files, recheckHooks{})
}

type recheckHooks struct {
	afterInitialStat func(index int) error
	afterHash        func(index int) error
}

func recheckAssessmentControlFiles(
	files []BoundAssessmentControlFile,
	hooks recheckHooks,
) error {
	if len(files) == 0 {
		return nil
	}
	if !stableIdentityPlatformSupported() {
		return assessmentControlChangedFailure()
	}
	budget, err := newControlReadBudget()
	if err != nil {
		return err
	}
	// Revalidation has an identically bounded, separately charged budget. A
	// single shared budget would make the strengthening below reject a valid
	// eight-file/64-MiB control set solely because each logical input is read
	// twice. Both budgets are consumed serially in the same caller order.
	revalidationBudget, err := newControlReadBudget()
	if err != nil {
		return err
	}
	for index, file := range files {
		followSymlinks := shouldFollowSymlinks(file.FollowSymlinks)
		if file.Digest == nil {
			if err := assertStillAbsent(file.Path, followSymlinks); err != nil {
				return err
			}
			continue
		}

		beforeInfo, err := pathInfo(file.Path, followSymlinks)
		if err != nil || !regularFile(beforeInfo, followSymlinks) {
			return assessmentControlChangedFailure()
		}
		before, ok := stableIdentity(beforeInfo)
		if !ok {
			return assessmentControlChangedFailure()
		}
		if file.Identity != nil && before != *file.Identity {
			return assessmentControlChangedFailure()
		}
		if err := invokeRecheckHook(hooks.afterInitialStat, index); err != nil {
			return assessmentControlChangedFailure()
		}

		current, err := artifacts.SHA256StableFile(
			file.Path,
			budget,
			artifacts.StableReadOptions{FollowSymlinks: followSymlinks},
		)
		if err != nil {
			return assessmentControlChangedFailure()
		}
		if current != *file.Digest {
			return assessmentControlChangedFailure()
		}
		if err := invokeRecheckHook(hooks.afterHash, index); err != nil {
			return assessmentControlChangedFailure()
		}

		// The Node source observes only dev/inode after hashing, which permits an
		// in-place same-inode overwrite in that window. Re-read stable bytes here
		// as an intentional fail-closed Go strengthening; this changes no report
		// or artifact bytes and retains the fixed redacted failure contract.
		revalidated, err := artifacts.SHA256StableFile(
			file.Path,
			revalidationBudget,
			artifacts.StableReadOptions{FollowSymlinks: followSymlinks},
		)
		if err != nil || revalidated != *file.Digest {
			return assessmentControlChangedFailure()
		}

		afterInfo, err := pathInfo(file.Path, followSymlinks)
		if err != nil || !regularFile(afterInfo, followSymlinks) {
			return assessmentControlChangedFailure()
		}
		after, ok := stableIdentity(afterInfo)
		if !ok || after != before {
			return assessmentControlChangedFailure()
		}
	}
	return nil
}

func assertStillAbsent(filePath string, followSymlinks bool) error {
	_, err := pathInfo(filePath, followSymlinks)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return assessmentControlChangedFailure()
}

func validatePath(filePath string) error {
	if !filepath.IsAbs(filePath) || strings.ContainsRune(filePath, '\x00') {
		return domainFailure(
			"UNRESOLVED_ASSESSMENT_CONTROL_PATH",
			"assessment control inputs require resolved absolute paths",
		)
	}
	return nil
}

func newControlReadBudget() (*artifacts.ReadBudget, error) {
	return artifacts.NewReadBudget(artifacts.BoundedReadLimits{
		MaxFiles:            maximumControlFiles,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       big.NewInt(maximumControlBytes),
		MaxFileBytes:        big.NewInt(maximumControlFileSize),
	})
}

func pathInfo(filePath string, followSymlinks bool) (fs.FileInfo, error) {
	if followSymlinks {
		return os.Stat(filePath)
	}
	return os.Lstat(filePath)
}

func regularFile(info fs.FileInfo, followSymlinks bool) bool {
	return info != nil && info.Mode().IsRegular() &&
		(followSymlinks || info.Mode()&fs.ModeSymlink == 0)
}

func shouldFollowSymlinks(value *bool) bool {
	return value == nil || *value
}

func boundFollowSymlinks(followSymlinks bool) *bool {
	if followSymlinks {
		return nil
	}
	value := false
	return &value
}

func copyControlFile(file BoundAssessmentControlFile) BoundAssessmentControlFile {
	result := BoundAssessmentControlFile{Path: file.Path}
	if file.Digest != nil {
		digest := *file.Digest
		result.Digest = &digest
	}
	if file.Identity != nil {
		identity := *file.Identity
		result.Identity = &identity
	}
	if file.FollowSymlinks != nil {
		followSymlinks := *file.FollowSymlinks
		result.FollowSymlinks = &followSymlinks
	}
	return result
}

func validDigest(digest artifacts.StableFileDigest) bool {
	if digest.Size < 0 || digest.Size > maximumControlFileSize || len(digest.SHA256) != 64 {
		return false
	}
	for _, value := range digest.SHA256 {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func invokeRecheckHook(hook func(index int) error, index int) (err error) {
	if hook == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = errors.New("assessment control recheck hook panicked")
		}
	}()
	return hook(index)
}

func domainFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func assessmentControlChangedFailure() *procerr.ProcessFailure {
	return domainFailure(
		"ASSESSMENT_CONTROL_CHANGED",
		"saved-plan assessment control input changed during assessment",
	)
}
