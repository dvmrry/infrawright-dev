# Terraform Demo Item-Parity Builder Handoff

## Intent

- Close the remaining semantic-checkpoint gap where the expected 20 demo roots
  and Terraform run sets could all exist even if a candidate transform silently
  dropped one or more items from a resource.
- Compare the exact sorted top-level `items` keys, and therefore the exact item
  count, in every candidate transformed JSON tfvars file against its committed
  demo-config counterpart before generating or planning the demo roots.
- Keep the intentionally empty `zia_ssl_inspection_rules` fixture valid while
  rejecting missing, null, non-object, or malformed `items` values.
- Keep production transform, environment generation, Terraform modules,
  provider behavior, committed fixtures, and generated artifacts unchanged.

## Base / Head

- Base: `07acfd50d9a3a20dee44df48f5d403cc35f526b7`, current `origin/main` after
  PR #268.
- Head: the review-ready tip of `feature/terraform-demo-item-parity`; the fresh
  reviewer must resolve and record its immutable commit with
  `git rev-parse HEAD` before reviewing.
- Diff command:
  `git diff 07acfd50d9a3a20dee44df48f5d403cc35f526b7...HEAD`.

## Prior Adversarial Review Evidence

- A fresh Codex reviewer inspected the original implementation range ending at
  `e0bf65896ddebf59864a8d4f50411930ed45a0d6` read-only and returned **Approve**
  with no blocking or non-blocking findings.
- The reviewer independently ran the full checkpoint from an isolated provider
  cache in 404.66 seconds and observed 151/151 module suites, 20/20 item-key
  matches, 20/20 demo roots, and the exact one-resource native-HCL plan with no
  failed, errored, or skipped runs.
- An ephemeral build overlay invoked the actual parity helper with a dropped key
  and an extra key. Both mutations failed the named child subtest, reported
  `0/1 matched`, and propagated failure to the parent test.
- The reviewer confirmed that missing, null, non-object, and malformed `items`
  fail closed; an empty object remains valid without a resource-specific branch;
  and no production or generated-artifact files changed.
- That approval is retained as prior evidence only. The refreshed current-main
  tip requires a new exact-head read-only review before promotion.

## Files Changed

- Files:
  - `go/cmd/iw/v2_vertical_slice_test.go`
    - decodes and sorts the `items` keys from JSON tfvars;
    - rejects malformed documents and missing, null, or non-object `items`;
    - compares candidate and committed key sets per resource before `gen-env`;
    - reports both key sets and counts on mismatch and a `20/20 matched`
      summary on success;
    - includes focused parser coverage, including an empty `items` object.
- Files intentionally left untouched:
  - production CLI and transform code;
  - environment and module generators;
  - provider schemas, packs, overrides, registries, and source evidence;
  - committed demo configs, imports, lookups, generated-expression files,
    fixtures, snapshots, and golden trees;
  - workflow structure and required-check names.

## Source Inputs Consulted

- Provider schemas: not changed; all 151 generated modules were still planned
  against the active pinned schemas by the existing checkpoint.
- OpenAPI/API contracts: N/A; no API or collection behavior changed.
- Provider source files: N/A; no provider-specific values or operations changed.
- Pack metadata: `packs/full.packset.json` and the shared Zscaler demo input used
  by the existing checkpoint.
- Existing docs or design records: `AGENTS.md`, the adversarial-review workflow
  and templates, and the PR #259 Terraform semantics handoff.
- Other source evidence:
  - the 20 committed `demo/config/demo/*.auto.tfvars.json` documents;
  - the candidate transform of `packs/_shared/zscaler/demo`;
  - `go/cmd/iw/v2_transform_authority_test.go`, which separately owns exact
    transform-output-tree byte parity.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none changed.
- Snapshots: none changed.
- Demo or lab outputs: generated only under test-owned temporary directories.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: the required v2 checkpoint now fails if any of the
  20 candidate JSON tfvars resources has a missing or extra item key relative
  to the committed demo config, even when Terraform can still plan the root.
- Expected report/count/coverage changes: checkpoint output adds 20
  `demo_config_items_*` subtests and the summary
  `candidate transformed demo item-key parity: 20/20 matched`.
- Expected generated-output changes: none.
- Expected no-op areas: module count remains 151; demo-root count remains 20;
  native-HCL evidence remains one exact `config_plan`; the 19 committed import
  files are not used as the config-root authority.

## Invariants Claimed

- Evidence must not be silently dropped: candidate resource filenames and exact
  per-resource item-key sets must both match the committed config corpus.
- Generic matcher evidence must not outrank source-backed evidence: unchanged;
  no matcher or source evidence changed.
- Source precedence/provenance must remain explicit: unchanged.
- Ambiguity must stay classified instead of being coerced to success: malformed
  or structurally invalid tfvars fail instead of being interpreted as empty.
- Provider-readiness counts must stay explainable: unchanged; this adds only
  checkpoint subtests, not readiness or inventory counts.
- Adoption safety invariants: unchanged; no adoption path is touched.

## Tests Run

- Commands:
  - `go test -count=1 -run '^TestV2ConfigItemKeys' ./cmd/iw`
  - An ephemeral test overlay invoked `v2VerifyDemoConfigItemParity` with a
    dropped key and an invented key; both focused `go test` runs were required
    to exit nonzero and report `0/1 matched`.
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-pr260-tf-cache.45TYgV INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `make check`
  - `go vet ./...`
  - `test -z "$(gofmt -l go)"`
  - `git diff --check origin/main...HEAD`
- Relevant output summary:
  - focused decoder cases passed for sorted keys, an empty object, missing and
    null `items`, a non-object value, and malformed JSON;
  - exact refreshed semantic checkpoint passed in 406.52 seconds;
  - 151/151 generated module suites passed;
  - candidate-versus-committed item-key parity matched 20/20 resources,
    including the intentionally empty SSL-inspection resource;
  - 20/20 demo Terraform roots passed;
  - native-HCL evidence passed with one expected resource and zero failed,
    errored, or skipped runs;
  - full repository check, vet, formatting, and whitespace checks passed.
- Focused regression and pre-fix/unsafe-mutation proof: the dropped-key case
  compared `[alpha]` with `[alpha beta]`; the invented-key case compared
  `[alpha beta gamma]` with `[alpha beta]`. Each failed its named child
  assertion and the aggregate `candidate transformed demo item-key parity:
  0/1 matched` assertion. The ephemeral overlay was removed before staging.
- Promotion efficiency: approximately 28 hours 45 minutes elapsed from the
  first candidate commit to this refreshed review-ready tree, including the
  intervening queued/inactive period. The current-main refresh and validation
  took approximately 10 minutes. Three local full demo-checkpoint sweeps were
  attempted across the PR lifecycle (original builder, original reviewer, and
  refreshed builder); all three passed. The original hosted checkpoint also
  passed.
- Tests not run and why: no live tenant, provider acceptance, import, apply, or
  adoption tests were run because this change affects only a hermetic test-side
  comparison after transform output is produced. Current hosted CI remains to
  run after the refreshed branch is pushed.

## Known Deferrals

- Deferred work: no new full-value comparison is added to this checkpoint.
- Reason it is safe to defer: exact transform output bytes are already owned by
  `TestV2TransformDefaultCrossStateAuthority`; this change specifically binds
  the semantic checkpoint to the committed demo corpus item identities/counts.
- Follow-up owner or trigger: revisit only if value-level committed-demo parity
  becomes a distinct required-check contract rather than transform-golden
  coverage.

## Review Focus

- Highest-risk files or paths: the new helpers and their call site in
  `v2VerifyDemoEnvironmentSemantics`.
- Specific assumptions to attack:
  - an empty JSON object must remain valid and distinguishable from missing or
    null `items`;
  - `t.Run` failures must propagate to the checkpoint while allowing all
    resource mismatches to be reported;
  - exact filename parity must run before item-key parity so no resource can be
    omitted from the comparison loop;
  - the 20 config documents, not the 19 import files, remain the root authority.
- Source evidence the reviewer should verify: committed demo item keys/counts,
  especially the empty `zia_ssl_inspection_rules` document.
- Generated artifacts the reviewer should compare: candidate temporary tfvars
  against committed tfvars semantically by `items` key set; no tracked artifact
  drift is expected.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  candidate empty maps for nonempty committed resources, extra candidate keys,
  missing/null/array `items`, malformed JSON, and the legitimate empty SSL root.
