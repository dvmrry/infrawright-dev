package tfrender

// interpolation_escaping_test.go pins the HCL-interpolation escaping
// contract adjudicated in the zia_dlp_notification_templates ADOPT-FAIL
// analysis (2026-07, downstream defect report):
//
//  1. JSON tfvars (.auto.tfvars.json): string values are written
//     BYTE-VERBATIM. JSON tfvars are literal — Terraform never HCL-lexes
//     them — so no ${ or %{ munging may ever apply. A provider-canonical
//     "$${TRANSACTION_ID}" (two dollars, as provider Read returns it)
//     lands as exactly those bytes; a raw one-dollar "${RAW}" lands as
//     exactly those bytes.
//  2. HCL tfvars (tfvars_format=hcl) and .tf emitters (import/moved
//     blocks): escaping applies exactly ONCE, mechanically, to the value
//     handed in ("N+1 dollars to evaluate to N"): ${ -> $${ and %{ -> %%{.
//     Never applied twice, never applied upstream of the renderer.
//
// The Python engine (engine/transform.py:886, retiring under the archive
// plan) violated this by escaping inside its transform quoting path; the
// Node engine on current main and this port are both conformant. These
// tests exist so the Go tree can never regress into the Python-era class.

import (
	"strings"
	"testing"
)

const providerCanonicalTemplate = "$${TRANSACTION_ID}"
const rawTemplate = "${RAW} and %{directive}"

func TestHclTfvarsEscapesInterpolationOnceFromInput(t *testing.T) {
	rendered, err := RenderTfvarsHcl(map[string]any{
		"template-item": map[string]any{
			"description": rawTemplate,
			"subject":     providerCanonicalTemplate,
		},
	}, nil, "items")
	if err != nil {
		t.Fatal(err)
	}
	// Raw one-dollar input escapes once: ${ -> $${, %{ -> %%{.
	if !strings.Contains(rendered, `"$${RAW} and %%{directive}"`) {
		t.Errorf("raw template not escaped exactly once in HCL tfvars:\n%s", rendered)
	}
	// Provider-canonical two-dollar input also escapes once, mechanically
	// ($${ contains ${ at offset 1 -> $$${), which HCL evaluates back to
	// the two-dollar provider state — the adopt no-op contract.
	if !strings.Contains(rendered, `"$$${TRANSACTION_ID}"`) {
		t.Errorf("provider-canonical template not escaped exactly once in HCL tfvars:\n%s", rendered)
	}
	// The Python-era double-application (four dollars) must never appear.
	if strings.Contains(rendered, "$$$$") {
		t.Errorf("double-applied interpolation escape in HCL tfvars:\n%s", rendered)
	}
}

func TestImportBlockEscapesImportIDOnceFromInput(t *testing.T) {
	rendered, err := RenderGeneratedImports("zia_dlp_notification_templates", []GeneratedImportPair{
		{Key: "template", ImportID: rawTemplate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, `"$${RAW} and %%{directive}"`) {
		t.Errorf("import id not escaped exactly once in .tf emitter:\n%s", rendered)
	}
	if strings.Contains(rendered, "$$$$") {
		t.Errorf("double-applied interpolation escape in .tf emitter:\n%s", rendered)
	}
}

func TestRenderHclQuotedStringNeverDoubleApplies(t *testing.T) {
	once, err := RenderHclQuotedString(rawTemplate)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := RenderHclQuotedString(strings.Trim(once, `"`))
	if err != nil {
		t.Fatal(err)
	}
	// Applying the renderer to its own (unquoted) output is the classic
	// already-escaped mistake: it must remain a caller error, and this
	// pin documents the observable shape so upstream stages keep handing
	// the renderer RAW values only.
	if once == twice {
		t.Errorf("renderer output unexpectedly idempotent; escaping contract changed: %q", once)
	}
}
