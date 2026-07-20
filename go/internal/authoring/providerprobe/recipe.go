package providerprobe

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// loadedRecipe is the categorical decoded recipe shared by the v1 and v2
// runners.  Its fields deliberately retain empty strings: v1 uses JavaScript
// falsey selection, while v2 applies its own local-only policy.
type loadedRecipe struct {
	mode             Mode
	path             string
	directory        string
	name             *string
	provider         *string
	version          *string
	resource         *string
	api              *string
	openAPI          recipeOpenAPI
	openAPIPresent   bool
	source           recipeSource
	sourcePresent    bool
	schema           recipeSchema
	schemaPresent    bool
	terraform        recipeTerraformProvider
	terraformPresent bool
	tools            recipeTools
	toolsPresent     bool
	provenance       *sourceProvenance
}

type recipeOpenAPI struct{ path, url, format *string }
type recipeSource struct{ path, git, ref, subdir *string }
type recipeSchema struct{ path *string }
type recipeTerraformProvider struct{ source, version, localName *string }
type recipeTools struct{ terraform *string }

// sourceProvenance is the recipe representation of sourcebind.LocalRoots.
// The v2 runner validates the local-only semantic constraints; decoding here
// only establishes the shape and preserves the recipe-relative locations.
type sourceProvenance struct {
	manifest     string
	providerRoot string
	schemaRoot   string
	openAPIRoot  *string
	sdkRoots     map[string]string
}

func loadRecipe(path string) (loadedRecipe, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return loadedRecipe{}, fmt.Errorf("resolve recipe path: %w", err)
	}
	bytes, err := os.ReadFile(abs)
	if err != nil {
		return loadedRecipe{}, fmt.Errorf("read recipe: %w", err)
	}
	value, err := canonjson.Decode(bytes)
	if err != nil {
		return loadedRecipe{}, fmt.Errorf("parse recipe: %w", err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return loadedRecipe{}, fmt.Errorf("recipe root must be an object")
	}

	r := loadedRecipe{mode: LegacyV1, path: abs, directory: filepath.Dir(abs)}
	// Keep this phase order aligned with validateProviderProbeRecipe in the
	// frozen Node v1 authority. In particular, all root scalars precede every
	// section shape check, and all section shapes precede any nested field.
	if r.name, err = recipeString(root, "name", "name"); err != nil {
		return loadedRecipe{}, err
	}
	if r.provider, err = recipeString(root, "provider_source", "provider_source"); err != nil {
		return loadedRecipe{}, err
	}
	if r.version, err = recipeString(root, "provider_version", "provider_version"); err != nil {
		return loadedRecipe{}, err
	}
	if r.resource, err = recipeString(root, "resource_prefix", "resource_prefix"); err != nil {
		return loadedRecipe{}, err
	}
	if r.api, err = recipeString(root, "api_prefix", "api_prefix"); err != nil {
		return loadedRecipe{}, err
	}
	openAPIObject, err := recipeObject(root, "openapi")
	if err != nil {
		return loadedRecipe{}, err
	}
	sourceObject, err := recipeObject(root, "source")
	if err != nil {
		return loadedRecipe{}, err
	}
	schemaObject, err := recipeObject(root, "terraform_schema")
	if err != nil {
		return loadedRecipe{}, err
	}
	terraformObject, err := recipeObject(root, "terraform_provider")
	if err != nil {
		return loadedRecipe{}, err
	}
	toolsObject, err := recipeObject(root, "tools")
	if err != nil {
		return loadedRecipe{}, err
	}
	qualifiedRaw, qualified := root["source_provenance"]
	qualified = qualified && qualifiedRaw != nil
	r.openAPIPresent = recipeSectionPresent(root, "openapi")
	if r.openAPI, err = decodeOpenAPI(openAPIObject); err != nil {
		return loadedRecipe{}, err
	}
	if !qualified && falsey(r.openAPI.path) && falsey(r.openAPI.url) {
		return loadedRecipe{}, fmt.Errorf("recipe openapi must include path or url")
	}
	r.sourcePresent = recipeSectionPresent(root, "source")
	r.source, err = decodeSource(sourceObject)
	if err != nil {
		return loadedRecipe{}, err
	}
	if !qualified && falsey(r.source.path) && falsey(r.source.git) {
		return loadedRecipe{}, fmt.Errorf("recipe source must include path or git")
	}
	if !qualified && !falsey(r.source.git) && falsey(r.source.ref) {
		return loadedRecipe{}, fmt.Errorf("recipe source.ref is required when source.git is used")
	}
	r.schemaPresent = recipeSectionPresent(root, "terraform_schema")
	r.schema, err = decodeSchema(schemaObject)
	if err != nil {
		return loadedRecipe{}, err
	}
	r.terraformPresent = recipeSectionPresent(root, "terraform_provider")
	r.terraform, err = decodeTerraformProvider(terraformObject)
	if err != nil {
		return loadedRecipe{}, err
	}
	r.toolsPresent = recipeSectionPresent(root, "tools")
	r.tools, err = decodeTools(toolsObject)
	if err != nil {
		return loadedRecipe{}, err
	}

	if qualified {
		p, decodeErr := decodeSourceProvenance(qualifiedRaw)
		if decodeErr != nil {
			return loadedRecipe{}, decodeErr
		}
		r.mode, r.provenance = QualifiedV2, &p
		return r, nil
	}
	if err := validateLegacySchemaFallback(r); err != nil {
		return loadedRecipe{}, err
	}
	return r, nil
}

func recipeObject(root map[string]any, key string) (map[string]any, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return map[string]any{}, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("recipe %s must be an object", key)
	}
	return o, nil
}

func recipeSectionPresent(root map[string]any, key string) bool {
	_, present := root[key]
	return present
}

func recipeString(object map[string]any, key, label string) (*string, error) {
	v, ok := object[key]
	if !ok || v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("recipe %s must be a string", label)
	}
	return &s, nil
}

func decodeOpenAPI(object map[string]any) (recipeOpenAPI, error) {
	p, err := recipeString(object, "path", "openapi.path")
	if err != nil {
		return recipeOpenAPI{}, err
	}
	u, err := recipeString(object, "url", "openapi.url")
	if err != nil {
		return recipeOpenAPI{}, err
	}
	f, err := recipeString(object, "format", "openapi.format")
	if err != nil {
		return recipeOpenAPI{}, err
	}
	return recipeOpenAPI{p, u, f}, nil
}
func decodeSource(object map[string]any) (recipeSource, error) {
	p, err := recipeString(object, "path", "source.path")
	if err != nil {
		return recipeSource{}, err
	}
	g, err := recipeString(object, "git", "source.git")
	if err != nil {
		return recipeSource{}, err
	}
	r, err := recipeString(object, "ref", "source.ref")
	if err != nil {
		return recipeSource{}, err
	}
	s, err := recipeString(object, "subdir", "source.subdir")
	if err != nil {
		return recipeSource{}, err
	}
	return recipeSource{p, g, r, s}, nil
}
func decodeSchema(object map[string]any) (recipeSchema, error) {
	p, err := recipeString(object, "path", "terraform_schema.path")
	return recipeSchema{p}, err
}
func decodeTerraformProvider(object map[string]any) (recipeTerraformProvider, error) {
	s, err := recipeString(object, "source", "terraform_provider.source")
	if err != nil {
		return recipeTerraformProvider{}, err
	}
	v, err := recipeString(object, "version", "terraform_provider.version")
	if err != nil {
		return recipeTerraformProvider{}, err
	}
	l, err := recipeString(object, "local_name", "terraform_provider.local_name")
	if err != nil {
		return recipeTerraformProvider{}, err
	}
	return recipeTerraformProvider{s, v, l}, nil
}
func decodeTools(object map[string]any) (recipeTools, error) {
	t, err := recipeString(object, "terraform", "tools.terraform")
	return recipeTools{t}, err
}

func decodeSourceProvenance(value any) (sourceProvenance, error) {
	o, ok := value.(map[string]any)
	if !ok {
		return sourceProvenance{}, fmt.Errorf("recipe source_provenance must be an object")
	}
	stringRequired := func(key string) (string, error) {
		v, err := recipeString(o, key, "source_provenance."+key)
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", nil
		}
		return *v, nil
	}
	p := sourceProvenance{}
	var err error
	if p.manifest, err = stringRequired("manifest"); err != nil {
		return sourceProvenance{}, err
	}
	if p.providerRoot, err = stringRequired("provider_root"); err != nil {
		return sourceProvenance{}, err
	}
	if p.schemaRoot, err = stringRequired("schema_root"); err != nil {
		return sourceProvenance{}, err
	}
	if p.openAPIRoot, err = recipeString(o, "openapi_root", "source_provenance.openapi_root"); err != nil {
		return sourceProvenance{}, err
	}
	v, exists := o["sdk_roots"]
	if exists && v != nil {
		raw, ok := v.(map[string]any)
		if !ok {
			return sourceProvenance{}, fmt.Errorf("recipe source_provenance.sdk_roots must be an object")
		}
		p.sdkRoots = make(map[string]string, len(raw))
		for key, item := range raw {
			s, ok := item.(string)
			if !ok {
				return sourceProvenance{}, fmt.Errorf("recipe source_provenance.sdk_roots.%s must be a string", key)
			}
			p.sdkRoots[key] = s
		}
	}
	return p, nil
}

func validateLegacySchemaFallback(r loadedRecipe) error {
	if falsey(r.schema.path) {
		if falsey(r.provider) {
			return fmt.Errorf("recipe provider_source is required when terraform_schema.path is omitted")
		}
		if falsey(r.version) && falsey(r.terraform.version) {
			return fmt.Errorf("recipe provider_version or terraform_provider.version is required when terraform_schema.path is omitted")
		}
	}
	return nil
}

func falsey(value *string) bool { return value == nil || *value == "" }

// nullishStringOr mirrors JavaScript's nullish coalescing: an explicit empty
// string is data, while an absent or null field takes the fallback.
func nullishStringOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func stringOr(value *string, fallback string) string {
	if falsey(value) {
		return fallback
	}
	return *value
}
func recipePath(recipe loadedRecipe, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(recipe.directory, value)
}
