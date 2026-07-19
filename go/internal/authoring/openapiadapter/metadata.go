package openapiadapter

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/reconcile"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// ParseForMetadata parses only the selected captured input needed by the
// retained metadata helper. Unlike Analyze, it deliberately does not require
// top-level info or full-document OpenAPI validation.
func ParseForMetadata(ctx context.Context, status sourcebind.OpenAPIStatus) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, fmt.Errorf("metadata parsing cancelled: %w", err)
	}
	if !status.Available || status.Err != nil {
		return Document{}, fmt.Errorf("OpenAPI metadata input is unavailable")
	}
	files, root, err := captureFiles(status.Files)
	if err != nil {
		return Document{}, fmt.Errorf("capture OpenAPI metadata: %w", err)
	}
	rootValue, err := parseDocument(files[root])
	if err != nil {
		return Document{}, fmt.Errorf("parse OpenAPI metadata: %w", err)
	}
	rootGraph, ok := rootValue.(map[string]any)
	if !ok {
		return Document{}, fmt.Errorf("OpenAPI metadata root must be an object")
	}
	document := Document{root: root, raw: cloneBytes(files), files: map[string]any{root: cloneValue(rootGraph)}, metadataOnly: true}
	if stringValue(rootGraph["swagger"]) != "2.0" && !supportedOpenAPI.MatchString(stringValue(rootGraph["openapi"])) {
		return Document{}, fmt.Errorf("OpenAPI metadata root must declare OpenAPI 3 or Swagger 2")
	}
	return document, nil
}

func inventoryAll(ctx context.Context, document Document) ([]Operation, error) {
	root, _ := document.files[document.root].(map[string]any)
	paths, ok := root["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI metadata paths must be object")
	}
	operations := []Operation{}
	for _, template := range sortedKeys(paths) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, _, err := resolvePathItem(document, document.root, paths[template], map[string]bool{}, 0)
		if err != nil {
			return nil, err
		}
		for method := range operationMethods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			operation, ok := raw.(map[string]any)
			if _, present := operation["$ref"]; !ok || present {
				return nil, fmt.Errorf("operation must be an object without $ref")
			}
			candidate := Operation{Method: strings.ToUpper(method), PathTemplate: template}
			if id := stringValue(operation["operationId"]); id != "" {
				candidate.OperationID = &id
			}
			operations = append(operations, candidate)
		}
	}
	sortOperations(operations)
	return operations, nil
}

func metadata(ctx context.Context, document Document, options MetadataOptions) (reconcile.APIMetadata, error) {
	fields := reconcile.APIMetadata{}
	for _, reference := range options.ReadOperations {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("metadata extraction cancelled: %w", err)
		}
		operation, err := exactOperation(document, reference)
		if err != nil {
			return nil, err
		}
		schema, err := responseSchema(document, operation)
		if err != nil {
			return nil, err
		}
		if schema != nil {
			if err := flatten(ctx, document, schema, fields, "read", "", 0); err != nil {
				return nil, err
			}
		}
	}
	for _, reference := range options.WriteOperations {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("metadata extraction cancelled: %w", err)
		}
		operation, err := exactOperation(document, reference)
		if err != nil {
			return nil, err
		}
		schema, err := requestSchema(document, operation)
		if err != nil {
			return nil, err
		}
		if schema != nil {
			if err := flatten(ctx, document, schema, fields, "write", "", 0); err != nil {
				return nil, err
			}
		}
	}
	if len(options.WriteOperations) != 0 {
		for _, item := range fields {
			readable, _ := item["readable"].(bool)
			writable, _ := item["writable"].(bool)
			readOnly, _ := item["read_only"].(bool)
			if readable && !writable && !readOnly {
				item["response_only"] = true
			}
		}
	}
	return cloneMetadata(fields), nil
}

func exactOperation(document Document, ref OperationReference) (map[string]any, error) {
	raw := string(ref)
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return nil, fmt.Errorf("OpenAPI operation must be METHOD:/path, got %q", raw)
	}
	method, template := strings.ToUpper(raw[:colon]), raw[colon+1:]
	if _, ok := operationMethods[strings.ToLower(method)]; !ok {
		return nil, fmt.Errorf("OpenAPI operation %q has unsupported method", raw)
	}
	root, _ := document.files[document.root].(map[string]any)
	paths, _ := root["paths"].(map[string]any)
	item, ok := paths[template]
	if !ok {
		return nil, fmt.Errorf("OpenAPI operation %s not found", raw)
	}
	pathItem, ok := item.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI operation %s not found", raw)
	}
	operation, ok := pathItem[strings.ToLower(method)]
	if !ok {
		return nil, fmt.Errorf("OpenAPI operation %s not found", raw)
	}
	value, ok := operation.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI operation %s not found", raw)
	}
	if _, present := value["$ref"]; present {
		return nil, fmt.Errorf("OpenAPI operation %s must not use $ref", raw)
	}
	return value, nil
}

// resolveReferenceTarget follows a response/requestBody/parameter reference
// with Node's replacement semantics: sibling fields do not modify its target.
func resolveReferenceTarget(document Document, current string, raw any) (map[string]any, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI reference target must be object")
	}
	value, present := object["$ref"]
	if !present {
		return object, nil
	}
	ref, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("OpenAPI reference must be nonempty string")
	}
	file, target, _, err := resolveRef(document, current, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve OpenAPI reference: %w", err)
	}
	_ = file
	resolved, ok := target.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI reference target must be object")
	}
	return resolved, nil
}

func responseSchema(document Document, operation map[string]any) (any, error) {
	responses, _ := operation["responses"].(map[string]any)
	if responses == nil {
		return nil, nil
	}
	raw, ok := responses["200"]
	if !ok {
		keys := sortedKeys(responses)
		for _, key := range keys {
			if strings.HasPrefix(key, "2") {
				raw = responses[key]
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, nil
	}
	if _, ok := raw.(map[string]any); !ok {
		return nil, nil
	}
	response, err := resolveReferenceTarget(document, document.root, raw)
	if err != nil {
		return nil, err
	}
	if content, exists := response["content"]; exists {
		return contentSchema(content), nil
	}
	return response["schema"], nil
}
func requestSchema(document Document, operation map[string]any) (any, error) {
	if body, exists := operation["requestBody"]; exists {
		if _, ok := body.(map[string]any); !ok {
			goto parameters
		}
		resolved, err := resolveReferenceTarget(document, document.root, body)
		if err != nil {
			return nil, err
		}
		if schema := contentSchema(resolved["content"]); schema != nil {
			return schema, nil
		}
	}
parameters:
	parameters, _ := operation["parameters"].([]any)
	for _, raw := range parameters {
		if _, ok := raw.(map[string]any); !ok {
			continue
		}
		parameter, err := resolveReferenceTarget(document, document.root, raw)
		if err != nil {
			return nil, err
		}
		if parameter["in"] == "body" {
			return parameter["schema"], nil
		}
	}
	return nil, nil
}
func contentSchema(raw any) any {
	content, _ := raw.(map[string]any)
	if content == nil {
		return nil
	}
	if preferred, exists := content["application/json"]; exists {
		media, ok := preferred.(map[string]any)
		if !ok {
			return nil
		}
		return media["schema"]
	}
	for _, name := range sortedKeys(content) {
		if media, ok := content[name].(map[string]any); ok {
			if schema, exists := media["schema"]; exists {
				return schema
			}
		}
	}
	return nil
}

func flatten(ctx context.Context, document Document, raw any, fields reconcile.APIMetadata, mode, prefix string, depth int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flatten OpenAPI schema cancelled: %w", err)
	}
	if depth > 8 {
		return nil
	}
	schema, err := mergeSchema(document, raw, map[string]bool{}, 0)
	if err != nil {
		return fmt.Errorf("merge OpenAPI schema: %w", err)
	}
	kind := schemaKind(schema)
	if kind == "array" {
		if prefix == "" {
			return flatten(ctx, document, schema["items"], fields, mode, "", depth+1)
		}
		return flatten(ctx, document, schema["items"], fields, mode, appendPath(prefix, "[]"), depth+1)
	}
	if kind != "object" {
		if prefix != "" {
			record(fields, prefix, schema, mode, false)
		}
		return nil
	}
	required := map[string]bool{}
	if values, ok := schema["required"].([]any); ok {
		for _, rawName := range values {
			if name, ok := rawName.(string); ok {
				required[name] = true
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, rawName := range sortedKeys(properties) {
		property, err := mergeSchema(document, properties[rawName], map[string]bool{}, 0)
		if err != nil {
			return fmt.Errorf("merge OpenAPI property %q: %w", rawName, err)
		}
		name, fieldPath := transform.SnakeName(rawName), appendPath(prefix, transform.SnakeName(rawName))
		record(fields, fieldPath, property, mode, required[rawName] || required[name])
		switch schemaKind(property) {
		case "object":
			if _, ok := property["properties"].(map[string]any); ok {
				if err := flatten(ctx, document, property, fields, mode, fieldPath, depth+1); err != nil {
					return fmt.Errorf("flatten OpenAPI object %q: %w", rawName, err)
				}
			}
		case "array":
			items, err := mergeSchema(document, property["items"], map[string]bool{}, 0)
			if err != nil {
				return fmt.Errorf("merge OpenAPI array %q: %w", rawName, err)
			}
			if schemaKind(items) == "object" {
				if err := flatten(ctx, document, items, fields, mode, appendPath(fieldPath, "[]"), depth+1); err != nil {
					return fmt.Errorf("flatten OpenAPI array %q: %w", rawName, err)
				}
			}
		}
	}
	return nil
}

func mergeSchema(document Document, raw any, active map[string]bool, depth int) (map[string]any, error) {
	if depth > maxRefDepth {
		return nil, fmt.Errorf("schema recursion limit exceeded")
	}
	input, ok := raw.(map[string]any)
	if !ok && raw != nil {
		return nil, fmt.Errorf("OpenAPI schema must be an object")
	}
	if !ok {
		return map[string]any{}, nil
	}
	input = cloneValue(input).(map[string]any)
	if ref, ok := input["$ref"].(string); ok {
		file, target, key, err := resolveRef(document, document.root, ref)
		if err != nil {
			return nil, err
		}
		if active[key] {
			return nil, fmt.Errorf("recursive OpenAPI ref")
		}
		active[key] = true
		resolved, err := mergeSchemaAt(document, file, target, active, depth+1)
		delete(active, key)
		if err != nil {
			return nil, err
		}
		for key, value := range input {
			if key != "$ref" {
				resolved[key] = value
			}
		}
		input = resolved
	}
	parts, _ := input["allOf"].([]any)
	delete(input, "allOf")
	if len(parts) == 0 {
		return input, nil
	}
	merged, properties := map[string]any{}, map[string]any{}
	required := []string{}
	for _, part := range parts {
		resolved, err := mergeSchema(document, part, active, depth+1)
		if err != nil {
			return nil, err
		}
		for key, value := range resolved {
			switch key {
			case "properties":
				if object, ok := value.(map[string]any); ok {
					for property, field := range object {
						properties[property] = field
					}
				}
			case "required":
				if list, ok := value.([]any); ok {
					for _, item := range list {
						if name, ok := item.(string); ok {
							required = append(required, name)
						}
					}
				}
			default:
				if _, exists := merged[key]; !exists {
					merged[key] = value
				}
			}
		}
	}
	for key, value := range input {
		merged[key] = value
	}
	if own, ok := input["properties"].(map[string]any); ok {
		for key, value := range own {
			properties[key] = value
		}
	}
	if len(properties) != 0 {
		merged["properties"] = properties
	}
	if list, ok := input["required"].([]any); ok {
		for _, item := range list {
			if name, ok := item.(string); ok {
				required = append(required, name)
			}
		}
	}
	if len(required) != 0 {
		sort.Strings(required)
		unique := required[:0]
		for _, name := range required {
			if len(unique) == 0 || unique[len(unique)-1] != name {
				unique = append(unique, name)
			}
		}
		values := make([]any, len(unique))
		for i := range unique {
			values[i] = unique[i]
		}
		merged["required"] = values
	}
	return merged, nil
}
func mergeSchemaAt(document Document, file string, raw any, active map[string]bool, depth int) (map[string]any, error) {
	original := document.root
	document.root = file
	result, err := mergeSchema(document, raw, active, depth)
	document.root = original
	return result, err
}
func schemaKind(schema map[string]any) string {
	if value := stringValue(schema["type"]); value != "" {
		return value
	}
	if _, ok := schema["properties"].(map[string]any); ok {
		return "object"
	}
	if value, ok := schema["additionalProperties"].(map[string]any); ok && value != nil {
		return "object"
	}
	if value, ok := schema["additionalProperties"].(bool); ok && value {
		return "object"
	}
	return ""
}
func appendPath(prefix, name string) string {
	if prefix == "" {
		return strings.TrimPrefix(name, ".")
	}
	if name == "[]" {
		return prefix + "[]"
	}
	return prefix + "." + name
}
func record(fields reconcile.APIMetadata, path string, schema map[string]any, mode string, required bool) {
	item, ok := fields[path]
	if !ok {
		item = reconcile.Object{"path": path}
		fields[path] = item
	}
	if mode == "read" {
		item["readable"] = true
	} else {
		item["writable"] = true
		if required {
			item["required"] = true
		}
	}
	if value, ok := schema["readOnly"].(bool); ok && value {
		item["read_only"] = true
	}
	if value, ok := schema["writeOnly"].(bool); ok && value {
		item["write_only"] = true
	}
	if kind := schemaKind(schema); kind != "" {
		types, _ := item["schema_types"].([]any)
		found := false
		for _, value := range types {
			if value == kind {
				found = true
			}
		}
		if !found {
			item["schema_types"] = append(types, kind)
		}
	}
}
func cloneMetadata(value reconcile.APIMetadata) reconcile.APIMetadata {
	result := reconcile.APIMetadata{}
	for key, item := range value {
		result[key] = cloneValue(item).(map[string]any)
	}
	return result
}
