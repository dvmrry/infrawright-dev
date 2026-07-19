package sourceoperation

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

var (
	legacyIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	legacyFunction   = regexp.MustCompile(`\bfunc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	legacySDKCall    = regexp.MustCompile(`\b((?:[A-Za-z_][A-Za-z0-9_]*\.)*(?:api|client)(?:\.[A-Za-z_][A-Za-z0-9_]*){1,})\s*\(`)
	legacyPkgCall    = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	legacyQuoted     = regexp.MustCompile(`"([^"]+)"`)
	legacyRegister   = regexp.MustCompile(`"([^"]+)"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	legacyReadCB     = regexp.MustCompile(`\bRead(?:Context|WithoutTimeout)?\s*:\s*([A-Za-z_][A-Za-z0-9_]*)`)
)

var legacyIgnoredDirs = map[string]bool{".git": true, ".terraform": true, "acceptance": true, "node_modules": true, "vendor": true}

// CanonicalSourceSymbol ports canonicalSourceSymbol from provider-source-evidence.ts.
func CanonicalSourceSymbol(value string) string {
	var output strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			output.WriteRune(r)
		}
	}
	return output.String()
}

func legacySorted(values []string) []string { return canonjson.SortedStrings(values) }

func legacySortedSet(values map[string]bool) []string {
	output := make([]string, 0, len(values))
	for value := range values {
		output = append(output, value)
	}
	return legacySorted(output)
}

func legacyRelative(root, filename string) string {
	value, err := filepath.Rel(root, filename)
	if err != nil {
		return filename
	}
	return filepath.ToSlash(value)
}

// DiscoverProviderGoFiles returns source files under root using the v1 pruning rules.
func DiscoverProviderGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && filename != root && legacyIgnoredDirs[entry.Name()] {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "sweep.go" {
			return nil
		}
		files = append(files, filename)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sortLegacyPaths(root, files)
	return files, nil
}

func sortLegacyPaths(root string, values []string) {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if canonjson.ComparePythonStrings(legacyRelative(root, values[right]), legacyRelative(root, values[left])) < 0 {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}

func legacySkipQuoted(text string, start int) int {
	quote := text[start]
	index := start + 1
	for index < len(text) {
		if text[index] == '\\' && quote != '`' {
			index += 2
			continue
		}
		if text[index] == quote {
			return index + 1
		}
		index++
	}
	return index
}

func legacyWithout(text string, stringsToo bool) string {
	var output strings.Builder
	for index := 0; index < len(text); {
		current := text[index]
		next := byte(0)
		if index+1 < len(text) {
			next = text[index+1]
		}
		if current == '/' && next == '/' {
			end := strings.IndexByte(text[index+2:], '\n')
			if end < 0 {
				break
			}
			output.WriteByte('\n')
			index += end + 3
			continue
		}
		if current == '/' && next == '*' {
			end := strings.Index(text[index+2:], "*/")
			if end < 0 {
				removed := text[index:]
				output.WriteString(strings.Repeat("\n", strings.Count(removed, "\n")))
				break
			}
			end += index + 4
			output.WriteString(strings.Repeat("\n", strings.Count(text[index:end], "\n")))
			index = end
			continue
		}
		if stringsToo && (current == '"' || current == '\'' || current == '`') {
			end := legacySkipQuoted(text, index)
			output.WriteString(strings.Repeat("\n", strings.Count(text[index:end], "\n")))
			index = end
			continue
		}
		output.WriteByte(current)
		index++
	}
	return output.String()
}

// GoCodeWithoutCommentsAndStrings removes comments and literals exactly enough for v1 scans.
func GoCodeWithoutCommentsAndStrings(text string) string { return legacyWithout(text, true) }

// GoCodeWithoutComments removes comments but retains string literals.
func GoCodeWithoutComments(text string) string { return legacyWithout(text, false) }

// GoIdentifierTokens reports canonical identifiers outside comments and literals.
func GoIdentifierTokens(text string) map[string]bool {
	tokens := map[string]bool{}
	for _, token := range legacyIdentifier.FindAllString(GoCodeWithoutCommentsAndStrings(text), -1) {
		canonical := CanonicalSourceSymbol(token)
		if canonical != "" {
			tokens[canonical] = true
		}
	}
	return tokens
}

// IdentifierWords splits camel-case and separators into lower-case words.
func IdentifierWords(value string) []string {
	var output []rune
	runes := []rune(value)
	for index, r := range runes {
		if index > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]) || index+1 < len(runes) && unicode.IsUpper(runes[index-1]) && unicode.IsLower(runes[index+1])) {
			output = append(output, ' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			output = append(output, unicode.ToLower(r))
		} else {
			output = append(output, ' ')
		}
	}
	return strings.Fields(string(output))
}

// SDKMethodRole returns the frozen v1 list/read classification.
func SDKMethodRole(method string) string {
	lower, words := strings.ToLower(method), IdentifierWords(method)
	if method == "Get" || method == "Read" || method == "Fetch" || legacyPrefixOrSuffix(lower, []string{"get", "read", "fetch", "retrieve"}) {
		return "read"
	}
	if method == "List" || method == "Search" || legacyPrefixOrSuffix(lower, []string{"list", "search"}) {
		return "list"
	}
	for _, word := range words {
		if word == "list" || word == "search" {
			return "list"
		}
	}
	for _, word := range words {
		if word == "fetch" || word == "get" || word == "read" || word == "retrieve" {
			return "read"
		}
	}
	return ""
}

func legacyPrefixOrSuffix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) || strings.HasSuffix(value, prefix) {
			return true
		}
	}
	return false
}

// SDKClientCalls derives client/API calls from provider source text.
func SDKClientCalls(text string, requireRole bool) []map[string]any {
	calls := map[string]map[string]any{}
	for _, match := range legacySDKCall.FindAllStringSubmatch(GoCodeWithoutCommentsAndStrings(text), -1) {
		parts := strings.Split(match[1], ".")
		last := -1
		for index, part := range parts {
			if part == "api" || part == "client" {
				last = index
			}
		}
		if last < 0 || last+1 >= len(parts) {
			continue
		}
		suffix := parts[last+1:]
		method := suffix[len(suffix)-1]
		role := SDKMethodRole(method)
		if role == "" && requireRole {
			continue
		}
		chain := append([]string(nil), suffix[:len(suffix)-1]...)
		symbol := strings.Join(suffix, ".")
		calls[symbol] = map[string]any{"chain": chain, "client_symbol": symbol, "method": method, "source_role": legacyNullableRole(role)}
	}
	return legacyCallMap(calls)
}

func legacyNullableRole(role string) any {
	if role == "" {
		return nil
	}
	return role
}
func legacyCallMap(calls map[string]map[string]any) []map[string]any {
	keys := make([]string, 0, len(calls))
	for key := range calls {
		keys = append(keys, key)
	}
	keys = legacySorted(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, calls[key])
	}
	return out
}

// GoImportAliases finds single and parenthesized imports, including aliases.
func GoImportAliases(text string) map[string]string {
	code, aliases := GoCodeWithoutComments(text), map[string]string{}
	pattern := regexp.MustCompile(`(?m)\bimport\s+([A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"`)
	for _, match := range pattern.FindAllStringSubmatch(code, -1) {
		alias := strings.TrimSpace(match[1])
		path := match[2]
		if alias == "" {
			alias = filepath.Base(path)
		}
		aliases[alias] = path
	}
	block := regexp.MustCompile(`(?s)\bimport\s*\((.*?)\)`).FindAllStringSubmatch(code, -1)
	line := regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"`)
	for _, match := range block {
		for _, candidate := range strings.Split(match[1], "\n") {
			item := line.FindStringSubmatch(candidate)
			if item == nil {
				continue
			}
			alias, path := strings.TrimSpace(item[1]), item[2]
			if alias == "" {
				alias = filepath.Base(path)
			}
			aliases[alias] = path
		}
	}
	return aliases
}

func legacyPackageRole(method string) string {
	lower, words := strings.ToLower(method), IdentifierWords(method)
	for _, prefix := range []string{"get", "read", "fetch", "retrieve"} {
		if strings.HasPrefix(lower, prefix) {
			if strings.Contains(lower, "all") || strings.Contains(lower, "list") {
				return "list"
			}
			return "read"
		}
	}
	if strings.HasPrefix(lower, "list") || strings.HasPrefix(lower, "search") {
		return "list"
	}
	for _, word := range words {
		if word == "list" || word == "search" {
			return "list"
		}
	}
	for _, word := range words {
		if word == "fetch" || word == "get" || word == "read" || word == "retrieve" {
			return "read"
		}
	}
	return ""
}
func legacyExternalImport(path string) bool {
	return strings.Contains(strings.Split(path, "/")[0], ".")
}
func legacyDirExists(path string) bool { stat, err := os.Stat(path); return err == nil && stat.IsDir() }
func legacyFileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}
func legacyLocalImportDirectory(root, importPath string) string {
	for index, part := range strings.Split(importPath, "/") {
		_ = part
		candidate := filepath.Join(append([]string{root}, strings.Split(importPath, "/")[index:]...)...)
		if legacyDirExists(candidate) {
			return candidate
		}
	}
	return ""
}

// PackageCalls finds calls to external imported packages.
func PackageCalls(text, root string) []map[string]any {
	imports, calls := GoImportAliases(text), map[string]map[string]any{}
	for _, match := range legacyPkgCall.FindAllStringSubmatch(GoCodeWithoutCommentsAndStrings(text), -1) {
		pkg, method := match[1], match[2]
		importPath := imports[pkg]
		if importPath == "" || !legacyExternalImport(importPath) || legacyLocalImportDirectory(root, importPath) != "" {
			continue
		}
		role := legacyPackageRole(method)
		if role == "" {
			continue
		}
		symbol := pkg + "." + method
		calls[symbol] = map[string]any{"client_symbol": symbol, "method": method, "package": pkg, "package_path": importPath, "source_role": role}
	}
	return legacyCallMap(calls)
}

func legacyDecodeGoLiteral(value string) (string, error) {
	if strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`") {
		return value[1 : len(value)-1], nil
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return strconv.Unquote(value)
	}
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			return "", fmt.Errorf("truncated escape in Go string literal")
		}
		i++
		switch value[i] {
		case '\\':
			out.WriteByte('\\')
		case '"':
			out.WriteByte('"')
		case '\'':
			out.WriteByte('\'')
		case 'a':
			out.WriteByte('\a')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'v':
			out.WriteByte('\v')
		case '\n':
		default:
			out.WriteByte('\\')
			out.WriteByte(value[i])
		}
	}
	return out.String(), nil
}

// NormalizeRawRESTPath converts printf verbs and repeated slashes into v1 paths.
func NormalizeRawRESTPath(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`%[#0 +\-]*[0-9]*(?:\.[0-9]+)?[bcdefgosqxXUvT]`).ReplaceAllString(value, "{arg}")
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return regexp.MustCompile(`/+`).ReplaceAllString(value, "/")
}

// RawRESTCalls scans the legacy NewRequest GET patterns, retaining raw and interpreted literals.
func RawRESTCalls(text string) []map[string]any {
	code, calls := GoCodeWithoutComments(text), map[string]map[string]any{}
	literal := `("(?:\\.|[^"\\])*"|` + "`[^`]*`" + `)`
	patterns := []*regexp.Regexp{regexp.MustCompile(`(?s)\b((?:[A-Za-z_][A-Za-z0-9_]*\.)+NewRequest)\s*\(\s*(?:"GET"|http\.MethodGet)\s*,\s*fmt\.Sprintf\s*\(\s*` + literal), regexp.MustCompile(`(?s)\b((?:[A-Za-z_][A-Za-z0-9_]*\.)+NewRequest)\s*\(\s*(?:"GET"|http\.MethodGet)\s*,\s*` + literal)}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(code, -1) {
			value, err := legacyDecodeGoLiteral(match[2])
			if err != nil {
				continue
			}
			rest := NormalizeRawRESTPath(value)
			key := match[1] + "\x00" + rest
			calls[key] = map[string]any{"client_symbol": match[1] + " GET " + rest, "method": "GET", "path": rest, "source_role": "read"}
		}
	}
	return legacyCallMap(calls)
}

// IsGraphQLSource detects the frozen set of GraphQL source hints.
func IsGraphQLSource(text string) bool {
	code := GoCodeWithoutComments(text)
	return regexp.MustCompile(`\bgithubv4\b|\bgraphql\s*:`).MatchString(code) || strings.Contains(code, "github.com/shurcooL/githubv4")
}
