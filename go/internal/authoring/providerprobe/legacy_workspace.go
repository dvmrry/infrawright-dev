package providerprobe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// legacyWorkspaceBinding pins a verified private legacy work directory. The
// Root keeps descriptor-relative operations within that directory even if its
// public pathname is renamed. Callers still need recheckPublicPath immediately
// before passing a pathname to an external executable; a same-UID peer can
// rebind that pathname after the check, which Go cannot eliminate while Git
// and Terraform require a pathname CWD/destination.
type legacyWorkspaceBinding struct {
	path string
	info os.FileInfo
	root *os.Root
}

func bindLegacyWorkspace(work string) (*legacyWorkspaceBinding, error) {
	info, err := os.Lstat(work)
	if err != nil || !privateDirectory(info) {
		return nil, fmt.Errorf("legacy work directory is not a private non-symlink directory: %s", work)
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		return nil, fmt.Errorf("bind legacy work directory: %w", err)
	}
	bound, err := root.Stat(".")
	if err != nil || !privateDirectory(bound) || !os.SameFile(info, bound) {
		_ = root.Close()
		return nil, fmt.Errorf("legacy work directory changed while binding: %s", work)
	}
	return &legacyWorkspaceBinding{path: work, info: info, root: root}, nil
}

func (b *legacyWorkspaceBinding) Close() error {
	if b == nil || b.root == nil {
		return nil
	}
	return b.root.Close()
}

func (b *legacyWorkspaceBinding) recheckPublicPath() error {
	if b == nil || b.root == nil {
		return errors.New("legacy work directory is not bound")
	}
	current, err := os.Lstat(b.path)
	if err != nil || !privateDirectory(current) || !os.SameFile(b.info, current) {
		return fmt.Errorf("legacy work directory changed before external command: %s", b.path)
	}
	bound, err := b.root.Stat(".")
	if err != nil || !privateDirectory(bound) || !os.SameFile(b.info, bound) {
		return fmt.Errorf("bound legacy work directory changed before external command: %s", b.path)
	}
	return nil
}

func (b *legacyWorkspaceBinding) directory(name string) (*os.Root, os.FileInfo, error) {
	info, err := b.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := b.root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("create legacy workspace directory %q: %w", name, err)
		}
		info, err = b.root.Lstat(name)
	}
	if err != nil || !privateDirectory(info) {
		return nil, nil, fmt.Errorf("legacy workspace directory %q must be a private non-symlink directory", name)
	}
	directory, err := b.root.OpenRoot(name)
	if err != nil {
		return nil, nil, fmt.Errorf("bind legacy workspace directory %q: %w", name, err)
	}
	bound, err := directory.Stat(".")
	if err != nil || !privateDirectory(bound) || !os.SameFile(info, bound) {
		_ = directory.Close()
		return nil, nil, fmt.Errorf("legacy workspace directory %q changed while binding", name)
	}
	return directory, info, nil
}

func (b *legacyWorkspaceBinding) verifyDirectoryPath(name string, expected os.FileInfo) error {
	if err := b.recheckPublicPath(); err != nil {
		return err
	}
	current, err := b.root.Lstat(name)
	if err != nil || !privateDirectory(current) || !os.SameFile(expected, current) {
		return fmt.Errorf("legacy workspace directory %q changed before external command", name)
	}
	public := filepath.Join(b.path, name)
	pathInfo, err := os.Lstat(public)
	if err != nil || !privateDirectory(pathInfo) || !os.SameFile(expected, pathInfo) {
		return fmt.Errorf("legacy workspace pathname %q changed before external command", name)
	}
	return nil
}

func (b *legacyWorkspaceBinding) rejectStaticAliases() error {
	for _, name := range []string{"inputs", "terraform-schema", "artifacts"} {
		info, err := b.root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !privateDirectory(info) {
			return fmt.Errorf("legacy workspace alias %q must be a private non-symlink directory", name)
		}
	}
	for _, name := range []string{
		"inputs/provider-schema.json",
		"inputs/openapi.json",
		"inputs/openapi.raw",
		"terraform-schema/main.tf",
		"artifacts/source-registry.json",
		"artifacts/source-diagnostics.json",
		"artifacts/openapi-map.json",
		"artifacts/summary.json",
		"artifacts/summary.md",
	} {
		info, err := b.root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("legacy workspace alias %q must be a regular non-symlink file", name)
		}
	}
	return nil
}

func legacyReadRegular(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("legacy workspace file %q must be a regular non-symlink file", name)
	}
	return root.ReadFile(name)
}

// legacyAtomicWrite replaces a fixed workspace file through a random sibling.
// Root.Rename replaces a final symlink rather than opening through it.
func legacyAtomicWrite(root *os.Root, name string, body []byte, mode os.FileMode) (err error) {
	temporary, err := legacyTemporaryName(root, name)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create private temporary workspace file: %w", err)
	}
	created, statErr := file.Stat()
	removeTemporary := true
	defer func() {
		if removeTemporary && statErr == nil {
			removeLegacyTemporary(root, temporary, created)
		}
	}()
	if statErr != nil || !created.Mode().IsRegular() {
		_ = file.Close()
		return errors.New("inspect private temporary workspace file")
	}
	if written, writeErr := file.Write(body); writeErr != nil || written != len(body) {
		_ = file.Close()
		return errors.New("write private temporary workspace file")
	}
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("close private temporary workspace file: %w", closeErr)
	}
	if err := root.Rename(temporary, name); err != nil {
		return fmt.Errorf("replace private workspace file: %w", err)
	}
	removeTemporary = false
	return nil
}

func legacyTemporaryName(root *os.Root, target string) (string, error) {
	for range 32 {
		bytes := make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
			return "", fmt.Errorf("generate workspace temporary name: %w", err)
		}
		name := "." + filepath.Base(target) + "." + hex.EncodeToString(bytes) + ".tmp"
		if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect workspace temporary name: %w", err)
		}
	}
	return "", errors.New("exhausted workspace temporary names")
}

func removeLegacyTemporary(root *os.Root, name string, expected os.FileInfo) {
	info, err := root.Lstat(name)
	if err == nil && info.Mode().IsRegular() && os.SameFile(expected, info) {
		_ = root.Remove(name)
	}
}
