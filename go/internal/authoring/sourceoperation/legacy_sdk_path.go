package sourceoperation

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

var legacyHTTPMethods = map[string]string{"http.MethodDelete": "DELETE", "http.MethodGet": "GET", "http.MethodHead": "HEAD", "http.MethodOptions": "OPTIONS", "http.MethodPatch": "PATCH", "http.MethodPost": "POST", "http.MethodPut": "PUT"}
var legacySDKIgnoredDirs = map[string]bool{".git": true, "test": true, "testdata": true}

// DiscoverSDKGoFiles returns non-test Go source files ordered as Python strings.
func DiscoverSDKGoFiles(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	var output []string
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && name != root && legacySDKIgnoredDirs[entry.Name()] {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			output = append(output, name)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for left := range output {
		for right := left + 1; right < len(output); right++ {
			if canonjson.ComparePythonStrings(legacyRelative(root, output[right]), legacyRelative(root, output[left])) < 0 {
				output[left], output[right] = output[right], output[left]
			}
		}
	}
	return output, nil
}

// ExtractSDKBasePaths finds BasePath constants.
func ExtractSDKBasePaths(code string) map[string]string {
	output := map[string]string{}
	inline := regexp.MustCompile(`\bconst\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"`)
	for _, match := range inline.FindAllStringSubmatch(code, -1) {
		if strings.HasSuffix(match[1], "BasePath") {
			output[match[1]] = match[2]
		}
	}
	blocks := regexp.MustCompile(`(?s)\bconst\s*\(([^)]*)\)`).FindAllStringSubmatch(code, -1)
	line := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"`)
	for _, block := range blocks {
		for _, text := range strings.Split(block[1], "\n") {
			match := line.FindStringSubmatch(text)
			if match != nil && strings.HasSuffix(match[1], "BasePath") {
				output[match[1]] = match[2]
			}
		}
	}
	return output
}

// SDKReceiverServiceName removes a v1 SDK receiver suffix.
func SDKReceiverServiceName(receiver string) string {
	for _, suffix := range []string{"ServiceOp", "Service", "Client", "API"} {
		if strings.HasSuffix(receiver, suffix) && len(receiver) > len(suffix) {
			return strings.TrimSuffix(receiver, suffix)
		}
	}
	return receiver
}

// ExtractBalancedGoBody extracts a braced body while respecting quoted strings.
func ExtractBalancedGoBody(code string, brace int) (string, int, bool) {
	depth := 0
	start := brace + 1
	for index := brace; index < len(code); {
		current := code[index]
		if current == '"' || current == '\'' || current == '`' {
			index = legacySkipQuoted(code, index)
			continue
		}
		if current == '{' {
			depth++
		}
		if current == '}' {
			depth--
			if depth == 0 {
				return code[start:index], index + 1, true
			}
		}
		index++
	}
	return "", len(code), false
}

// SplitSDKReceiverFunctions returns receiver functions with their service and body.
func SplitSDKReceiverFunctions(code string) []map[string]any {
	pattern := regexp.MustCompile(`\bfunc\s*\(([^)]*)\)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	typeName := regexp.MustCompile(`\*?([A-Za-z_][A-Za-z0-9_]*)\s*$`)
	var output []map[string]any
	for _, match := range pattern.FindAllStringSubmatchIndex(code, -1) {
		receiver := strings.TrimSpace(code[match[2]:match[3]])
		candidate := typeName.FindStringSubmatch(receiver)
		if candidate == nil {
			continue
		}
		brace := strings.IndexByte(code[match[1]:], '{')
		if brace < 0 {
			continue
		}
		brace += match[1]
		body, _, ok := ExtractBalancedGoBody(code, brace)
		if ok {
			output = append(output, map[string]any{"body": body, "method_name": code[match[4]:match[5]], "service": SDKReceiverServiceName(candidate[1])})
		}
	}
	return output
}

// SplitGoCallArguments splits a call argument string at top-level commas.
func SplitGoCallArguments(text string) []string {
	var out []string
	depth, start := 0, 0
	for index := 0; index < len(text); {
		current := text[index]
		if current == '"' || current == '\'' || current == '`' {
			index = legacySkipQuoted(text, index)
			continue
		}
		if strings.ContainsRune("([{", rune(current)) {
			depth++
		} else if strings.ContainsRune(")]}`", rune(current)) {
			depth--
		} else if current == ',' && depth == 0 {
			out = append(out, text[start:index])
			start = index + 1
		}
		index++
	}
	out = append(out, text[start:])
	if len(out) == 1 && strings.TrimSpace(out[0]) == "" {
		return nil
	}
	return out
}
func legacyArgumentPlaceholder(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{param}"
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}
	if regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(value) {
		return "{" + value + "}"
	}
	return "{param}"
}

// RenderSDKFormatTemplate replaces fmt verbs just as the frozen extractor does.
func RenderSDKFormatTemplate(format, base string, extra []string) string {
	pattern := regexp.MustCompile(`%[a-zA-Z]`)
	matches := pattern.FindAllStringIndex(format, -1)
	var out strings.Builder
	cursor, arg, consumed := 0, 0, false
	for _, match := range matches {
		out.WriteString(format[cursor:match[0]])
		if !consumed {
			out.WriteString(base)
			consumed = true
		} else {
			value := ""
			if arg < len(extra) {
				value = extra[arg]
			}
			out.WriteString(legacyArgumentPlaceholder(value))
			arg++
		}
		cursor = match[1]
	}
	out.WriteString(format[cursor:])
	return out.String()
}
func legacyRenderSprintf(expr string, base map[string]string, current string) (string, bool) {
	if !strings.HasPrefix(expr, "fmt.Sprintf(") || !strings.HasSuffix(expr, ")") {
		return "", false
	}
	args := SplitGoCallArguments(expr[len("fmt.Sprintf(") : len(expr)-1])
	if len(args) < 2 {
		return "", false
	}
	raw := strings.TrimSpace(args[0])
	if !strings.HasPrefix(raw, `"`) || !strings.HasSuffix(raw, `"`) {
		return "", false
	}
	format := raw[1 : len(raw)-1]
	first := strings.TrimSpace(args[1])
	if value, ok := base[first]; ok {
		return RenderSDKFormatTemplate(format, value, trimArguments(args[2:])), true
	}
	if current != "" && first == "path" {
		return RenderSDKFormatTemplate(format, current, trimArguments(args[2:])), true
	}
	return "", false
}
func trimArguments(input []string) []string {
	output := make([]string, len(input))
	for index := range input {
		output[index] = strings.TrimSpace(input[index])
	}
	return output
}

// FindSDKPathAssignments returns all recognized assignments to path.
func FindSDKPathAssignments(body string, base map[string]string) []string {
	var output []string
	current := ""
	pattern := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*([^;\n]+)`)
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if match[1] != "path" {
			continue
		}
		expression := strings.TrimSpace(match[2])
		rendered, found := base[expression]
		if !found {
			rendered, found = legacyRenderSprintf(expression, base, current)
		}
		if found {
			current = rendered
			output = append(output, rendered)
		}
	}
	return output
}

// DetectSDKRequestMethod returns the first NewRequest HTTP method.
func DetectSDKRequestMethod(body string) string {
	pattern := regexp.MustCompile(`\bNewRequest\s*\(([^)]*)\)`)
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		args := SplitGoCallArguments(match[1])
		if len(args) < 2 {
			continue
		}
		method := strings.TrimSpace(args[1])
		if value := legacyHTTPMethods[method]; value != "" {
			return value
		}
		if strings.HasPrefix(method, `"`) && strings.HasSuffix(method, `"`) {
			return strings.ToUpper(method[1 : len(method)-1])
		}
	}
	return ""
}
func legacySDKPathRole(method string) any {
	lower := strings.ToLower(method)
	if method == "Get" || method == "Read" || method == "Fetch" || strings.HasPrefix(lower, "get") || strings.HasPrefix(lower, "read") || strings.HasPrefix(lower, "fetch") {
		return "read"
	}
	if method == "List" || method == "Search" || strings.HasPrefix(lower, "list") || strings.HasPrefix(lower, "search") {
		return "list"
	}
	return nil
}

// ExtractSDKPaths returns evidence and unresolved path/method records keyed by client symbol.
func ExtractSDKPaths(root string) (map[string]map[string]any, map[string]map[string]any, error) {
	evidence, unresolved := map[string]map[string]any{}, map[string]map[string]any{}
	if root == "" {
		return evidence, unresolved, nil
	}
	files, err := DiscoverSDKGoFiles(root)
	if err != nil {
		return nil, nil, err
	}
	for _, filename := range files {
		text, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		code := GoCodeWithoutComments(string(text))
		bases, sdkFile := ExtractSDKBasePaths(code), legacyRelative(root, filename)
		for _, item := range SplitSDKReceiverFunctions(code) {
			service, methodName, body := item["service"].(string), item["method_name"].(string), item["body"].(string)
			symbol := service + "." + methodName
			assignments, method := FindSDKPathAssignments(body, bases), DetectSDKRequestMethod(body)
			if len(assignments) == 0 && method == "" {
				continue
			}
			if len(assignments) == 0 {
				unresolved[symbol] = map[string]any{"reason": "path_template_not_found", "sdk_file": sdkFile}
			} else if method == "" {
				unresolved[symbol] = map[string]any{"reason": "method_not_detected", "sdk_file": sdkFile}
			} else {
				evidence[symbol] = map[string]any{"client_symbol": symbol, "method": method, "path_template": assignments[len(assignments)-1], "sdk_file": sdkFile, "source_role": legacySDKPathRole(methodName)}
			}
		}
	}
	return evidence, unresolved, nil
}

// NormalizeSDKPathSegments normalizes OpenAPI and SDK templates for matching.
func NormalizeSDKPathSegments(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			out = append(out, "{param}")
		} else {
			out = append(out, strings.ToLower(part))
		}
	}
	return out
}
func SDKPathSegmentsMatch(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] && left[i] != "{param}" && right[i] != "{param}" {
			return false
		}
	}
	return true
}
func legacyMatchOpenAPIBySDKPath(operations []legacyOperation, path, method string) (*legacyOperation, []legacyOperation) {
	template := NormalizeSDKPathSegments(path)
	var matches []legacyOperation
	for _, operation := range operations {
		if operation.Method == method && SDKPathSegmentsMatch(template, NormalizeSDKPathSegments(operation.Path)) {
			matches = append(matches, operation)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, matches
	}
	return nil, nil
}
