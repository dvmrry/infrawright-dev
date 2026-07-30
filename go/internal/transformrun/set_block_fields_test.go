package transformrun

import (
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

func setBlockFieldsSchema() metadata.JsonObject {
	return metadata.JsonObject{"block": metadata.JsonObject{
		"attributes": metadata.JsonObject{
			"url_categories": metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
		},
		"block_types": metadata.JsonObject{
			// The wave-2 blocker shape: a true multi set block.
			"services": metadata.JsonObject{
				"nesting_mode": "set",
				"block": metadata.JsonObject{"attributes": metadata.JsonObject{
					"id": metadata.JsonObject{"required": true, "type": []any{"set", "number"}},
				}},
			},
			// A set block collapsed to one object by max_items=1: the
			// schema-path validator traverses it as single, so it must not
			// be classified a set here.
			"labels": metadata.JsonObject{
				"nesting_mode": "set", "max_items": float64(1),
				"block": metadata.JsonObject{"attributes": metadata.JsonObject{
					"id": metadata.JsonObject{"required": true, "type": []any{"set", "number"}},
				}},
			},
			// An ordered list block: indexed traversal is valid.
			"rules": metadata.JsonObject{
				"nesting_mode": "list",
				"block": metadata.JsonObject{
					"attributes": metadata.JsonObject{
						"name": metadata.JsonObject{"required": true, "type": "string"},
					},
					"block_types": metadata.JsonObject{
						// A set nested under the list: the first set segment
						// is index 1.
						"targets": metadata.JsonObject{
							"nesting_mode": "set",
							"block": metadata.JsonObject{"attributes": metadata.JsonObject{
								"id": metadata.JsonObject{"required": true, "type": "number"},
							}},
						},
					},
				},
			},
		},
	}}
}

// TestSetBlockFieldIndexesMirrorsTheSchemaPathValidator pins the
// classification that keeps the binding producer and gen-env's schema-path
// validator agreeing. The producer skipping what the validator accepts is the
// same defect as the producer emitting what the validator refuses -- both are
// the two halves of the engine disagreeing about one schema.
func TestSetBlockFieldIndexesMirrorsTheSchemaPathValidator(t *testing.T) {
	references := map[string]tfrender.TransformReferenceSpec{
		"services.id":      {NameField: "name", Referent: "zia_x"},
		"labels.id":        {NameField: "name", Referent: "zia_y"},
		"rules.targets.id": {NameField: "name", Referent: "zia_z"},
		"url_categories":   {NameField: "configured_name", Referent: "zia_url_categories"},
		"absent_field.id":  {NameField: "name", Referent: "zia_w"},
	}
	got, err := setBlockFieldIndexes(setBlockFieldsSchema(), "zia_sample", references)
	if err != nil {
		t.Fatalf("setBlockFieldIndexes: %v", err)
	}
	want := map[string]int{
		// A true multi set marks its own segment.
		"services.id": 0,
		// A set nested under a list marks the set, not the list.
		"rules.targets.id": 1,
		// max_items=1 set blocks traverse as single; attributes and unknown
		// segments never mark anything.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setBlockFieldIndexes = %#v, want %#v", got, want)
	}
}
