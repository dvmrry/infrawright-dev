// Package transformrun implements the batch orchestration behind `iw
// transform`: selection, input reads, kernel invocation, artifact writes,
// drop diagnostics, and DROPS_CHECK accounting.
package transformrun

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/textcompat"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// TransformBatchResult ports TransformBatchResult from transform-runner.ts.
type TransformBatchResult struct {
	DropCheckFailed []string
	Failed          []string
	Processed       []string
	Skipped         []string
}

// RunTransformBatchOptions ports runTransformBatch's options bag.
// Environment carries the process environment (DROPS_CHECK); nil means an
// empty environment, matching the Node source's optional field.
type RunTransformBatchOptions struct {
	BeforeArtifactWrite func(resourceType string) error
	Deployment          deployment.Deployment
	Environment         map[string]string
	InputDirectory      string
	OnDiagnostic        func(message string)
	Root                metadata.LoadedPackRoot
	Selectors           []string
	Tenant              string
}

// transformReferenceSpecs ports transformReferenceSpecs.
func transformReferenceSpecs(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
) map[string]tfrender.TransformReferenceSpec {
	output := map[string]tfrender.TransformReferenceSpec{}
	merged := transform.MergedTransformReferences(root)
	resourceReferences, ok := merged[resource.Type]
	if !ok {
		return output
	}
	for field, raw := range resourceReferences {
		spec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		referent, referentOk := spec["referent"].(string)
		nameField, nameOk := spec["name_field"].(string)
		if referentOk && nameOk {
			output[field] = tfrender.TransformReferenceSpec{
				Referent:  referent,
				NameField: nameField,
			}
		}
	}
	return output
}

type inboundLookupReference struct {
	nameField string
	provider  string
	source    string
}

// inboundLookupReferences ports inboundLookupReferences: sorted walks so
// conflict diagnostics are deterministic exactly like the Node source's
// sortedStrings walks.
func inboundLookupReferences(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
) []inboundLookupReference {
	output := []inboundLookupReference{}
	references := transform.MergedTransformReferences(root)
	referrers := make([]string, 0, len(references))
	for referrer := range references {
		referrers = append(referrers, referrer)
	}
	for _, referrer := range canonjson.SortedStrings(referrers) {
		referrerResource, ok := root.Resources[referrer]
		if !ok || referrer == resource.Type {
			continue
		}
		if referrerResource.Registry["generate"] != true {
			continue
		}
		if _, derived := referrerResource.Registry["derive"].(map[string]any); derived {
			continue
		}
		fields, ok := references[referrer]
		if !ok {
			continue
		}
		names := make([]string, 0, len(fields))
		for field := range fields {
			names = append(names, field)
		}
		for _, field := range canonjson.SortedStrings(names) {
			spec, ok := fields[field].(map[string]any)
			if !ok || spec["referent"] != resource.Type {
				continue
			}
			nameField, ok := spec["name_field"].(string)
			if !ok {
				continue
			}
			output = append(output, inboundLookupReference{
				nameField: nameField,
				provider:  referrerResource.Provider,
				source:    referrer + "." + field,
			})
		}
	}
	return output
}

// transformLookupNameField ports transformLookupNameField, including the
// conflicting-inferred-fields TypeError text verbatim.
func transformLookupNameField(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
	dep deployment.Deployment,
) (*string, error) {
	sources := transform.MergedTransformLookupSources(root)
	if resourceSource, ok := sources[resource.Type]; ok {
		if nameField, ok := resourceSource["name_field"].(string); ok {
			return &nameField, nil
		}
	}
	inferred := map[string][]string{}
	for _, reference := range inboundLookupReferences(root, resource) {
		if deployment.DeploymentReferenceBindingMode(dep, reference.provider) == deployment.ReferenceBindingDisabled {
			continue
		}
		inferred[reference.nameField] = append(inferred[reference.nameField], reference.source)
	}
	if len(inferred) == 0 {
		return nil, nil
	}
	if len(inferred) > 1 {
		fields := make([]string, 0, len(inferred))
		for field := range inferred {
			fields = append(fields, field)
		}
		conflicts := make([]string, 0, len(fields))
		for _, field := range canonjson.SortedStrings(fields) {
			quoted := jsonStringifyString(field)
			conflicts = append(conflicts, fmt.Sprintf(
				"%s from %s",
				quoted,
				strings.Join(canonjson.SortedStrings(inferred[field]), ", "),
			))
		}
		return nil, fmt.Errorf(
			"%s has conflicting inferred reference lookup name fields: %s; declare an explicit lookup_sources entry",
			resource.Type,
			strings.Join(conflicts, "; "),
		)
	}
	for field := range inferred {
		return &field, nil
	}
	return nil, nil
}

// transformHasInferredLookupLifecycle ports the same-named export.
func transformHasInferredLookupLifecycle(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
) bool {
	return len(inboundLookupReferences(root, resource)) > 0
}

func dataReferentLookupNameField(root metadata.LoadedPackRoot, resource metadata.LoadedResourceMetadata) (string, error) {
	sources := transform.MergedTransformLookupSources(root)
	source, ok := sources[resource.Type]
	if !ok {
		return "", fmt.Errorf("data referent %s requires an explicit lookup_sources entry", resource.Type)
	}
	nameField, ok := source["name_field"].(string)
	if !ok || strings.TrimSpace(nameField) == "" {
		return "", fmt.Errorf("data referent %s requires a non-empty lookup_sources name_field", resource.Type)
	}
	return nameField, nil
}

// shouldUnescape ports shouldUnescape (pack manifest unescape_products).
func shouldUnescape(root metadata.LoadedPackRoot, resourceType string) bool {
	active := map[string]bool{}
	for _, pack := range root.Active.Packs {
		active[pack] = true
	}
	for _, manifest := range root.Packs.Manifests {
		if !active[manifest.Name] {
			continue
		}
		prefixes, ok := manifest.Data["unescape_products"].([]any)
		if !ok {
			continue
		}
		for _, prefix := range prefixes {
			text, ok := prefix.(string)
			if ok && strings.HasPrefix(resourceType, text) {
				return true
			}
		}
	}
	return false
}

// knownHoldPaths ports knownHoldPaths: shared-component adoption_status
// sidecar reads through the same optional-read and lossless-parse contracts
// as the Node source.
func knownHoldPaths(
	root metadata.LoadedPackRoot,
	resourceType string,
) (map[string]bool, error) {
	output := map[string]bool{}
	for _, component := range root.Active.Shared {
		source := filepath.Join(root.Root, "_shared", component, "adoption_status.json")
		text, err := metadata.ReadOptionalUTF8(source, component+" adoption status")
		if err != nil {
			return nil, err
		}
		if text == nil {
			continue
		}
		data, err := canonjson.ParseDataJSONLosslessly(*text)
		if err != nil {
			return nil, err
		}
		record, ok := data.(map[string]any)
		if !ok {
			continue
		}
		knownHolds, ok := record["known_holds"].(map[string]any)
		if !ok {
			continue
		}
		holds, ok := knownHolds[resourceType].([]any)
		if !ok {
			continue
		}
		for _, hold := range holds {
			entry, ok := hold.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := entry["path"].(string); ok {
				output[path] = true
			}
		}
	}
	return output, nil
}

// setBlockFieldIndexes reports, for each declared reference field, the index
// of the first dotted segment that names a set-nested block in the referrer's
// provider schema. Fields that never cross a set block are absent. The walk
// stops at the first attribute segment: whatever follows an attribute is
// value shape, not schema traversal.
func setBlockFieldIndexes(
	schema metadata.JsonObject,
	resourceType string,
	references map[string]tfrender.TransformReferenceSpec,
) (map[string]int, error) {
	output := map[string]int{}
	rootBlock, err := metadata.TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return nil, err
	}
	for field := range references {
		segments := strings.Split(field, ".")
		current := rootBlock
		label := resourceType
		for index, segment := range segments {
			attributes, err := metadata.TerraformAttributesForBlock(current, label)
			if err != nil {
				return nil, err
			}
			if _, isAttribute := attributes[segment]; isAttribute {
				break
			}
			blockTypes, err := metadata.TerraformBlockTypesForBlock(current, label)
			if err != nil {
				return nil, err
			}
			rawBlockType, isBlock := blockTypes[segment]
			if !isBlock {
				break
			}
			blockType, err := metadata.TerraformRequireObject(rawBlockType, label+".block_types."+segment)
			if err != nil {
				return nil, err
			}
			// Single-collapsed blocks descend, whatever their nesting mode.
			// This mirrors the schema-path validator's blockCursor exactly:
			// TerraformBlockIsSingle wins before the set switch, so a
			// max_items=1 set block projects to one object, traverses by
			// name with no index, and needs no complete-leaf treatment.
			// Classifying it as a set here while the validator classifies
			// it as single would make the producer skip bindings the
			// validator accepts -- the same two-halves disagreement this
			// map exists to remove, pointed the other way.
			if !metadata.TerraformBlockIsSingle(blockType) {
				if nesting, _ := blockType["nesting_mode"].(string); nesting == "set" {
					output[field] = index
					break
				}
			}
			child, err := metadata.TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
			if err != nil {
				return nil, err
			}
			current = child
			label += "." + segment
		}
	}
	return output, nil
}

// transformBindingContext ports transformBindingContext, extended with the
// schema-derived set-block field map (see BindingContext.SetBlockFields).
func transformBindingContext(
	dep deployment.Deployment,
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
	resourceRoots map[string]string,
	references map[string]tfrender.TransformReferenceSpec,
	schema metadata.JsonObject,
) (tfrender.BindingContext, error) {
	generated := map[string]bool{}
	derived := map[string]bool{}
	dataReferents := map[string]bool{}
	for _, loaded := range root.Resources {
		if loaded.Registry["generate"] == true {
			generated[loaded.Type] = true
			if _, ok := loaded.Registry["derive"].(map[string]any); ok {
				derived[loaded.Type] = true
			}
		}
		if dataReferent, _ := loaded.Registry["data_referent"].(bool); dataReferent {
			dataReferents[loaded.Type] = true
		}
	}
	setBlockFields, err := setBlockFieldIndexes(schema, resource.Type, references)
	if err != nil {
		return tfrender.BindingContext{}, err
	}
	return tfrender.BindingContext{
		Mode:           deployment.DeploymentReferenceBindingMode(dep, resource.Provider),
		Generated:      generated,
		Derived:        derived,
		DataReferents:  dataReferents,
		ResourceRoots:  resourceRoots,
		References:     references,
		SetBlockFields: setBlockFields,
	}, nil
}

// jsToFixed1 reproduces JS Number.prototype.toFixed(1) for the slim-input
// warning: round half away from zero on the tenths digit. Go's %.1f uses
// round-half-even, which differs on exact .x5 ties, so the digit pair is
// assembled from the scaled-and-rounded integer instead.
func jsToFixed1(value float64) string {
	scaled := int64(math.Round(value * 10))
	sign := ""
	if scaled < 0 {
		sign = "-"
		scaled = -scaled
	}
	return fmt.Sprintf("%s%d.%d", sign, scaled/10, scaled%10)
}

// warnIfSlim ports warnIfSlim, including the WARNING text verbatim.
func warnIfSlim(
	rawItems []any,
	resourceType string,
	schema metadata.JsonObject,
	write func(string),
) error {
	if len(rawItems) == 0 {
		return nil
	}
	block, err := metadata.TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return err
	}
	classified, err := metadata.TerraformResourceInputAttributes(block, resourceType)
	if err != nil {
		return err
	}
	expected := len(classified.Required) + len(classified.Optional)
	if expected == 0 {
		return nil
	}
	total := 0
	for _, item := range rawItems {
		record, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		total += len(record)
	}
	average := float64(total) / float64(len(rawItems))
	if average < float64(expected)/3 {
		write(fmt.Sprintf(
			"WARNING: %s input looks slim (avg %s keys vs %d schema inputs); did the fetcher use the list endpoint instead of detail?",
			resourceType, jsToFixed1(average), expected,
		))
	}
	return nil
}

// reportedDrops ports reportedDrops, including the acknowledged-drops
// guidance block and snippet rendering, message-for-message.
func reportedDrops(
	drops []string,
	held map[string]bool,
	override map[string]any,
	resourceType string,
	write func(string),
) ([]string, error) {
	heldDrops := []string{}
	unexpected := []string{}
	for _, item := range drops {
		if held[item] {
			heldDrops = append(heldDrops, item)
		} else {
			unexpected = append(unexpected, item)
		}
	}
	heldDrops = canonjson.SortedStrings(heldDrops)
	unexpected = canonjson.SortedStrings(unexpected)
	for _, field := range heldDrops {
		write("known-held " + resourceType + "." + field)
	}
	for _, field := range unexpected {
		write("dropped " + resourceType + "." + field)
	}
	if len(unexpected) > 0 {
		write(fmt.Sprintf(
			"%d unacknowledged dropped field(s) above — NEW API surface for %s. Confirm each against the provider read/expand, then add the safe ones to acknowledged_drops in packs/<provider>/overrides/%s.json (a dropped field can be write-REQUIRED under another schema name — the signingCertId class — so verify before acknowledging). DROPS_CHECK=1 makes this exit 4.",
			len(unexpected), resourceType, resourceType,
		))
		merged := map[string]bool{}
		if existing, ok := override["acknowledged_drops"].([]any); ok {
			for _, item := range existing {
				if text, ok := item.(string); ok {
					merged[text] = true
				}
			}
		}
		for _, item := range unexpected {
			merged[item] = true
		}
		keys := make([]string, 0, len(merged))
		for key := range merged {
			keys = append(keys, key)
		}
		sortedKeys := canonjson.SortedStrings(keys)
		acknowledged := make([]any, len(sortedKeys))
		for index, key := range sortedKeys {
			acknowledged[index] = key
		}
		snippet, err := canonjson.RenderLosslessArtifactJSON(map[string]any{
			"acknowledged_drops": acknowledged,
		})
		if err != nil {
			return nil, err
		}
		write(fmt.Sprintf(
			"Exact paths from this run (merge into packs/<provider>/overrides/%s.json only after verification):\n%s",
			resourceType, strings.TrimRight(snippet, "\n"),
		))
	}
	return unexpected, nil
}

// jsonStringifyString reproduces JSON.stringify(<string>) for diagnostic
// text: standard JSON escapes for control characters, quote, and
// backslash; non-ASCII emitted raw (V8 does not ASCII-escape here, unlike
// the canonical renderers).
func jsonStringifyString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if r < 0x20 {
				builder.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				builder.WriteRune(r)
			}
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

// RunTransformBatch ports runTransformBatch: "Execute the real batch
// transform target without invoking Python."
func RunTransformBatch(options RunTransformBatchOptions) (TransformBatchResult, error) {
	result := TransformBatchResult{
		Failed:    []string{},
		Processed: []string{},
		Skipped:   []string{},
	}
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return result, err
	}
	write := options.OnDiagnostic
	if write == nil {
		write = func(string) {}
	}
	selection, err := transform.SelectTransformResources(options.Root, options.Selectors)
	if err != nil {
		return result, err
	}
	for _, note := range selection.Notes {
		write(strings.TrimRight(note, " \t\r\n"))
	}
	topology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root:       options.Root,
		Deployment: options.Deployment,
		Tenant:     &options.Tenant,
		Selectors:  []string{},
	})
	if err != nil {
		return result, err
	}
	for _, resourceType := range selection.ResourceTypes {
		sourceType, err := transform.TransformSourceType(options.Root, resourceType)
		if err != nil {
			return result, err
		}
		sourcePath := filepath.Join(options.InputDirectory, sourceType+".json")
		text, err := metadata.ReadOptionalUTF8(sourcePath, resourceType+" transform input")
		if err != nil {
			return result, err
		}
		if text == nil {
			result.Skipped = append(result.Skipped, resourceType)
			write(fmt.Sprintf("skip %s (no %s)", resourceType, sourcePath))
			continue
		}
		failure := func(err error) {
			result.Failed = append(result.Failed, resourceType)
			write(fmt.Sprintf("error: %s: %s", resourceType, err.Error()))
		}
		processResource := func() error {
			raw, err := canonjson.ParseDataJSONLosslessly(*text)
			if err != nil {
				return err
			}
			rawItems, ok := raw.([]any)
			if !ok {
				return fmt.Errorf(
					"%s must be a JSON LIST of items — re-run make fetch TENANT=%s RESOURCE=%s; if it persists the fetcher wrote an envelope instead of the item list",
					sourcePath, options.Tenant, resourceType,
				)
			}
			resource, ok := options.Root.Resources[resourceType]
			if !ok {
				return fmt.Errorf("unknown resource %s", resourceType)
			}
			references := transformReferenceSpecs(options.Root, resource)
			rootLabel, ok := topology.Topology.ResourceRoots[resourceType]
			if !ok {
				rootLabel = resourceType
			}
			variableName := "items"
			if rootLabel != resourceType {
				variableName = resourceType + "_items"
			}
			if dataReferent, _ := resource.Registry["data_referent"].(bool); dataReferent {
				nameField, err := dataReferentLookupNameField(options.Root, resource)
				if err != nil {
					return err
				}
				transformed, err := transform.TransformDataReferentItems(transform.DataReferentTransformOptions{
					NameField:    nameField,
					RawItems:     rawItems,
					ResourceType: resourceType,
				})
				if err != nil {
					return err
				}
				if options.BeforeArtifactWrite != nil {
					if err := options.BeforeArtifactWrite(resourceType); err != nil {
						return err
					}
				}
				if _, err := tfrender.WriteTransformArtifacts(tfrender.TransformArtifactCompileOptions{
					ArtifactMode:           tfrender.TransformArtifactModeDataReferent,
					Deployment:             options.Deployment,
					LookupNameField:        &nameField,
					RemoveLookupWhenAbsent: transformHasInferredLookupLifecycle(options.Root, resource),
					OnDiagnostic:           write,
					ResourceType:           resourceType,
					Result: tfrender.PullTransformResult{
						Items:     transformed.Items,
						Originals: transformed.Originals,
						Drops:     transformed.Drops,
					},
					Tenant:       options.Tenant,
					VariableName: variableName,
				}); err != nil {
					return err
				}
				result.Processed = append(result.Processed, resourceType)
				return nil
			}
			if _, derived := resource.Registry["derive"].(map[string]any); derived {
				if options.BeforeArtifactWrite != nil {
					if err := options.BeforeArtifactWrite(resourceType); err != nil {
						return err
					}
				}
				derive := resource.Registry["derive"].(map[string]any)
				items, err := transform.DeriveReorderItems(rawItems, derive)
				if err != nil {
					return err
				}
				if _, err := tfrender.WriteDerivedTransformArtifact(
					options.Deployment, items, references,
					resourceType, sourceType, options.Tenant, variableName,
					write,
				); err != nil {
					return err
				}
				result.Processed = append(result.Processed, resourceType)
				return nil
			}
			schema, err := options.Root.LoadResourceSchema(resourceType)
			if err != nil {
				return err
			}
			if err := warnIfSlim(rawItems, resourceType, schema, write); err != nil {
				return err
			}
			transformed, err := transform.TransformLoadedItems(transform.TransformLoadedItemsOptions{
				Resource:     resource,
				Schema:       schema,
				RawItems:     rawItems,
				HTMLUnescape: textcompat.HTMLUnescape,
				UnescapeHTML: shouldUnescape(options.Root, resourceType),
			})
			if err != nil {
				return err
			}
			if options.BeforeArtifactWrite != nil {
				if err := options.BeforeArtifactWrite(resourceType); err != nil {
					return err
				}
			}
			lookupNameField, err := transformLookupNameField(options.Root, resource, options.Deployment)
			if err != nil {
				return err
			}
			bindingContext, err := transformBindingContext(
				options.Deployment, options.Root, resource,
				topology.Topology.ResourceRoots, references, schema,
			)
			if err != nil {
				return err
			}
			if _, err := tfrender.WriteTransformArtifacts(tfrender.TransformArtifactCompileOptions{
				BindingContext:         bindingContext,
				Deployment:             options.Deployment,
				LookupNameField:        lookupNameField,
				RemoveLookupWhenAbsent: transformHasInferredLookupLifecycle(options.Root, resource),
				OnDiagnostic:           write,
				Override:               resource.Override,
				References:             references,
				ResourceType:           resourceType,
				Result: tfrender.PullTransformResult{
					Items:     transformed.Items,
					Originals: transformed.Originals,
					Drops:     transformed.Drops,
				},
				Tenant:       options.Tenant,
				VariableName: variableName,
			}); err != nil {
				return err
			}
			held, err := knownHoldPaths(options.Root, resourceType)
			if err != nil {
				return err
			}
			unexpected, err := reportedDrops(
				transformed.Drops, held, resource.Override, resourceType, write,
			)
			if err != nil {
				return err
			}
			if len(unexpected) > 0 && options.Environment["DROPS_CHECK"] != "" {
				result.Failed = append(result.Failed, resourceType)
				result.DropCheckFailed = append(result.DropCheckFailed, resourceType)
			} else {
				result.Processed = append(result.Processed, resourceType)
			}
			return nil
		}
		if err := processResource(); err != nil {
			failure(err)
		}
	}
	if len(result.Failed) > 0 {
		write("\ntransform FAILED for: " + strings.Join(result.Failed, " "))
	}
	return result, nil
}
