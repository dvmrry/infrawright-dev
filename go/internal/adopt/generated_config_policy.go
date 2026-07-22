package adopt

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

var (
	generatedResourceStart = regexp.MustCompile(`^resource\s+"([^"]+)"\s+"([^"]+)"\s*\{\s*$`)
	generatedBlockStart    = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$`)
	generatedAttribute     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)
	generatedDecimal       = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	generatedHeredoc       = regexp.MustCompile(`^<<-?\s*([A-Za-z_][A-Za-z0-9_]*)$`)
)

// GeneratedConfigPolicyError ports GeneratedConfigPolicyError from
// the original implementation.
type GeneratedConfigPolicyError struct{ Message string }

// Error implements error.
func (e *GeneratedConfigPolicyError) Error() string { return e.Message }

func generatedConfigErrorf(format string, args ...any) error {
	return &GeneratedConfigPolicyError{Message: fmt.Sprintf(format, args...)}
}

type omitMode string

const (
	omitPackDefault  omitMode = "pack_drop_if_default"
	omitProjection   omitMode = "projection_omit"
	omitProjectionIf omitMode = "projection_omit_if"
)

type generatedOmitEntry struct {
	Entry    *metadata.PolicyEntry
	Mode     omitMode
	Selector []any
	Values   []any
}

type generatedPolicyEntries struct {
	Fills  []metadata.PolicyEntry
	Omits  []generatedOmitEntry
	Schema metadata.JsonObject
}

func exactPolicyIndex(path []any) bool {
	for _, segment := range path {
		switch segment.(type) {
		case int64, *big.Int:
			return true
		}
	}
	return false
}

func generatedPolicyEntriesFor(root *metadata.LoadedPackRoot, resourceType string, policy *metadata.DriftPolicy) (generatedPolicyEntries, error) {
	dropDefaults := map[string]any{}
	if resource, ok := root.Resources[resourceType]; ok && resource.Override != nil {
		if values, ok := resource.Override["drop_if_default"].(map[string]any); ok {
			dropDefaults = values
		}
	}
	var fills []metadata.PolicyEntry
	var projectionOmits, projectionOmitIf []metadata.PolicyEntry
	if policy != nil {
		fills = policy.Entries(resourceType, metadata.PolicyProjectionFill)
		projectionOmits = policy.Entries(resourceType, metadata.PolicyProjectionOmit)
		projectionOmitIf = policy.Entries(resourceType, metadata.PolicyProjectionOmitIf)
	}
	if len(fills) == 0 && len(projectionOmits) == 0 && len(projectionOmitIf) == 0 && len(dropDefaults) == 0 {
		return generatedPolicyEntries{}, nil
	}
	schema, err := root.LoadResourceSchema(resourceType)
	if err != nil {
		return generatedPolicyEntries{}, err
	}
	result := generatedPolicyEntries{Fills: fills, Schema: schema}
	addPolicy := func(mode omitMode, entries []metadata.PolicyEntry) error {
		for index := range entries {
			entry := entries[index]
			pathText, _ := entry.Data()["path"].(string)
			selector, err := metadata.ParsePolicyPath(pathText)
			if err != nil {
				return fmt.Errorf("%w", err)
			}
			status, err := ProviderSchemaStatus(schema, resourceType, selector, false)
			if err != nil {
				return fmt.Errorf("%w", err)
			}
			if status == "required" {
				return generatedConfigErrorf("%s generated import config policy cannot %s required path %s", resourceType, mode, pathText)
			}
			if !exactPolicyIndex(selector) {
				copyEntry := entry
				result.Omits = append(result.Omits, generatedOmitEntry{Entry: &copyEntry, Mode: mode, Selector: selector})
			}
		}
		return nil
	}
	if err := addPolicy(omitProjection, projectionOmits); err != nil {
		return generatedPolicyEntries{}, err
	}
	for _, pathText := range canonjson.SortedStrings(mapKeys(dropDefaults)) {
		selector, err := metadata.ParsePolicyPath(pathText)
		if err != nil {
			return generatedPolicyEntries{}, err
		}
		status, err := ProviderSchemaStatus(schema, resourceType, selector, false)
		if err != nil {
			return generatedPolicyEntries{}, err
		}
		if status != "optional" {
			return generatedPolicyEntries{}, generatedConfigErrorf("%s generated import config pack drop_if_default path %s is not optional (schema status %s)", resourceType, pathText, status)
		}
		if !exactPolicyIndex(selector) {
			result.Omits = append(result.Omits, generatedOmitEntry{Mode: omitPackDefault, Selector: selector, Values: []any{dropDefaults[pathText]}})
		}
	}
	if err := addPolicy(omitProjectionIf, projectionOmitIf); err != nil {
		return generatedPolicyEntries{}, err
	}
	return result, nil
}

type parsedGeneratedScalar struct {
	Known bool
	Value any
}

func parseGeneratedScalar(raw string) parsedGeneratedScalar {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, `"`) {
		parsed, err := tfrender.ParseHclQuotedString(text, 0)
		if err == nil && strings.TrimSpace(text[parsed.End:]) == "" {
			return parsedGeneratedScalar{Known: true, Value: parsed.Value}
		}
		return parsedGeneratedScalar{}
	}
	switch text {
	case "true":
		return parsedGeneratedScalar{Known: true, Value: true}
	case "false":
		return parsedGeneratedScalar{Known: true, Value: false}
	case "null":
		return parsedGeneratedScalar{Known: true, Value: nil}
	}
	if generatedDecimal.MatchString(text) {
		return parsedGeneratedScalar{Known: true, Value: json.Number(text)}
	}
	return parsedGeneratedScalar{}
}

func generatedValueDepthDelta(text string) int {
	depth := 0
	escaped := false
	inString := false
	for index := 0; index < len(text); index++ {
		character := text[index]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		switch {
		case character == '"':
			inString = true
		case character == '#':
			return depth
		case character == '/' && index+1 < len(text) && text[index+1] == '/':
			return depth
		case strings.ContainsRune("{[(", rune(character)):
			depth++
		case strings.ContainsRune("}])", rune(character)):
			depth--
		}
	}
	return depth
}

func matchGeneratedOmit(path []any, parsed parsedGeneratedScalar, entries []generatedOmitEntry, resourceType string, schema metadata.JsonObject) (*generatedOmitEntry, error) {
	if !parsed.Known {
		return nil, nil
	}
	for index := range entries {
		candidate := &entries[index]
		actual := path
		if candidate.Mode == omitPackDefault {
			actual = make([]any, 0, len(path))
			for _, segment := range path {
				switch segment.(type) {
				case int, int64, *big.Int:
					continue
				}
				actual = append(actual, segment)
			}
		}
		if !metadata.PolicySelectorMatches(candidate.Selector, actual) {
			continue
		}
		status, err := ProviderSchemaStatus(schema, resourceType, candidate.Selector, false)
		if err != nil {
			return nil, err
		}
		if status != "optional" {
			pathLabel := policyPathLabel(candidate)
			return nil, generatedConfigErrorf("%s generated import config policy matched non-optional path %s (schema status %s)", resourceType, pathLabel, status)
		}
		if candidate.Mode == omitProjection {
			return candidate, nil
		}
		values := candidate.Values
		if candidate.Entry != nil {
			values, _ = candidate.Entry.Data()["values"].([]any)
		}
		for _, value := range values {
			matches := canonjson.TerraformJSONEqual(parsed.Value, value)
			if candidate.Mode == omitPackDefault {
				matches = transform.MatchesTransformDefault(parsed.Value, value)
			}
			if matches {
				return candidate, nil
			}
		}
	}
	return nil, nil
}

func policyPathLabel(candidate *generatedOmitEntry) string {
	if candidate.Entry != nil {
		if path, ok := candidate.Entry.Data()["path"].(string); ok {
			return path
		}
	}
	parts := make([]string, len(candidate.Selector))
	for index, segment := range candidate.Selector {
		parts[index] = fmt.Sprint(segment)
	}
	return strings.Join(parts, ".")
}

func renderGeneratedHCLValue(value any, indent int) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case string:
		return tfrender.RenderHclQuotedString(typed)
	case json.Number:
		token, err := canonjson.CanonicalNumberToken(string(typed))
		if err != nil {
			return "", generatedConfigErrorf("cannot render non-finite projection_fill number")
		}
		return token, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", generatedConfigErrorf("cannot render projection_fill value for generated config")
		}
		if typed == 0 {
			return "0", nil
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", generatedConfigErrorf("cannot render projection_fill value for generated config")
		}
		return string(encoded), nil
	case []any:
		if len(typed) == 0 {
			return "[]", nil
		}
		var output strings.Builder
		output.WriteString("[\n")
		for _, child := range typed {
			rendered, err := renderGeneratedHCLValue(child, indent+2)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&output, "%s%s,\n", strings.Repeat(" ", indent+2), rendered)
		}
		output.WriteString(strings.Repeat(" ", indent) + "]")
		return output.String(), nil
	case map[string]any:
		if len(typed) == 0 {
			return "{}", nil
		}
		var output strings.Builder
		output.WriteString("{\n")
		for _, key := range canonjson.SortedStrings(mapKeys(typed)) {
			quoted, err := tfrender.RenderHclQuotedString(key)
			if err != nil {
				return "", err
			}
			rendered, err := renderGeneratedHCLValue(typed[key], indent+2)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&output, "%s%s = %s\n", strings.Repeat(" ", indent+2), quoted, rendered)
		}
		output.WriteString(strings.Repeat(" ", indent) + "}")
		return output.String(), nil
	default:
		return "", generatedConfigErrorf("cannot render projection_fill value for generated config")
	}
}

func renderGeneratedHCLBlock(name string, block metadata.JsonObject, value map[string]any, indent int) ([]string, error) {
	pad := strings.Repeat(" ", indent)
	output := []string{pad + name + " {\n"}
	attributes, err := metadata.TerraformAttributesForBlock(block, name)
	if err != nil {
		return nil, err
	}
	blocks, err := metadata.TerraformInputBlockTypes(block, name)
	if err != nil {
		return nil, err
	}
	for _, key := range canonjson.SortedStrings(mapKeys(value)) {
		if _, exists := attributes[key]; exists {
			rendered, err := renderGeneratedHCLValue(value[key], indent+2)
			if err != nil {
				return nil, err
			}
			output = append(output, strings.Repeat(" ", indent+2)+key+" = "+rendered+"\n")
			continue
		}
		blockType, exists := blocks[key]
		if !exists {
			continue
		}
		children, ok := value[key].([]any)
		if metadata.TerraformBlockIsSingle(blockType) {
			children = []any{value[key]}
			ok = true
		}
		if !ok {
			continue
		}
		childBlock, err := metadata.TerraformRequireObject(blockType["block"], name+"."+key+".block")
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			childRecord, ok := child.(map[string]any)
			if !ok || len(childRecord) == 0 {
				continue
			}
			lines, err := renderGeneratedHCLBlock(key, childBlock, childRecord, indent+2)
			if err != nil {
				return nil, err
			}
			output = append(output, lines...)
		}
	}
	output = append(output, pad+"}\n")
	return output, nil
}

// GeneratedConfigPolicyResource ports GeneratedConfigPolicyResource from
// the original implementation.
type GeneratedConfigPolicyResource struct {
	AddressToKey map[string]string
	Policy       *metadata.DriftPolicy
	RawItems     map[string]map[string]any
	ResourceType string
}

type rewriteResourceOptions struct {
	GeneratedConfigPolicyResource
	Fills  []metadata.PolicyEntry
	Omits  []generatedOmitEntry
	Schema metadata.JsonObject
}

type generatedStackEntry struct {
	Address      string
	Counts       map[string]int
	Kind         string
	Path         []any
	Present      map[string]struct{}
	ResourceType string
}

func generatedContext(resources map[string]rewriteResourceOptions) string {
	if len(resources) == 1 {
		return mapKeys(resources)[0] + " "
	}
	return ""
}

func fillGeneratedResource(current *generatedStackEntry, options rewriteResourceOptions) ([]string, int, error) {
	if len(options.Fills) == 0 {
		return nil, 0, nil
	}
	if options.Policy == nil {
		return nil, 0, generatedConfigErrorf("%s generated import config policy entries require a policy", options.ResourceType)
	}
	key, ok := options.AddressToKey[current.Address]
	if !ok {
		return nil, 0, generatedConfigErrorf("%s generated import config missing key for %s", options.ResourceType, current.Address)
	}
	raw, ok := options.RawItems[key]
	if !ok {
		return nil, 0, generatedConfigErrorf("%s generated import config projection_fill missing raw item for key %s", options.ResourceType, key)
	}
	block, err := metadata.TerraformBlockForSchema(options.Schema, options.ResourceType)
	if err != nil {
		return nil, 0, err
	}
	blocks, err := metadata.TerraformInputBlockTypes(block, options.ResourceType)
	if err != nil {
		return nil, 0, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, options.ResourceType)
	if err != nil {
		return nil, 0, err
	}
	var lines []string
	edits := 0
	for _, entry := range options.Fills {
		data := entry.Data()
		path, _ := data["path"].(string)
		parsed, err := metadata.ParsePolicyPath(path)
		if err != nil {
			return nil, 0, err
		}
		target, ok := parsed[0].(string)
		if !ok {
			continue
		}
		if _, present := current.Present[target]; present {
			continue
		}
		value, present, err := ProjectionFillValue(entry, raw, options.ResourceType, options.Schema)
		if err != nil {
			return nil, 0, err
		}
		if !present {
			continue
		}
		before := len(lines)
		if _, isAttribute := attributes[target]; isAttribute {
			rendered, err := renderGeneratedHCLValue(value, 2)
			if err != nil {
				return nil, 0, err
			}
			lines = append(lines, "  "+target+" = "+rendered+"\n")
		} else {
			blockType, exists := blocks[target]
			if !exists {
				return nil, 0, generatedConfigErrorf("%s projection_fill target %s is not a writable input", options.ResourceType, target)
			}
			children, ok := value.([]any)
			if metadata.TerraformBlockIsSingle(blockType) {
				children = []any{value}
				ok = true
			}
			if !ok {
				return nil, 0, generatedConfigErrorf("%s projection_fill block %s did not shape to a list", options.ResourceType, target)
			}
			childBlock, err := metadata.TerraformRequireObject(blockType["block"], options.ResourceType+"."+target+".block")
			if err != nil {
				return nil, 0, err
			}
			for _, child := range children {
				record, ok := child.(map[string]any)
				if !ok || len(record) == 0 {
					continue
				}
				childLines, err := renderGeneratedHCLBlock(target, childBlock, record, 2)
				if err != nil {
					return nil, 0, err
				}
				lines = append(lines, childLines...)
			}
		}
		if len(lines) > before {
			current.Present[target] = struct{}{}
			options.Policy.MarkMatched(entry)
			edits++
		}
	}
	return lines, edits, nil
}

func splitGeneratedLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func rewriteGeneratedConfig(text string, resources map[string]rewriteResourceOptions) (GeneratedConfigPolicyResult, error) {
	context := generatedContext(resources)
	lines := splitGeneratedLines(text)
	output := make([]string, 0, len(lines))
	stack := make([]*generatedStackEntry, 0)
	seen := make(map[string]struct{})
	heredoc := ""
	valueDepth := 0
	edits := 0
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if heredoc != "" {
			output = append(output, line)
			if stripped == heredoc {
				heredoc = ""
			}
			continue
		}
		if valueDepth != 0 {
			output = append(output, line)
			valueDepth += generatedValueDepthDelta(stripped)
			if valueDepth <= 0 {
				valueDepth = 0
			}
			continue
		}
		if match := generatedResourceStart.FindStringSubmatch(stripped); match != nil {
			address := match[1] + "." + match[2]
			resourceOptions, exists := resources[match[1]]
			if !exists {
				return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%sgenerated import config contained unexpected resource block %s", context, address)
			}
			if _, expected := resourceOptions.AddressToKey[address]; !expected {
				return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%sgenerated import config contained unexpected resource block %s", context, address)
			}
			if _, duplicate := seen[address]; duplicate {
				return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%sgenerated import config contained duplicate resource block %s", context, address)
			}
			seen[address] = struct{}{}
			stack = append(stack, &generatedStackEntry{Address: address, Counts: map[string]int{}, Kind: "resource", Present: map[string]struct{}{}, ResourceType: match[1]})
			output = append(output, line)
			continue
		}
		if stripped == "}" {
			if len(stack) == 1 && stack[0].Kind == "resource" {
				resourceOptions, exists := resources[stack[0].ResourceType]
				if !exists {
					return GeneratedConfigPolicyResult{}, generatedConfigErrorf("generated import config contained unknown sibling resource type %s", stack[0].ResourceType)
				}
				fills, count, err := fillGeneratedResource(stack[0], resourceOptions)
				if err != nil {
					return GeneratedConfigPolicyResult{}, err
				}
				output = append(output, fills...)
				edits += count
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			output = append(output, line)
			continue
		}
		if match := generatedBlockStart.FindStringSubmatch(stripped); match != nil && len(stack) > 0 {
			parent := stack[len(stack)-1]
			name := match[1]
			if len(stack) == 1 {
				parent.Present[name] = struct{}{}
			}
			index := parent.Counts[name]
			parent.Counts[name] = index + 1
			path := append(append([]any(nil), parent.Path...), name, int64(index))
			stack = append(stack, &generatedStackEntry{Counts: map[string]int{}, Kind: "block", Path: path})
			output = append(output, line)
			continue
		}
		if match := generatedAttribute.FindStringSubmatch(stripped); match != nil && len(stack) > 0 && stack[0].Kind == "resource" {
			resourceOptions, exists := resources[stack[0].ResourceType]
			if !exists {
				return GeneratedConfigPolicyResult{}, generatedConfigErrorf("generated import config contained unknown sibling resource type %s", stack[0].ResourceType)
			}
			name, value := match[1], match[2]
			if len(stack) == 1 {
				stack[0].Present[name] = struct{}{}
			}
			path := append(append([]any(nil), stack[len(stack)-1].Path...), name)
			candidate, err := matchGeneratedOmit(path, parseGeneratedScalar(value), resourceOptions.Omits, stack[0].ResourceType, resourceOptions.Schema)
			if err != nil {
				return GeneratedConfigPolicyResult{}, err
			}
			if candidate != nil {
				if candidate.Entry != nil && resourceOptions.Policy != nil {
					resourceOptions.Policy.MarkMatched(*candidate.Entry)
				}
				edits++
				continue
			}
			if heredocMatch := generatedHeredoc.FindStringSubmatch(strings.TrimSpace(value)); heredocMatch != nil {
				heredoc = heredocMatch[1]
			} else {
				valueDepth = max(0, generatedValueDepthDelta(value))
			}
		}
		output = append(output, line)
	}
	missing := make([]string, 0)
	for _, resource := range resources {
		for address := range resource.AddressToKey {
			if _, exists := seen[address]; !exists {
				missing = append(missing, address)
			}
		}
	}
	if len(missing) > 0 {
		return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%sgenerated import config missing resource block(s): %s", context, strings.Join(canonjson.SortedStrings(missing), ", "))
	}
	return GeneratedConfigPolicyResult{Edits: edits, Text: strings.Join(output, "")}, nil
}

// ApplyGeneratedConfigPoliciesOptions ports the options bag accepted by
// applyGeneratedConfigPolicies in the original implementation.
type ApplyGeneratedConfigPoliciesOptions struct {
	GeneratedConfig string
	Resources       []GeneratedConfigPolicyResource
	Root            *metadata.LoadedPackRoot
}

// GeneratedConfigPolicyResult is the text and edit count returned by the
// generated-config policy rewriter.
type GeneratedConfigPolicyResult struct {
	Edits int
	Text  string
}

// ApplyGeneratedConfigPolicies ports applyGeneratedConfigPolicies from
// the original implementation. Every resource is filled before
// any resource is omitted, preserving the source's load-bearing order.
func ApplyGeneratedConfigPolicies(options ApplyGeneratedConfigPoliciesOptions) (GeneratedConfigPolicyResult, error) {
	if options.Root == nil {
		return GeneratedConfigPolicyResult{}, generatedConfigErrorf("generated-config policy requires pack root")
	}
	resources := make(map[string]rewriteResourceOptions, len(options.Resources))
	sorted := append([]GeneratedConfigPolicyResource(nil), options.Resources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ResourceType < sorted[j].ResourceType })
	for _, resource := range sorted {
		if _, duplicate := resources[resource.ResourceType]; duplicate {
			return GeneratedConfigPolicyResult{}, generatedConfigErrorf("duplicate generated-config policy resource type %s", resource.ResourceType)
		}
		entries, err := generatedPolicyEntriesFor(options.Root, resource.ResourceType, resource.Policy)
		if err != nil {
			return GeneratedConfigPolicyResult{}, err
		}
		if len(entries.Fills) > 0 && resource.RawItems == nil {
			return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%s generated import config projection_fill requires raw_items", resource.ResourceType)
		}
		resources[resource.ResourceType] = rewriteResourceOptions{GeneratedConfigPolicyResource: resource, Fills: entries.Fills, Omits: entries.Omits, Schema: entries.Schema}
	}
	hasFills, hasOmits := false, false
	for _, resource := range resources {
		hasFills = hasFills || len(resource.Fills) > 0
		hasOmits = hasOmits || len(resource.Omits) > 0
	}
	if !hasFills && !hasOmits {
		if len(resources) > 1 && options.GeneratedConfig != "" {
			return rewriteGeneratedConfig(options.GeneratedConfig, resources)
		}
		return GeneratedConfigPolicyResult{Text: options.GeneratedConfig}, nil
	}
	if options.GeneratedConfig == "" {
		return GeneratedConfigPolicyResult{}, generatedConfigErrorf("%sgenerated import config is missing; projection policy cannot be applied safely", generatedContext(resources))
	}
	text := options.GeneratedConfig
	edits := 0
	if hasFills {
		fillResources := copyRewriteResources(resources)
		for key, resource := range fillResources {
			resource.Omits = nil
			fillResources[key] = resource
		}
		result, err := rewriteGeneratedConfig(text, fillResources)
		if err != nil {
			return GeneratedConfigPolicyResult{}, err
		}
		text, edits = result.Text, edits+result.Edits
	}
	if hasOmits {
		omitResources := copyRewriteResources(resources)
		for key, resource := range omitResources {
			resource.Fills = nil
			omitResources[key] = resource
		}
		result, err := rewriteGeneratedConfig(text, omitResources)
		if err != nil {
			return GeneratedConfigPolicyResult{}, err
		}
		text, edits = result.Text, edits+result.Edits
	}
	return GeneratedConfigPolicyResult{Edits: edits, Text: text}, nil
}

func copyRewriteResources(input map[string]rewriteResourceOptions) map[string]rewriteResourceOptions {
	output := make(map[string]rewriteResourceOptions, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// ApplyGeneratedConfigPolicy ports applyGeneratedConfigPolicy from
// the original implementation.
func ApplyGeneratedConfigPolicy(generatedConfig string, resource GeneratedConfigPolicyResource, root *metadata.LoadedPackRoot) (GeneratedConfigPolicyResult, error) {
	return ApplyGeneratedConfigPolicies(ApplyGeneratedConfigPoliciesOptions{GeneratedConfig: generatedConfig, Resources: []GeneratedConfigPolicyResource{resource}, Root: root})
}
