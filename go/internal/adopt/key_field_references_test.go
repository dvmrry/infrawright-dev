package adopt

import (
	"encoding/json"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

func keyReferenceOverride() metadata.JsonObject {
	return metadata.JsonObject{
		"key_field": []any{"parent_id", "name"},
		"key_field_references": metadata.JsonObject{
			"parent_id": metadata.JsonObject{"referent": "sample_location", "name_field": "name"},
		},
	}
}

func keyReferenceRawItems() []any {
	return []any{
		map[string]any{"id": json.Number("100"), "name": "GLOBAL_PSEN", "parent_id": json.Number("0")},
		map[string]any{"id": json.Number("200"), "name": "EDGE_LON", "parent_id": json.Number("0")},
		map[string]any{"id": json.Number("101"), "name": "IoT Device Segments", "parent_id": json.Number("100")},
		map[string]any{"id": json.Number("201"), "name": "IoT Device Segments", "parent_id": json.Number("200")},
	}
}

func keyReferenceResource() metadata.LoadedResourceMetadata {
	return metadata.LoadedResourceMetadata{
		Type: "sample_location", Product: "sample", Provider: "sample",
		Override: keyReferenceOverride(),
	}
}

// The lanes derive keys through entirely separate code paths, so the contract
// that matters is that they agree: the same object must land under one
// identity whether it arrived through transform or adopt.
func TestAdoptionAndTransformDeriveIdenticalResolvedKeys(t *testing.T) {
	identities, err := DeriveAdoptionIdentities(keyReferenceRawItems(), keyReferenceResource())
	if err != nil {
		t.Fatalf("DeriveAdoptionIdentities: %v", err)
	}
	adoptKeys := make([]string, 0, len(identities.Identities))
	for _, identity := range identities.Identities {
		adoptKeys = append(adoptKeys, identity.Key)
	}
	adoptKeys = canonjson.SortedStrings(adoptKeys)

	attribute := func(t string) metadata.JsonObject {
		return metadata.JsonObject{"type": t, "optional": true}
	}
	transformed, err := transform.TransformLoadedItems(transform.TransformLoadedItemsOptions{
		Resource: keyReferenceResource(),
		Schema: metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"id":        metadata.JsonObject{"type": "number", "computed": true},
			"name":      attribute("string"),
			"parent_id": attribute("number"),
		}}},
		RawItems:     keyReferenceRawItems(),
		HTMLUnescape: func(s string) string { return s },
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	transformKeys := make([]string, 0, len(transformed.Items))
	for key := range transformed.Items {
		transformKeys = append(transformKeys, key)
	}
	transformKeys = canonjson.SortedStrings(transformKeys)

	want := []string{
		"edge_lon", "edge_lon_iot_device_segments",
		"global_psen", "global_psen_iot_device_segments",
	}
	if len(adoptKeys) != len(want) {
		t.Fatalf("adopt keys = %v, want %v", adoptKeys, want)
	}
	for i, key := range want {
		if adoptKeys[i] != key {
			t.Errorf("adopt key[%d] = %q, want %q", i, adoptKeys[i], key)
		}
		if transformKeys[i] != key {
			t.Errorf("transform key[%d] = %q, want %q", i, transformKeys[i], key)
		}
	}
}

func TestAdoptionResolvedKeysRefuseCrossTypeReferent(t *testing.T) {
	resource := keyReferenceResource()
	resource.Override = metadata.JsonObject{
		"key_field": []any{"parent_id", "name"},
		"key_field_references": metadata.JsonObject{
			"parent_id": metadata.JsonObject{"referent": "other_type", "name_field": "name"},
		},
	}
	if _, err := DeriveAdoptionIdentities(keyReferenceRawItems(), resource); err == nil {
		t.Fatal("cross-type key referent accepted in adoption, want refusal")
	}
}
