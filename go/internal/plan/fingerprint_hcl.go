package plan

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/posixpath"
)

// Python re's Unicode \s/str.strip set. JavaScript \s differs for FEFF,
// C0 separators, and NEL, all of which can alter structural recognition.
const pythonWhitespacePattern = "[\\t\\n\\v\\f\\r \\x1c-\\x1f\u0085\u00a0" +
	"\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]"

var (
	pythonStripPattern = regexp.MustCompile(
		"^" + pythonWhitespacePattern + "+|" + pythonWhitespacePattern + "+$",
	)
	moduleStartPattern = regexp.MustCompile(
		"^" + pythonWhitespacePattern + "*module" + pythonWhitespacePattern +
			`+"([^"]+)"` + pythonWhitespacePattern + `*\{` + pythonWhitespacePattern + "*$",
	)
	sourceLinePattern = regexp.MustCompile(
		"^" + pythonWhitespacePattern + "*source" + pythonWhitespacePattern + "*=" +
			pythonWhitespacePattern + `*"([^"\\]+)"` + pythonWhitespacePattern + "*$",
	)
	itemsLinePattern = regexp.MustCompile(
		"^" + pythonWhitespacePattern + "*items" + pythonWhitespacePattern + "*=" +
			pythonWhitespacePattern + `*(?:var|local)\.[A-Za-z_][A-Za-z0-9_]*` +
			pythonWhitespacePattern + "*$",
	)
	heredocPattern = regexp.MustCompile(`^<<-?[A-Za-z_][A-Za-z0-9_-]*`)
)

func splitLinesKeepEnds(text string) []string {
	out := make([]string, 0)
	start := 0
	for index := 0; index < len(text); {
		if text[index] == '\r' {
			end := index + 1
			if end < len(text) && text[end] == '\n' {
				end++
			}
			out = append(out, text[start:end])
			start = end
			index = end
			continue
		}
		r, size := utf8.DecodeRuneInString(text[index:])
		if isFingerprintLineSeparator(r) {
			end := index + size
			out = append(out, text[start:end])
			start = end
			index = end
			continue
		}
		index += size
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

func isFingerprintLineSeparator(r rune) bool {
	switch r {
	case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	default:
		return false
	}
}

func pythonStrip(value string) string {
	return pythonStripPattern.ReplaceAllString(value, "")
}

func hclStructureLines(text, filePath string) ([]string, error) {
	out := make([]string, 0)
	blockComment := false
	for lineIndex, line := range splitLinesKeepEnds(text) {
		lineNumber := lineIndex + 1
		var code strings.Builder
		inString := false
		escaped := false
		index := 0
		for index < len(line) {
			if blockComment {
				end := strings.Index(line[index:], "*/")
				if end < 0 {
					if strings.HasSuffix(line, "\r") || strings.HasSuffix(line, "\n") {
						code.WriteByte('\n')
					}
					index = len(line)
					continue
				}
				end += index
				code.WriteString(strings.Repeat(" ", end+2-index))
				blockComment = false
				index = end + 2
				continue
			}

			character := line[index]
			if inString {
				code.WriteByte(character)
				if escaped {
					escaped = false
				} else if character == '\\' {
					escaped = true
				} else if character == '"' {
					inString = false
				}
				index++
				continue
			}
			if character == '"' {
				code.WriteByte(character)
				inString = true
				index++
				continue
			}
			if character == '#' || strings.HasPrefix(line[index:], "//") {
				if strings.HasSuffix(line, "\r") || strings.HasSuffix(line, "\n") {
					code.WriteByte('\n')
				}
				break
			}
			if strings.HasPrefix(line[index:], "/*") {
				code.WriteString("  ")
				blockComment = true
				index += 2
				continue
			}
			if strings.HasPrefix(line[index:], "<<") && heredocPattern.MatchString(line[index:]) {
				return nil, fmt.Errorf(
					"%s:%d contains a heredoc outside the generated-root contract; run make gen-env to regenerate the root",
					filePath,
					lineNumber,
				)
			}
			code.WriteByte(character)
			index++
		}
		if inString {
			return nil, fmt.Errorf("%s:%d contains an unterminated quoted string", filePath, lineNumber)
		}
		out = append(out, code.String())
	}
	if blockComment {
		return nil, fmt.Errorf("%s contains an unterminated block comment", filePath)
	}
	return out, nil
}

func hclBraceDelta(line string) int {
	delta := 0
	inString := false
	escaped := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		if inString {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta
}

func readFingerprintUTF8(filePath string, budget *artifacts.ReadBudget) (string, error) {
	file, err := artifacts.ReadBoundedUTF8File(filePath, budget, artifacts.StableReadOptions{
		FollowSymlinks: true,
	})
	if err != nil {
		return "", err
	}
	return file.Text, nil
}

// RootModuleSources ports rootModuleSources from
// the original implementation. A nil budget selects the source default.
func RootModuleSources(envDir string, budget *artifacts.ReadBudget) (map[string]string, error) {
	budget = fingerprintBudget(budget)
	sources := make(map[string]string)
	if !isDirectory(envDir) {
		return sources, nil
	}
	names, err := directoryNames(envDir, budget, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range canonjson.SortedStrings(names) {
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		filePath := posixpath.Join(envDir, name)
		if !isFile(filePath) {
			continue
		}
		text, err := readFingerprintUTF8(filePath, budget)
		if err != nil {
			return nil, err
		}
		lines, err := hclStructureLines(text, filePath)
		if err != nil {
			return nil, err
		}
		if err := scanModuleSourceLines(lines, filePath, envDir, sources); err != nil {
			return nil, err
		}
	}
	return sources, nil
}

func scanModuleSourceLines(lines []string, filePath, envDir string, sources map[string]string) error {
	current := ""
	source := ""
	sourceSeen := false
	itemsSeen := false
	moduleDepth := -1
	depth := 0

	for lineIndex, line := range lines {
		lineNumber := lineIndex + 1
		if current == "" && depth == 0 {
			match := moduleStartPattern.FindStringSubmatch(line)
			if match != nil {
				current = match[1]
				source = ""
				sourceSeen = false
				itemsSeen = false
				moduleDepth = 1
			}
		} else if current != "" && depth == moduleDepth {
			stripped := pythonStrip(line)
			sourceMatch := sourceLinePattern.FindStringSubmatch(line)
			if sourceMatch != nil {
				if sourceSeen {
					return fmt.Errorf("%s:%d module %s has multiple source values", filePath, lineNumber, current)
				}
				candidate := sourceMatch[1]
				if strings.Contains(candidate, "${") || strings.Contains(candidate, "%{") {
					return fmt.Errorf(
						"%s:%d module %s source uses HCL template syntax outside the generated-root contract; run make gen-env to regenerate the root",
						filePath,
						lineNumber,
						current,
					)
				}
				source = candidate
				sourceSeen = true
			} else if itemsLinePattern.MatchString(line) {
				if itemsSeen {
					return fmt.Errorf("%s:%d module %s has multiple items values", filePath, lineNumber, current)
				}
				itemsSeen = true
			} else if stripped != "" && stripped != "}" {
				return fmt.Errorf(
					"%s:%d module %s is outside the generated-root contract; run make gen-env to regenerate the root",
					filePath,
					lineNumber,
					current,
				)
			}
		}

		depth += hclBraceDelta(line)
		if depth < 0 {
			return fmt.Errorf("%s:%d has an unexpected closing brace", filePath, lineNumber)
		}
		if current != "" && moduleDepth >= 0 && depth < moduleDepth {
			if !sourceSeen || !itemsSeen {
				return fmt.Errorf(
					"%s module %s is outside the generated-root contract; run make gen-env to regenerate the root",
					filePath,
					current,
				)
			}
			if _, exists := sources[current]; exists {
				return fmt.Errorf("%s contains duplicate module %s", envDir, current)
			}
			sources[current] = source
			current = ""
			source = ""
			sourceSeen = false
			itemsSeen = false
			moduleDepth = -1
		}
	}
	if depth != 0 {
		return fmt.Errorf("%s has unbalanced braces", filePath)
	}
	return nil
}

// ModuleFingerprints ports moduleFingerprints from
// the original implementation. A nil budget selects the source default.
func ModuleFingerprints(
	envDir string,
	memberTypes []string,
	budget *artifacts.ReadBudget,
) ([]ModuleFingerprint, error) {
	budget = fingerprintBudget(budget)
	sources, err := RootModuleSources(envDir, budget)
	if err != nil {
		return nil, err
	}
	out := make([]ModuleFingerprint, 0, len(memberTypes))
	for _, resourceType := range canonjson.SortedStrings(memberTypes) {
		source, exists := sources[resourceType]
		if !exists {
			return nil, fmt.Errorf(
				"%s member %s has no module source; run make gen-env to regenerate the root",
				envDir,
				resourceType,
			)
		}
		modulePath, local := LocalModulePath(envDir, source)
		if !local {
			return nil, fmt.Errorf(
				"%s member %s module source %s is not local; generated roots must use local module sources",
				envDir,
				resourceType,
				quoteJSONString(source),
			)
		}
		files, err := TreeFingerprints(modulePath, budget)
		if err != nil {
			return nil, err
		}
		out = append(out, ModuleFingerprint{
			Files:        files,
			Local:        true,
			Present:      isDirectory(modulePath),
			ResourceType: resourceType,
			Source:       source,
		})
	}
	return out, nil
}

func quoteJSONString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
