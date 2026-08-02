package modulesgen

import (
	"fmt"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

type dataModuleContext struct {
	ResourceType   string
	Provider       string
	ProviderSource string
	ProviderPin    string
	NameField      string
}

func buildDataModuleContext(root metadata.LoadedPackRoot, resourceType string) (dataModuleContext, error) {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return dataModuleContext{}, fmt.Errorf("unknown active resource type %s", jsonQuote(resourceType))
	}
	dataReferent, _ := resource.Registry["data_referent"].(bool)
	if !dataReferent {
		return dataModuleContext{}, fmt.Errorf("resource type %s is not a data referent", jsonQuote(resourceType))
	}

	manifest, err := metadata.ManifestForProvider(root.Packs, resource.Provider)
	if err != nil {
		return dataModuleContext{}, err
	}
	lookupSources, ok := manifest.Data["lookup_sources"].(metadata.JsonObject)
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s has no lookup_sources.%s.name_field",
			jsonQuote(resourceType), resourceType,
		)
	}
	rawLookup, ok := lookupSources[resourceType]
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s has no lookup_sources.%s.name_field",
			jsonQuote(resourceType), resourceType,
		)
	}
	lookup, ok := rawLookup.(metadata.JsonObject)
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"lookup_sources.%s for data referent %s must be an object",
			resourceType, jsonQuote(resourceType),
		)
	}
	nameField, ok := lookup["name_field"].(string)
	if !ok || nameField == "" {
		return dataModuleContext{}, fmt.Errorf(
			"lookup_sources.%s.name_field for data referent %s must be a non-empty string",
			resourceType, jsonQuote(resourceType),
		)
	}

	providerSchema, err := root.LoadProviderSchema(resource.Provider)
	if err != nil {
		return dataModuleContext{}, err
	}
	dataSourceSchemas, ok := providerSchema.Data["data_source_schemas"].(metadata.JsonObject)
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"provider %s has no data_source_schemas entry for %s",
			jsonQuote(resource.Provider), jsonQuote(resourceType),
		)
	}
	dataSourceSchema, ok := dataSourceSchemas[resourceType].(metadata.JsonObject)
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"provider %s has no data_source_schemas entry for %s",
			jsonQuote(resource.Provider), jsonQuote(resourceType),
		)
	}
	block, err := metadata.TerraformBlockForSchema(dataSourceSchema, resourceType)
	if err != nil {
		return dataModuleContext{}, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, resourceType+".block")
	if err != nil {
		return dataModuleContext{}, err
	}
	nameAttributeValue, ok := attributes[nameField]
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source schema is missing input attribute %s named by lookup_sources.%s.name_field",
			jsonQuote(resourceType), jsonQuote(nameField), resourceType,
		)
	}
	nameAttribute, err := metadata.TerraformRequireObject(
		nameAttributeValue, resourceType+".block.attributes."+nameField,
	)
	if err != nil {
		return dataModuleContext{}, err
	}
	if !metadata.TerraformBooleanField(nameAttribute, "required") &&
		!metadata.TerraformBooleanField(nameAttribute, "optional") {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source attribute %s named by lookup_sources.%s.name_field must be optional or required, not computed-only",
			jsonQuote(resourceType), jsonQuote(nameField), resourceType,
		)
	}
	nameType, err := metadata.TerraformAttributeType(
		nameAttribute, resourceType+".block.attributes."+nameField,
	)
	if err != nil {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source attribute %s named by lookup_sources.%s.name_field must be string-typed: %w",
			jsonQuote(resourceType), jsonQuote(nameField), resourceType, err,
		)
	}
	if primitive, ok := nameType.(metadata.TerraformPrimitiveType); !ok || primitive != metadata.TerraformPrimitiveType("string") {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source attribute %s named by lookup_sources.%s.name_field must be string-typed",
			jsonQuote(resourceType), jsonQuote(nameField), resourceType,
		)
	}
	idAttributeValue, ok := attributes["id"]
	if !ok {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source schema is missing readable id attribute",
			jsonQuote(resourceType),
		)
	}
	idAttribute, err := metadata.TerraformRequireObject(idAttributeValue, resourceType+".block.attributes.id")
	if err != nil {
		return dataModuleContext{}, err
	}
	if !metadata.TerraformBooleanField(idAttribute, "required") &&
		!metadata.TerraformBooleanField(idAttribute, "optional") &&
		!metadata.TerraformBooleanField(idAttribute, "computed") {
		return dataModuleContext{}, fmt.Errorf(
			"data referent %s data-source id attribute must be readable",
			jsonQuote(resourceType),
		)
	}

	providerSource, hasProviderSource := root.Packs.ProviderSources[resource.Provider]
	ownerSource, hasOwnerSource := manifest.ProviderSources[resource.Provider]
	if !hasProviderSource || !hasOwnerSource {
		return dataModuleContext{}, fmt.Errorf("provider %s has no source in pack metadata", jsonQuote(resource.Provider))
	}
	if providerSource != ownerSource {
		return dataModuleContext{}, fmt.Errorf("provider %s has contradictory source metadata", jsonQuote(resource.Provider))
	}
	providerPin, _ := manifest.Data["pin"].(string)
	if providerPin == "" {
		return dataModuleContext{}, fmt.Errorf("provider %s has no version pin in pack metadata", jsonQuote(resource.Provider))
	}

	return dataModuleContext{
		ResourceType:   resourceType,
		Provider:       resource.Provider,
		ProviderSource: providerSource,
		ProviderPin:    providerPin,
		NameField:      nameField,
	}, nil
}

func renderDataMain(context dataModuleContext) string {
	return fmt.Sprintf(
		"%sdata \"%s\" \"items\" {\n  for_each = var.items\n  %s = each.value.%s\n\n  lifecycle {\n    postcondition {\n      condition     = self.%s == each.value.%s\n      error_message = \"provider returned name does not exactly match requested name\"\n    }\n  }\n}\n\noutput \"items\" {\n  description = \"All read-only %s data source objects, keyed as in var.items.\"\n  value       = data.%s.items\n}\n",
		header(context.Provider), context.ResourceType, context.NameField, context.NameField,
		context.NameField, context.NameField, context.ResourceType, context.ResourceType,
	)
}

func renderDataVariables(context dataModuleContext) string {
	return fmt.Sprintf(
		"%svariable \"items\" {\n  description = \"%s instances, keyed by a stable identifier.\"\n  type = map(object({\n    %s = string\n  }))\n}\n",
		header(context.Provider), context.ResourceType, context.NameField,
	)
}

func defaultDataSampleItems(context dataModuleContext) metadata.JsonObject {
	return metadata.JsonObject{
		"group-a": metadata.JsonObject{context.NameField: "Group A"},
		"group-b": metadata.JsonObject{context.NameField: "Group B"},
	}
}

func renderDataSample(context dataModuleContext, items metadata.JsonObject) (string, error) {
	return canonjson.RenderLosslessArtifactJSON(metadata.JsonObject{
		"items": items,
	})
}

func renderDataTest(context dataModuleContext) string {
	return fmt.Sprintf(
		"# GENERATED smoke test — plan against a mocked provider; no credentials.\nmock_provider \"%s\" {}\n\nrun \"defaults_plan\" {\n  command = plan\n\n  assert {\n    condition     = length(var.items) == 2\n    error_message = \"sample fixture must contain exactly two items\"\n  }\n}\n",
		context.Provider,
	)
}

func renderDataReadme(context moduleContext) string {
	return fmt.Sprintf(
		"# %s (generated module)\n\nReads `%s` from the provider; managed nowhere — data source only. GENERATED — do not edit by\nhand (AGENTS.md rule 6). Regenerate with `iw modules generate` or `make gen-modules`.\n",
		context.ResourceType, context.ResourceType,
	)
}

func renderDataModule(root metadata.LoadedPackRoot, resourceType string) (RenderedModule, error) {
	return renderDataModuleWithSample(root, resourceType, nil)
}

func renderDataModuleWithSample(root metadata.LoadedPackRoot, resourceType string, sampleItems metadata.JsonObject) (RenderedModule, error) {
	context, err := buildDataModuleContext(root, resourceType)
	if err != nil {
		return RenderedModule{}, err
	}
	dataContext := moduleContext{
		ResourceType:   context.ResourceType,
		Provider:       context.Provider,
		ProviderSource: context.ProviderSource,
		ProviderPin:    context.ProviderPin,
	}
	if sampleItems == nil {
		sampleItems = defaultDataSampleItems(context)
	}
	sample, err := renderDataSample(context, sampleItems)
	if err != nil {
		return RenderedModule{}, err
	}
	dataReadme := renderDataReadme(dataContext)
	return RenderedModule{
		ResourceType: resourceType,
		Files: []ModuleFile{
			{FileMain, renderDataMain(context)},
			{FileVariables, renderDataVariables(context)},
			{FileOutputs, header(context.Provider)},
			{FileVersions, renderVersions(dataContext)},
			{FileReadme, dataReadme},
			{FileDefaultsTest, renderDataTest(context)},
			{FileSampleTfvars, sample},
		},
	}, nil
}
