package openapimap

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/reconcile"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// Object is a detached generic JSON object consumed and emitted by the Node
// openapi-resource-map.ts-compatible kernel.
type Object = map[string]any

// Options controls Build and ports buildOpenApiResourceMap's arguments from
// the original implementation.
type Options struct {
	SchemaData     Object
	Document       openapiadapter.Document
	ProviderSource *string
	ResourcePrefix string
	// APIPrefix is nil when omitted (the Node default /api/ applies). A non-nil
	// empty string is an explicit match-all prefix.
	APIPrefix    *string
	RegistryData *Object
}

// Report is a sealed generic diagnostic report from
// the original implementation.
type Report struct{ data Object }

// Data returns detached structured generic diagnostic data from
// the original implementation.
func (r Report) Data() Object { return cloneObject(r.data) }

// Render returns exact Python-compatible report bytes from
// the original implementation.
func (r Report) Render() ([]byte, error) {
	if r.data == nil {
		return nil, fmt.Errorf("openapi map report must come from Build")
	}
	rendered, err := canonjson.Render(renderableJSON(r.data))
	if err != nil {
		return nil, fmt.Errorf("render OpenAPI map report: %w", err)
	}
	return []byte(rendered), nil
}

func renderableJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = renderableJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = renderableJSON(typed[i])
		}
		return out
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case []string:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = typed[i]
		}
		return out
	default:
		return typed
	}
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
var surfaceHint = regexp.MustCompile(`(?:^|_)(?:url|uri|host|endpoint|token|auth|cloud|region|realm)(?:$|_)`)

// Build creates the sealed generic diagnostic report from
// the original implementation. It intentionally cannot be
// used as source-first readiness evidence.
func Build(ctx context.Context, options Options) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, fmt.Errorf("build OpenAPI map cancelled: %w", err)
	}
	if options.SchemaData == nil {
		return Report{}, fmt.Errorf("schema data must be a JSON object")
	}
	provider, err := providerFromSchema(options.SchemaData, options.ProviderSource)
	if err != nil {
		return Report{}, err
	}
	schemas, ok := provider["resource_schemas"].(map[string]any)
	if !ok {
		schemas = Object{}
	}
	view, err := options.Document.LegacyMap(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("build legacy OpenAPI view: %w", err)
	}
	prefix, apiPrefix := options.ResourcePrefix, "/api/"
	if options.APIPrefix != nil {
		apiPrefix = *options.APIPrefix
	}
	resources := make([]any, 0, len(schemas))
	summary := Object{"ambiguous": 0, "matched": 0, "resources": len(schemas), "special": 0, "static_provider_gap_resources": 0, "unmatched": 0}
	families, surfaces := map[string]map[string]int{}, map[string]int{}
	for _, resourceType := range sortedKeys(schemas) {
		if err := ctx.Err(); err != nil {
			return Report{}, fmt.Errorf("build OpenAPI map cancelled: %w", err)
		}
		schema, ok := schemas[resourceType].(map[string]any)
		if !ok {
			schema = Object{}
		}
		match, err := matchResource(view, resourceType, schema, prefix, apiPrefix)
		if err != nil {
			return Report{}, err
		}
		if match["status"] != "matched" {
			if special, specialErr := specialResource(ctx, options.Document, view, resourceType, schema, prefix, apiPrefix); specialErr != nil {
				return Report{}, specialErr
			} else if special != nil {
				match = special
			}
		}
		item := Object{"resource": resourceType}
		merge(item, match)
		status, _ := match["status"].(string)
		summary[status] = summary[status].(int) + 1
		family := familyFor(resourceType, prefix)
		if families[family] == nil {
			families[family] = map[string]int{}
		}
		families[family][status]++
		if status == "matched" {
			contract, contractErr := staticContract(ctx, options.Document, schema, str(match["collection_path"]), str(match["detail_path"]))
			if contractErr != nil {
				return Report{}, contractErr
			}
			item["static_contract"] = contract
		}
		if contract, ok := item["static_contract"].(map[string]any); ok {
			if gaps, ok := contract["provider_gap_top_level_paths"].([]any); ok && len(gaps) != 0 {
				summary["static_provider_gap_resources"] = summary["static_provider_gap_resources"].(int) + 1
			}
		}
		if status == "matched" || status == "special" {
			surface := strOr(match["surface"], "unknown")
			surfaces[surface]++
		}
		resources = append(resources, item)
	}
	profile := openAPIProfile(view, apiPrefix)
	hints := providerHints(provider)
	coverage := coverageDiagnostics(summary, families, profile, hints)
	registry := Object{}
	if options.RegistryData != nil {
		registry = cloneObject(*options.RegistryData)
	}
	fetch := registryCoverage(view, apiPrefix, prefix, registry, "fetch")
	read := registryCoverage(view, apiPrefix, prefix, registry, "read")
	result := Object{"api_prefix": apiPrefix, "coverage": coverage, "openapi": Object{"path_count": len(view.Paths), "profile": profile, "schema_count": view.ComponentSchemaCount, "version": pointerValue(view.Version)}, "provider_config_hints": hints, "provider_source": pointerValue(options.ProviderSource), "registry_fetch_coverage": fetch, "registry_read_coverage": read, "resource_prefix": prefix, "resources": resources, "summary": summary, "surface_map": surfaceMap(options.ProviderSource, prefix, resources, fetch, read, objects(coverage["warnings"])), "surfaces": intMapObject(surfaces)}
	return Report{data: cloneObject(result)}, nil
}

func providerFromSchema(data Object, providerSource *string) (Object, error) {
	if _, ok := data["resource_schemas"].(map[string]any); ok {
		return cloneObject(data), nil
	}
	providers, ok := data["provider_schemas"].(map[string]any)
	if !ok {
		providers = Object{}
	}
	if providerSource != nil {
		if provider, ok := providers[*providerSource].(map[string]any); ok {
			return cloneObject(provider), nil
		}
		var matches []Object
		for source, value := range providers {
			if strings.HasSuffix(source, "/"+*providerSource) {
				if provider, ok := value.(map[string]any); ok {
					matches = append(matches, provider)
				}
			}
		}
		if len(matches) == 1 {
			return cloneObject(matches[0]), nil
		}
		return nil, fmt.Errorf("provider source %q not found", *providerSource)
	}
	values := []Object{}
	for _, value := range providers {
		if provider, ok := value.(map[string]any); ok {
			values = append(values, provider)
		}
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("schema has multiple providers; pass providerSource")
	}
	return cloneObject(values[0]), nil
}

func methods(path openapiadapter.LegacyPath) []string { return append([]string(nil), path.Methods...) }
func hasMethod(path openapiadapter.LegacyPath, method string) bool {
	for _, item := range path.Methods {
		if item == strings.ToLower(method) {
			return true
		}
	}
	return false
}
func pathMap(view openapiadapter.LegacyMap) map[string]openapiadapter.LegacyPath {
	output := map[string]openapiadapter.LegacyPath{}
	for _, p := range view.Paths {
		output[p.Template] = p
	}
	return output
}
func openAPIPathParts(path, prefix string) []string {
	return splitPath(strings.TrimPrefix(path, prefix))
}
func canonicalParts(path string) []string {
	parts := splitPath(path)
	for i, part := range parts {
		if parameter(part) {
			parts[i] = "{}"
		}
	}
	return parts
}
func splitPath(path string) []string {
	return filter(strings.Split(strings.Trim(path, "/"), "/"), func(v string) bool { return v != "" })
}
func parameter(value string) bool {
	return strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")
}
func collectionPaths(view openapiadapter.LegacyMap, prefix string) []string {
	out := []string{}
	for _, p := range view.Paths {
		if !strings.HasPrefix(p.Template, prefix) {
			continue
		}
		parts := openAPIPathParts(p.Template, prefix)
		if len(parts) == 0 || parameter(parts[len(parts)-1]) {
			continue
		}
		if hasMethod(p, "get") || hasMethod(p, "post") {
			out = append(out, p.Template)
		}
	}
	return sorted(out)
}
func detailPaths(view openapiadapter.LegacyMap, collection string) []string {
	separator := "/"
	if strings.HasSuffix(collection, "/") {
		separator = ""
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(collection) + separator + `\{[^/]+\}/?$`)
	out := []string{}
	for _, p := range view.Paths {
		if pattern.MatchString(p.Template) {
			out = append(out, p.Template)
		}
	}
	return sorted(out)
}
func canonicalSlug(value string) string {
	value = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(value, "$1-$2")
	value = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(value, "$1-$2")
	value = regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(value, "-")
	return strings.ToLower(strings.Trim(value, "-"))
}
func plural(token string) string {
	if token == "address" {
		return "addresses"
	}
	if token == "chassis" {
		return "chassis"
	}
	if len(token) > 1 && strings.HasSuffix(token, "y") && !strings.ContainsRune("aeiou", rune(token[len(token)-2])) {
		return token[:len(token)-1] + "ies"
	}
	for _, s := range []string{"s", "x", "ch", "sh"} {
		if strings.HasSuffix(token, s) {
			return token + "es"
		}
	}
	return token + "s"
}
func pluralSlug(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) > 0 {
		parts[len(parts)-1] = plural(parts[len(parts)-1])
	}
	return strings.Join(parts, "-")
}
func singularSlug(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) == 0 {
		return value
	}
	t := parts[len(parts)-1]
	switch {
	case t == "addresses":
		t = "address"
	case strings.HasSuffix(t, "ies") && len(t) > 3:
		t = t[:len(t)-3] + "y"
	case strings.HasSuffix(t, "ches") || strings.HasSuffix(t, "shes") || strings.HasSuffix(t, "xes") || strings.HasSuffix(t, "ses"):
		t = t[:len(t)-2]
	case strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss"):
		t = t[:len(t)-1]
	}
	parts[len(parts)-1] = t
	return strings.Join(parts, "-")
}
func baseTokens(resource, prefix string) []string {
	if prefix != "" && strings.HasPrefix(resource, prefix+"_") {
		resource = strings.TrimPrefix(resource, prefix+"_")
	}
	return filter(strings.Split(resource, "_"), func(v string) bool { return v != "" })
}
func slugCandidates(resource, prefix string) map[string]int {
	tokens := baseTokens(resource, prefix)
	out := map[string]int{}
	for start := range tokens {
		slug := strings.Join(tokens[start:], "-")
		score := 80 - start
		if start == 0 {
			score = 120
		}
		if _, ok := out[slug]; !ok {
			out[slug] = score - 8
		}
		p := pluralSlug(slug)
		if _, ok := out[p]; !ok {
			out[p] = score
		}
	}
	aliases := map[string]map[string][]string{"ztc": {"dns-forwarding-gateway": {"dns-gateways"}, "forwarding-gateway": {"gateways"}, "ip-pool-groups": {"ip-groups"}, "provisioning-url": {"prov-url"}, "traffic-forwarding-dns-rule": {"ec-dns"}, "traffic-forwarding-log-rule": {"self"}, "traffic-forwarding-rule": {"ec-rdr"}}}
	for _, alias := range aliases[prefix][strings.Join(tokens, "-")] {
		if out[alias] < 150 {
			out[alias] = 150
		}
	}
	return out
}
func schemaInputs(schema Object) (map[string]bool, map[string]bool, error) {
	block, err := metadata.TerraformBlockForSchema(schema, "resource")
	if err != nil {
		return nil, nil, err
	}
	classified, err := metadata.TerraformClassifyAttributes(block, "resource.block")
	if err != nil {
		return nil, nil, err
	}
	inputs := map[string]bool{}
	computed := map[string]bool{}
	for _, v := range append(classified.Required, classified.Optional...) {
		inputs[v] = true
	}
	for _, v := range classified.ComputedOnly {
		computed[v] = true
	}
	nested, err := metadata.TerraformInputBlockTypes(block, "resource.block")
	if err != nil {
		return nil, nil, err
	}
	for v := range nested {
		inputs[v] = true
	}
	return inputs, computed, nil
}

func matchResource(view openapiadapter.LegacyMap, resource string, schema Object, prefix, apiPrefix string) (Object, error) {
	tokens := baseTokens(resource, prefix)
	slugs := slugCandidates(resource, prefix)
	paths := pathMap(view)
	candidates := []Object{}
	inputs, _, err := schemaInputs(schema)
	if err != nil {
		return nil, err
	}
	for _, collection := range collectionPaths(view, apiPrefix) {
		parts := openAPIPathParts(collection, apiPrefix)
		segment := canonicalSlug(parts[len(parts)-1])
		base, ok := slugs[segment]
		if !ok {
			continue
		}
		details := detailPaths(view, collection)
		var detail any = nil
		if len(details) > 0 {
			detail = details[0]
		}
		app := 0
		if len(parts) > 0 && len(tokens) > 0 && (parts[0] == tokens[0] || singularSlug(parts[0]) == tokens[0]) {
			app = 12
		}
		hint := 0
		if len(parts) > 0 && parts[0] == "dcim" && inputs["device_id"] {
			hint = 25
		}
		if len(parts) > 0 && parts[0] == "virtualization" && inputs["virtual_machine_id"] {
			hint = 25
		}
		methodScore := 0
		if d, ok := detail.(string); ok && hasMethod(paths[d], "get") {
			methodScore += 10
		}
		if hasMethod(paths[collection], "post") {
			methodScore += 6
		}
		if d, ok := detail.(string); ok && (hasMethod(paths[d], "put") || hasMethod(paths[d], "patch")) {
			methodScore += 6
		}
		confidence := "suffix_plural"
		if segment == pluralSlug(strings.Join(tokens, "-")) {
			confidence = "exact_plural"
		}
		score := base + app + hint + methodScore
		if confidence == "suffix_plural" && app == 0 {
			score -= 60
		}
		surface := any(nil)
		if len(parts) > 0 {
			surface = parts[0]
		}
		candidates = append(candidates, Object{"collection_path": collection, "confidence": confidence, "detail_path": detail, "matched_segment": segment, "score": score, "surface": surface})
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		return a["score"].(int) > b["score"].(int) || (a["score"].(int) == b["score"].(int) && canonjson.ComparePythonStrings(str(a["collection_path"]), str(b["collection_path"])) < 0)
	})
	if len(candidates) == 0 {
		return Object{"candidates": []any{}, "reason": "no_openapi_collection_path_match", "status": "unmatched"}, nil
	}
	top := candidates[0]
	tied := []any{}
	for _, c := range candidates {
		if c["score"] == top["score"] {
			tied = append(tied, c)
		}
	}
	if len(tied) > 1 {
		return Object{"candidates": tied[:min(5, len(tied))], "reason": "multiple_equal_score_matches", "status": "ambiguous"}, nil
	}
	status, reason := "matched", any(nil)
	if top["score"].(int) < 60 {
		status, reason = "unmatched", "low_confidence_suffix_match"
	} else if top["detail_path"] == nil {
		status, reason = "unmatched", "matched_collection_has_no_standard_detail_path"
	}
	out := Object{"candidates": objectsFrom(candidates[:min(5, len(candidates))]), "confidence": top["confidence"], "reason": reason, "score": top["score"], "status": status}
	if status == "matched" {
		out["collection_path"] = top["collection_path"]
		out["detail_path"] = top["detail_path"]
		out["surface"] = top["surface"]
	} else {
		out["collection_path"] = nil
		out["detail_path"] = nil
		out["surface"] = nil
	}
	return out, nil
}

func topLevelMetadata(ctx context.Context, document openapiadapter.Document, schema Object, read, write []string) (Object, error) {
	r := make([]openapiadapter.OperationReference, len(read))
	for i, v := range read {
		r[i] = openapiadapter.OperationReference(v)
	}
	w := make([]openapiadapter.OperationReference, len(write))
	for i, v := range write {
		w[i] = openapiadapter.OperationReference(v)
	}
	fields, err := document.Metadata(ctx, openapiadapter.MetadataOptions{ReadOperations: r, WriteOperations: w})
	if err != nil {
		return nil, err
	}
	inputs, computed, err := schemaInputs(schema)
	if err != nil {
		return nil, err
	}
	reads, writes, response := []string{}, []string{}, []string{}
	aliases := []any{}
	gaps := []string{}
	fieldNames := make([]string, 0, len(fields))
	for path := range fields {
		fieldNames = append(fieldNames, path)
	}
	for _, path := range canonjson.SortedStrings(fieldNames) {
		if strings.Contains(strings.ReplaceAll(path, "[]", "."), ".") {
			continue
		}
		field := fields[path]
		if boolean(field["readable"]) {
			reads = append(reads, path)
		}
		if boolean(field["writable"]) {
			writes = append(writes, path)
		}
		if boolean(field["response_only"]) || boolean(field["read_only"]) {
			response = append(response, path)
		}
	}
	inputSet, computedSet := map[string]struct{}{}, map[string]struct{}{}
	for field := range inputs {
		inputSet[field] = struct{}{}
	}
	for field := range computed {
		computedSet[field] = struct{}{}
	}
	for _, path := range writes {
		field := strings.Split(strings.ReplaceAll(path, "[]", ""), ".")[0]
		if inputs[field] {
			continue
		}
		alias, kind, reason, found := reconcile.ReconciliationFieldAlias(field, inputSet, computedSet)
		if found && kind == "input" {
			aliases = append(aliases, Object{"api_path": path, "reason": reason, "terraform_path": alias})
		} else if !computed[field] {
			gaps = append(gaps, path)
		}
	}
	return Object{"aliased_top_level_paths": aliases, "provider_gap_top_level_paths": stringsToAny(gaps), "read_operations": stringsToAny(read), "read_top_level_paths": stringsToAny(reads), "response_only_top_level_paths": stringsToAny(response), "summary": Object{"aliased_top_level": len(aliases), "provider_gap_top_level": len(gaps), "read_top_level": len(reads), "response_only_top_level": len(response), "write_top_level": len(writes)}, "write_operations": stringsToAny(write), "write_top_level_paths": stringsToAny(writes)}, nil
}
func staticContract(ctx context.Context, document openapiadapter.Document, schema Object, collection, detail string) (Object, error) {
	view, err := document.LegacyMap(ctx)
	if err != nil {
		return nil, err
	}
	paths := pathMap(view)
	read := []string{}
	if hasMethod(paths[detail], "get") {
		read = []string{"GET:" + detail}
	}
	write := []string{}
	if hasMethod(paths[collection], "post") {
		write = append(write, "POST:"+collection)
	}
	if hasMethod(paths[detail], "put") {
		write = append(write, "PUT:"+detail)
	}
	if hasMethod(paths[detail], "patch") {
		write = append(write, "PATCH:"+detail)
	}
	return topLevelMetadata(ctx, document, schema, read, write)
}
func staticActionContract(ctx context.Context, document openapiadapter.Document, schema Object, write []string) (Object, error) {
	contract, err := topLevelMetadata(ctx, document, schema, nil, write)
	if err != nil {
		return nil, err
	}
	return Object{"aliased_top_level_paths": contract["aliased_top_level_paths"], "provider_gap_top_level_paths": contract["provider_gap_top_level_paths"], "summary": Object{"aliased_top_level": object(contract["summary"])["aliased_top_level"], "provider_gap_top_level": object(contract["summary"])["provider_gap_top_level"], "write_top_level": object(contract["summary"])["write_top_level"]}, "write_operations": stringsToAny(write), "write_top_level_paths": contract["write_top_level_paths"]}, nil
}
func specialResource(ctx context.Context, document openapiadapter.Document, view openapiadapter.LegacyMap, resource string, schema Object, prefix, apiPrefix string) (Object, error) {
	if value, err := allocationAction(ctx, document, view, resource, schema, prefix, apiPrefix); value != nil || err != nil {
		return value, err
	}
	if value, err := primaryIP(ctx, document, view, resource, schema, prefix, apiPrefix); value != nil || err != nil {
		return value, err
	}
	if value, err := primaryMAC(ctx, document, view, resource, schema, prefix, apiPrefix); value != nil || err != nil {
		return value, err
	}
	return aliasedAction(ctx, document, view, resource, schema, apiPrefix)
}
func parentCandidates(field string, objectTokens []string) map[string]bool {
	base := strings.TrimSuffix(field, "_id")
	tokens := filter(strings.Split(base, "_"), func(v string) bool { return v != "" })
	if len(tokens) == 0 {
		return map[string]bool{}
	}
	slug := strings.Join(tokens, "-")
	out := map[string]bool{slug: true, pluralSlug(slug): true}
	if tokens[0] == "parent" && len(tokens) > 1 {
		p := strings.Join(tokens[1:], "-")
		out[p] = true
		out[pluralSlug(p)] = true
	}
	if strings.Join(tokens, "_") == "ip_range" {
		out["ip-ranges"] = true
	}
	if strings.Join(tokens, "_") == "virtual_machine" {
		out["virtual-machines"] = true
	}
	if tokens[0] == "group" && len(objectTokens) > 0 {
		x := strings.Join(objectTokens, "-")
		out[x+"-groups"] = true
		out[pluralSlug(x)+"-groups"] = true
	}
	return out
}
func allocationAction(ctx context.Context, document openapiadapter.Document, view openapiadapter.LegacyMap, resource string, schema Object, prefix, apiPrefix string) (Object, error) {
	tokens := baseTokens(resource, prefix)
	if len(tokens) < 2 || tokens[0] != "available" {
		return nil, nil
	}
	objects := tokens[1:]
	slug := strings.Join(objects, "-")
	wanted := map[string]bool{"available-" + pluralSlug(slug): true}
	if slug == "ip" || slug == "ip-address" {
		wanted["available-ips"] = true
	}
	inputs, computed, err := schemaInputs(schema)
	if err != nil {
		return nil, err
	}
	parents := map[string][]string{}
	for field := range inputs {
		if !strings.HasSuffix(field, "_id") || computed[field] {
			continue
		}
		for candidate := range parentCandidates(field, objects) {
			parents[candidate] = append(parents[candidate], field)
		}
	}
	actions := []any{}
	paths := pathMap(view)
	pathNames := make([]string, 0, len(paths))
	for path := range paths {
		pathNames = append(pathNames, path)
	}
	for _, path := range canonjson.SortedStrings(pathNames) {
		if !strings.HasPrefix(path, apiPrefix) || !strings.HasSuffix(path, "/") || !hasMethod(paths[path], "post") {
			continue
		}
		parts := openAPIPathParts(path, apiPrefix)
		if len(parts) < 3 || !wanted[parts[len(parts)-1]] || !parameter(parts[len(parts)-2]) {
			continue
		}
		fields := sorted(parents[parts[len(parts)-3]])
		if len(parents) > 0 && len(fields) == 0 {
			continue
		}
		actions = append(actions, Object{"action_segment": parts[len(parts)-1], "operation": "POST:" + path, "parent_collection_segment": parts[len(parts)-3], "parent_id_fields": stringsToAny(fields), "path": path})
	}
	if len(actions) == 0 {
		return nil, nil
	}
	writes := []string{}
	for _, action := range actions {
		writes = append(writes, object(action)["operation"].(string))
	}
	contract, err := staticActionContract(ctx, document, schema, writes)
	if err != nil {
		return nil, err
	}
	return Object{"actions": actions, "candidates": []any{}, "canonical_resource": strings.Replace(resource, "_available_", "_", 1), "collection_path": nil, "detail_path": nil, "reason": "parent_scoped_openapi_action", "special_type": "allocation_action", "static_contract": contract, "status": "special", "surface": openAPIPathParts(object(actions[0])["path"].(string), apiPrefix)[0]}, nil
}
func parentCollections(view openapiadapter.LegacyMap, slug, prefix string) []Object {
	wanted := pluralSlug(slug)
	paths := pathMap(view)
	out := []Object{}
	for _, collection := range collectionPaths(view, prefix) {
		parts := openAPIPathParts(collection, prefix)
		if parts[len(parts)-1] != wanted {
			continue
		}
		details := detailPaths(view, collection)
		if len(details) > 0 && (hasMethod(paths[details[0]], "patch") || hasMethod(paths[details[0]], "put")) {
			out = append(out, Object{"collection": collection, "detail": details[0], "surface": parts[0]})
		}
	}
	return out
}
func primaryIP(ctx context.Context, document openapiadapter.Document, view openapiadapter.LegacyMap, resource string, schema Object, prefix, apiPrefix string) (Object, error) {
	tokens := baseTokens(resource, prefix)
	inputs, _, err := schemaInputs(schema)
	if err != nil {
		return nil, err
	}
	if len(tokens) < 2 || strings.Join(tokens[len(tokens)-2:], "_") != "primary_ip" || !inputs["ip_address_id"] {
		return nil, nil
	}
	parents := [][2]string{}
	if inputs["device_id"] {
		parents = append(parents, [2]string{"device_id", "device"})
	}
	if inputs["virtual_machine_id"] {
		parents = append(parents, [2]string{"virtual_machine_id", "virtual-machine"})
	}
	paths := pathMap(view)
	assignments := []any{}
	for _, pair := range parents {
		for _, parent := range parentCollections(view, pair[1], apiPrefix) {
			detail := str(parent["detail"])
			writes := []string{}
			if hasMethod(paths[detail], "patch") {
				writes = append(writes, "PATCH:"+detail)
			}
			if hasMethod(paths[detail], "put") {
				writes = append(writes, "PUT:"+detail)
			}
			meta, metaErr := topLevelMetadata(ctx, document, schema, []string{"GET:" + detail}, writes)
			if metaErr != nil {
				return nil, metaErr
			}
			fields := []string{}
			for _, p := range anyStrings(meta["write_top_level_paths"]) {
				if p == "primary_ip4" || p == "primary_ip6" {
					fields = append(fields, p)
				}
			}
			if len(fields) > 0 {
				version := any(nil)
				if inputs["ip_address_version"] {
					version = "ip_address_version"
				}
				assignments = append(assignments, Object{"ip_address_id_field": "ip_address_id", "parent_collection_path": parent["collection"], "parent_detail_path": detail, "parent_id_field": pair[0], "surface": parent["surface"], "version_field": version, "write_fields": stringsToAny(fields), "write_operations": stringsToAny(writes)})
			}
		}
	}
	if len(assignments) == 0 {
		return nil, nil
	}
	first := object(assignments[0])
	canonical := prefix + "_virtual_machine"
	if first["parent_id_field"] == "device_id" {
		canonical = prefix + "_device"
	}
	return Object{"assignments": assignments, "candidates": []any{}, "canonical_parent_resource": canonical, "collection_path": nil, "detail_path": nil, "reason": "parent_field_assignment", "special_type": "derived_relationship", "status": "special", "surface": first["surface"]}, nil
}
func primaryMAC(ctx context.Context, document openapiadapter.Document, view openapiadapter.LegacyMap, resource string, schema Object, prefix, apiPrefix string) (Object, error) {
	tokens := baseTokens(resource, prefix)
	inputs, _, err := schemaInputs(schema)
	if err != nil {
		return nil, err
	}
	if len(tokens) < 3 || strings.Join(tokens[len(tokens)-3:], "_") != "primary_mac_address" || !inputs["interface_id"] || !inputs["mac_address_id"] {
		return nil, nil
	}
	device := len(tokens) >= 2 && strings.Join(tokens[:2], "_") == "device_interface"
	virtual := len(tokens) >= 3 && strings.Join(tokens[:3], "_") == "virtual_machine_interface"
	if !device && !virtual {
		return nil, nil
	}
	expected := "virtualization"
	if device {
		expected = "dcim"
	}
	paths := pathMap(view)
	assignments := []any{}
	for _, parent := range parentCollections(view, "interface", apiPrefix) {
		if parent["surface"] != expected {
			continue
		}
		detail := str(parent["detail"])
		writes := []string{}
		if hasMethod(paths[detail], "patch") {
			writes = append(writes, "PATCH:"+detail)
		}
		if hasMethod(paths[detail], "put") {
			writes = append(writes, "PUT:"+detail)
		}
		meta, metaErr := topLevelMetadata(ctx, document, schema, []string{"GET:" + detail}, writes)
		if metaErr != nil {
			return nil, metaErr
		}
		if contains(anyStrings(meta["write_top_level_paths"]), "primary_mac_address") {
			assignments = append(assignments, Object{"mac_address_id_field": "mac_address_id", "parent_collection_path": parent["collection"], "parent_detail_path": detail, "parent_id_field": "interface_id", "surface": parent["surface"], "write_fields": []any{"primary_mac_address"}, "write_operations": stringsToAny(writes)})
		}
	}
	if len(assignments) == 0 {
		return nil, nil
	}
	parent := prefix + "_interface"
	if device {
		parent = prefix + "_device_interface"
	}
	return Object{"assignments": assignments, "candidates": []any{}, "canonical_child_resource": prefix + "_mac_address", "canonical_parent_resource": parent, "collection_path": nil, "detail_path": nil, "reason": "parent_field_assignment", "special_type": "derived_relationship", "status": "special", "surface": object(assignments[0])["surface"]}, nil
}
func aliasedAction(ctx context.Context, document openapiadapter.Document, view openapiadapter.LegacyMap, resource string, schema Object, apiPrefix string) (Object, error) {
	alias, ok := map[string]Object{"ztc_activation_status": {"read_operations": []string{"GET:/ecAdminActivateStatus"}, "surface": "ecAdminActivateStatus", "write_operations": []string{"PUT:/ecAdminActivateStatus/activate"}}}[resource]
	if !ok {
		return nil, nil
	}
	paths := pathMap(view)
	filterOps := func(values []string) []string {
		out := []string{}
		for _, op := range values {
			split := strings.SplitN(op, ":", 2)
			if len(split) == 2 && strings.HasPrefix(split[1], apiPrefix) && hasMethod(paths[split[1]], split[0]) {
				out = append(out, op)
			}
		}
		return out
	}
	read, write := filterOps(alias["read_operations"].([]string)), filterOps(alias["write_operations"].([]string))
	if len(read) == 0 && len(write) == 0 {
		return nil, nil
	}
	contract, err := topLevelMetadata(ctx, document, schema, read, write)
	if err != nil {
		return nil, err
	}
	return Object{"candidates": []any{}, "collection_path": nil, "detail_path": nil, "read_operations": stringsToAny(read), "reason": "provider_resource_maps_to_openapi_action", "special_type": "aliased_action", "static_contract": contract, "status": "special", "surface": alias["surface"], "write_operations": stringsToAny(write)}, nil
}

func openAPIProfile(view openapiadapter.LegacyMap, prefix string) Object {
	paths := []string{}
	first, collections := map[string]int{}, map[string]int{}
	for _, p := range view.Paths {
		if !strings.HasPrefix(p.Template, prefix) {
			continue
		}
		paths = append(paths, p.Template)
		concrete := []string{}
		for _, part := range openAPIPathParts(p.Template, prefix) {
			if !parameter(part) {
				concrete = append(concrete, canonicalSlug(part))
			}
		}
		if len(concrete) == 0 {
			continue
		}
		first[concrete[0]]++
		collections[concrete[len(concrete)-1]]++
	}
	top := func(values map[string]int) []any {
		keys := sortedKeysInt(values)
		sort.Slice(keys, func(i, j int) bool {
			return values[keys[i]] > values[keys[j]] || (values[keys[i]] == values[keys[j]] && canonjson.ComparePythonStrings(keys[i], keys[j]) < 0)
		})
		out := []any{}
		for _, key := range keys[:min(25, len(keys))] {
			out = append(out, Object{"paths": values[key], "segment": key})
		}
		return out
	}
	return Object{"path_count_for_api_prefix": len(paths), "servers": stringsToAny(view.ServerURLs), "title": pointerValue(view.Title), "top_collection_segments": top(collections), "top_first_segments": top(first)}
}
func providerHints(provider Object) []any {
	attrs := object(object(object(provider["provider"])["block"])["attributes"])
	out := []any{}
	for _, name := range sortedKeys(attrs) {
		if !surfaceHint.MatchString(name) {
			continue
		}
		meta := object(attrs[name])
		out = append(out, Object{"description": valueOrNil(meta, "description"), "name": name, "sensitive": boolean(meta["sensitive"])})
	}
	return out
}
func familyFor(resource, prefix string) string {
	tokens := baseTokens(resource, prefix)
	if len(tokens) == 0 {
		return "unknown"
	}
	return tokens[0]
}

// RoundPythonRatio4 ports roundPythonRatio4 from the original implementation.
func RoundPythonRatio4(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator)
	sign := 1.0
	if value < 0 {
		sign, value = -1, -value
	}
	rat := new(big.Rat).SetFloat64(value)
	rat.Mul(rat, big.NewRat(10000, 1))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(rat.Num(), rat.Denom(), r)
	twice := new(big.Int).Lsh(r, 1)
	if twice.Cmp(rat.Denom()) > 0 || (twice.Cmp(rat.Denom()) == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	out, _ := new(big.Rat).SetFrac(q, big.NewInt(10000)).Float64()
	return sign * out
}

func ratioNumber(numerator, denominator int) json.Number {
	value := RoundPythonRatio4(numerator, denominator)
	text := strconv.FormatFloat(value, 'f', 4, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return json.Number(text)
}
func coverageDiagnostics(summary Object, families map[string]map[string]int, profile Object, hints []any) Object {
	total := summary["resources"].(int)
	covered := summary["matched"].(int) + summary["special"].(int)
	ratio := float64(0)
	if total > 0 {
		ratio = float64(covered) / float64(total)
	}
	warnings := []any{}
	if profile["path_count_for_api_prefix"].(int) == 0 {
		warnings = append(warnings, Object{"code": "api_prefix_matches_no_paths", "message": "The selected API prefix matches zero OpenAPI paths. Check whether the spec stores the product base path in servers[] instead of paths[]."})
	}
	if total > 0 && ratio < .25 {
		warnings = append(warnings, Object{"code": "low_openapi_resource_coverage", "coverage_ratio": ratioNumber(covered, total), "message": "Fewer than 25% of Terraform resources mapped to this OpenAPI document. This often means the spec is the wrong product surface, only a partial surface, or the provider contains orchestration resources that do not map to CRUD collections."})
	}
	if total > 0 && len(hints) > 0 && ratio < .75 {
		names := []string{}
		for _, hint := range hints {
			names = append(names, object(hint)["name"].(string))
		}
		warnings = append(warnings, Object{"code": "provider_config_suggests_multiple_surfaces", "hint_attributes": stringsToAny(names), "message": "Provider configuration exposes URL/token/cloud-style knobs while OpenAPI coverage is incomplete. Classify resources by surface before field-level reconciliation."})
	}
	uncovered := []any{}
	for _, name := range sortedKeysFamilies(families) {
		counts := families[name]
		count := 0
		for _, v := range counts {
			count += v
		}
		if count > 0 && counts["matched"]+counts["special"] == 0 {
			uncovered = append(uncovered, Object{"family": name, "resources": count, "statuses": intMapObject(counts)})
		}
	}
	if len(uncovered) > 0 {
		warnings = append(warnings, Object{"code": "uncovered_resource_families", "families": uncovered[:min(50, len(uncovered))], "message": "At least one Terraform resource family had no mapped OpenAPI CRUD endpoint."})
	}
	family := Object{}
	for _, name := range sortedKeysFamilies(families) {
		family[name] = intMapObject(families[name])
	}
	return Object{"coverage_ratio": ratioNumber(covered, total), "covered_resources": covered, "family_coverage": family, "warnings": warnings}
}
func detectedProducts(view openapiadapter.LegacyMap) map[string]bool {
	text := strings.ToLower(strOr(pointerValue(view.Title), ""))
	for _, server := range view.ServerURLs {
		text += " " + strings.ToLower(server)
	}
	markers := map[string][]string{"zia": {"zia", "internet access"}, "zpa": {"zpa", "private access"}, "zcc": {"zcc", "client connector"}, "ztc": {"ztc", "ztw", "zcloudconnector", "cloud & branch connector", "cloud and branch connector"}}
	out := map[string]bool{}
	for product, values := range markers {
		for _, mark := range values {
			if strings.Contains(text, mark) {
				out[product] = true
				break
			}
		}
	}
	return out
}
func productMatches(view openapiadapter.LegacyMap, prefix string) bool {
	known := map[string]bool{"zia": true, "zpa": true, "zcc": true, "ztc": true}
	if !known[prefix] {
		return true
	}
	detected := detectedProducts(view)
	return len(detected) == 0 || detected[prefix]
}
func fetchVariants(path, product, prefix string) []Object {
	parts := canonicalParts(path)
	api := canonicalParts(prefix)
	out := []Object{}
	if len(parts) > 0 {
		out = append(out, Object{"parts": stringsToAny(parts), "variant": "exact"})
	}
	if len(api) > 0 && len(parts) >= len(api) && sameParts(parts[:len(api)], api) {
		out = append(out, Object{"parts": stringsToAny(parts[len(api):]), "variant": "api_prefix_stripped"})
	}
	base := append([]Object(nil), out...)
	for _, item := range base {
		candidate := anyStrings(item["parts"])
		if product != "" && len(candidate) > 0 && strings.EqualFold(candidate[0], product) {
			variant := item["variant"].(string)
			if variant == "exact" {
				variant = "product_prefix_stripped"
			} else {
				variant += "_product_prefix_stripped"
			}
			out = append(out, Object{"parts": stringsToAny(candidate[1:]), "variant": variant})
		}
	}
	seen := map[string]bool{}
	filtered := []Object{}
	for _, item := range out {
		parts := anyStrings(item["parts"])
		key := strings.Join(parts, "\x00") + "\x01" + item["variant"].(string)
		if len(parts) > 0 && !seen[key] {
			seen[key] = true
			filtered = append(filtered, item)
		}
	}
	return filtered
}
func matchRegistryPath(view openapiadapter.LegacyMap, prefix, fetch, product string) Object {
	matches := []Object{}
	for _, variant := range fetchVariants(fetch, product, prefix) {
		left := anyStrings(variant["parts"])
		for _, path := range view.Paths {
			if !strings.HasPrefix(path.Template, prefix) || !hasMethod(path, "get") {
				continue
			}
			right := canonicalParts(strings.TrimPrefix(path.Template, prefix))
			kind := ""
			if equalPath(left, right) {
				kind = "exact"
			} else if len(left) > 0 && len(right) >= len(left) && equalPath(right[len(right)-len(left):], left) {
				kind = "suffix"
			}
			if kind != "" {
				matches = append(matches, Object{"match": kind, "openapi_path": path.Template, "variant": variant["variant"]})
			}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	rank := map[string]int{"exact": 0, "suffix": 1}
	vrank := map[string]int{"exact": 0, "api_prefix_stripped": 1, "product_prefix_stripped": 2, "api_prefix_stripped_product_prefix_stripped": 3}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		return rank[str(a["match"])] < rank[str(b["match"])] || (rank[str(a["match"])] == rank[str(b["match"])] && (vrank[str(a["variant"])] < vrank[str(b["variant"])] || (vrank[str(a["variant"])] == vrank[str(b["variant"])] && canonjson.ComparePythonStrings(str(a["openapi_path"]), str(b["openapi_path"])) < 0)))
	})
	return matches[0]
}
func registryCoverage(view openapiadapter.LegacyMap, apiPrefix, prefix string, registry Object, key string) Object {
	resources := []any{}
	matches := productMatches(view, prefix)
	for _, resource := range sortedKeys(registry) {
		entry := object(registry[resource])
		product, productOK := entry["product"].(string)
		if !productOK || product != prefix {
			continue
		}
		if key == "read" && jsTruthy(entry["status"]) && entry["status"] != "mapped" {
			resources = append(resources, Object{"reason": nullish(entry, "reason", entry["status"]), "resource": resource, "status": entry["status"]})
			continue
		}
		pathEntry := object(entry[key])
		path, ok := pathEntry["path"].(string)
		if !ok {
			continue
		}
		item := Object{key + "_path": path, "resource": resource}
		if key == "fetch" {
			item["pagination"] = nullish(pathEntry, "pagination", prefix)
		}
		if jsTruthy(pathEntry["operation_id"]) {
			item["operation_id"] = pathEntry["operation_id"]
		}
		if jsTruthy(pathEntry["path_kind"]) {
			item["path_kind"] = pathEntry["path_kind"]
		}
		match := Object(nil)
		if matches {
			match = matchRegistryPath(view, apiPrefix, path, prefix)
		}
		if match == nil {
			reason := "fetch_path_not_found_in_openapi_get_paths"
			if !matches {
				reason = "openapi_product_mismatch"
			}
			item["reason"], item["status"] = reason, "unmatched"
		} else {
			merge(item, match)
			item["status"] = "matched"
		}
		resources = append(resources, item)
	}
	matched, ambiguous := 0, 0
	for _, v := range resources {
		status := str(object(v)["status"])
		if status == "matched" {
			matched++
		}
		if status == "ambiguous_source_operation" {
			ambiguous++
		}
	}
	total := len(resources)
	warnings := []any{}
	label := "registry_" + key
	if total > 0 && !matches {
		detected := []string{}
		for p := range detectedProducts(view) {
			detected = append(detected, p)
		}
		mismatchCode := label + "_openapi_product_mismatch"
		if key == "fetch" {
			mismatchCode = "registry_openapi_product_mismatch"
		}
		warnings = append(warnings, Object{"code": mismatchCode, "detected_products": stringsToAny(sorted(detected)), "message": "The OpenAPI document advertises a different known product than the resource prefix; registry path suffix matches were suppressed."})
	}
	nonMapped, missing := []string{}, []string{}
	for _, v := range resources {
		item := object(v)
		status := str(item["status"])
		if key == "read" && status != "matched" && status != "unmatched" {
			nonMapped = append(nonMapped, str(item["resource"]))
		}
		if status == "unmatched" {
			missing = append(missing, str(item["resource"]))
		}
	}
	if key == "read" && len(nonMapped) > 0 {
		warnings = append(warnings, Object{"code": "registry_read_entries_not_mapped", "message": "At least one source evidence entry did not produce a selected read path; inspect the source diagnostics before OpenAPI path matching.", "resources": stringsToAny(nonMapped[:min(50, len(nonMapped))])})
	}
	if len(missing) > 0 {
		warnings = append(warnings, Object{"code": label + "_paths_missing_from_openapi", "message": "At least one registry path was not present as an OpenAPI GET path.", "resources": stringsToAny(missing[:min(50, len(missing))])})
	}
	summary := Object{}
	if key == "fetch" {
		summary["fetch_resources"] = total
	} else {
		summary["read_resources"] = total
		summary["ambiguous"] = ambiguous
	}
	summary["matched"], summary["unmatched"] = matched, total-matched-ambiguous
	if total == 0 {
		summary["coverage_ratio"] = nil
	} else {
		summary["coverage_ratio"] = ratioNumber(matched, total)
	}
	return Object{"resources": resources, "summary": summary, "warnings": warnings}
}

func operationPath(value any) any {
	operation, ok := value.(string)
	if !ok {
		return nil
	}
	if index := strings.IndexByte(operation, ':'); index >= 0 {
		return operation[index+1:]
	}
	return operation
}
func surfaceRecord(resource Object, provider *string, prefix string) Object {
	status := str(resource["status"])
	candidates := valueOr(resource, "candidates", []any{})
	surface := valueOr(resource, "surface", prefix)
	if surface == nil {
		surface = prefix
	}
	if surface == "" {
		surface = nil
	}
	if status == "matched" {
		read := first(anyStrings(object(resource["static_contract"])["read_operations"]))
		return Object{"adapter_required": false, "ambiguity_reason": nil, "api_surface": surface, "confidence": resource["confidence"], "evidence": []any{Object{"collection_path": resource["collection_path"], "detail_path": resource["detail_path"], "kind": "generic_crud_candidate", "matched_segment": valueOrNil(resource, "matched_segment"), "score": resource["score"]}}, "match_status": "matched", "provider": pointerValue(provider), "read_operation": read, "read_path": operationPath(read), "resource_type": resource["resource"], "source": "generic_crud"}
	}
	if status == "ambiguous" {
		return Object{"adapter_required": false, "ambiguity_reason": resource["reason"], "api_surface": surface, "confidence": valueOrNil(resource, "confidence"), "evidence": []any{Object{"candidates": candidates, "kind": "generic_crud_candidates"}}, "match_status": "ambiguous", "provider": pointerValue(provider), "read_operation": nil, "read_path": nil, "resource_type": resource["resource"], "source": "generic_crud"}
	}
	if status == "special" {
		reads := anyStrings(resource["read_operations"])
		return Object{"adapter_required": true, "ambiguity_reason": resource["reason"], "api_surface": surface, "confidence": "static_adapter", "evidence": []any{Object{"actions": valueOr(resource, "actions", []any{}), "kind": "special_resource_match", "read_operations": stringsToAny(reads), "reason": resource["reason"], "special_type": resource["special_type"], "write_operations": valueOr(resource, "write_operations", []any{})}}, "match_status": "action_shaped", "provider": pointerValue(provider), "read_operation": first(reads), "read_path": operationPath(first(reads)), "resource_type": resource["resource"], "source": "generic_crud"}
	}
	adapter := resource["reason"] == "matched_collection_has_no_standard_detail_path"
	state := "missing"
	if adapter {
		state = "adapter_required"
	}
	return Object{"adapter_required": adapter, "ambiguity_reason": resource["reason"], "api_surface": surface, "confidence": valueOrNil(resource, "confidence"), "evidence": []any{Object{"candidates": candidates, "kind": "generic_crud_miss", "reason": resource["reason"]}}, "match_status": state, "provider": pointerValue(provider), "read_operation": nil, "read_path": nil, "resource_type": resource["resource"], "source": "generic_crud"}
}
func registrySurface(item Object, provider *string, prefix, key string) Object {
	matched := item["status"] == "matched"
	path := any(nil)
	if matched {
		path = nullish(item, "openapi_path", valueOrNil(item, "read_path"))
	}
	source := "registry_fetch"
	if key == "read" {
		source = "source_read_registry"
	}
	ambiguous := item["status"] == "ambiguous_source_operation"
	unsupported := key == "read" && item["status"] == "graphql_source"
	state := "missing"
	if matched {
		state = "matched"
	} else if ambiguous {
		state = "ambiguous"
	} else if unsupported {
		state = "unsupported_for_now"
	}
	evidence := Object{"kind": "registry_fetch_path", "match": nullish(item, "match", nil), "openapi_path": nullish(item, "openapi_path", nil), "reason": nullish(item, "reason", nil), "variant": nullish(item, "variant", nil)}
	if key == "fetch" {
		evidence["fetch_path"] = item["fetch_path"]
		evidence["pagination"] = item["pagination"]
	} else {
		evidence["kind"] = "source_read_registry"
		evidence["operation_id"] = nullish(item, "operation_id", nil)
		evidence["path_kind"] = nullish(item, "path_kind", nil)
		evidence["read_path"] = nullish(item, "read_path", nil)
	}
	confidence := any(nil)
	if matched {
		if key == "fetch" {
			confidence = "registry_fetch"
		} else {
			confidence = "source_read"
		}
	}
	readOperation := any(nil)
	if matched {
		readOperation = nullish(item, "operation_id", operationPath(path))
		if nullish(item, "operation_id", nil) == nil && path != nil {
			readOperation = "GET:" + str(path)
		}
	}
	return Object{"adapter_required": unsupported, "ambiguity_reason": func() any {
		if matched {
			return nil
		}
		return nullish(item, "reason", item["status"])
	}(), "api_surface": func() any {
		if prefix == "" {
			return nil
		}
		return prefix
	}(), "confidence": confidence, "evidence": []any{evidence}, "match_status": state, "provider": pointerValue(provider), "read_operation": readOperation, "read_path": path, "resource_type": item["resource"], "source": source}
}
func surfaceMap(provider *string, prefix string, resources []any, fetch, read Object, warnings []Object) Object {
	records := []Object{}
	for _, resource := range resources {
		records = append(records, surfaceRecord(object(resource), provider, prefix))
	}
	for _, item := range objects(fetch["resources"]) {
		records = append(records, registrySurface(item, provider, prefix, "fetch"))
	}
	for _, item := range objects(read["resources"]) {
		records = append(records, registrySurface(item, provider, prefix, "read"))
	}
	sort.Slice(records, func(i, j int) bool {
		return canonjson.ComparePythonStrings(recordKey(records[i]), recordKey(records[j])) < 0
	})
	bySource := map[string]map[string]int{}
	byStatus := map[string]int{}
	for _, record := range records {
		source, status := str(record["source"]), str(record["match_status"])
		if bySource[source] == nil {
			bySource[source] = map[string]int{}
		}
		bySource[source][status]++
		byStatus[status]++
	}
	diagnostics := []Object{}
	for _, warning := range warnings {
		diagnostics = append(diagnostics, Object{"code": warning["code"], "message": warning["message"], "source": "generic_crud"})
	}
	for _, pair := range []struct {
		source   string
		coverage Object
	}{{"registry_fetch", fetch}, {"source_read_registry", read}} {
		for _, warning := range objects(pair.coverage["warnings"]) {
			diagnostics = append(diagnostics, Object{"code": warning["code"], "message": warning["message"], "source": pair.source})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		return canonjson.ComparePythonStrings(str(diagnostics[i]["source"])+"\x00"+str(diagnostics[i]["code"]), str(diagnostics[j]["source"])+"\x00"+str(diagnostics[j]["code"])) < 0
	})
	resultRecords := make([]any, len(records))
	for i := range records {
		resultRecords[i] = records[i]
	}
	sourceObject := Object{}
	for _, source := range sortedKeysFamilies(bySource) {
		sourceObject[source] = intMapObject(bySource[source])
	}
	statusObject := intMapObject(byStatus)
	resultDiagnostics := make([]any, len(diagnostics))
	for i := range diagnostics {
		resultDiagnostics[i] = diagnostics[i]
	}
	return Object{"diagnostics": resultDiagnostics, "records": resultRecords, "schema_version": 1, "summary": Object{"by_source": sourceObject, "by_status": statusObject, "records": len(records)}}
}
func recordKey(v Object) string {
	return str(v["resource_type"]) + "\x00" + str(v["source"]) + "\x00" + str(v["match_status"]) + "\x00" + str(v["read_path"]) + "\x00" + str(v["read_operation"])
}

func object(value any) Object {
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return Object{}
}
func objects(value any) []Object {
	out := []Object{}
	if values, ok := value.([]any); ok {
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				out = append(out, item)
			}
		}
	}
	return out
}
func objectsFrom(values []Object) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}
func cloneObject(value Object) Object {
	if value == nil {
		return nil
	}
	output := make(Object, len(value))
	for key, child := range value {
		output[key] = cloneJSON(child)
	}
	return output
}
func cloneJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneObject(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneJSON(typed[i])
		}
		return out
	case []string:
		out := make([]string, len(typed))
		copy(out, typed)
		return out
	case json.Number:
		return json.Number(string(typed))
	default:
		return typed
	}
}
func sortedKeys(value map[string]any) []string {
	out := make([]string, 0, len(value))
	for key := range value {
		out = append(out, key)
	}
	return canonjson.SortedStrings(out)
}
func sortedKeysInt(value map[string]int) []string {
	out := make([]string, 0, len(value))
	for key := range value {
		out = append(out, key)
	}
	return canonjson.SortedStrings(out)
}
func sortedKeysFamilies(value map[string]map[string]int) []string {
	out := make([]string, 0, len(value))
	for key := range value {
		out = append(out, key)
	}
	return canonjson.SortedStrings(out)
}
func sorted(values []string) []string { return canonjson.SortedStrings(values) }
func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}
func anyStrings(value any) []string {
	if values, ok := value.([]any); ok {
		out := []string{}
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	return []string{}
}
func intMapObject(values map[string]int) Object {
	out := Object{}
	for _, key := range sortedKeysInt(values) {
		out[key] = values[key]
	}
	return out
}
func merge(left, right Object) {
	for key, value := range right {
		left[key] = value
	}
}
func str(value any) string { text, _ := value.(string); return text }
func strOr(value any, fallback string) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fallback
}
func pointerValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
func valueOr(value Object, key string, fallback any) any {
	if out, ok := value[key]; ok {
		return out
	}
	return fallback
}
func valueOrNil(value Object, key string) any {
	if out, ok := value[key]; ok {
		return out
	}
	return nil
}

// nullish ports JavaScript's `value ?? fallback`: absent and JSON null use the
// fallback, while false, zero, and empty strings remain present values.
func nullish(value Object, key string, fallback any) any {
	if out, ok := value[key]; ok && out != nil {
		return out
	}
	return fallback
}

// jsTruthy ports JavaScript truthiness for registry optional fields. Objects
// and arrays, including empty ones, remain truthy; JSON null is false.
func jsTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case json.Number:
		rat, ok := new(big.Rat).SetString(string(typed))
		return ok && rat.Sign() != 0
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case int32:
		return typed != 0
	default:
		return true
	}
}

func boolean(value any) bool { out, _ := value.(bool); return out }
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func equalPath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] && left[i] != "{}" && right[i] != "{}" {
			return false
		}
	}
	return true
}
func sameParts(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func filter(values []string, keep func(string) bool) []string {
	out := []string{}
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}
func first(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}
func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
