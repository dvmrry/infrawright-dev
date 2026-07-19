package sourcebind

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

// verifyLocalReplaceTargets binds go.mod's reviewed local replacement labels to
// independently supplied SDK roots. The replacement path is never returned and
// is never used to locate analyzer bytes.
func verifyLocalReplaceTargets(
	providerRoot string,
	sdkRoots map[string]string,
	replacements []contracts.LocalModuleReplaceBinding,
) error {
	for index, replacement := range replacements {
		binding := "provider_module.local_replaces[" + strconv.Itoa(index) + "]"
		sdkRoot, exists := sdkRoots[replacement.ModulePath]
		if !exists {
			return failure(ErrorModule, binding, "local replacement must name an explicitly supplied SDK root")
		}
		target, err := resolveLocalReplaceTarget(providerRoot, replacement.LocalPath)
		if err != nil {
			return failure(ErrorModule, binding, "local replacement target cannot be verified safely")
		}
		if err := sameHeldDirectory(target, sdkRoot); err != nil {
			return failure(ErrorModule, binding, "local replacement target does not match the explicitly supplied SDK root")
		}
	}
	return nil
}

func resolveLocalReplaceTarget(providerRoot, localPath string) (string, error) {
	if localPath == "" || strings.ContainsRune(localPath, 0) || strings.Contains(localPath, "\\") || filepath.IsAbs(filepath.FromSlash(localPath)) {
		return "", os.ErrInvalid
	}
	current := providerRoot
	for _, component := range strings.Split(localPath, "/") {
		switch component {
		case ".":
			continue
		case "..":
			current = filepath.Dir(current)
		default:
			current = filepath.Join(current, component)
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", os.ErrInvalid
		}
	}
	return filepath.Clean(current), nil
}

func sameHeldDirectory(targetPath, explicitPath string) error {
	target, err := os.Open(targetPath)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	explicit, err := os.Open(explicitPath)
	if err != nil {
		return err
	}
	defer func() { _ = explicit.Close() }()

	targetIdentity, err := target.Stat()
	if err != nil || !targetIdentity.IsDir() {
		return os.ErrInvalid
	}
	explicitIdentity, err := explicit.Stat()
	if err != nil || !explicitIdentity.IsDir() || !os.SameFile(targetIdentity, explicitIdentity) {
		return os.ErrInvalid
	}
	// Recheck both names against the held descriptors. This refuses a pathname
	// rebind observed during identity comparison without introducing another
	// filesystem abstraction.
	targetAfter, err := os.Lstat(targetPath)
	if err != nil || targetAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(targetIdentity, targetAfter) {
		return os.ErrInvalid
	}
	explicitAfter, err := os.Lstat(explicitPath)
	if err != nil || explicitAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(explicitIdentity, explicitAfter) {
		return os.ErrInvalid
	}
	return nil
}
