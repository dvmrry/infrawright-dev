package assessment

// The Go validator claims to mirror docs/schemas/saved-plan-assessment.schema.json,
// and nothing enforced that claim. The version bump to 2 and the additive
// `changes` key landed in the validator while the published schema still
// pinned version 1 with additionalProperties:false on every finding -- so a
// consumer following the documented instruction to reject unsupported versions
// would have rejected every report we emit. Nothing in CI noticed, because no
// test read the schema file at all.
//
// These tests read it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The published schema lives in the documentation tree, which Go source may
// not read (cmd/iw/repository_boundary_test.go). testdata carries a byte copy
// instead, and `make check-schema-parity` keeps the two identical -- so this
// package asserts against the contract consumers actually receive without
// reaching across that boundary.
func publishedAssessmentSchema(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("testdata", "published-assessment-schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", path, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parsing %q: %v", path, err)
	}
	return schema
}

func schemaObject(t *testing.T, parent map[string]any, keys ...string) map[string]any {
	t.Helper()
	current := parent
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("published schema is missing object at %v", keys)
		}
		current = next
	}
	return current
}

// TestPublishedSchemaVersionMatchesEmittedReports pins the constant a consumer
// validates against to the one production actually writes. These drifting
// apart is silent on our side and total on theirs: every report fails.
func TestPublishedSchemaVersionMatchesEmittedReports(t *testing.T) {
	schema := publishedAssessmentSchema(t)
	published := schemaObject(t, schema, "properties", "schema_version")["const"]

	emitted := assessmentReportJSONValue(buildReportForTest(t, Blocked))["schema_version"]

	// JSON numbers decode as float64 here and json.Number in the report, so
	// compare their text.
	publishedText := jsonNumberText(t, published)
	emittedText := jsonNumberText(t, emitted)
	if publishedText != emittedText {
		t.Errorf(
			"published schema_version const = %s, emitted reports carry %s; "+
				"a consumer rejecting unsupported versions would reject every report",
			publishedText, emittedText,
		)
	}
}

func jsonNumberText(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%#v) error = %v, want nil", value, err)
	}
	return string(data)
}

// TestPublishedSchemaAcceptsEveryEmittedFindingKey pins that the published
// finding definition admits what we emit. It declares
// additionalProperties:false, so a key we add and it does not list is a hard
// refusal on the consumer's side rather than a field they can ignore.
func TestPublishedSchemaAcceptsEveryEmittedFindingKey(t *testing.T) {
	finding := schemaObject(t, publishedAssessmentSchema(t), "$defs", "finding")
	if finding["additionalProperties"] != false {
		t.Fatal("published finding no longer refuses unknown keys; this test assumes it does")
	}
	declared := map[string]struct{}{}
	for key := range schemaObject(t, finding, "properties") {
		declared[key] = struct{}{}
	}

	value := assessmentReportJSONValue(buildReportForTest(t, Blocked))
	roots, ok := value["roots"].([]any)
	if !ok || len(roots) == 0 {
		t.Fatal("blocked report fixture has no roots")
	}
	findings, ok := roots[0].(map[string]any)["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatal("blocked report fixture has no findings")
	}
	for _, raw := range findings {
		for key := range raw.(map[string]any) {
			if _, present := declared[key]; !present {
				t.Errorf(
					"emitted findings carry %q, which the published schema does not declare; "+
						"additionalProperties:false makes that a refusal, not a field to ignore",
					key,
				)
			}
		}
	}
}

// TestPublishedSchemaChangeKeysMatchTheValidator pins the change object's key
// list on both sides. The Go validator requires exactly these keys and refuses
// unknown ones; the published schema has to agree or the two disagree about
// what a valid report is.
func TestPublishedSchemaChangeKeysMatchTheValidator(t *testing.T) {
	change := schemaObject(t, publishedAssessmentSchema(t), "$defs", "change")
	if change["additionalProperties"] != false {
		t.Error("published change object should refuse unknown keys, matching the validator")
	}
	published := map[string]struct{}{}
	for key := range schemaObject(t, change, "properties") {
		published[key] = struct{}{}
	}
	// The same list validateReportChange enforces.
	for _, key := range []string{"path", "kind", "sensitive", "before", "after", "added", "removed"} {
		if _, present := published[key]; !present {
			t.Errorf("published change object is missing %q, which the validator requires", key)
		}
	}
	if len(published) != 7 {
		t.Errorf("published change object declares %d keys, want the validator's 7", len(published))
	}
}
