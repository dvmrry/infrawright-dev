package terraformcmd

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/pyoserr"
)

// terraformExecutableGetwd is replaced only by tests that pin lazy cwd reads.
var terraformExecutableGetwd = os.Getwd

// TerraformExecutableCandidateOptions selects lexical path behavior for
// focused portability tests. CWDSet distinguishes an explicitly empty cwd
// from an omitted cwd, matching JavaScript's optional-property semantics.
type TerraformExecutableCandidateOptions struct {
	CWD      string
	CWDSet   bool
	Platform string
}

// TerraformExecutableCandidates ports the source's lexical candidate
// expansion. It performs no filesystem access.
func TerraformExecutableCandidates(
	selected string,
	environment map[string]string,
	options *TerraformExecutableCandidateOptions,
) ([]string, error) {
	requested := selected
	if requested == "" {
		requested = "terraform"
	}
	if strings.IndexByte(requested, 0) >= 0 {
		return nil, domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform executable path contains an embedded null character",
		)
	}
	if !utf8.ValidString(requested) {
		return nil, domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform executable path is not valid UTF-8",
		)
	}

	processCWD := ""
	currentCWD := func() (string, error) {
		if processCWD != "" {
			return processCWD, nil
		}
		value, err := terraformExecutableGetwd()
		if err != nil {
			return "", err
		}
		if !utf8.ValidString(value) {
			return "", domainFailure(
				"UNRESOLVED_TERRAFORM_COMMAND_PATH",
				"Terraform executable path is not valid UTF-8",
			)
		}
		processCWD = value
		return processCWD, nil
	}
	cwd := ""
	cwdSet := false
	platform := runtime.GOOS
	if options != nil {
		if options.CWDSet || options.CWD != "" {
			cwd = options.CWD
			cwdSet = true
		}
		if options.Platform != "" {
			platform = options.Platform
		}
	}
	if !cwdSet {
		var err error
		cwd, err = currentCWD()
		if err != nil {
			return nil, err
		}
	}
	if !utf8.ValidString(cwd) {
		return nil, domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform executable path is not valid UTF-8",
		)
	}
	isWindows := platform == "windows" || platform == "win32"
	winAbsolute := isWindowsAbsolute(requested)
	explicit := path.IsAbs(requested) || winAbsolute || strings.Contains(requested, "/") ||
		(isWindows && strings.Contains(requested, `\`))
	if explicit {
		if winAbsolute && !isWindows {
			return []string{requested}, nil
		}
		if isWindows {
			resolved, err := resolveWindowsPath(currentCWD, cwd, requested)
			if err != nil {
				return nil, err
			}
			return []string{resolved}, nil
		}
		resolved := resolvePosixPath(cwd, requested)
		if !path.IsAbs(resolved) {
			hostCWD, err := currentCWD()
			if err != nil {
				return nil, err
			}
			resolved = resolvePosixPath(hostCWD, cwd, requested)
		}
		return []string{resolved}, nil
	}

	pathValue, ok := environmentValue(environment, "PATH", "Path", "path")
	if !ok {
		return []string{}, nil
	}
	if !utf8.ValidString(pathValue) {
		return nil, domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform executable search path is not valid UTF-8",
		)
	}
	delimiter := ":"
	if isWindows {
		delimiter = ";"
	}
	components := strings.Split(pathValue, delimiter)
	directories := make([]string, 0, len(components))
	for _, component := range components {
		if isWindows {
			if component != "" {
				directories = append(directories, component)
			}
			continue
		}
		if component == "" {
			component = cwd
		}
		directories = append(directories, component)
	}

	names := []string{requested}
	if isWindows && windowsExt(requested) == "" {
		pathExt := ".COM;.EXE;.BAT;.CMD"
		if configured, exists := environment["PATHEXT"]; exists {
			pathExt = configured
		}
		if !utf8.ValidString(pathExt) {
			return nil, domainFailure(
				"UNRESOLVED_TERRAFORM_COMMAND_PATH",
				"Terraform executable search path is not valid UTF-8",
			)
		}
		names = names[:0]
		for _, extension := range strings.Split(pathExt, ";") {
			if extension != "" {
				lowered, err := nodeUnicode16Lower(extension)
				if err != nil {
					return nil, err
				}
				names = append(names, requested+lowered)
			}
		}
	}

	// path.resolve uses the process cwd for non-empty relative PATH entries,
	// not options.cwd. Preserve that subtle source behavior.
	result := make([]string, 0, len(directories)*len(names))
	for _, directory := range directories {
		for _, name := range names {
			if isWindows {
				resolved, err := resolveWindowsPath(currentCWD, directory, name)
				if err != nil {
					return nil, err
				}
				result = append(result, resolved)
			} else {
				resolved := resolvePosixPath(directory, name)
				if !path.IsAbs(resolved) {
					hostCWD, err := currentCWD()
					if err != nil {
						return nil, err
					}
					resolved = resolvePosixPath(hostCWD, directory, name)
				}
				result = append(result, resolved)
			}
		}
	}
	return result, nil
}

func resolvePosixPath(parts ...string) string {
	resolved := ""
	for index := len(parts) - 1; index >= 0; index-- {
		part := parts[index]
		if part == "" {
			continue
		}
		if resolved == "" {
			resolved = part
		} else {
			resolved = part + "/" + resolved
		}
		if strings.HasPrefix(part, "/") {
			break
		}
	}
	if resolved == "" {
		return "."
	}
	return path.Clean(resolved)
}

func environmentValue(environment map[string]string, names ...string) (string, bool) {
	for _, name := range names {
		if value, ok := environment[name]; ok {
			return value, true
		}
	}
	return "", false
}

// ResolveTerraformExecutable resolves the CLI/TF selection to a trusted real
// regular executable. Candidate failures are deliberately collapsed to the
// frozen Python-compatible missing-path error.
func ResolveTerraformExecutable(selected string, environment map[string]string) (string, error) {
	requested := selected
	if requested == "" {
		requested = "terraform"
	}
	candidates, err := TerraformExecutableCandidates(selected, environment, nil)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if err := executableAccess(candidate); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if !utf8.ValidString(resolved) {
			continue
		}
		metadata, err := os.Lstat(resolved)
		if err != nil || !metadata.Mode().IsRegular() ||
			(runtime.GOOS != "windows" && metadata.Mode().Perm()&0o111 == 0) {
			continue
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}
		return absolute, nil
	}
	return "", pyoserr.Missing(requested)
}

func isWindowsAbsolute(value string) bool {
	if len(value) >= 3 && isASCIILetter(value[0]) && value[1] == ':' && isWindowsSeparator(value[2]) {
		return true
	}
	return len(value) > 0 && isWindowsSeparator(value[0])
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isWindowsSeparator(value byte) bool {
	return value == '\\' || value == '/'
}

func windowsExt(value string) string {
	start := 0
	startDot := -1
	startPart := 0
	end := -1
	matchedSlash := true
	preDotState := 0
	if len(value) >= 2 && value[1] == ':' && isASCIILetter(value[0]) {
		start = 2
		startPart = 2
	}
	for index := len(value) - 1; index >= start; index-- {
		character := value[index]
		if isWindowsSeparator(character) {
			if !matchedSlash {
				startPart = index + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = index + 1
		}
		if character == '.' {
			if startDot == -1 {
				startDot = index
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}
	if startDot == -1 || end == -1 || preDotState == 0 ||
		preDotState == 1 && startDot == end-1 && startDot == startPart+1 {
		return ""
	}
	return value[startDot:end]
}

// resolveWindowsPath ports node:path.win32.resolve. currentCWD stays lazy so
// an absolute rightmost argument does not inspect a missing process cwd.
func resolveWindowsPath(currentCWD func() (string, error), parts ...string) (string, error) {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false
	for index := len(parts) - 1; index >= -1; index-- {
		var value string
		if index >= 0 {
			value = parts[index]
			if value == "" {
				continue
			}
		} else if resolvedDevice == "" {
			var err error
			value, err = currentCWD()
			if err != nil {
				return "", err
			}
		} else {
			value = os.Getenv("=" + resolvedDevice)
			if value == "" {
				var err error
				value, err = currentCWD()
				if err != nil {
					return "", err
				}
			}
			if value == "" || len(value) > 2 &&
				!strings.EqualFold(value[:2], resolvedDevice) && value[2] == '\\' {
				value = resolvedDevice + `\`
			}
		}

		rootEnd, device, absolute := windowsPathRoot(value)
		if device != "" {
			if resolvedDevice != "" && !strings.EqualFold(device, resolvedDevice) {
				continue
			}
			if resolvedDevice == "" {
				resolvedDevice = device
			}
		}
		if resolvedAbsolute {
			if resolvedDevice != "" {
				break
			}
			continue
		}
		resolvedTail = value[rootEnd:] + `\` + resolvedTail
		resolvedAbsolute = absolute
		if absolute && resolvedDevice != "" {
			break
		}
	}

	resolvedTail = normalizeWindowsTail(resolvedTail, !resolvedAbsolute)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail, nil
	}
	if resolvedDevice+resolvedTail == "" {
		return ".", nil
	}
	return resolvedDevice + resolvedTail, nil
}

func windowsPathRoot(value string) (rootEnd int, device string, absolute bool) {
	length := len(value)
	if length == 0 {
		return 0, "", false
	}
	if length == 1 {
		if isWindowsSeparator(value[0]) {
			return 1, "", true
		}
		return 0, "", false
	}
	if isWindowsSeparator(value[0]) {
		absolute = true
		if !isWindowsSeparator(value[1]) {
			return 1, "", true
		}
		index := 2
		last := index
		for index < length && !isWindowsSeparator(value[index]) {
			index++
		}
		if index >= length || index == last {
			return 0, "", true
		}
		firstPart := value[last:index]
		last = index
		for index < length && isWindowsSeparator(value[index]) {
			index++
		}
		if index >= length || index == last {
			return 0, "", true
		}
		last = index
		for index < length && !isWindowsSeparator(value[index]) {
			index++
		}
		if index != length && index == last {
			return 0, "", true
		}
		if firstPart == "." || firstPart == "?" {
			return 4, `\\` + firstPart, true
		}
		return index, `\\` + firstPart + `\` + value[last:index], true
	}
	if isASCIILetter(value[0]) && value[1] == ':' {
		rootEnd = 2
		device = value[:2]
		if length > 2 && isWindowsSeparator(value[2]) {
			rootEnd = 3
			absolute = true
		}
	}
	return rootEnd, device, absolute
}

func normalizeWindowsTail(value string, allowAboveRoot bool) string {
	result := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var character byte
	for index := 0; index <= len(value); index++ {
		if index < len(value) {
			character = value[index]
		} else if isWindowsSeparator(character) {
			break
		} else {
			character = '/'
		}
		if isWindowsSeparator(character) {
			switch {
			case lastSlash == index-1 || dots == 1:
			case dots == 2:
				if len(result) < 2 || lastSegmentLength != 2 ||
					result[len(result)-1] != '.' || result[len(result)-2] != '.' {
					if len(result) > 2 {
						lastSlashIndex := len(result) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							result = ""
							lastSegmentLength = 0
						} else {
							result = result[:lastSlashIndex]
							lastSegmentLength = len(result) - 1 - strings.LastIndexByte(result, '\\')
						}
						lastSlash = index
						dots = 0
						continue
					}
					if result != "" {
						result = ""
						lastSegmentLength = 0
						lastSlash = index
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if result != "" {
						result += `\..`
					} else {
						result = ".."
					}
					lastSegmentLength = 2
				}
			default:
				if result != "" {
					result += `\` + value[lastSlash+1:index]
				} else {
					result = value[lastSlash+1 : index]
				}
				lastSegmentLength = index - lastSlash - 1
			}
			lastSlash = index
			dots = 0
		} else if character == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}
	return result
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}
