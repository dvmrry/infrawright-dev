package artifactpublish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Artifact is one caller-owned detached byte stream to publish under Name.
// Name must occur exactly once in Options.Vocabulary and must be a portable
// relative filename, not a path.
type Artifact struct {
	Name  string
	Bytes []byte
}

// Vocabulary fixes the complete artifact namespace for one publication mode.
// Every Required name must be supplied exactly once. Optional names may be
// supplied once; omitted optional names are absent after a successful
// publication, including when an older destination contained them.
type Vocabulary struct {
	Required []string
	Optional []string
}

// Options selects one complete artifact-directory replacement. Destination's
// parent must already exist. Publish copies all caller-owned slices before it
// starts filesystem work.
type Options struct {
	Destination string
	Vocabulary  Vocabulary
	Artifacts   []Artifact
}

// FailureKind identifies the transaction phase that could not complete.
type FailureKind string

const (
	// FailureLockConflict means another cooperative publisher already owns the
	// sibling lock. Publish never steals that lock.
	FailureLockConflict FailureKind = "lock_conflict"
	// FailurePreflight means inputs or the destination topology were rejected
	// before publication began.
	FailurePreflight FailureKind = "preflight"
	// FailureCancelled means context cancellation was observed before the old
	// destination was moved to backup.
	FailureCancelled FailureKind = "cancelled"
	// FailureStage means stage creation, writes, or readback validation failed.
	FailureStage FailureKind = "stage"
	// FailureBackup means the old destination could not move to backup.
	FailureBackup FailureKind = "backup"
	// FailurePromote means the validated stage could not replace the destination.
	FailurePromote FailureKind = "promote"
	// FailureRollback means promotion failed and the old destination could not
	// be restored. The retained backup and stage are recovery evidence.
	FailureRollback FailureKind = "rollback"
	// FailureCommittedCleanup means the new destination was committed but a
	// backup or lock cleanup failed. Publish never rolls back a committed set.
	FailureCommittedCleanup FailureKind = "committed_cleanup"
	// FailureCleanup means cleanup after an uncommitted transaction failed. The
	// retained stage is recovery evidence; the primary transaction failure is
	// joined with this error.
	FailureCleanup FailureKind = "cleanup"
)

// Error reports a classified publication failure. Committed is true only when
// Destination already contains the new complete set and a later cleanup failed.
type Error struct {
	Kind      FailureKind
	Committed bool
	Err       error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("artifact publication %s", e.Kind)
	}
	return fmt.Sprintf("artifact publication %s: %v", e.Kind, e.Err)
}

// Unwrap returns the filesystem or context error that caused this failure.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type artifact struct {
	name  string
	bytes []byte
}

type preparedOptions struct {
	destination string
	parent      string
	base        string
	lock        string
	backup      string
	artifacts   []artifact
}

type operations struct {
	lstat     func(string) (fs.FileInfo, error)
	mkdir     func(string, fs.FileMode) error
	mkdirTemp func(string, string) (string, error)
	writeFile func(string, []byte, fs.FileMode) error
	readFile  func(string) ([]byte, error)
	readDir   func(string) ([]os.DirEntry, error)
	rename    func(string, string) error
	remove    func(string) error
	removeAll func(string) error
}

func productionOperations() operations {
	return operations{
		lstat:     os.Lstat,
		mkdir:     os.Mkdir,
		mkdirTemp: os.MkdirTemp,
		writeFile: os.WriteFile,
		readFile:  os.ReadFile,
		readDir:   os.ReadDir,
		rename:    os.Rename,
		remove:    os.Remove,
		removeAll: os.RemoveAll,
	}
}

// Publish validates and transactionally replaces Destination with exactly the
// supplied artifact set. The lock is cooperative: a pre-existing sibling lock
// is reported as FailureLockConflict and is never removed or reused.
//
// Cancellation is checked only before the old destination moves to backup. A
// transaction that has moved the old destination always either promotes the
// stage or attempts rollback before it returns.
func Publish(ctx context.Context, options Options) error {
	return publish(ctx, options, productionOperations())
}

func publish(ctx context.Context, options Options, ops operations) (result error) {
	committed := false
	prepared, err := prepare(options, ops)
	if err != nil {
		return err
	}
	if ctx == nil {
		return publicationError(FailurePreflight, false, errors.New("context is required"))
	}
	if err := ctx.Err(); err != nil {
		return publicationError(FailureCancelled, false, err)
	}

	if err := ops.mkdir(prepared.lock, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return publicationError(FailureLockConflict, false, err)
		}
		return publicationError(FailurePreflight, false, fmt.Errorf("create sibling lock: %w", err))
	}
	defer func() {
		if err := ops.remove(prepared.lock); err != nil {
			kind := FailureCleanup
			if committed {
				kind = FailureCommittedCleanup
			}
			cleanup := publicationError(kind, committed, fmt.Errorf("remove sibling lock: %w", err))
			if result == nil {
				result = cleanup
				return
			}
			result = errors.Join(result, cleanup)
		}
	}()

	stage, err := ops.mkdirTemp(prepared.parent, "."+prepared.base+".stage-")
	if err != nil {
		return publicationError(FailureStage, false, fmt.Errorf("create private stage: %w", err))
	}
	stageCleanup := true
	defer func() {
		if !stageCleanup {
			return
		}
		if err := ops.removeAll(stage); err != nil {
			cleanup := publicationError(FailureCleanup, false, fmt.Errorf("remove stage: %w", err))
			if result == nil {
				result = cleanup
				return
			}
			result = errors.Join(result, cleanup)
		}
	}()

	if err := validatePrivateStageDirectory(stage, ops); err != nil {
		return publicationError(FailureStage, false, err)
	}
	if err := writeAndValidateStage(stage, prepared.artifacts, ops); err != nil {
		return publicationError(FailureStage, false, err)
	}
	if err := ctx.Err(); err != nil {
		return publicationError(FailureCancelled, false, err)
	}

	hadDestination, err := existingDestination(prepared.destination, ops)
	if err != nil {
		return publicationError(FailurePreflight, false, err)
	}
	if hadDestination {
		if err := ops.rename(prepared.destination, prepared.backup); err != nil {
			return publicationError(FailureBackup, false, fmt.Errorf("move old destination to backup: %w", err))
		}
	}

	if err := ops.rename(stage, prepared.destination); err != nil {
		promotion := publicationError(FailurePromote, false, fmt.Errorf("promote staged destination: %w", err))
		if !hadDestination {
			return promotion
		}
		if rollbackErr := ops.rename(prepared.backup, prepared.destination); rollbackErr != nil {
			stageCleanup = false
			return errors.Join(promotion, publicationError(FailureRollback, false, fmt.Errorf("restore old destination: %w", rollbackErr)))
		}
		return promotion
	}
	stageCleanup = false
	committed = true

	if !hadDestination {
		return nil
	}
	if err := ops.removeAll(prepared.backup); err != nil {
		return publicationError(FailureCommittedCleanup, true, fmt.Errorf("remove old backup: %w", err))
	}
	return nil
}

func prepare(options Options, ops operations) (preparedOptions, error) {
	prepared, err := prepareOptions(options)
	if err != nil {
		return preparedOptions{}, publicationError(FailurePreflight, false, err)
	}
	parentInfo, err := ops.lstat(prepared.parent)
	if err != nil {
		return preparedOptions{}, publicationError(FailurePreflight, false, fmt.Errorf("inspect destination parent: %w", err))
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return preparedOptions{}, publicationError(FailurePreflight, false, errors.New("destination parent must be a directory"))
	}
	if _, err := ops.lstat(prepared.backup); err == nil {
		return preparedOptions{}, publicationError(FailurePreflight, false, errors.New("sibling backup already exists"))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return preparedOptions{}, publicationError(FailurePreflight, false, fmt.Errorf("inspect sibling backup: %w", err))
	}
	if _, err := existingDestination(prepared.destination, ops); err != nil {
		return preparedOptions{}, publicationError(FailurePreflight, false, err)
	}
	return prepared, nil
}

func prepareOptions(options Options) (preparedOptions, error) {
	if options.Destination == "" {
		return preparedOptions{}, errors.New("destination is required")
	}
	destination := filepath.Clean(options.Destination)
	base := filepath.Base(destination)
	parent := filepath.Dir(destination)
	if base == "." || base == ".." || base == string(filepath.Separator) || parent == destination {
		return preparedOptions{}, errors.New("destination must name a child directory")
	}
	artifacts, err := validateArtifacts(options.Vocabulary, options.Artifacts)
	if err != nil {
		return preparedOptions{}, err
	}
	return preparedOptions{
		destination: destination,
		parent:      parent,
		base:        base,
		lock:        destination + ".lock",
		backup:      destination + ".backup",
		artifacts:   artifacts,
	}, nil
}

func validateArtifacts(vocabulary Vocabulary, supplied []Artifact) ([]artifact, error) {
	allowed := make(map[string]bool, len(vocabulary.Required)+len(vocabulary.Optional))
	required := make(map[string]bool, len(vocabulary.Required))
	for _, name := range vocabulary.Required {
		if err := validateArtifactName(name); err != nil {
			return nil, fmt.Errorf("required vocabulary: %w", err)
		}
		if allowed[name] {
			return nil, fmt.Errorf("duplicate vocabulary name %q", name)
		}
		allowed[name] = true
		required[name] = true
	}
	for _, name := range vocabulary.Optional {
		if err := validateArtifactName(name); err != nil {
			return nil, fmt.Errorf("optional vocabulary: %w", err)
		}
		if allowed[name] {
			return nil, fmt.Errorf("duplicate vocabulary name %q", name)
		}
		allowed[name] = true
	}
	if len(allowed) == 0 {
		return nil, errors.New("artifact vocabulary is empty")
	}

	seen := make(map[string]bool, len(supplied))
	artifacts := make([]artifact, 0, len(supplied))
	for _, suppliedArtifact := range supplied {
		if err := validateArtifactName(suppliedArtifact.Name); err != nil {
			return nil, err
		}
		if !allowed[suppliedArtifact.Name] {
			return nil, fmt.Errorf("artifact %q is outside the fixed vocabulary", suppliedArtifact.Name)
		}
		if seen[suppliedArtifact.Name] {
			return nil, fmt.Errorf("duplicate artifact %q", suppliedArtifact.Name)
		}
		if suppliedArtifact.Bytes == nil {
			return nil, fmt.Errorf("artifact %q bytes are nil", suppliedArtifact.Name)
		}
		seen[suppliedArtifact.Name] = true
		artifacts = append(artifacts, artifact{
			name:  suppliedArtifact.Name,
			bytes: append([]byte(nil), suppliedArtifact.Bytes...),
		})
	}
	for name := range required {
		if !seen[name] {
			return nil, fmt.Errorf("required artifact %q is missing", name)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].name < artifacts[j].name })
	return artifacts, nil
}

func validateArtifactName(name string) error {
	if name == "" || name == "." || name == ".." ||
		filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("artifact name %q is not a portable relative filename", name)
	}
	return nil
}

func existingDestination(destination string, ops operations) (bool, error) {
	info, err := ops.lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect destination before backup: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("destination must be a directory")
	}
	return true, nil
}

func validatePrivateStageDirectory(stage string, ops operations) error {
	info, err := ops.lstat(stage)
	if err != nil {
		return fmt.Errorf("inspect private stage: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("private stage is not a private directory")
	}
	return nil
}

func writeAndValidateStage(stage string, artifacts []artifact, ops operations) error {
	for _, artifact := range artifacts {
		if err := ops.writeFile(filepath.Join(stage, artifact.name), artifact.bytes, 0o600); err != nil {
			return fmt.Errorf("write staged artifact %q: %w", artifact.name, err)
		}
	}
	entries, err := ops.readDir(stage)
	if err != nil {
		return fmt.Errorf("read staged artifact directory: %w", err)
	}
	if len(entries) != len(artifacts) {
		return fmt.Errorf("staged artifact count = %d, want %d", len(entries), len(artifacts))
	}
	for index, artifact := range artifacts {
		entry := entries[index]
		if entry.Name() != artifact.name || !entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged artifact %q is not the expected regular file", artifact.name)
		}
		data, err := ops.readFile(filepath.Join(stage, artifact.name))
		if err != nil {
			return fmt.Errorf("read staged artifact %q: %w", artifact.name, err)
		}
		if !bytes.Equal(data, artifact.bytes) {
			return fmt.Errorf("staged artifact %q bytes changed before promotion", artifact.name)
		}
	}
	return nil
}

func publicationError(kind FailureKind, committed bool, err error) error {
	return &Error{Kind: kind, Committed: committed, Err: err}
}
