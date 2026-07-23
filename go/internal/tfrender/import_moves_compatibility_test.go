package tfrender

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const importMovesCompatibilitySHA256 = "18fe39262486f99e03814bc1620fb0b78b7ddb20ad0f965513c3870ffe49f3ba"

type importMovesCompatibilityFixture struct {
	SchemaVersion int                            `json:"schema_version"`
	Cases         []importMovesCompatibilityCase `json:"cases"`
}

type importMovesCompatibilityPair struct {
	Key      string `json:"key"`
	ImportID string `json:"importId"`
}

type importMovesCompatibilityMove struct {
	OldKey string `json:"oldKey"`
	NewKey string `json:"newKey"`
}

type importMovesCompatibilitySuppression struct {
	OldKey   string `json:"oldKey"`
	NewKey   string `json:"newKey"`
	ImportID string `json:"importId"`
	Reason   string `json:"reason"`
}

type importMovesCompatibilityCase struct {
	Name       string                         `json:"name"`
	OldText    string                         `json:"oldText"`
	NewText    string                         `json:"newText"`
	OldPairs   []importMovesCompatibilityPair `json:"oldPairs"`
	NewPairs   []importMovesCompatibilityPair `json:"newPairs"`
	Derivation struct {
		Moves      []importMovesCompatibilityMove        `json:"moves"`
		Suppressed []importMovesCompatibilitySuppression `json:"suppressed"`
	} `json:"derivation"`
	MovesText string `json:"movesText"`
}

func TestImportMovesCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "import_moves_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != importMovesCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, importMovesCompatibilitySHA256)
	}
	var fixture importMovesCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 || len(fixture.Cases) != 12 {
		t.Fatalf("%s schema/cases = %d/%d, want 1/12", fixturePath, fixture.SchemaVersion, len(fixture.Cases))
	}
	seen := map[string]bool{}
	for _, test := range fixture.Cases {
		if seen[test.Name] {
			t.Fatalf("duplicate compatibility case %q", test.Name)
		}
		seen[test.Name] = true
		t.Run(test.Name, func(t *testing.T) {
			oldPairs := compatibilityImportPairs(test.OldPairs)
			newPairs := compatibilityImportPairs(test.NewPairs)
			if got, err := RenderGeneratedImports(importMovesResourceType, oldPairs); err != nil || got != test.OldText {
				t.Errorf("RenderGeneratedImports(old) = %q, %v; want fixed %q", got, err, test.OldText)
			}
			if got, err := RenderGeneratedImports(importMovesResourceType, newPairs); err != nil || got != test.NewText {
				t.Errorf("RenderGeneratedImports(new) = %q, %v; want fixed %q", got, err, test.NewText)
			}
			parsedOld, err := ParseGeneratedImports(importMovesResourceType, test.OldText)
			if err != nil {
				t.Fatalf("ParseGeneratedImports(fixed old text) error: %v", err)
			}
			compatibilityPairsEqual(t, "old", parsedOld, oldPairs)
			parsedNew, err := ParseGeneratedImports(importMovesResourceType, test.NewText)
			if err != nil {
				t.Fatalf("ParseGeneratedImports(fixed new text) error: %v", err)
			}
			compatibilityPairsEqual(t, "new", parsedNew, newPairs)

			derivation, err := DeriveImportMoves(importMovesResourceType, test.OldText, test.NewText)
			if err != nil {
				t.Fatalf("DeriveImportMoves(fixed texts) error: %v", err)
			}
			compatibilityMovesEqual(t, derivation.Moves, test.Derivation.Moves)
			compatibilitySuppressionsEqual(t, derivation.Suppressed, test.Derivation.Suppressed)
			movesText, err := RenderMovedBlocks(importMovesResourceType, derivation.Moves)
			if err != nil {
				t.Fatalf("RenderMovedBlocks() error: %v", err)
			}
			if movesText != test.MovesText {
				t.Errorf("RenderMovedBlocks() = %q, want fixed %q", movesText, test.MovesText)
			}
		})
	}
}

func compatibilityImportPairs(input []importMovesCompatibilityPair) []GeneratedImportPair {
	output := make([]GeneratedImportPair, len(input))
	for index, pair := range input {
		output[index] = GeneratedImportPair{Key: pair.Key, ImportID: pair.ImportID}
	}
	return output
}

func compatibilityPairsEqual(t *testing.T, label string, got, want []GeneratedImportPair) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s parsed pairs = %d, want %d", label, len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("%s parsed pair %d = %#v, want %#v", label, index, got[index], want[index])
		}
	}
}

func compatibilityMovesEqual(t *testing.T, got []ImportMove, want []importMovesCompatibilityMove) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("derived moves = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].OldKey != want[index].OldKey || got[index].NewKey != want[index].NewKey {
			t.Errorf("derived move %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func compatibilitySuppressionsEqual(t *testing.T, got []ImportMoveSuppression, want []importMovesCompatibilitySuppression) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("derived suppressions = %d, want %d", len(got), len(want))
	}
	for index := range want {
		actual := got[index]
		expected := want[index]
		if actual.OldKey != expected.OldKey || actual.NewKey != expected.NewKey || actual.ImportID != expected.ImportID || string(actual.Reason) != expected.Reason {
			t.Errorf("derived suppression %d = %#v, want %#v", index, actual, expected)
		}
	}
}
