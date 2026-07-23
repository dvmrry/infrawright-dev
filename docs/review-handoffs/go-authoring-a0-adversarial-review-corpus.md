# Go authoring A0 adversarial review: corpus and byte authority

Review target: staged A0 diff from
`cbb8f01a5d7aae4785645a8c75a0eb3ad55fcc4f` on
`feature/go-canonjson-foundation` (50 files, 8,746 additions).

Reviewer context: independent fresh, read-only Terra High reviewer. No files
were edited.

## Blocking Findings

- Finding: **Source locations are ambiguous across provider and SDK roots, so
  call-chain provenance is not actually input-bound.**
- Source evidence: `SourceLocation` contains only a relative path, function,
  line, and column. Input-bound validation accepts that path if it exists in
  either the provider or any SDK and does not require an SDK-call call site to
  belong to the expected source tree.
- Impact: common colliding names such as `client.go` or `internal/...` can make
  a wrong-root call site pass validation, weakening the provider-to-SDK-to-HTTP
  proof.
- Required change: namespace every source location as provider or an exact SDK
  module and enforce the namespace for registrations, callbacks, helpers, call
  sites, SDK declarations, raw HTTP, and endpoints.
- Suggested regression: give a provider and SDK the same relative path, mutate
  an otherwise valid step to the wrong root, and require rejection.

- Finding: **Cross-artifact digests hash re-rendered values rather than proving
  the supplied artifact bytes.**
- Source evidence: report validation hashes `RenderInputProvenance(input)` and
  diagnostics validation hashes `RenderSourceEvidenceReport(source)`, while the
  corresponding decoders accept valid noncanonical JSON without comparing it
  with canonical rendering.
- Impact: whitespace or key-order changes alter actual sidecar bytes while the
  downstream join still accepts the canonical-reconstruction digest, contrary
  to the exact-byte claim.
- Required change: pass and hash the actual canonical artifact bytes, or reject
  every decoded document whose bytes differ from its canonical `Render*` form.
- Suggested regression: whitespace/key-order variants of input provenance and
  source report must be rejected by their downstream byte-binding path.

## Non-Blocking Risks

- OpenAPI conflict `basis_reference` remains a free-form assertion. Before A3
  emits conflicts, bind it to a reviewed explicit input and reject arbitrary
  labels.
- Provider/SDK repository strings are manifest claims; local loading verifies
  revisions, exact bytes, module identity, and replacements but does not compare
  repository strings with checkout remote provenance. The handoff must not
  overstate that property.

## Source Evidence Review

- Full staged diff, handoff, three embedded schemas, semantic validators, all
  eight synthetic cases, Node-v1 authority, ZPA matrix/effective-pack/source-
  range gate, and isolated OpenAPI comparison rules were inspected.
- No external ZPA checkout was supplied; the optional range recheck correctly
  skipped.

## Generated Artifact Review

- Reproduced digests for source provenance, input provenance, source report,
  OpenAPI diagnostics, Node authority, frozen ZPA matrix, and Node bundle.
- The eight classifications, seven source partition counts, raw-HTTP terminal
  rules, dynamic/unresolved decoys, and absent-OpenAPI partition are internally
  consistent.
- The Node and ZPA gates remain test-only and do not resurrect a ZPA command or
  confer endpoint qualification.
- Artifact drift is rejected pending both binding fixes.

## Commands Run

The reviewer ran staged diff/status checks, formatting, vet, focused/full/race
authoring tests, offline full-module tests, `go mod tidy -diff`, and independent
digest reproduction. All commands passed; the blockers concern accepted
semantic states not present in the current positive corpus.

## Verdict

**Request changes.** The corpus and offline gates are otherwise carefully
constructed, but root identity and exact-byte digest joins are load-bearing A0
authority requirements.

## Corrected-Surface Recheck

Target: current staged diff from `cbb8f01` (52 files, 9,822 additions).

No blocking or new non-blocking findings remain. The reviewer independently
cross-checked every synthetic provider/SDK declaration and callsite, the
eight-resource seven-state counts, the repeated catalog helper path, the
unresolved Read/Create decoy, scoped location/package/module validation, strict
canonical bytes on all public decoders, and the migrated digest chain. Full,
race, offline, vet, tidy, formatting, diff, and digest checks passed.

Recheck verdict: **Approve.**
