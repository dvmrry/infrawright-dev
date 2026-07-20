package transform

// coerce_test.go pins coerceValue's collection-branch (list/set/map)
// null/absent/empty-string ordering -- the exact logic order pull-transform.ts's
// own coerceValue applies at node-src/domain/pull-transform.ts:568-591 --
// against the inherited coerce.go implementation. Every "TS:" comment below
// records the literal value node's compiled pull-transform.ts produced for
// the same (value, encoding, strictFrozenCompatibility) input, captured via
// `npx esbuild node-src/domain/pull-transform.ts --bundle --format=esm
// --outfile=/tmp/probe.mjs` against a copy with `coerceValue` exported, then
// `node -e` calling it directly -- per this port's CRITICAL FIRST TASK to
// re-derive and pin this exact ordering before building anything else on
// top of it.
//
// AUDIT FINDING: the inherited coerce.go's coerceValue collection branch
// (the `case nil: return nil` arm, checked before the array-vs-wrap
// dispatch) already matches node-src/domain/pull-transform.ts's own
// ordering exactly -- `if (value === "") return [];` first, then
// Array.isArray, then `value === null` (an early return that bypasses the
// "set" sort/strict-check tail entirely), then the wrap-in-a-one-element-array
// default. No bug was found in this specific branch as inherited; every
// vector below (and the ones the prior agent's "nil handling bug" note
// pointed at) already passes. These tests exist to pin that finding, not to
// document a fix.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func mustPanic(t *testing.T, name string, fn func()) (message string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
		err, ok := r.(*TransformError)
		if !ok {
			t.Fatalf("%s: expected *TransformError panic, got %T: %v", name, r, r)
		}
		message = err.Error()
	}()
	fn()
	return ""
}

func TestCoerceValueCollectionBranchNilOrdering(t *testing.T) {
	listString := metadata.TerraformCollectionType{Kind: "list", Inner: metadata.TerraformPrimitiveType("string")}
	setString := metadata.TerraformCollectionType{Kind: "set", Inner: metadata.TerraformPrimitiveType("string")}
	mapString := metadata.TerraformCollectionType{Kind: "map", Inner: metadata.TerraformPrimitiveType("string")}
	listNumber := metadata.TerraformCollectionType{Kind: "list", Inner: metadata.TerraformPrimitiveType("number")}
	setObject := metadata.TerraformCollectionType{Kind: "set", Inner: metadata.TerraformObjectType{
		Members: map[string]metadata.TerraformTypeEncoding{"id": metadata.TerraformPrimitiveType("string")},
	}}
	listOfListString := metadata.TerraformCollectionType{Kind: "list", Inner: listString}

	t.Run("null value returns nil directly, bypassing set sort/strict checks", func(t *testing.T) {
		// TS: coerceValue(null, ["list","string"]) => null
		if got := coerceValue(nil, listString, false); got != nil {
			t.Fatalf("list<string> nil = %#v, want nil", got)
		}
		// TS: coerceValue(null, ["set","string"]) => null
		if got := coerceValue(nil, setString, false); got != nil {
			t.Fatalf("set<string> nil = %#v, want nil", got)
		}
		// TS: coerceValue(null, ["map","string"]) => null
		if got := coerceValue(nil, mapString, false); got != nil {
			t.Fatalf("map<string> nil = %#v, want nil", got)
		}
		// TS: coerceValue(null, ["list",["list","string"]]) => null
		if got := coerceValue(nil, listOfListString, false); got != nil {
			t.Fatalf("list<list<string>> nil = %#v, want nil", got)
		}
	})

	t.Run("empty string becomes an empty list even under set/strict", func(t *testing.T) {
		// TS: coerceValue("", ["list","string"]) => []
		got := coerceValue("", listString, false)
		assertAnySliceEqual(t, "list<string> ''", got, []any{})
		// TS: coerceValue("", ["set","string"]) => []
		got = coerceValue("", setString, true)
		assertAnySliceEqual(t, "set<string> '' strict", got, []any{})
	})

	t.Run("null survives inside an array element untouched", func(t *testing.T) {
		// TS: coerceValue([null,"a"], ["list","string"]) => [null,"a"]
		got := coerceValue([]any{nil, "a"}, listString, false)
		assertAnySliceEqual(t, "list<string> [nil,'a']", got, []any{nil, "a"})
		// TS: coerceValue([null,"a"], ["set","string"]) => [null,"a"] (sorted; "" < "a")
		got = coerceValue([]any{nil, "a"}, setString, false)
		assertAnySliceEqual(t, "set<string> [nil,'a']", got, []any{nil, "a"})
		// TS: coerceValue([null,null,"b","a"], ["set","string"]) => [null,null,"a","b"]
		got = coerceValue([]any{nil, nil, "b", "a"}, setString, false)
		assertAnySliceEqual(t, "set<string> [nil,nil,'b','a']", got, []any{nil, nil, "a", "b"})
		// TS: coerceValue([null,"3","x"], ["list","number"]) => [null, 3, "x"]
		// ("3" parses as a Python integer and coerces to this package's
		// json.Number numeric leaf; "x" is not numeric-string-shaped, so
		// coercePrimitive's "number" branch returns it untouched.)
		got = coerceValue([]any{nil, "3", "x"}, listNumber, false)
		assertAnySliceEqual(t, "list<number> [nil,'3','x']", got, []any{nil, json.Number("3"), "x"})
	})

	t.Run("strict set(string): null items are exempt from the non-string check", func(t *testing.T) {
		// TS: coerceValue([null,"a"], ["set","string"], true) => [null,"a"] (no throw)
		got := coerceValue([]any{nil, "a"}, setString, true)
		assertAnySliceEqual(t, "set<string> strict [nil,'a']", got, []any{nil, "a"})
		// TS: coerceValue({id:null}, ["set","string"], true) => [null] (unwrapReference then null passes through)
		got = coerceValue(map[string]any{"id": nil}, setString, true)
		assertAnySliceEqual(t, "set<string> strict {id:nil}", got, []any{nil})
		// TS: coerceValue([{id:null},"a"], ["set","string"], true) => [null,"a"]
		got = coerceValue([]any{map[string]any{"id": nil}, "a"}, setString, true)
		assertAnySliceEqual(t, "set<string> strict [{id:nil},'a']", got, []any{nil, "a"})
		// TS: coerceValue([{id:"x"},"a"], ["set","string"], true) => ["a","x"] (unwrapped+sorted)
		got = coerceValue([]any{map[string]any{"id": "x"}, "a"}, setString, true)
		assertAnySliceEqual(t, "set<string> strict [{id:'x'},'a']", got, []any{"a", "x"})
	})

	t.Run("strict set(string): a genuinely non-string coerced item throws", func(t *testing.T) {
		// TS: coerceValue({}, ["set","string"], true) throws
		// (unwrapReference({}) stays {}, coercePrimitive leaves the object
		// untouched, and the strict check rejects a non-null, non-string item)
		message := mustPanic(t, "set<string> strict {}", func() {
			coerceValue(map[string]any{}, setString, true)
		})
		wantSubstring(t, message, "set(string) coercion produced a non-string provider value")
	})

	t.Run("null wraps to nil, not into a one-element list, inside a list-of-list", func(t *testing.T) {
		// TS: coerceValue([null,"a"], ["list",["list","string"]]) => [null, ["a"]]
		got := coerceValue([]any{nil, "a"}, listOfListString, false)
		outer, ok := got.([]any)
		if !ok || len(outer) != 2 {
			t.Fatalf("list<list<string>> [nil,'a'] = %#v", got)
		}
		if outer[0] != nil {
			t.Fatalf("list<list<string>> [nil,'a'][0] = %#v, want nil", outer[0])
		}
		assertAnySliceEqual(t, "list<list<string>> [nil,'a'][1]", outer[1], []any{"a"})
	})

	t.Run("null inside a set of objects passes through unsorted-key untouched", func(t *testing.T) {
		// TS: coerceValue([null,{id:"a"}], ["set",["object",{id:"string"}]]) =>
		// [null, {id:"a"}]
		got := coerceValue([]any{nil, map[string]any{"id": "a"}}, setObject, false)
		arr, ok := got.([]any)
		if !ok || len(arr) != 2 {
			t.Fatalf("set<object> [nil,{id:'a'}] = %#v", got)
		}
		if arr[0] != nil {
			t.Fatalf("set<object> [nil,{id:'a'}][0] = %#v, want nil", arr[0])
		}
	})

	t.Run("a bare scalar wraps into a single-element list", func(t *testing.T) {
		// TS: coerceValue("a", ["list","string"]) => ["a"]
		got := coerceValue("a", listString, false)
		assertAnySliceEqual(t, "list<string> 'a'", got, []any{"a"})
	})

	t.Run("map kind returns non-object values unchanged, including null", func(t *testing.T) {
		if got := coerceValue(nil, mapString, false); got != nil {
			t.Fatalf("map<string> nil = %#v, want nil", got)
		}
		// TS: coerceValue("", ["map","string"]) => "" (the ""->[] rule is
		// scoped to list/set; the map branch returns unconditionally before
		// that check is ever reached)
		if got := coerceValue("", mapString, false); got != "" {
			t.Fatalf("map<string> '' = %#v, want ''", got)
		}
	})
}

func assertAnySliceEqual(t *testing.T, label string, got any, want []any) {
	t.Helper()
	gotSlice, ok := got.([]any)
	if !ok {
		t.Fatalf("%s: got %#v (%T), want a []any", label, got, got)
	}
	if len(gotSlice) != len(want) {
		t.Fatalf("%s: got %#v, want %#v", label, gotSlice, want)
	}
	for i := range want {
		if gotSlice[i] != want[i] {
			t.Fatalf("%s[%d]: got %#v, want %#v", label, i, gotSlice[i], want[i])
		}
	}
}

func wantSubstring(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want a message containing %q", got, want)
	}
}
