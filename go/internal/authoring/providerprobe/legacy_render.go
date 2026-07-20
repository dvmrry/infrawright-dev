package providerprobe

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func terraformSchemaHCL(provider recipeTerraformProvider, providerSource string, providerVersion *string) string {
	source := stringOr(provider.source, strings.TrimPrefix(providerSource, "registry.terraform.io/"))
	version := provider.version
	if falsey(version) {
		version = providerVersion
	}
	name := stringOr(provider.localName, strings.ReplaceAll(lastPath(source), "-", "_"))
	quote := func(value string) string { encoded, _ := json.Marshal(value); return string(encoded) }
	lines := []string{"terraform {", "  required_providers {", "    " + name + " = {", "      source = " + quote(source)}
	if version != nil {
		lines = append(lines, "      version = "+quote(*version))
	}
	lines = append(lines, "    }", "  }", "}", "")
	return strings.Join(lines, "\n")
}

func lastPath(value string) string {
	parts := strings.Split(strings.TrimRight(value, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func openAPIOperationProfile(openAPI map[string]any) (map[string]any, error) {
	operations, gets, missing := 0, 0, 0
	pathsRaw := openAPI["paths"]
	if !jsonFalsey(pathsRaw) {
		if _, ok := pathsRaw.(map[string]any); !ok {
			return nil, fmt.Errorf("OpenAPI paths must be an object")
		}
	}
	paths, _ := pathsRaw.(map[string]any)
	for apiPath, raw := range paths {
		if !jsonFalsey(raw) {
			if _, ok := raw.(map[string]any); !ok {
				return nil, fmt.Errorf("OpenAPI path item must be an object: %s", apiPath)
			}
		}
		item, _ := raw.(map[string]any)
		for method, operation := range item {
			if !legacyHTTPMethod(method) {
				continue
			}
			if _, ok := operation.(map[string]any); !ok {
				continue
			}
			operations++
			if strings.EqualFold(method, "get") {
				gets++
			}
			id, ok := operation.(map[string]any)["operationId"].(string)
			if !ok || id == "" {
				missing++
			}
		}
	}
	profile := map[string]any{"get_operations": gets, "missing_operation_ids": missing, "operations": operations}
	if operations == 0 {
		profile["operation_id_coverage_ratio"] = nil
	} else {
		profile["operation_id_coverage_ratio"] = ratio4(operations-missing, operations)
	}
	return profile, nil
}

func legacyHTTPMethod(value string) bool {
	switch strings.ToLower(value) {
	case "get", "post", "put", "patch", "delete":
		return true
	}
	return false
}
func jsonFalsey(value any) bool {
	if value == nil {
		return true
	}
	if b, ok := value.(bool); ok {
		return !b
	}
	if s, ok := value.(string); ok {
		return s == ""
	}
	if n, ok := value.(json.Number); ok {
		parsed, err := n.Float64()
		return err == nil && parsed == 0
	}
	if n, ok := value.(float64); ok {
		return n == 0
	}
	if n, ok := value.(int); ok {
		return n == 0
	}
	if a, ok := value.([]any); ok {
		return len(a) == 0
	}
	if m, ok := value.(map[string]any); ok {
		return len(m) == 0
	}
	return false
}

func ratio4(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator)
	if value == 0 {
		return 0
	}
	bits := math.Float64bits(math.Abs(value))
	exponentBits := (bits >> 52) & 0x7ff
	fraction := new(big.Int).SetUint64(bits & ((uint64(1) << 52) - 1))
	significand := new(big.Int).Set(fraction)
	exponent := int(exponentBits) - 1075
	if exponentBits != 0 {
		significand.SetBit(significand, 52, 1)
	}
	scaled := new(big.Int).Mul(significand, big.NewInt(10000))
	divisor := big.NewInt(1)
	if exponent >= 0 {
		scaled.Lsh(scaled, uint(exponent))
	} else {
		divisor.Lsh(divisor, uint(-exponent))
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(scaled, divisor, r)
	twice := new(big.Int).Lsh(r, 1)
	if twice.Cmp(divisor) > 0 || (twice.Cmp(divisor) == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	result := float64(q.Int64()) / 10000
	if value < 0 {
		return -result
	}
	return result
}

func buildLegacySummary(recipe loadedRecipe, source, openAPI, profile map[string]any) (map[string]any, error) {
	summary := func(value any, label string) (map[string]any, error) {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", label)
		}
		return object, nil
	}
	generic, err := summary(openAPI["summary"], "openapi report summary")
	if err != nil {
		return nil, err
	}
	readReport, err := summary(openAPI["registry_read_coverage"], "registry read coverage")
	if err != nil {
		return nil, err
	}
	read, err := summary(readReport["summary"], "registry read coverage summary")
	if err != nil {
		return nil, err
	}
	fetchReport, err := summary(openAPI["registry_fetch_coverage"], "registry fetch coverage")
	if err != nil {
		return nil, err
	}
	fetch, err := summary(fetchReport["summary"], "registry fetch coverage summary")
	if err != nil {
		return nil, err
	}
	sourceSummary, err := summary(source["summary"], "source report summary")
	if err != nil {
		return nil, err
	}
	return map[string]any{"generic_openapi_map": generic, "openapi_operation_profile": profile, "provider": map[string]any{"api_prefix": nullishStringOr(recipe.api, "/api/"), "name": nullable(recipe.name), "provider_source": nullable(recipe.provider), "provider_version": nullable(recipe.version), "resource_prefix": stringOr(recipe.resource, "")}, "registry_fetch_coverage": fetch, "registry_read_coverage": read, "source_evidence": sourceSummary, "warning_codes": legacyWarningCodes(openAPI)}, nil
}
func nullable(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
func legacyWarningCodes(report map[string]any) []any {
	codes := []string{}
	collect := func(value any) {
		object, _ := value.(map[string]any)
		warnings, _ := object["warnings"].([]any)
		for _, warning := range warnings {
			item, _ := warning.(map[string]any)
			if code, ok := item["code"].(string); ok && code != "" {
				codes = append(codes, code)
			}
		}
	}
	collect(report["coverage"])
	collect(report["registry_read_coverage"])
	collect(report["registry_fetch_coverage"])
	sort.Slice(codes, func(i, j int) bool { return canonjson.ComparePythonStrings(codes[i], codes[j]) < 0 })
	out := make([]any, len(codes))
	for i := range codes {
		out[i] = codes[i]
	}
	return out
}

func renderLegacyMarkdown(summary map[string]any, artifacts map[string]string) (string, error) {
	object := func(value any, label string) (map[string]any, error) {
		o, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", label)
		}
		return o, nil
	}
	provider, err := object(summary["provider"], "provider summary")
	if err != nil {
		return "", err
	}
	source, err := object(summary["source_evidence"], "source evidence summary")
	if err != nil {
		return "", err
	}
	generic, err := object(summary["generic_openapi_map"], "OpenAPI summary")
	if err != nil {
		return "", err
	}
	read, err := object(summary["registry_read_coverage"], "read coverage summary")
	if err != nil {
		return "", err
	}
	profile, err := object(summary["openapi_operation_profile"], "OpenAPI operation profile")
	if err != nil {
		return "", err
	}
	heading := display(provider["name"])
	if heading == "" {
		heading = display(provider["resource_prefix"])
	}
	if heading == "" {
		heading = "unknown"
	}
	row := func(label string, data map[string]any) string {
		keys := []string{"resources", "mapped", "ambiguous", "unmapped", "matched", "coverage"}
		values := make([]string, len(keys))
		for i, k := range keys {
			if k == "coverage" {
				if value, present := data[k]; present {
					values[i] = coverageDisplay(value)
				}
			} else {
				values[i] = display(data[k])
			}
		}
		return "| " + label + " | " + strings.Join(values, " | ") + " |"
	}
	lines := []string{"# Provider Probe: " + heading, "", "- Provider source: `" + display(provider["provider_source"]) + "`", "- Provider version: `" + display(provider["provider_version"]) + "`", "- Resource prefix: `" + display(provider["resource_prefix"]) + "`", "- API prefix: `" + display(provider["api_prefix"]) + "`", "", "## Coverage", "", "| Section | Resources | Mapped | Ambiguous | Unmapped | Matched | Coverage |", "|---|---:|---:|---:|---:|---:|---:|", row("source evidence", map[string]any{"resources": source["resources"], "mapped": source["mapped"], "ambiguous": source["ambiguous"], "unmapped": source["unmapped"]}), row("generic OpenAPI map", map[string]any{"resources": generic["resources"], "ambiguous": generic["ambiguous"], "unmapped": generic["unmatched"], "matched": generic["matched"]}), row("registry read coverage", map[string]any{"resources": read["read_resources"], "ambiguous": read["ambiguous"], "unmapped": read["unmatched"], "matched": read["matched"], "coverage": read["coverage_ratio"]}), "", "## OpenAPI", "", "- Operations: `" + display(profile["operations"]) + "`", "- GET operations: `" + display(profile["get_operations"]) + "`", "- Missing operationIds: `" + display(profile["missing_operation_ids"]) + "`", "- operationId coverage: `" + coverageDisplay(profile["operation_id_coverage_ratio"]) + "`", "", "## Warnings", ""}
	warnings, _ := summary["warning_codes"].([]any)
	if len(warnings) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, warning := range warnings {
			if code, ok := warning.(string); ok {
				lines = append(lines, "- `"+code+"`")
			}
		}
	}
	if artifacts != nil {
		lines = append(lines, "", "## Artifacts", "")
		names := make([]string, 0, len(artifacts))
		for name := range artifacts {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return canonjson.ComparePythonStrings(names[i], names[j]) < 0 })
		for _, name := range names {
			lines = append(lines, "- `"+name+"`: `"+artifacts[name]+"`")
		}
	}
	return strings.Join(append(lines, ""), "\n"), nil
}
func display(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return fmt.Sprint(typed)
	case float64:
		return fmt.Sprint(typed)
	case json.Number:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}
func coverageDisplay(value any) string {
	if value == nil {
		return "None"
	}
	if n, ok := value.(float64); ok && n == float64(int64(n)) {
		return fmt.Sprintf("%.1f", n)
	}
	return display(value)
}

// renderLegacyJSON mirrors renderAuthoringJson's float-field spelling.
func renderLegacyJSON(value any) ([]byte, error) {
	rendered, err := canonjson.Render(authoringNumbers(value, ""))
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}
func authoringNumbers(value any, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = authoringNumbers(v, k)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = authoringNumbers(v, "")
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = v
		}
		return out
	case float64:
		if key == "coverage_ratio" || key == "operation_id_coverage_ratio" {
			if typed == float64(int64(typed)) {
				return json.Number(fmt.Sprintf("%.1f", typed))
			}
			return json.Number(fmt.Sprint(typed))
		}
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	default:
		return value
	}
}
