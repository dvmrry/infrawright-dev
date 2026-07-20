package openapiadapter

import (
	"context"
	"fmt"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// LegacyMap is the detached, deliberately narrow view used by
// node-src/authoring/openapi-resource-map.ts. It does not expose the parsed
// document graph, references, files, readers, or arbitrary OpenAPI objects.
type LegacyMap struct {
	Version              *string
	Title                *string
	ServerURLs           []string
	Paths                []LegacyPath
	ComponentSchemaCount int
}

// LegacyPath is one deterministic legacy OpenAPI path/method inventory row
// used by node-src/authoring/openapi-resource-map.ts.
type LegacyPath struct {
	Template string
	Methods  []string
}

// LegacyMap returns the narrow detached legacy matcher view used by
// node-src/authoring/openapi-resource-map.ts. It is available on documents
// from ParseForMetadata so legacy reports may omit top-level info.
func (d Document) LegacyMap(ctx context.Context) (LegacyMap, error) {
	if err := ctx.Err(); err != nil {
		return LegacyMap{}, fmt.Errorf("legacy OpenAPI map cancelled: %w", err)
	}
	root, ok := d.files[d.root].(map[string]any)
	if !ok {
		return LegacyMap{}, fmt.Errorf("legacy OpenAPI root must be object")
	}
	result := LegacyMap{}
	if value, ok := root["openapi"].(string); ok {
		result.Version = legacyStringPointer(value)
	} else if value, ok := root["swagger"].(string); ok {
		result.Version = legacyStringPointer(value)
	}
	if info, ok := root["info"].(map[string]any); ok {
		if title, ok := info["title"].(string); ok {
			result.Title = legacyStringPointer(title)
		}
	}
	if servers, ok := root["servers"].([]any); ok {
		for _, raw := range servers {
			if server, ok := raw.(map[string]any); ok {
				if url, ok := server["url"].(string); ok {
					result.ServerURLs = append(result.ServerURLs, url)
				}
			}
		}
	}
	paths, ok := root["paths"].(map[string]any)
	if !ok {
		paths = map[string]any{}
	}
	for _, template := range canonjson.SortedStrings(mapKeys(paths)) {
		if err := ctx.Err(); err != nil {
			return LegacyMap{}, fmt.Errorf("legacy OpenAPI map cancelled: %w", err)
		}
		item, _ := paths[template].(map[string]any)
		methods := make([]string, 0, 5)
		for _, method := range []string{"delete", "get", "patch", "post", "put"} {
			if _, present := item[method]; present {
				methods = append(methods, method)
			}
		}
		result.Paths = append(result.Paths, LegacyPath{Template: template, Methods: methods})
	}
	if components, ok := root["components"].(map[string]any); ok {
		if schemas, ok := components["schemas"].(map[string]any); ok {
			result.ComponentSchemaCount = len(schemas)
		}
	}
	return cloneLegacyMap(result), nil
}

func cloneLegacyMap(value LegacyMap) LegacyMap {
	output := LegacyMap{ComponentSchemaCount: value.ComponentSchemaCount}
	if value.Version != nil {
		output.Version = legacyStringPointer(*value.Version)
	}
	if value.Title != nil {
		output.Title = legacyStringPointer(*value.Title)
	}
	output.ServerURLs = append([]string(nil), value.ServerURLs...)
	output.Paths = make([]LegacyPath, len(value.Paths))
	for index, path := range value.Paths {
		output.Paths[index] = LegacyPath{Template: path.Template, Methods: append([]string(nil), path.Methods...)}
	}
	return output
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func legacyStringPointer(value string) *string { return &value }
