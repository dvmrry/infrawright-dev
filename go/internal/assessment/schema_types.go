package assessment

import (
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
// Only top-level attributes are read. A collection nested inside a block keeps
// its positional identity in the plan -- Terraform addresses block instances by
// index -- so collapsing one would lose the address a reviewer needs. The
// resource-level attribute is the only place where a whole set is one value
// with one name.
//
// A schema this cannot read is an error rather than a resource type quietly
// left positional: the difference between the two is thousands of findings, and
// discovering it from a report is far worse than discovering it here.
func NewPlanSchemaTypes(root metadata.LoadedPackRoot) (PlanSchemaTypes, error) {
	setAttributes := make(map[string]map[string]struct{}, len(root.Resources))
	for resourceType := range root.Resources {
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
