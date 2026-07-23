# Block C2 Plan Lifecycle and Reference-Backend Adversarial Review

## Blocking Findings

None remaining.

The initial review found that Go's decoded control-JSON tree collapsed lone UTF-16 surrogate escapes to U+FFFD, allowing a non-save cross-state backend mutation to evade the freshness comparison. The builder retained `ParseControlJSON` as the sole validator and added a narrow raw-token recovery for the five allowed string fields. Changed-surface re-review accepted the fix.

## Non-Blocking Risks

None. Initial test gaps for imports-only preservation, non-save preservation, and later-root failure ordering/preservation were added and rechecked.

## Source Evidence Review

- Diff inspected: lifecycle and reference-backend files against `98682a739af92011beb71ddd54872aac23860e3f`.
- Handoff inspected: Yes.
- Provider schemas inspected: N/A.
- OpenAPI/API contracts inspected: Node lifecycle and reference-backend sources.
- Provider source inspected: N/A.
- Pack metadata inspected: existing loaded-root/lifecycle substrate.
- Fixtures or snapshots inspected: Go runtime plan artifacts and compiled Node lifecycle oracle.
- Missing evidence or review gaps: real Terraform/provider execution remains the accepted deferred checkpoint.

The re-review confirmed validation and allowlist/type checks precede narrow token recovery; boolean handling is unchanged; lone high/low surrogates, valid pairs, U+FFFD, controls, non-ASCII, and hex normalization match `JSON.stringify`; and the `\ud800` to U+FFFD init mutation returns `INIT_INPUTS_CHANGED` before planning. Focused tests repeated 20 times, race/full/vet/formatting, zero-dependency listing, Node lifecycle 18/18, and independent UTF-16 oracle 5/5 passed.

## Generated Artifact Review

- Reports reviewed: None.
- Schemas reviewed: None.
- Fixtures reviewed: runtime `tfplan`/`tfplan.sources` behavior.
- Snapshots reviewed: None.
- Count/coverage deltas reviewed: root-based counts unchanged.
- Artifact drift accepted or rejected: no tracked drift.

## Verdict

Approve.

Verdict rationale: The remediation closes the parity and freshness weakness without broadening parsing authority or changing unrelated lifecycle behavior.
