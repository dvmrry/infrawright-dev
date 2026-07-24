package sourceanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

// FieldWitnessDisposition describes whether independent captured witnesses
// agree. It is diagnostic only and never changes source qualification or
// provider-readiness accounting.
type FieldWitnessDisposition string

const (
	// FieldWitnessCorroborated means at least two witness classes describe the
	// field and no direct disagreement was recovered.
	FieldWitnessCorroborated FieldWitnessDisposition = "corroborated"
	// FieldWitnessConflicting means captured witnesses directly disagree.
	FieldWitnessConflicting FieldWitnessDisposition = "conflicting"
	// FieldWitnessUntested means only one witness class was recovered. Missing
	// acceptance coverage is silence, not a conflict.
	FieldWitnessUntested FieldWitnessDisposition = "untested"
)

// FieldWitnessReviewPriority ranks diagnostic attention only. It is not an
// adoption-risk or provider-readiness classification.
type FieldWitnessReviewPriority string

const (
	FieldWitnessReviewHigh   FieldWitnessReviewPriority = "high"
	FieldWitnessReviewMedium FieldWitnessReviewPriority = "medium"
	FieldWitnessReviewLow    FieldWitnessReviewPriority = "low"
)

// FieldWitnessReport contains source-derived, corroborating field evidence.
// It is intentionally separate from the frozen source-evidence-report-v1
// contract until downstream policy for these witnesses is reviewed.
type FieldWitnessReport struct {
	SourceTrust           contracts.SourceTrust           `json:"source_trust"`
	SourceManifestSHA256  *string                         `json:"source_manifest_sha256,omitempty"`
	InputProvenanceSHA256 string                          `json:"input_provenance_sha256"`
	Resources             map[string]FieldWitnessResource `json:"resources"`
}

// FieldWitnessResource contains the field witnesses and explicit analysis
// diagnostics for one selected Terraform resource.
type FieldWitnessResource struct {
	Fields      map[string]FieldWitness  `json:"fields"`
	Diagnostics []FieldWitnessDiagnostic `json:"diagnostics"`
	ReviewQueue []FieldWitnessReviewItem `json:"review_queue"`
}

// FieldWitnessReviewItem turns captured disagreements and evidence gaps into
// a deterministic worklist without changing their authority or disposition.
type FieldWitnessReviewItem struct {
	FieldPath            string                     `json:"field_path"`
	Priority             FieldWitnessReviewPriority `json:"priority"`
	ReasonCodes          []string                   `json:"reason_codes"`
	Details              []string                   `json:"details"`
	AbsentWitnessClasses []string                   `json:"absent_witness_classes"`
	SuggestedValidation  string                     `json:"suggested_validation"`
}

// FieldWitness combines independent observations for one normalized Terraform
// field path. Collection elements use [] in paths, for example ports[].end.
type FieldWitness struct {
	Disposition       FieldWitnessDisposition        `json:"disposition"`
	TerraformSchema   *TerraformSchemaFieldWitness   `json:"terraform_schema,omitempty"`
	ProviderSchemas   []ProviderSchemaFieldWitness   `json:"provider_schemas"`
	ReadBacks         []ReadBackFieldWitness         `json:"read_backs"`
	AcceptanceConfigs []AcceptanceConfigFieldWitness `json:"acceptance_configs"`
	AcceptanceChecks  []AcceptanceCheckFieldWitness  `json:"acceptance_checks"`
	Conflicts         []string                       `json:"conflicts"`
}

// TerraformSchemaFieldWitness records flags represented by the captured
// Terraform schema JSON. Nil flags are not represented by that JSON shape.
type TerraformSchemaFieldWitness struct {
	Required    *bool  `json:"required,omitempty"`
	Optional    *bool  `json:"optional,omitempty"`
	Computed    *bool  `json:"computed,omitempty"`
	NestingMode string `json:"nesting_mode,omitempty"`
	Type        string `json:"type,omitempty"`
	JSONPath    string `json:"json_path"`
}

// ProviderSchemaFieldWitness records one actual helper/schema declaration.
type ProviderSchemaFieldWitness struct {
	Required   bool                     `json:"required"`
	Optional   bool                     `json:"optional"`
	Computed   bool                     `json:"computed"`
	Type       string                   `json:"type,omitempty"`
	Validators []string                 `json:"validators"`
	Location   contracts.SourceLocation `json:"location"`
}

// ReadBackFieldWitness records a literal-key ResourceData.Set call rooted in
// the selected resource's resolved Read callback.
type ReadBackFieldWitness struct {
	Expression string                   `json:"expression"`
	Location   contracts.SourceLocation `json:"location"`
}

// AcceptanceConfigFieldWitness records literal HCL usage in the exact
// constructor-companion acceptance test file. Syntax distinguishes attribute
// declarations from repeated blocks so Occurrences is not mistaken for an
// attribute collection's element count.
type AcceptanceConfigFieldWitness struct {
	Occurrences     int                      `json:"occurrences"`
	ParentInstances int                      `json:"parent_instances"`
	Syntax          string                   `json:"syntax"`
	Values          []string                 `json:"values"`
	Location        contracts.SourceLocation `json:"location"`
}

// AcceptanceCheckFieldWitness records TestCheckResourceAttr assertions. Path
// retains the exact Terraform state path used by the provider authors.
type AcceptanceCheckFieldWitness struct {
	ResourceAddress       string                   `json:"resource_address"`
	ResourceAddressStatic bool                     `json:"resource_address_static"`
	Path                  string                   `json:"path"`
	Expected              string                   `json:"expected"`
	Location              contracts.SourceLocation `json:"location"`
}

// FieldWitnessDiagnostic surfaces unsupported or ambiguous source shapes
// without turning absence into negative behavioral evidence.
type FieldWitnessDiagnostic struct {
	Code      string                    `json:"code"`
	FieldPath string                    `json:"field_path,omitempty"`
	Message   string                    `json:"message"`
	Location  *contracts.SourceLocation `json:"location,omitempty"`
}

// AnalyzeFieldWitnesses derives corroborating field witnesses from one
// qualified defensive snapshot. It reads no paths and invokes no tools.
func AnalyzeFieldWitnesses(ctx context.Context, inputs sourcebind.QualifiedInputs) (FieldWitnessReport, error) {
	if err := ctx.Err(); err != nil {
		return FieldWitnessReport{}, fmt.Errorf("field witness analysis cancelled: %w", err)
	}
	snapshot, err := inputs.Snapshot()
	if err != nil {
		return FieldWitnessReport{}, fmt.Errorf("snapshot qualified inputs for field witnesses: %w", err)
	}
	index, err := newIndex(ctx, snapshot)
	if err != nil {
		return FieldWitnessReport{}, err
	}
	return index.fieldWitnessReport(ctx)
}

// AnalyzeUnverifiedFieldWitnesses derives diagnostic-only field witnesses from
// one defensive copy of explicitly unverified captured bytes.
func AnalyzeUnverifiedFieldWitnesses(ctx context.Context, inputs sourcebind.UnverifiedInputs) (FieldWitnessReport, error) {
	if err := ctx.Err(); err != nil {
		return FieldWitnessReport{}, fmt.Errorf("field witness analysis cancelled: %w", err)
	}
	snapshot, err := snapshotUnverified(inputs)
	if err != nil {
		return FieldWitnessReport{}, fmt.Errorf("snapshot unverified inputs for field witnesses: %w", err)
	}
	index, err := newUnverifiedIndex(ctx, snapshot)
	if err != nil {
		return FieldWitnessReport{}, err
	}
	return index.fieldWitnessReport(ctx)
}

type fieldWitnessAccumulator struct {
	terraformSchema   *TerraformSchemaFieldWitness
	providerSchemas   []ProviderSchemaFieldWitness
	readBacks         []ReadBackFieldWitness
	acceptanceConfigs []AcceptanceConfigFieldWitness
	acceptanceChecks  []AcceptanceCheckFieldWitness
	conflicts         []string
}

func (i *analysisIndex) fieldWitnessReport(ctx context.Context) (FieldWitnessReport, error) {
	if err := ctx.Err(); err != nil {
		return FieldWitnessReport{}, fmt.Errorf("field witness analysis cancelled: %w", err)
	}
	var schemaData map[string]any
	if err := json.Unmarshal(i.snapshot.TerraformSchema.Bytes, &schemaData); err != nil {
		return FieldWitnessReport{}, fmt.Errorf("decode captured Terraform schema for field witnesses: %w", err)
	}
	if schemaData == nil {
		return FieldWitnessReport{}, fmt.Errorf("captured Terraform schema for field witnesses must be an object")
	}

	resources := make(map[string]FieldWitnessResource, len(i.selection.ResourceTypes))
	for _, resourceType := range i.selection.ResourceTypes {
		if err := ctx.Err(); err != nil {
			return FieldWitnessReport{}, fmt.Errorf("field witness analysis cancelled: %w", err)
		}
		resources[resourceType] = i.fieldWitnessResource(resourceType, schemaData)
	}
	manifest := cloneStringPointer(i.manifestSHA256)
	return FieldWitnessReport{
		SourceTrust:           i.sourceTrust,
		SourceManifestSHA256:  manifest,
		InputProvenanceSHA256: i.inputProvenanceID,
		Resources:             resources,
	}, nil
}

func (i *analysisIndex) fieldWitnessResource(resourceType string, schemaData map[string]any) FieldWitnessResource {
	fields := make(map[string]*fieldWitnessAccumulator)
	diagnostics := make([]FieldWitnessDiagnostic, 0)
	field := func(fieldPath string) *fieldWitnessAccumulator {
		if fields[fieldPath] == nil {
			fields[fieldPath] = &fieldWitnessAccumulator{}
		}
		return fields[fieldPath]
	}

	resourceSchema, jsonPath, err := terraformResourceSchema(schemaData, resourceType)
	if err != nil {
		diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "terraform_schema_unresolved", Message: err.Error()})
	} else if block, ok := objectValue(resourceSchema["block"]); ok {
		walkTerraformSchemaBlock(block, "", jsonPath+".block", field)
	} else {
		diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "terraform_schema_block_missing", Message: "captured Terraform resource schema has no object block"})
	}

	registration := i.registrations[resourceType]
	var constructor *function
	if registration == nil || registration.constructorKey == "" {
		diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "provider_schema_constructor_unresolved", Message: "selected provider registration does not resolve to a captured constructor"})
	} else {
		constructor = i.providerFunctions[registration.constructorKey]
		if constructor == nil {
			diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "provider_schema_constructor_unresolved", Message: "selected provider constructor declaration is not captured"})
		} else if literal, issue := i.returnedResourceLiteral(constructor); issue != "" {
			location := i.loc(constructor.file, constructor.symbol, constructor.decl.Name.Pos())
			diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "provider_schema_literal_unresolved", Message: issue, Location: &location})
		} else {
			i.collectProviderSchemaFields(constructor, literal, "", field, &diagnostics)
		}
	}

	if registration != nil {
		if registration.callback == nil {
			diagnostics = append(diagnostics, FieldWitnessDiagnostic{Code: "read_callback_unresolved", Message: "selected provider registration has no statically resolved Read callback"})
		} else {
			for fieldPath, witness := range i.readBackWitnesses(registration.callback, &diagnostics) {
				field(fieldPath).readBacks = append(field(fieldPath).readBacks, witness...)
			}
		}
	}
	if constructor != nil {
		i.collectAcceptanceWitnesses(resourceType, constructor, field, &diagnostics)
	}

	final := make(map[string]FieldWitness, len(fields))
	for fieldPath, accumulated := range fields {
		final[fieldPath] = finalizeFieldWitness(fieldPath, accumulated)
	}
	sortFieldWitnessDiagnostics(diagnostics)
	return FieldWitnessResource{
		Fields:      final,
		Diagnostics: diagnostics,
		ReviewQueue: fieldWitnessReviewQueue(final, diagnostics),
	}
}

func terraformResourceSchema(data map[string]any, resourceType string) (map[string]any, string, error) {
	if resources, ok := objectValue(data["resource_schemas"]); ok {
		resource, present := objectValue(resources[resourceType])
		if !present {
			return nil, "", fmt.Errorf("resource type %q is absent from Terraform resource_schemas", resourceType)
		}
		return resource, "resource_schemas." + resourceType, nil
	}
	providers, ok := objectValue(data["provider_schemas"])
	if !ok {
		return nil, "", fmt.Errorf("Terraform schema must contain resource_schemas or provider_schemas")
	}
	type match struct {
		provider string
		schema   map[string]any
	}
	matches := make([]match, 0, 1)
	for _, provider := range sortedObjectKeys(providers) {
		providerData, providerOK := objectValue(providers[provider])
		if !providerOK {
			continue
		}
		resources, resourcesOK := objectValue(providerData["resource_schemas"])
		if !resourcesOK {
			continue
		}
		resource, present := objectValue(resources[resourceType])
		if present {
			matches = append(matches, match{provider: provider, schema: resource})
		}
	}
	switch len(matches) {
	case 0:
		return nil, "", fmt.Errorf("resource type %q is absent from Terraform provider schemas", resourceType)
	case 1:
		return matches[0].schema, "provider_schemas." + matches[0].provider + ".resource_schemas." + resourceType, nil
	default:
		return nil, "", fmt.Errorf("resource type %q appears in multiple Terraform provider schemas", resourceType)
	}
}

func walkTerraformSchemaBlock(
	block map[string]any,
	prefix string,
	jsonPath string,
	field func(string) *fieldWitnessAccumulator,
) {
	if attributes, ok := objectValue(block["attributes"]); ok {
		for _, name := range sortedObjectKeys(attributes) {
			attribute, attributeOK := objectValue(attributes[name])
			if !attributeOK {
				continue
			}
			fieldPath := joinFieldPath(prefix, name)
			witness := &TerraformSchemaFieldWitness{
				Required: boolPointerFromObject(attribute, "required"),
				Optional: boolPointerFromObject(attribute, "optional"),
				Computed: boolPointerFromObject(attribute, "computed"),
				Type:     compactJSON(attribute["type"]),
				JSONPath: jsonPath + ".attributes." + name,
			}
			field(fieldPath).terraformSchema = witness
			if nested, nestedOK := objectValue(attribute["nested_type"]); nestedOK {
				mode, _ := nested["nesting_mode"].(string)
				witness.NestingMode = mode
				walkTerraformSchemaBlock(nested, nestedFieldPrefix(fieldPath, mode), witness.JSONPath+".nested_type", field)
			}
		}
	}
	if blockTypes, ok := objectValue(block["block_types"]); ok {
		for _, name := range sortedObjectKeys(blockTypes) {
			blockType, blockOK := objectValue(blockTypes[name])
			if !blockOK {
				continue
			}
			fieldPath := joinFieldPath(prefix, name)
			mode, _ := blockType["nesting_mode"].(string)
			witness := &TerraformSchemaFieldWitness{
				NestingMode: mode,
				JSONPath:    jsonPath + ".block_types." + name,
			}
			field(fieldPath).terraformSchema = witness
			if child, childOK := objectValue(blockType["block"]); childOK {
				walkTerraformSchemaBlock(child, nestedFieldPrefix(fieldPath, mode), witness.JSONPath+".block", field)
			}
		}
	}
}

func objectValue(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func sortedObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolPointerFromObject(object map[string]any, key string) *bool {
	value, ok := object[key].(bool)
	if !ok {
		return nil
	}
	copy := value
	return &copy
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func joinFieldPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func nestedFieldPrefix(fieldPath, nestingMode string) string {
	switch nestingMode {
	case "list", "set", "tuple":
		return fieldPath + "[]"
	default:
		return fieldPath
	}
}

type resolvedComposite struct {
	owner   *function
	literal *ast.CompositeLit
}

func (i *analysisIndex) returnedResourceLiteral(constructor *function) (resolvedComposite, string) {
	resolved := make([]resolvedComposite, 0, 1)
	for _, statement := range constructor.decl.Body.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			continue
		}
		literal := resourceLiteral(returned.Results[0])
		if literal == nil || !isHashicorpSchemaComposite(constructor.file, literal, "Resource") {
			continue
		}
		resolved = append(resolved, resolvedComposite{owner: constructor, literal: literal})
	}
	switch len(resolved) {
	case 0:
		return resolvedComposite{}, "constructor has no direct returned *schema.Resource literal"
	case 1:
		return resolved[0], ""
	default:
		return resolvedComposite{}, "constructor has multiple returned *schema.Resource literals"
	}
}

func (i *analysisIndex) collectProviderSchemaFields(
	owner *function,
	resource resolvedComposite,
	prefix string,
	field func(string) *fieldWitnessAccumulator,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	schemaExpression, ok := compositeField(resource.literal, "Schema")
	if !ok {
		location := i.loc(resource.owner.file, resource.owner.symbol, resource.literal.Pos())
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "provider_schema_map_missing", Message: "*schema.Resource literal has no direct Schema field", Location: &location})
		return
	}
	schemaMap := resourceLiteral(schemaExpression)
	if schemaMap == nil {
		location := i.loc(resource.owner.file, resource.owner.symbol, schemaExpression.Pos())
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "provider_schema_map_unresolved", Message: "schema map is not a direct composite literal", Location: &location})
		return
	}
	for _, element := range schemaMap.Elts {
		entry, entryOK := element.(*ast.KeyValueExpr)
		if !entryOK {
			continue
		}
		name, nameOK := stringLiteral(entry.Key)
		if !nameOK || name == "" {
			location := i.loc(owner.file, owner.symbol, entry.Key.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "provider_field_name_dynamic", Message: "provider schema contains a non-literal field name", Location: &location})
			continue
		}
		fieldPath := joinFieldPath(prefix, name)
		resolved, issue := i.resolveSchemaLiteral(owner, entry.Value, map[string]bool{}, 0)
		if issue != "" {
			location := i.loc(owner.file, owner.symbol, entry.Value.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "provider_field_schema_unresolved", FieldPath: fieldPath, Message: fieldPath + ": " + issue, Location: &location})
			continue
		}
		witness, issue := i.providerSchemaWitness(resolved)
		if issue != "" {
			location := i.loc(resolved.owner.file, resolved.owner.symbol, resolved.literal.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "provider_field_schema_dynamic", FieldPath: fieldPath, Message: fieldPath + ": " + issue, Location: &location})
			continue
		}
		field(fieldPath).providerSchemas = append(field(fieldPath).providerSchemas, witness)

		elemExpression, hasElem := compositeField(resolved.literal, "Elem")
		if !hasElem {
			continue
		}
		elemLiteral := resourceLiteral(elemExpression)
		if elemLiteral == nil || !isHashicorpSchemaComposite(resolved.owner.file, elemLiteral, "Resource") {
			continue
		}
		typeName := providerSchemaType(resolved.literal)
		childPrefix := fieldPath
		if typeName == "schema.TypeList" || typeName == "schema.TypeSet" || strings.HasSuffix(typeName, ".TypeList") || strings.HasSuffix(typeName, ".TypeSet") {
			childPrefix += "[]"
		}
		i.collectProviderSchemaFields(resolved.owner, resolvedComposite{owner: resolved.owner, literal: elemLiteral}, childPrefix, field, diagnostics)
	}
}

func (i *analysisIndex) resolveSchemaLiteral(
	owner *function,
	expression ast.Expr,
	seen map[string]bool,
	depth int,
) (resolvedComposite, string) {
	if depth >= 16 {
		return resolvedComposite{}, "schema helper depth limit exceeded"
	}
	if literal := resourceLiteral(expression); literal != nil {
		if literal.Type == nil || isHashicorpSchemaComposite(owner.file, literal, "Schema") {
			return resolvedComposite{owner: owner, literal: literal}, ""
		}
		return resolvedComposite{}, "field value is not a *schema.Schema literal"
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return resolvedComposite{}, "field value is neither a schema literal nor a captured helper call"
	}
	helper := i.providerHelperForCall(owner.file, call)
	if helper == nil {
		return resolvedComposite{}, "schema helper declaration is not captured"
	}
	key := functionKey(helper.packagePath, helper.symbol)
	if seen[key] {
		return resolvedComposite{}, "recursive schema helper"
	}
	nextSeen := make(map[string]bool, len(seen)+1)
	for prior, value := range seen {
		nextSeen[prior] = value
	}
	nextSeen[key] = true
	resolved := make([]resolvedComposite, 0, 1)
	for _, statement := range helper.decl.Body.List {
		returned, returnedOK := statement.(*ast.ReturnStmt)
		if !returnedOK || len(returned.Results) != 1 {
			continue
		}
		candidate, issue := i.resolveSchemaLiteral(helper, returned.Results[0], nextSeen, depth+1)
		if issue == "" {
			resolved = append(resolved, candidate)
		}
	}
	switch len(resolved) {
	case 0:
		return resolvedComposite{}, "schema helper has no directly resolvable return literal"
	case 1:
		return resolved[0], ""
	default:
		return resolvedComposite{}, "schema helper has multiple resolvable return literals"
	}
}

func (i *analysisIndex) providerHelperForCall(file *sourceFile, call *ast.CallExpr) *function {
	switch called := call.Fun.(type) {
	case *ast.Ident:
		return i.providerFunctions[functionKey(file.packagePath, called.Name)]
	case *ast.SelectorExpr:
		alias, ok := called.X.(*ast.Ident)
		if !ok {
			return nil
		}
		packagePath, ok := file.imports[alias.Name]
		if !ok || !i.isProviderPackage(packagePath) {
			return nil
		}
		return i.providerFunctions[functionKey(packagePath, called.Sel.Name)]
	default:
		return nil
	}
}

func (i *analysisIndex) providerSchemaWitness(resolved resolvedComposite) (ProviderSchemaFieldWitness, string) {
	flags := map[string]bool{"Required": false, "Optional": false, "Computed": false}
	for _, name := range []string{"Required", "Optional", "Computed"} {
		expression, present := compositeField(resolved.literal, name)
		if !present {
			continue
		}
		value, known := boolLiteral(expression, nil)
		if !known {
			return ProviderSchemaFieldWitness{}, name + " is not a static boolean"
		}
		flags[name] = value
	}
	validators := make([]string, 0, 2)
	for _, name := range []string{"ValidateFunc", "ValidateDiagFunc"} {
		if expression, present := compositeField(resolved.literal, name); present {
			validators = append(validators, goExpression(expression))
		}
	}
	validators = sortedUniqueStrings(validators)
	return ProviderSchemaFieldWitness{
		Required:   flags["Required"],
		Optional:   flags["Optional"],
		Computed:   flags["Computed"],
		Type:       providerSchemaType(resolved.literal),
		Validators: validators,
		Location:   i.loc(resolved.owner.file, resolved.owner.symbol, resolved.literal.Pos()),
	}, ""
}

func providerSchemaType(literal *ast.CompositeLit) string {
	expression, present := compositeField(literal, "Type")
	if !present {
		return ""
	}
	return goExpression(expression)
}

func compositeField(literal *ast.CompositeLit, name string) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == name {
			return field.Value, true
		}
	}
	return nil, false
}

func isHashicorpSchemaComposite(file *sourceFile, literal *ast.CompositeLit, name string) bool {
	expression := literal.Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	return hashicorpSchemaPackage(file.imports[alias.Name])
}

func hashicorpSchemaPackage(packagePath string) bool {
	return packagePath == "github.com/hashicorp/terraform-plugin-sdk/helper/schema" ||
		packagePath == "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
}

func (i *analysisIndex) readBackWitnesses(
	callback *function,
	diagnostics *[]FieldWitnessDiagnostic,
) map[string][]ReadBackFieldWitness {
	receiver, ok := resourceDataParameter(callback)
	if !ok {
		location := i.loc(callback.file, callback.symbol, callback.decl.Name.Pos())
		*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "resource_data_parameter_unresolved", Message: "Read callback has no statically named *schema.ResourceData parameter", Location: &location})
		return nil
	}
	witnesses := make(map[string][]ReadBackFieldWitness)
	ast.Inspect(callback.decl.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		call, callOK := node.(*ast.CallExpr)
		if !callOK {
			return true
		}
		selector, selectorOK := call.Fun.(*ast.SelectorExpr)
		if !selectorOK || selector.Sel.Name != "Set" {
			return true
		}
		identifier, receiverOK := selector.X.(*ast.Ident)
		if !receiverOK || identifier.Name != receiver {
			return true
		}
		if len(call.Args) != 2 {
			location := i.loc(callback.file, callback.symbol, call.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "read_back_call_invalid", Message: "ResourceData.Set call does not have exactly two arguments", Location: &location})
			return true
		}
		fieldPath, literal := stringLiteral(call.Args[0])
		if !literal || fieldPath == "" {
			location := i.loc(callback.file, callback.symbol, call.Args[0].Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "read_back_key_dynamic", Message: "ResourceData.Set field key is not a non-empty string literal", Location: &location})
			return true
		}
		witnesses[fieldPath] = append(witnesses[fieldPath], ReadBackFieldWitness{
			Expression: goExpression(call.Args[1]),
			Location:   i.loc(callback.file, callback.symbol, call.Pos()),
		})
		return true
	})
	return witnesses
}

func resourceDataParameter(callback *function) (string, bool) {
	if callback.decl.Type.Params == nil {
		return "", false
	}
	for _, parameter := range callback.decl.Type.Params.List {
		typeExpression := parameter.Type
		if pointer, ok := typeExpression.(*ast.StarExpr); ok {
			typeExpression = pointer.X
		} else {
			continue
		}
		selector, ok := typeExpression.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "ResourceData" {
			continue
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || !hashicorpSchemaPackage(callback.file.imports[alias.Name]) || len(parameter.Names) != 1 {
			continue
		}
		return parameter.Names[0].Name, true
	}
	return "", false
}

func (i *analysisIndex) collectAcceptanceWitnesses(
	resourceType string,
	constructor *function,
	field func(string) *fieldWitnessAccumulator,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	if !strings.HasSuffix(constructor.file.path, ".go") || strings.HasSuffix(constructor.file.path, "_test.go") {
		return
	}
	testPath := strings.TrimSuffix(constructor.file.path, ".go") + "_test.go"
	var testFile *sourceFile
	for _, candidate := range i.files {
		if candidate.origin == contracts.SourceLocationProvider && candidate.path == testPath {
			testFile = candidate
			break
		}
	}
	if testFile == nil {
		return
	}
	i.collectAcceptanceChecks(resourceType, testFile, field, diagnostics)
	i.collectAcceptanceConfigs(resourceType, testFile, field, diagnostics)
}

func (i *analysisIndex) collectAcceptanceChecks(
	resourceType string,
	file *sourceFile,
	field func(string) *fieldWitnessAccumulator,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	ast.Inspect(file.parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "TestCheckResourceAttr" {
			return true
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || !hashicorpResourceTestPackage(file.imports[alias.Name]) {
			return true
		}
		resourceAddress, addressStatic := staticString(file, call.Args[0])
		if addressStatic {
			addressType, _, _ := strings.Cut(resourceAddress, ".")
			if addressType != resourceType {
				return true
			}
		} else {
			resourceAddress = goExpression(call.Args[0])
			if resourceAddress == "" {
				return true
			}
		}
		statePath, literal := staticString(file, call.Args[1])
		if !literal || statePath == "" {
			location := i.loc(file, enclosingFunction(file.parsed, call.Pos()), call.Args[1].Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "acceptance_check_path_dynamic", Message: "TestCheckResourceAttr state path is not a non-empty static string", Location: &location})
			return true
		}
		expected, known := staticString(file, call.Args[2])
		if !known {
			expected = goExpression(call.Args[2])
		}
		fieldPath := normalizeStatePath(statePath)
		field(fieldPath).acceptanceChecks = append(field(fieldPath).acceptanceChecks, AcceptanceCheckFieldWitness{
			ResourceAddress:       resourceAddress,
			ResourceAddressStatic: addressStatic,
			Path:                  statePath,
			Expected:              expected,
			Location:              i.loc(file, enclosingFunction(file.parsed, call.Pos()), call.Pos()),
		})
		return true
	})
}

func hashicorpResourceTestPackage(packagePath string) bool {
	return packagePath == "github.com/hashicorp/terraform-plugin-sdk/helper/resource" ||
		packagePath == "github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
}

func staticString(file *sourceFile, expression ast.Expr) (string, bool) {
	if value, ok := stringLiteral(expression); ok {
		return value, true
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", false
	}
	value, ok := file.constants[identifier.Name]
	return value, ok
}

func normalizeStatePath(statePath string) string {
	if strings.HasSuffix(statePath, ".#") || strings.HasSuffix(statePath, ".%") {
		return statePath[:len(statePath)-2]
	}
	parts := strings.Split(statePath, ".")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err == nil && len(normalized) > 0 {
			normalized[len(normalized)-1] += "[]"
			continue
		}
		normalized = append(normalized, part)
	}
	return strings.Join(normalized, ".")
}

func enclosingFunction(file *ast.File, position token.Pos) string {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Pos() <= position && position <= function.End() {
			return function.Name.Name
		}
	}
	return ""
}

type hclConfigFact struct {
	occurrences     int
	parentInstances int
	syntax          string
	values          []string
}

const (
	acceptanceConfigSyntaxAttribute = "attribute"
	acceptanceConfigSyntaxBlock     = "block"
	acceptanceConfigSyntaxMixed     = "mixed"
)

func (i *analysisIndex) collectAcceptanceConfigs(
	resourceType string,
	file *sourceFile,
	field func(string) *fieldWitnessAccumulator,
	diagnostics *[]FieldWitnessDiagnostic,
) {
	ast.Inspect(file.parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		config, ok := stringLiteral(literal)
		if !ok || !looksLikeHCLConfig(config) {
			return true
		}
		sanitized := sanitizeFormatVerbs(config)
		parsed, parseDiagnostics := hclsyntax.ParseConfig([]byte(sanitized), file.path, hcl.InitialPos)
		if parseDiagnostics.HasErrors() {
			location := i.loc(file, enclosingFunction(file.parsed, literal.Pos()), literal.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "acceptance_config_unparsed", Message: parseDiagnostics.Error(), Location: &location})
			return true
		}
		body, bodyOK := parsed.Body.(*hclsyntax.Body)
		if !bodyOK {
			return true
		}
		blocks, selectionIssue := selectedResourceBlocks(body, resourceType)
		if selectionIssue != "" {
			location := i.loc(file, enclosingFunction(file.parsed, literal.Pos()), literal.Pos())
			*diagnostics = append(*diagnostics, FieldWitnessDiagnostic{Code: "acceptance_resource_ambiguous", Message: selectionIssue, Location: &location})
		}
		if len(blocks) == 0 {
			return true
		}
		bodies := make([]*hclsyntax.Body, 0, len(blocks))
		for _, block := range blocks {
			bodies = append(bodies, block.Body)
		}
		facts := make(map[string]*hclConfigFact)
		// Sanitization is length-preserving, so parser ranges can recover the
		// author-written expressions from the original format string.
		collectHCLInstances(bodies, "", []byte(config), facts)
		location := i.loc(file, enclosingFunction(file.parsed, literal.Pos()), literal.Pos())
		for fieldPath, fact := range facts {
			field(fieldPath).acceptanceConfigs = append(field(fieldPath).acceptanceConfigs, AcceptanceConfigFieldWitness{
				Occurrences:     fact.occurrences,
				ParentInstances: fact.parentInstances,
				Syntax:          fact.syntax,
				Values:          sortedUniqueStrings(fact.values),
				Location:        location,
			})
		}
		return true
	})
}

func looksLikeHCLConfig(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "resource ") {
			return true
		}
	}
	return false
}

func selectedResourceBlocks(body *hclsyntax.Body, resourceType string) ([]*hclsyntax.Block, string) {
	resources := make([]*hclsyntax.Block, 0)
	exact := make([]*hclsyntax.Block, 0)
	dynamic := make([]*hclsyntax.Block, 0)
	for _, block := range body.Blocks {
		if block.Type != "resource" || len(block.Labels) < 1 {
			continue
		}
		resources = append(resources, block)
		if block.Labels[0] == resourceType {
			exact = append(exact, block)
		}
		if strings.Trim(block.Labels[0], "x") == "" {
			dynamic = append(dynamic, block)
		}
	}
	if len(exact) != 0 {
		return exact, ""
	}
	// A single dynamic resource label in the exact constructor-companion test
	// file is accepted as usage evidence. Multiple resources remain silent.
	if len(resources) == 1 && len(dynamic) == 1 {
		return resources, ""
	}
	if len(dynamic) != 0 {
		return nil, "acceptance config has multiple resource blocks and its formatted resource label cannot be tied uniquely to the selected resource"
	}
	return nil, ""
}

func collectHCLInstances(bodies []*hclsyntax.Body, prefix string, source []byte, facts map[string]*hclConfigFact) {
	for _, body := range bodies {
		for name, attribute := range body.Attributes {
			if prefix == "" && isTerraformResourceMetaArgument(name) {
				continue
			}
			fieldPath := joinFieldPath(prefix, name)
			fact := ensureHCLFact(facts, fieldPath)
			fact.occurrences++
			fact.syntax = mergeAcceptanceConfigSyntax(fact.syntax, acceptanceConfigSyntaxAttribute)
			if value := hclExpressionSource(attribute.Expr, source); value != "" {
				fact.values = append(fact.values, value)
			}
		}
	}
	for fieldPath, fact := range facts {
		if prefix == "" || strings.HasPrefix(fieldPath, prefix+".") {
			if fact.parentInstances == 0 {
				fact.parentInstances = len(bodies)
			}
		}
	}

	grouped := make(map[string][]*hclsyntax.Block)
	for _, body := range bodies {
		for _, block := range body.Blocks {
			grouped[block.Type] = append(grouped[block.Type], block)
		}
	}
	for _, name := range sortedHCLBlockKeys(grouped) {
		blocks := grouped[name]
		fieldPath := joinFieldPath(prefix, name)
		fact := ensureHCLFact(facts, fieldPath)
		fact.occurrences += len(blocks)
		fact.parentInstances = len(bodies)
		fact.syntax = mergeAcceptanceConfigSyntax(fact.syntax, acceptanceConfigSyntaxBlock)
		childBodies := make([]*hclsyntax.Body, 0, len(blocks))
		for _, block := range blocks {
			childBodies = append(childBodies, block.Body)
		}
		collectHCLInstances(childBodies, fieldPath+"[]", source, facts)
	}
}

func isTerraformResourceMetaArgument(name string) bool {
	switch name {
	case "count", "depends_on", "for_each", "provider":
		return true
	default:
		return false
	}
}

func ensureHCLFact(facts map[string]*hclConfigFact, fieldPath string) *hclConfigFact {
	if facts[fieldPath] == nil {
		facts[fieldPath] = &hclConfigFact{}
	}
	return facts[fieldPath]
}

func mergeAcceptanceConfigSyntax(current, next string) string {
	switch {
	case current == "":
		return next
	case current == next:
		return current
	default:
		return acceptanceConfigSyntaxMixed
	}
}

func sortedHCLBlockKeys(blocks map[string][]*hclsyntax.Block) []string {
	keys := make([]string, 0, len(blocks))
	for key := range blocks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hclExpressionSource(expression hclsyntax.Expression, source []byte) string {
	rangeValue := expression.Range()
	if rangeValue.Start.Byte < 0 || rangeValue.End.Byte < rangeValue.Start.Byte || rangeValue.End.Byte > len(source) {
		return ""
	}
	value := strings.TrimSpace(string(source[rangeValue.Start.Byte:rangeValue.End.Byte]))
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
}

func sanitizeFormatVerbs(value string) string {
	bytes := []byte(value)
	for index := 0; index+1 < len(bytes); index++ {
		if bytes[index] == '%' && strings.ContainsRune("sdqv", rune(bytes[index+1])) {
			bytes[index], bytes[index+1] = 'x', 'x'
			index++
		}
	}
	return string(bytes)
}

func finalizeFieldWitness(fieldPath string, accumulated *fieldWitnessAccumulator) FieldWitness {
	witness := FieldWitness{
		TerraformSchema:   accumulated.terraformSchema,
		ProviderSchemas:   append([]ProviderSchemaFieldWitness(nil), accumulated.providerSchemas...),
		ReadBacks:         append([]ReadBackFieldWitness(nil), accumulated.readBacks...),
		AcceptanceConfigs: append([]AcceptanceConfigFieldWitness(nil), accumulated.acceptanceConfigs...),
		AcceptanceChecks:  append([]AcceptanceCheckFieldWitness(nil), accumulated.acceptanceChecks...),
		Conflicts:         append([]string(nil), accumulated.conflicts...),
	}
	if witness.ProviderSchemas == nil {
		witness.ProviderSchemas = []ProviderSchemaFieldWitness{}
	}
	if witness.ReadBacks == nil {
		witness.ReadBacks = []ReadBackFieldWitness{}
	}
	if witness.AcceptanceConfigs == nil {
		witness.AcceptanceConfigs = []AcceptanceConfigFieldWitness{}
	}
	if witness.AcceptanceChecks == nil {
		witness.AcceptanceChecks = []AcceptanceCheckFieldWitness{}
	}

	if witness.TerraformSchema != nil {
		for _, provider := range witness.ProviderSchemas {
			compareFieldFlag(fieldPath, "required", witness.TerraformSchema.Required, provider.Required, &witness.Conflicts)
			compareFieldFlag(fieldPath, "optional", witness.TerraformSchema.Optional, provider.Optional, &witness.Conflicts)
			compareFieldFlag(fieldPath, "computed", witness.TerraformSchema.Computed, provider.Computed, &witness.Conflicts)
		}
	}
	if expected, declared, comparable := comparableAcceptanceBlockCounts(witness); comparable && expected != declared {
		witness.Conflicts = append(witness.Conflicts, fmt.Sprintf("%s acceptance check expects block count %d but config declares %d blocks", fieldPath, expected, declared))
	}
	witness.Conflicts = sortedUniqueStrings(witness.Conflicts)
	sortProviderSchemaWitnesses(witness.ProviderSchemas)
	sortReadBackWitnesses(witness.ReadBacks)
	sortAcceptanceConfigWitnesses(witness.AcceptanceConfigs)
	sortAcceptanceCheckWitnesses(witness.AcceptanceChecks)

	classes := 0
	if witness.TerraformSchema != nil {
		classes++
	}
	for _, present := range []bool{len(witness.ProviderSchemas) != 0, len(witness.ReadBacks) != 0, len(witness.AcceptanceConfigs) != 0, len(witness.AcceptanceChecks) != 0} {
		if present {
			classes++
		}
	}
	switch {
	case len(witness.Conflicts) != 0:
		witness.Disposition = FieldWitnessConflicting
	case classes >= 2:
		witness.Disposition = FieldWitnessCorroborated
	default:
		witness.Disposition = FieldWitnessUntested
	}
	return witness
}

func fieldWitnessReviewQueue(fields map[string]FieldWitness, diagnostics []FieldWitnessDiagnostic) []FieldWitnessReviewItem {
	fieldDiagnostics := make(map[string][]FieldWitnessDiagnostic)
	paths := make(map[string]struct{}, len(fields))
	for fieldPath := range fields {
		paths[fieldPath] = struct{}{}
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.FieldPath == "" {
			continue
		}
		fieldDiagnostics[diagnostic.FieldPath] = append(fieldDiagnostics[diagnostic.FieldPath], diagnostic)
		paths[diagnostic.FieldPath] = struct{}{}
	}

	orderedPaths := make([]string, 0, len(paths))
	for fieldPath := range paths {
		orderedPaths = append(orderedPaths, fieldPath)
	}
	sort.Strings(orderedPaths)

	queue := make([]FieldWitnessReviewItem, 0)
	for _, fieldPath := range orderedPaths {
		witness, hasWitness := fields[fieldPath]
		item := FieldWitnessReviewItem{
			FieldPath:            fieldPath,
			ReasonCodes:          []string{},
			Details:              []string{},
			AbsentWitnessClasses: absentFieldWitnessClasses(witness, hasWitness),
		}

		if hasWitness && (witness.Disposition == FieldWitnessConflicting || len(witness.Conflicts) != 0) {
			item.Priority = FieldWitnessReviewHigh
			item.ReasonCodes = append(item.ReasonCodes, fieldWitnessConflictReasonCodes(witness)...)
			item.Details = append(item.Details, witness.Conflicts...)
			item.SuggestedValidation = "Resolve the listed witness disagreement; run a targeted provider apply-and-refresh round-trip when source evidence cannot decide it."
		}

		if associated := fieldDiagnostics[fieldPath]; len(associated) != 0 {
			item.Priority = higherFieldWitnessReviewPriority(item.Priority, FieldWitnessReviewMedium)
			item.ReasonCodes = append(item.ReasonCodes, "source_analysis_incomplete")
			for _, diagnostic := range associated {
				item.ReasonCodes = append(item.ReasonCodes, diagnostic.Code)
				item.Details = append(item.Details, diagnostic.Message)
			}
			if item.SuggestedValidation == "" {
				item.SuggestedValidation = "Inspect the cited source shape and recover the missing witness; if it remains dynamic, run a targeted provider round-trip for this field."
			}
		}

		if hasWitness && witness.Disposition == FieldWitnessUntested {
			reasons, details, priority, validation := fieldWitnessBehaviorReview(witness)
			item.ReasonCodes = append(item.ReasonCodes, reasons...)
			item.Details = append(item.Details, details...)
			item.Priority = higherFieldWitnessReviewPriority(item.Priority, priority)
			if len(fieldDiagnostics[fieldPath]) == 0 {
				item.Priority = higherFieldWitnessReviewPriority(item.Priority, FieldWitnessReviewLow)
				item.ReasonCodes = append(item.ReasonCodes, "evidence_silence")
				item.Details = append(item.Details, "Fewer than two witness classes were recovered; absent classes remain silence, not negative evidence.")
				item.SuggestedValidation = validation
			}
		}

		if item.Priority == "" {
			continue
		}
		item.ReasonCodes = sortedUniqueStrings(item.ReasonCodes)
		item.Details = sortedUniqueStrings(item.Details)
		if item.SuggestedValidation == "" {
			item.SuggestedValidation = "Review the absent witness classes; if adoption behavior depends on this field, run a targeted provider apply-and-refresh round-trip."
		}
		queue = append(queue, item)
	}

	sort.Slice(queue, func(left, right int) bool {
		leftPriority := fieldWitnessReviewPriorityRank(queue[left].Priority)
		rightPriority := fieldWitnessReviewPriorityRank(queue[right].Priority)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		leftReason := fieldWitnessReviewReasonRank(queue[left].ReasonCodes)
		rightReason := fieldWitnessReviewReasonRank(queue[right].ReasonCodes)
		if leftReason != rightReason {
			return leftReason < rightReason
		}
		return queue[left].FieldPath < queue[right].FieldPath
	})
	return queue
}

func absentFieldWitnessClasses(witness FieldWitness, present bool) []string {
	absent := make([]string, 0, 5)
	if !present || witness.TerraformSchema == nil {
		absent = append(absent, "terraform_schema")
	}
	if !present || len(witness.ProviderSchemas) == 0 {
		absent = append(absent, "provider_schema")
	}
	if !present || len(witness.ReadBacks) == 0 {
		absent = append(absent, "read_back")
	}
	if !present || len(witness.AcceptanceConfigs) == 0 {
		absent = append(absent, "acceptance_config")
	}
	if !present || len(witness.AcceptanceChecks) == 0 {
		absent = append(absent, "acceptance_check")
	}
	return absent
}

func fieldWitnessConflictReasonCodes(witness FieldWitness) []string {
	reasons := []string{"witness_conflict"}
	if witness.TerraformSchema != nil {
		for _, provider := range witness.ProviderSchemas {
			if fieldFlagDisagrees(witness.TerraformSchema.Required, provider.Required) ||
				fieldFlagDisagrees(witness.TerraformSchema.Optional, provider.Optional) ||
				fieldFlagDisagrees(witness.TerraformSchema.Computed, provider.Computed) {
				reasons = append(reasons, "schema_flag_mismatch")
				break
			}
		}
	}
	if expected, declared, comparable := comparableAcceptanceBlockCounts(witness); comparable && expected != declared {
		reasons = append(reasons, "acceptance_block_count_mismatch")
	}
	return reasons
}

func fieldFlagDisagrees(terraform *bool, provider bool) bool {
	return terraform != nil && *terraform != provider
}

func fieldWitnessBehaviorReview(witness FieldWitness) ([]string, []string, FieldWitnessReviewPriority, string) {
	optionalComputed := witness.TerraformSchema != nil &&
		witness.TerraformSchema.Optional != nil && *witness.TerraformSchema.Optional &&
		witness.TerraformSchema.Computed != nil && *witness.TerraformSchema.Computed
	computed := witness.TerraformSchema != nil &&
		witness.TerraformSchema.Computed != nil && *witness.TerraformSchema.Computed
	validators := make([]string, 0)
	for _, provider := range witness.ProviderSchemas {
		optionalComputed = optionalComputed || provider.Optional && provider.Computed
		computed = computed || provider.Computed
		validators = append(validators, provider.Validators...)
	}
	validators = sortedUniqueStrings(validators)

	reasons := make([]string, 0, 2)
	details := make([]string, 0, 2)
	priority := FieldWitnessReviewPriority("")
	switch {
	case optionalComputed:
		reasons = append(reasons, "optional_computed_round_trip")
		details = append(details, "Captured schema marks the field Optional+Computed.")
		priority = FieldWitnessReviewMedium
	case computed:
		reasons = append(reasons, "computed_round_trip")
		details = append(details, "Captured schema marks the field Computed.")
		priority = FieldWitnessReviewMedium
	}
	if len(validators) != 0 {
		reasons = append(reasons, "validator_write_behavior")
		details = append(details, "Captured provider validators: "+strings.Join(validators, ", ")+".")
		priority = higherFieldWitnessReviewPriority(priority, FieldWitnessReviewMedium)
	}
	if optionalComputed && len(validators) != 0 {
		priority = FieldWitnessReviewHigh
		return reasons, details, priority, "Omit the field, apply, and refresh; then exercise accepted and rejected validator boundary values to verify ownership, round-trip, and write rejection."
	}
	if optionalComputed || computed {
		return reasons, details, priority, "Omit the field, apply, and refresh to verify provider ownership and read-back behavior."
	}
	if len(validators) != 0 {
		return reasons, details, priority, "Exercise an accepted boundary value and a rejected value from the captured validator, then refresh to verify write behavior."
	}
	return reasons, details, priority, "Review the absent witness classes; if adoption behavior depends on this field, run a targeted provider apply-and-refresh round-trip."
}

func higherFieldWitnessReviewPriority(current, candidate FieldWitnessReviewPriority) FieldWitnessReviewPriority {
	if current == "" || fieldWitnessReviewPriorityRank(candidate) < fieldWitnessReviewPriorityRank(current) {
		return candidate
	}
	return current
}

func fieldWitnessReviewPriorityRank(priority FieldWitnessReviewPriority) int {
	switch priority {
	case FieldWitnessReviewHigh:
		return 0
	case FieldWitnessReviewMedium:
		return 1
	case FieldWitnessReviewLow:
		return 2
	default:
		return 3
	}
}

func fieldWitnessReviewReasonRank(reasons []string) int {
	rank := 4
	for _, reason := range reasons {
		switch reason {
		case "witness_conflict":
			return 0
		case "source_analysis_incomplete":
			if rank > 1 {
				rank = 1
			}
		case "optional_computed_round_trip", "computed_round_trip", "validator_write_behavior":
			if rank > 2 {
				rank = 2
			}
		case "evidence_silence":
			if rank > 3 {
				rank = 3
			}
		}
	}
	return rank
}

// comparableAcceptanceBlockCounts requires one block-form config and one
// consistent static expected count. Anything less tightly associated remains
// corroborating evidence without becoming a conflict claim.
func comparableAcceptanceBlockCounts(witness FieldWitness) (int, int, bool) {
	if len(witness.AcceptanceConfigs) != 1 || witness.AcceptanceConfigs[0].Syntax != acceptanceConfigSyntaxBlock {
		return 0, 0, false
	}
	var expected int
	found := false
	for _, check := range witness.AcceptanceChecks {
		if !strings.HasSuffix(check.Path, ".#") {
			continue
		}
		value, err := strconv.Atoi(check.Expected)
		if err != nil || (found && value != expected) {
			return 0, 0, false
		}
		expected = value
		found = true
	}
	return expected, witness.AcceptanceConfigs[0].Occurrences, found
}

func compareFieldFlag(fieldPath, name string, terraform *bool, provider bool, conflicts *[]string) {
	if terraform != nil && *terraform != provider {
		*conflicts = append(*conflicts, fmt.Sprintf("%s %s is %t in Terraform schema and %t in provider schema", fieldPath, name, *terraform, provider))
	}
}

func sortProviderSchemaWitnesses(values []ProviderSchemaFieldWitness) {
	sort.Slice(values, func(left, right int) bool {
		return locationKey(values[left].Location) < locationKey(values[right].Location)
	})
}

func sortReadBackWitnesses(values []ReadBackFieldWitness) {
	sort.Slice(values, func(left, right int) bool {
		return locationKey(values[left].Location) < locationKey(values[right].Location)
	})
}

func sortAcceptanceConfigWitnesses(values []AcceptanceConfigFieldWitness) {
	for index := range values {
		values[index].Values = sortedUniqueStrings(values[index].Values)
	}
	sort.Slice(values, func(left, right int) bool {
		return locationKey(values[left].Location) < locationKey(values[right].Location)
	})
}

func sortAcceptanceCheckWitnesses(values []AcceptanceCheckFieldWitness) {
	sort.Slice(values, func(left, right int) bool {
		if locationKey(values[left].Location) != locationKey(values[right].Location) {
			return locationKey(values[left].Location) < locationKey(values[right].Location)
		}
		return values[left].Path+"\x00"+values[left].Expected < values[right].Path+"\x00"+values[right].Expected
	})
}

func sortFieldWitnessDiagnostics(values []FieldWitnessDiagnostic) {
	sort.Slice(values, func(left, right int) bool {
		leftLocation, rightLocation := "", ""
		if values[left].Location != nil {
			leftLocation = locationKey(*values[left].Location)
		}
		if values[right].Location != nil {
			rightLocation = locationKey(*values[right].Location)
		}
		return values[left].Code+"\x00"+values[left].FieldPath+"\x00"+leftLocation+"\x00"+values[left].Message < values[right].Code+"\x00"+values[right].FieldPath+"\x00"+rightLocation+"\x00"+values[right].Message
	})
}

func locationKey(location contracts.SourceLocation) string {
	return fmt.Sprintf("%s\x00%s\x00%09d\x00%09d", location.Origin, location.Path, location.Line, location.Column)
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func goExpression(expression ast.Expr) string {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), expression); err != nil {
		return ""
	}
	return buffer.String()
}
