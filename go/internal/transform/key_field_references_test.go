package transform

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// locationLikeItems mirrors the ZIA shape this surface exists for: parents
// carrying the sentinel parent_id 0, and children under two different parents
// sharing one name -- the collision that makes a bare name key ambiguous.
func locationLikeItems() []any {
	return []any{
		map[string]any{"id": json.Number("100"), "name": "GLOBAL_PSEN", "parentId": json.Number("0")},
		map[string]any{"id": json.Number("200"), "name": "EDGE_LON", "parentId": json.Number("0")},
		map[string]any{"id": json.Number("101"), "name": "IoT Device Segments", "parentId": json.Number("100")},
		map[string]any{"id": json.Number("201"), "name": "IoT Device Segments", "parentId": json.Number("200")},
	}
}

func locationLikeSchema() metadata.JsonObject {
	attribute := func(t string) metadata.JsonObject {
		return metadata.JsonObject{"type": t, "optional": true}
	}
	return metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
		"id":        metadata.JsonObject{"type": "number", "computed": true},
		"name":      attribute("string"),
		"parent_id": attribute("number"),
	}}}
}

func transformLocationLike(t *testing.T, override metadata.JsonObject) PullTransformResult {
	t.Helper()
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: metadata.LoadedResourceMetadata{
			Type: "sample_location", Product: "sample", Provider: "sample", Override: override,
		},
		Schema:       locationLikeSchema(),
		RawItems:     locationLikeItems(),
		HTMLUnescape: func(s string) string { return s },
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	return result
}

func TestKeyFieldReferencesResolveParentNameAndLeaveParentsAlone(t *testing.T) {
	result := transformLocationLike(t, metadata.JsonObject{
		"key_field": []any{"parent_id", "name"},
		"key_field_references": metadata.JsonObject{
			"parent_id": metadata.JsonObject{"referent": "sample_location", "name_field": "name"},
		},
	})
	got := canonjson.SortedStrings(mapKeys(result.Items))
	want := []string{
		"edge_lon",
		"edge_lon_iot_device_segments",
		"global_psen",
		"global_psen_iot_device_segments",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived keys = %v, want %v", got, want)
	}
}

// The real migration: before follow_paths only parents were fetched and keyed
// by plain name. After it, parents and children arrive together. Every parent
// must keep the exact key its committed config already carries, or adopting
// sublocations silently re-keys production objects.
func TestKeyFieldReferencesLeaveParentKeysByteIdentical(t *testing.T) {
	parentsOnly, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: metadata.LoadedResourceMetadata{
			Type: "sample_location", Product: "sample", Provider: "sample",
			Override: metadata.JsonObject{},
		},
		Schema: locationLikeSchema(),
		RawItems: []any{
			locationLikeItems()[0], locationLikeItems()[1],
		},
		HTMLUnescape: func(s string) string { return s },
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems(parents only): %v", err)
	}
	plain := parentsOnly
	resolved := transformLocationLike(t, metadata.JsonObject{
		"key_field": []any{"parent_id", "name"},
		"key_field_references": metadata.JsonObject{
			"parent_id": metadata.JsonObject{"referent": "sample_location", "name_field": "name"},
		},
	})
	for parent := range plain.Items {
		if _, ok := resolved.Items[parent]; !ok {
			t.Errorf("adopting sublocations re-keyed parent %q; resolved keys = %v",
				parent, canonjson.SortedStrings(mapKeys(resolved.Items)))
		}
	}
	if len(plain.Items) != 2 {
		t.Fatalf("parents-only keys = %v, want two", canonjson.SortedStrings(mapKeys(plain.Items)))
	}
}

// Without resolution the same corpus collides, which is the failure this
// surface removes; the raw-ID composite avoids the collision but embeds a
// tenant ID, which is what makes resolution worth having.
func TestPlainNameKeyingCollidesAndRawCompositeEmbedsTenantIDs(t *testing.T) {
	_, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: metadata.LoadedResourceMetadata{
			Type: "sample_location", Product: "sample", Provider: "sample",
			Override: metadata.JsonObject{},
		},
		Schema: locationLikeSchema(), RawItems: locationLikeItems(),
		HTMLUnescape: func(s string) string { return s },
	})
	if err == nil {
		t.Fatal("plain name keying accepted colliding sublocation names, want duplicate-key failure")
	}
	raw := transformLocationLike(t, metadata.JsonObject{"key_field": []any{"parent_id", "name"}})
	if _, ok := raw.Items["100_iot_device_segments"]; !ok {
		t.Errorf("raw composite keys = %v, want a tenant-ID-prefixed child key", canonjson.SortedStrings(mapKeys(raw.Items)))
	}
	if _, ok := raw.Items["0_global_psen"]; !ok {
		t.Errorf("raw composite keys = %v, want the sentinel-prefixed parent key", canonjson.SortedStrings(mapKeys(raw.Items)))
	}
}

func TestKeyFieldReferencesRefuseCrossTypeReferent(t *testing.T) {
	_, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: metadata.LoadedResourceMetadata{
			Type: "sample_location", Product: "sample", Provider: "sample",
			Override: metadata.JsonObject{
				"key_field": []any{"parent_id", "name"},
				"key_field_references": metadata.JsonObject{
					"parent_id": metadata.JsonObject{"referent": "other_type", "name_field": "name"},
				},
			},
		},
		Schema: locationLikeSchema(), RawItems: locationLikeItems(),
		HTMLUnescape: func(s string) string { return s },
	})
	if err == nil {
		t.Fatal("cross-type key referent accepted, want refusal")
	}
}
