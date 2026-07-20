package providerprobe

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapimap"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceoperation"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const openAPIMapArtifactName = "openapi-map.json"

// runQualified composes only the manifest-bound source-first pipeline. It has
// no preparation directory and deliberately accepts no LegacyHost capability.
func runQualified(ctx context.Context, recipe loadedRecipe) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("qualified provider probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("qualified provider probe cancelled: %w", err)
	}
	if recipe.mode != QualifiedV2 || recipe.provenance == nil {
		return Result{}, fmt.Errorf("qualified provider probe requires source_provenance")
	}
	if err := rejectQualifiedLegacyFields(recipe); err != nil {
		return Result{}, err
	}
	roots, err := qualifiedRoots(recipe)
	if err != nil {
		return Result{}, err
	}

	verified, err := sourcebind.LoadVerified(ctx, roots)
	if err != nil {
		return Result{}, fmt.Errorf("load qualified source inputs: %w", err)
	}
	inputs, err := sourcebind.RequireQualification(verified)
	if err != nil {
		return Result{}, fmt.Errorf("qualify verified source inputs: %w", err)
	}
	evidence, err := sourceanalysis.Analyze(ctx, inputs)
	if err != nil {
		return Result{}, fmt.Errorf("analyze qualified source inputs: %w", err)
	}
	bundle, err := sourceoperation.CompileQualified(ctx, evidence, inputs)
	if err != nil {
		return Result{}, fmt.Errorf("compile qualified provider probe: %w", err)
	}

	artifacts := providerProbeArtifacts(bundle.Artifacts())
	mapArtifact, ok := qualifiedOpenAPIMap(ctx, recipe, evidence, inputs)
	if ok {
		artifacts = append(artifacts, mapArtifact)
	}
	return Result{mode: QualifiedV2, artifacts: artifacts}, nil
}

func qualifiedRoots(recipe loadedRecipe) (sourcebind.LocalRoots, error) {
	p := recipe.provenance
	if p == nil {
		return sourcebind.LocalRoots{}, fmt.Errorf("qualified provider probe requires source_provenance")
	}
	if p.manifest == "" {
		return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.manifest is required")
	}
	if p.providerRoot == "" {
		return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.provider_root is required")
	}
	if p.schemaRoot == "" {
		return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.schema_root is required")
	}
	roots := sourcebind.LocalRoots{
		ManifestPath: recipeLocalPath(recipe, p.manifest),
		ProviderRoot: recipeLocalPath(recipe, p.providerRoot),
		SchemaRoot:   recipeLocalPath(recipe, p.schemaRoot),
		SDKRoots:     make(map[string]string, len(p.sdkRoots)),
	}
	for module, root := range p.sdkRoots {
		if module == "" {
			return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.sdk_roots has an empty module key")
		}
		if root == "" {
			return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.sdk_roots.%s is required", module)
		}
		roots.SDKRoots[module] = recipeLocalPath(recipe, root)
	}
	if p.openAPIRoot != nil {
		if *p.openAPIRoot == "" {
			return sourcebind.LocalRoots{}, fmt.Errorf("recipe source_provenance.openapi_root must be non-empty when present")
		}
		roots.OpenAPIRoot = recipeLocalPath(recipe, *p.openAPIRoot)
	}
	return roots, nil
}

func recipeLocalPath(recipe loadedRecipe, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(recipe.directory, value)
}

// rejectQualifiedLegacyFields treats field presence, rather than truthiness,
// as an attempted legacy preparation control. Empty aliases are therefore
// rejected before any source loader or legacy host can be reached.
func rejectQualifiedLegacyFields(recipe loadedRecipe) error {
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"openapi.path", recipe.openAPI.path},
		{"openapi.url", recipe.openAPI.url},
		{"openapi.format", recipe.openAPI.format},
		{"source.path", recipe.source.path},
		{"source.git", recipe.source.git},
		{"source.ref", recipe.source.ref},
		{"source.subdir", recipe.source.subdir},
		{"terraform_schema.path", recipe.schema.path},
		{"terraform_provider.source", recipe.terraform.source},
		{"terraform_provider.version", recipe.terraform.version},
		{"terraform_provider.local_name", recipe.terraform.localName},
		{"tools.terraform", recipe.tools.terraform},
	} {
		if field.value != nil {
			return fmt.Errorf("qualified provider probe forbids legacy field %s", field.name)
		}
	}
	for _, section := range []struct {
		name    string
		present bool
	}{
		{"openapi", recipe.openAPIPresent},
		{"source", recipe.sourcePresent},
		{"terraform_schema", recipe.schemaPresent},
		{"terraform_provider", recipe.terraformPresent},
		{"tools", recipe.toolsPresent},
	} {
		if section.present {
			return fmt.Errorf("qualified provider probe forbids legacy section %s", section.name)
		}
	}
	return nil
}

// qualifiedOpenAPIMap is deliberately best-effort. CompileQualified has
// already produced the sealed core bundle before this helper runs, so an
// adapter, schema, generic-map, render, or late-cancellation failure can only
// omit this diagnostic artifact; it cannot suppress source-first output.
func qualifiedOpenAPIMap(ctx context.Context, recipe loadedRecipe, evidence sourceanalysis.QualifiedEvidence, inputs sourcebind.QualifiedInputs) (Artifact, bool) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, false
	}
	snapshot, err := inputs.Snapshot()
	if err != nil {
		return Artifact{}, false
	}
	report, err := evidence.Snapshot()
	if err != nil {
		return Artifact{}, false
	}
	adapter, err := openapiadapter.Analyze(ctx, snapshot.OpenAPI, report)
	if err != nil {
		return Artifact{}, false
	}
	document, usable := adapter.Document()
	if !usable {
		return Artifact{}, false
	}
	schemaValue, err := canonjson.Decode(snapshot.TerraformSchema.Bytes)
	if err != nil {
		return Artifact{}, false
	}
	schema, ok := schemaValue.(map[string]any)
	if !ok {
		return Artifact{}, false
	}
	mapReport, err := openapimap.Build(ctx, openapimap.Options{
		SchemaData:     schema,
		Document:       document,
		ProviderSource: cloneOptionalString(recipe.provider),
		ResourcePrefix: stringOr(recipe.resource, ""),
		APIPrefix:      cloneOptionalString(recipe.api),
	})
	if err != nil {
		return Artifact{}, false
	}
	bytes, err := mapReport.Render()
	if err != nil {
		return Artifact{}, false
	}
	return Artifact{Name: openAPIMapArtifactName, Bytes: append([]byte(nil), bytes...)}, true
}

func providerProbeArtifacts(bundle []sourceoperation.Artifact) []Artifact {
	artifacts := make([]Artifact, len(bundle))
	for i, artifact := range bundle {
		artifacts[i] = Artifact{Name: artifact.Name, Bytes: append([]byte(nil), artifact.Bytes...)}
	}
	return artifacts
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
