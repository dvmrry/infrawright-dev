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

- Base: `51a6577aa5036a6e3c094df3d1b79cb4d2f8735c`, the merge commit for PR #259 on
  `origin/main` when this branch was created.
- Head: `e0bf65896ddebf59864a8d4f50411930ed45a0d6` on
  `feature/terraform-demo-item-parity`.
- Diff command:
  `git diff 51a6577aa5036a6e3c094df3d1b79cb4d2f8735c...e0bf65896ddebf59864a8d4f50411930ed45a0d6`.

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
  - `go vet ./cmd/iw`
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-semantics-probe.V0uxEg/plugin-cache INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `make check`
  - `go vet ./...`
  - `gofmt -d go/cmd/iw/v2_vertical_slice_test.go`
  - `git diff --check`
- Relevant output summary:
  - focused decoder cases passed for sorted keys, an empty object, missing and
    null `items`, a non-object value, and malformed JSON;
  - exact semantic checkpoint passed in 395.41 seconds;
  - 151/151 generated module suites passed;
  - candidate-versus-committed item-key parity matched 20/20 resources,
    including the intentionally empty SSL-inspection resource;
  - 20/20 demo Terraform roots passed;
  - native-HCL evidence passed with one expected resource and zero failed,
    errored, or skipped runs;
  - full repository check, vet, formatting, and whitespace checks passed.
- Tests not run and why: no live tenant, provider acceptance, import, apply, or
  adoption tests were run because this change affects only a hermetic test-side
  comparison after transform output is produced.

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
