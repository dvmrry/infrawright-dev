# Operational Review Boundaries — Builder Handoff

## Intent

- Clarify that Infrawright's adoption/codegen core is provider-neutral while
  live collection requires a matching compiled adapter.
- Add the operational state-safety paths that require fresh adversarial review.
- Keep runtime behavior, pack metadata, generated artifacts, and CI unchanged.

## Base / Head

- Base: `aa062eb584a40c8ba93ab3f0dca17e9881d20f4d`
- Implementation head: `f1e50daba3fc0eb2211498c1611b283ba16bebd8`
- Diff command: `git diff aa062eb584a40c8ba93ab3f0dca17e9881d20f4d..f1e50daba3fc0eb2211498c1611b283ba16bebd8`

## Files Changed

- `AGENTS.md`
- `README.md`
- Files intentionally left untouched: runtime code, collector code, pack
  metadata, workflows, generated artifacts, and the broader cleanup backlog.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: committed `provider_sources` declarations in `packs/*/pack.json`.
- Existing docs or design records: `docs/adoption-command-surface.md` collector
  boundary and the existing adversarial-review contract.
- Other source evidence: `go/cmd/iw/commands_fetch.go`,
  `go/internal/collectors/authority.go`, `go/internal/collectors/rest.go`, and
  `go/internal/collectors/zscaler_adapters.go`.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: None; documentation and review policy only.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: None.
- Expected no-op areas: all executable and generated surfaces.

## Invariants Claimed

- Evidence must not be silently dropped: unchanged.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged.
- Ambiguity must stay classified instead of being coerced to success: unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: changes to identity keys, moved-block handling,
  saved-plan classification, and apply guardrails now explicitly require the
  existing fresh-context adversarial-review process.

## Tests Run

- Commands: `make check`, `git diff --check`.
- Relevant output summary: the complete Go/runtime/distribution gate passed;
  whitespace validation passed.
- Tests not run and why: no live provider or tenant tests were relevant to a
  documentation-only change.

## Known Deferrals

- Deferred work: collector implementation changes and unrelated cleanup items.
- Reason it is safe to defer: this slice only corrects the public boundary
  description and review routing; it makes no runtime support claim.
- Follow-up owner or trigger: add a compiled collector adapter when a provider
  graduates from lab metadata to live collection support.

## Review Focus

- Highest-risk files or paths: `AGENTS.md` and the opening README claim.
- Specific assumptions to attack: whether the shipped CLI actually requires a
  compiled adapter for live fetch; whether every newly listed operational path
  can affect tenant state safety.
- Source evidence the reviewer should verify: adapter construction in
  `commands_fetch.go`, provider-source resolution in `authority.go`, and the
  collector/adoption separation in `docs/adoption-command-surface.md`.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  wording that implies pack metadata alone enables live fetch, or a review list
  that omits identity, move, classification, or apply-authority changes.
