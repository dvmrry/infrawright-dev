package assessment

import (
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// PlanSchemaTypes records which top-level attributes of each resource type the
// provider declares as Terraform sets.
//
// A set has no element order: Terraform compares two set values by membership,
// so two serializations with the same members are the same value, and a
// membership change reshuffles every position after the one that moved. The
// plan JSON alone cannot distinguish that from an ordered list -- both are JSON
// arrays -- which is why comparing every array positionally reports a five-member
// edit of a 7,650-member set as thousands of unrelated retargetings. The
// provider schema is the only place the distinction exists, and this type is
// how it reaches the classifier.
//
// The zero value declares nothing a set, which is exactly the positional
// behaviour that predates this type. Every field is therefore opt-in and no
// caller that does not supply a schema changes behaviour.
type PlanSchemaTypes struct {
	setAttributes map[string]map[string]struct{}
}

// A set carrying a sensitive member is a case the clearing rule never reaches.
// Terraform's sensitivity masks are positional arrays, so reordering such a set
// reorders its mask, updatePathsFrom sees the mask move, and the record blocks
// on SensitivityChange before set equality is consulted. That is fail-closed
// and correct, but it means "a set whose members match is reported clean" holds
// only for sets with no sensitive member.

// SetAttributes returns the top-level set-typed attribute names for one
// resource type. A nil result means every array on that resource is compared by
// position, which is what an unknown resource type gets: absent schema evidence
// the walk over-reports rather than assuming order is meaningless.
func (types PlanSchemaTypes) SetAttributes(resourceType string) map[string]struct{} {
	if types.setAttributes == nil {
		return nil
	}
	return types.setAttributes[resourceType]
}

// Empty reports whether no resource type declares a set attribute, which is
// true of the zero value.
func (types PlanSchemaTypes) Empty() bool {
	return len(types.setAttributes) == 0
}

// NewPlanSchemaTypes reads the set-typed top-level attributes of every resource
// type active in root.
//
// Only top-level attributes are read, and that is a scope limit rather than a
// principle. It would be wrong to claim nested collections are positional: a
// block_types entry with nesting_mode "set" is tracked by element hash exactly
// as a set attribute is, and the installed packs carry hundreds of them (318 in
// zia alone, plus 254 set attributes nested inside blocks -- more nested
// surfaces than top-level ones). The reported defect is a top-level attribute,
// this reads top-level attributes, and everything else stays positional, which
// over-reports rather than under-reports. Extending to nested set blocks is
// tracked separately; until then the limitation is here, in the open.
//
// Note that the same schema can spell one concept two ways: a plugin-framework
// nested_type attribute with mode "set" arrives through TerraformAttributeType
// as a TerraformCollectionType and is therefore already collapsed, while an
// SDKv2 block_types entry with nesting_mode "set" is not, because this function
// never reads block_types. That inconsistency is a consequence of the scope
// limit above, not a deliberate distinction.
//
// A schema this cannot read is an error rather than a resource type quietly
// left positional: the difference between the two is thousands of findings, and
// discovering it from a report is far worse than discovering it here.
func NewPlanSchemaTypes(root metadata.LoadedPackRoot) (PlanSchemaTypes, error) {
	setAttributes := make(map[string]map[string]struct{}, len(root.Resources))
	// Sorted, so the error a malformed pack produces is always the same one.
	// Ranging the map directly returns whichever of several bad resource types
	// Go's randomised iteration reached first, which makes the failure message
	// -- and the report digest carrying it -- differ between identical runs.
	resourceTypes := make([]string, 0, len(root.Resources))
	for resourceType := range root.Resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	resourceTypes = canonjson.SortedStrings(resourceTypes)
	for _, resourceType := range resourceTypes {
		schema, err := root.LoadResourceSchema(resourceType)
		if err != nil {
			return PlanSchemaTypes{}, err
		}
		block, err := metadata.TerraformBlockForSchema(schema, resourceType)
		if err != nil {
			return PlanSchemaTypes{}, err
		}
		attributes, err := metadata.TerraformAttributesForBlock(block, resourceType)
		if err != nil {
			return PlanSchemaTypes{}, err
		}
		names := make(map[string]struct{})
		for name, rawAttribute := range attributes {
			attribute, err := metadata.TerraformRequireObject(
				rawAttribute,
				resourceType+".attributes."+name,
			)
			if err != nil {
				return PlanSchemaTypes{}, err
			}
			encoding, err := metadata.TerraformAttributeType(
				attribute,
				resourceType+".attributes."+name,
			)
			if err != nil {
				return PlanSchemaTypes{}, err
			}
			collection, isCollection := encoding.(metadata.TerraformCollectionType)
			if isCollection && collection.Kind == "set" {
				names[name] = struct{}{}
			}
		}
		if len(names) > 0 {
			setAttributes[resourceType] = names
		}
	}
	return PlanSchemaTypes{setAttributes: setAttributes}, nil
}
