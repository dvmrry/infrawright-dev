# Builder Review Handoff: Generic Field-Witness Semantics and SDK Shape Analysis

## Intent

- What problem does this change solve? The field-lineage spike treated generated
  Terraform schema JSON and the provider's Go `schema.Schema` declaration as two
  independent votes. That could label a declaration-only field corroborated
  even when the provider never reads, writes, or tests it. It also recorded a
  literal-key `ResourceData.Set` without checking a statically recoverable
  nested value shape against the declared Plugin SDK schema.
- What user-visible or maintainer-visible behavior should change? The opt-in
  field-witness report now renders four separate assessment axes: declaration,
  Read, Create/Update write input, and acceptance-test evidence. Terraform JSON
  and Go schema form one declaration family; acceptance configuration and
  assertions form one acceptance family. A bounded, provider-neutral Plugin SDK
  shape pass can identify returned nested maps that can emit undeclared object
  keys, and a bounded Create/Update call-graph pass records literal or
  call-bound `Get`, `GetOk`, and `GetOkExists` access. Review items name missing
  Read/write paths and static shape conflicts directly.
- What behavior must stay unchanged? The analyzer remains opt-in and
  diagnostic. No CLI, Make, CI, pack, transform, adoption, readiness,
  source-operation, source-evidence-report-v1, or generated-output authority is
  changed. No Zscaler resource or field name appears in the production engine.

## Base / Head

- Base: `feature/field-lineage-spike` at
  `6ef303dce2a2f5763ed1852670fe80120205978f`.
- Head: the exact `feature/field-lineage-spike` review tip supplied with the
  adversarial-review run prompt; resolve locally with `git rev-parse HEAD`.
- Diff command: `git diff 6ef303dce2a2f5763ed1852670fe80120205978f...HEAD`.

## Files Changed

- Files: `README.md`;
  `go/internal/authoring/sourceanalysis/field_witnesses.go` and its test;
  new colocated `field_value_shapes.go`, `field_value_shapes_test.go`, and
  `field_write_witnesses.go`; this handoff.
- Files intentionally left untouched: `tools/`; commands; Make targets; CI;
  field-witness callers; frozen contracts; source-operation/readiness code;
  packs and generated artifacts. The separate ZIA 4.8.0 worktree/branch and its
  pre-existing untracked `reports/` directory were not modified.

## Source Inputs Consulted

- Provider schemas: the neutral, in-memory Terraform schema fixture in
  `field_value_shapes_test.go`; and
  `packs/zia/schemas/provider/zia.json` from the separate ZIA 4.8.0 branch for a
  removed real-source probe.
- OpenAPI/API contracts: None. This analyzer does not infer API behavior.
- Provider source files: neutral test sources generated in `t.TempDir`; and
  `zscaler/terraform-provider-zia` tag `v4.8.0`, commit
  `1c9167d3105b60597ded1388cf97024de2d6c470`: `main.go`, `zia/provider.go`,
  `zia/common.go`, `zia/data_source_zia_endpoint_dlp_rules.go`,
  `zia/resource_zia_endpoint_dlp_rules.go`, and the resource/test pairs for
  firewall DNS, filtering, IPS, and SSL inspection rules.
- Pack metadata: ZIA 4.8.0 overrides and its existing review handoff were read
  to choose fields for the real-source comparison; no pack input is consumed
  by the implementation.
- Existing docs or design records: `README.md`, `docs/adversarial-review.md`,
  and the review handoff/run/finding templates.
- Other source evidence: Terraform Plugin SDK v2.40.1
  `helper/schema/resource_data.go` and `helper/schema/field_writer_map.go`.
  `ResourceData.Set` delegates to `MapFieldWriter.WriteField`; `setObject`
  writes every emitted map key through the nested schema address; primitive
  writers use `mapstructure` coercion. The implementation deliberately does
  not classify `MaxItems` or a primitive Go-type difference as a definite Set
  failure.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: Neutral provider, schema, and acceptance inputs are written only
  inside test `t.TempDir` directories.
- Snapshots: None.
- Demo or lab outputs: None tracked. `make check` regenerated only ignored/temp
  outputs. A temporary detached v4.8.0 provider worktree and temporary probe
  test were removed after verification.
- Artifact drift intentionally expected: None outside the opt-in in-memory
  field-witness JSON shape.

## Expected Delta

- Expected behavior change: Declaration JSON plus Go schema count as one
  evidence family. Literal-key Read assignments, Create/Update field access,
  and acceptance sources are independent families. Config and assertion
  evidence remain separately rendered but contribute one acceptance family.
  A direct Read assignment whose value shape is unresolved remains a Read
  witness and carries an explicit diagnostic; a recovered incompatible nested
  shape makes the field conflicting. Arbitrary unknown return/assignment/append
  values survive shape merging, while a proven `nil` path is represented
  separately as SDK-safe absence. Missing acceptance coverage remains silence.
  Optional/Computed or validated fields with a missing write path are ranked
  for human review rather than silently accepted.
- Expected report/count/coverage changes: Existing reports, readiness counts,
  and coverage do not change. Callers of the opt-in field-witness API receive
  an `assessment`, `write_inputs`, provider `value_shape`/`shape_issue`, and
  per-Read `shape_assessment`.
- Expected generated-output changes: None. No current generator consumes this
  report.
- Expected no-op areas: Provider import/read execution, transform and adoption
  projection, registry overrides, holds, packs, OpenAPI mapping, and all ZIA
  4.8.0 branch artifacts.

## Invariants Claimed

- Evidence must not be silently dropped: Dynamic or uncaptured schema shapes,
  Read shapes, callbacks, write helpers, and field keys produce diagnostics at
  the closest recoverable field path. Unsupported analysis never becomes a
  positive runtime claim.
- Generic matcher evidence must not outrank source-backed evidence: No matcher
  or precedence code changes. The new observations come only from captured
  Terraform schema, provider source, and acceptance source.
- Source precedence/provenance must remain explicit: The analyzer still uses
  defensive `sourcebind` snapshots and portable source locations. Cross-file
  and cross-package helper argument bindings retain the expression's source
  scope. It performs no network access, source execution, or path reads after
  binding.
- Ambiguity must stay classified instead of being coerced to success: Static
  shape analysis is bounded and path-insensitive. It says a return shape *can*
  emit an incompatible key, not that every runtime path does. Unresolved
  shapes remain unresolved; Plugin SDK scalar coercion and set/list input
  representation do not become false conflicts. A known sibling branch cannot
  erase an unresolved value shape.
- Provider-readiness counts must stay explainable: No readiness input or count
  is changed.
- Adoption safety invariants: No adoption or provider-state Oracle path is
  changed. This report can guide a later policy decision but cannot make one.
- Review priority must not become policy: High/medium/low remain diagnostic
  ordering only.

## Tests Run

- Commands: `go test ./internal/authoring/sourceanalysis`;
  `go test -race ./internal/authoring/sourceanalysis`; `go test ./...`;
  `go vet ./...`; `gofmt -d .`; `make check`; `git diff --check`.
- Relevant output summary: All commands passed. The neutral fixture proves that
  helper-bound schema keys work across provider subpackages without scope
  confusion; declaration JSON plus Go schema alone remains `untested`; a
  compatible nested flattener plus captured write access is corroborated; a
  flattener that can return undeclared `id`/`name` keys is a high-priority shape
  conflict; missing provider element shape is diagnostic; Create calling Read
  does not misclassify Read-side `Get` as write evidence; and a captured helper
  passed `nil` instead of the callback's ResourceData cannot manufacture a
  write witness. Unsupported helper control flow remains unresolved instead of
  becoming a conflict; comparable Terraform/SDK declaration kinds disagree
  explicitly; and partial scalar-coercion shapes enter the review queue. Unit
  coverage also fixes the intended Plugin SDK set/list, scalar-coercion,
  open-map, and evidence-family semantics.
- Adversarial review loop: Fresh reviewer verdict on `42ccfc06` was `Request
  changes`. Finding -> root cause -> fix -> regression: unknown return and
  append shapes were discarded because inference status was ignored and
  `unknown` acted as a merge identity; observed unresolved values now have a
  distinct contagious shape, inference issues reach the field diagnostic, and
  true `nil` has a separate safe shape; `mixed_targets`, `appended_targets`, and
  `nullable_targets` cover the three cases. The review's variadic-write nit was
  also accepted: variadic binding now stays unsupported but emits a
  field-associated `write_helper_unresolved` diagnostic and cannot manufacture
  a scalar key binding. Focused/race, repository-wide, vet/format, `make check`,
  and real ZIA probe verification are rerun after the fix before re-review.
- Real-source comparison: A removed temporary probe loaded the actual ZIA
  v4.8.0 sources and the refreshed schema. DNS, filtering, and SSL endpoint
  applications/groups were each `conflicting` with consistent declarations,
  shape-conflicting Reads, observed writes, and silent acceptance evidence (14
  recovered extra keys for applications and 5 for groups). Both IPS endpoint
  fields were declaration-only/`untested`, with absent Read and write axes. The
  three new DNS and three new filtering scalar fields were corroborated by
  declaration, direct Read, and write access, with acceptance remaining silent.
- Tests not run and why: No provider binary, live tenant fetch/import/read,
  acceptance apply, or no-op plan was run. Static shape compatibility is not a
  substitute for those runtime checks.

## Known Deferrals

- Deferred work: Plugin Framework schemas; helper-propagated `ResourceData.Set`;
  method/closure call graphs; full variadic write-helper binding; general alias
  analysis; path-feasibility and value-sensitive branch evaluation; complex
  dynamic schema and field keys; acceptance tests outside the existing
  companion-file boundary; and runtime provider execution.
- Reason it is safe to defer: Each implemented seam is bounded. Known dynamic
  cases are diagnostic or silence, and no existing authority consumes the
  output. The checker reports only statically recovered Plugin SDK structure;
  it does not claim general Go type checking or runtime success.
- Follow-up owner or trigger: A separate reviewed change that versions a
  field-witness artifact or makes downstream pack/adoption policy consume an
  assessment axis. Provider-specific runtime probes belong with the provider
  refresh/integration work, not in this generic package.

## Review Focus

- Highest-risk files or paths: `field_value_shapes.go` (may-shape recovery and
  SDK comparison), `field_write_witnesses.go` (ResourceData flow and Read
  exclusion), and aggregate disposition/review-queue logic in
  `field_witnesses.go`.
- Specific assumptions to attack: Terraform JSON plus Go declaration are one
  family; config plus assertions are one acceptance family; a literal-key
  unresolved `d.Set` is still Read evidence but remains visibly unresolved;
  declaration kinds are compared only when both Terraform and SDK categories
  are statically comparable;
  only a recovered extra nested object key is treated as the ZIA-relevant SDK
  conflict; `MaxItems` and scalar representation are not overclassified;
  Optional+Computed fields can legitimately surface missing-write guidance.
- Source evidence the reviewer should verify: Plugin SDK v2.40.1
  `ResourceData.Set`, `MapFieldWriter.WriteField`, `setObject`, and primitive
  coercion; the neutral fixture's provider-subpackage binding and bad flattener;
  ZIA v4.8.0 endpoint schemas, Read flatteners, write expanders, absent IPS
  wiring, and six direct scalar Read/write paths.
- Generated artifacts the reviewer should compare: Confirm no tracked report,
  schema, snapshot, module, pack, or fixture corpus changes.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  cross-scope helper bindings; multiple return paths; dynamic map keys; map
  assignment versus literals; list/set representation; uncaptured helpers;
  Create/Update calling Read; helper calls that receive a different
  ResourceData value; multiple provider schema witnesses; unresolved expected
  shape; mixed known/unknown returns and appends; `nil` versus arbitrary
  unknown; nested fields whose parent flattener supplies the actual Read/write
  path; and deterministic ordering/JSON rendering.
