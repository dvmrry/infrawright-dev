# Builder Review Handoff Template

Use this template when a high-risk agent-built change is ready for adversarial
review. Fill every section; use `None` or `N/A` only when that is the real
answer.

## Intent

- What problem does this change solve?
- What user-visible or maintainer-visible behavior should change?
- What behavior must stay unchanged?

## Base / Head

- Base:
- Head:
- Diff command:

## Files Changed

- Files:
- Files intentionally left untouched:

## Source Inputs Consulted

- Provider schemas:
- OpenAPI/API contracts:
- Provider source files:
- Pack metadata:
- Existing docs or design records:
- Other source evidence:

## Generated Artifacts

- Reports:
- Schemas:
- Fixtures:
- Snapshots:
- Demo or lab outputs:
- Artifact drift intentionally expected:

## Expected Delta

- Expected behavior change:
- Expected report/count/coverage changes:
- Expected generated-output changes:
- Expected no-op areas:

## Invariants Claimed

- Evidence must not be silently dropped:
- Generic matcher evidence must not outrank source-backed evidence:
- Source precedence/provenance must remain explicit:
- Ambiguity must stay classified instead of being coerced to success:
- Provider-readiness counts must stay explainable:
- Adoption safety invariants:

## Tests Run

- Commands:
- Relevant output summary:
- Tests not run and why:

## Known Deferrals

- Deferred work:
- Reason it is safe to defer:
- Follow-up owner or trigger:

## Review Focus

- Highest-risk files or paths:
- Specific assumptions to attack:
- Source evidence the reviewer should verify:
- Generated artifacts the reviewer should compare:
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
