# Builder Review Handoff: value_rewrite fix round

This handoff follows `docs/review-handoff-template.md`. It covers the
uncommitted `value_rewrite` implementation and the requested fix round in the
Go module and authoring documentation. The builder stops here for a fresh
adversarial review and does not self-approve the changes.

## Intent

- What problem does this change solve?

  It completes the safety, projection, policy-loading, parity, diagnostics,
  and documentation contracts for the `value_rewrite` primitive. The fix
  round makes the primitive fail closed for unsupported adoption types,
  non-string targets, and `drop_if_default` collisions; validates operator
  policy entries against the provider schema at load time; classifies only
  exact intended transform/adopt differences; aggregates repeated projection
  diagnostics; and documents the vendor-normalization precondition and v1
  scope.

- What user-visible or maintainer-visible behavior should change?

  A valid rewrite is applied in the generated-adoption projection after
  projection fill and before pack default dropping. Pack validation rejects
  rewrites that cannot reach the generated-adoption lane, target a non-string
  attribute, or rewrite to the same-path `drop_if_default` value. An operator
  policy file with an invalid writable path or enum value fails while the
  policy is loaded. Parity reports an exact `from`-to divergence as the
  `value_rewrite` classification with its `why` text, while non-matching
  differences remain unclassified. Repeated firings are emitted once per
  distinct tuple with a count.

- What behavior must stay unchanged?

  The primitive remains adoption-projection-only and does not change the
  transform/write lane. Matching is exact whole decoded-value matching in v1;
  template and substring matching remain out of scope. Existing projection,
  pack-drop, conditional-omit, stale-order, parity, and policy precedence
  behavior remains unchanged except at the explicitly documented rewrite
  seam. The deferred schema-walker duplication and the separately tracked
  non-terminating loop are untouched.

## Base / Head

- Base: `a7c71c39` (`claude/post-import-assessment-scope`)
- Head: uncommitted working tree on `claude/post-import-assessment-scope`; no
  commit, stage, push, or other git write was performed
- Diff command: `git diff a7c71c39 --` plus inspection of the untracked
  `go/internal/metadata/schema_status.go` and
  `go/internal/metadata/value_rewrite_test.go`

The working tree already contained the initial uncommitted `value_rewrite`
implementation before this fix round. That implementation and unrelated
untracked files were preserved; the reviewer should inspect the complete
working-tree delta rather than treating this handoff as a commit boundary.

## Files Changed

- Files in the value-rewrite change surface:

  - `docs/import-oracle.md`
  - `docs/pack-authoring.md`
  - `docs/reviews/2026-08-31-value-rewrite-fix-round-handoff.md`
  - `go/internal/adopt/policy.go`
  - `go/internal/adopt/policy_test.go`
  - `go/internal/adopt/runner.go`
  - `go/internal/adopt/state_project.go`
  - `go/internal/adopt/state_project_test.go`
  - `go/internal/authoring/transformadoptparity/parity.go`
  - `go/internal/authoring/transformadoptparity/parity_test.go`
  - `go/internal/metadata/driftpolicy.go`
  - `go/internal/metadata/driftpolicy_runtime_test.go`
  - `go/internal/metadata/resources.go`
  - `go/internal/metadata/schema_status.go`
  - `go/internal/metadata/value_rewrite_test.go`

  The complete working-tree value-rewrite implementation also includes the
  already-uncommitted changes in `go/internal/adopt/generated_config_policy.go`,
  `go/internal/adopt/generated_config_schema.go`,
  `go/internal/authoring/reconcile/reconcile.go`,
  `go/internal/metadata/loader.go`, and `go/internal/transform/kernel.go`.

- Files intentionally left untouched:

  - Provider packs and provider-specific source mappings.
  - The duplicated F7 schema walker; only the requested agreement comment on
    `TerraformValueRewriteAttribute` was added.
  - The pre-existing non-terminating loop in `go/internal/adopt/state_project.go`
    around lines 503–524.
  - The unrelated untracked `engine/` tree and Python cache directories.

## Source Inputs Consulted

- Provider schemas: `ProviderSchemaStatus`, `TerraformValueRewriteAttribute`,
  `guardProjectionPath`, and the synthetic provider schemas used by the
  metadata and adoption tests.
- OpenAPI/API contracts: None changed or added for this fix round.
- Provider source files: None changed; the convergence behavior is a live
  vendor property and is intentionally not inferred from static metadata.
- Pack metadata: resource registry entries, `drop_if_default`, enum and
  writable-attribute metadata, and the derive/data-referent routing used by
  the adoption runner.
- Existing docs or design records: `docs/pack-authoring.md`,
  `docs/import-oracle.md`, `docs/adversarial-review.md`, and the existing
  `value_rewrite` implementation and tests.
- Other source evidence: `ProjectProviderState` operation order,
  `LoadAdoptionPolicy` merge behavior, `TransformAdoptParity` difference
  accounting, and the dropped-field diagnostic aggregation precedent in
  `go/internal/tfrender/transform_artifacts.go`.

## Generated Artifacts

- Reports: None generated or changed.
- Schemas: None generated or changed.
- Fixtures: No provider-pack fixtures or snapshots generated.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change:

  - The documented first rule is vendor normalization: after a successful
    write of `to`, Read must return `to`; otherwise committed configuration and
    refreshed state diverge forever and the post-import assert-clean gate
    remains blocked.
  - The override-key table and primitive documentation include
    `value_rewrite`, its required shape, seven guards, exact-match-only v1
    scope, and removability after a vendor fix.
  - Import-oracle projection order is explicitly
    `projection_sync -> projection_fill -> value_rewrite -> pack
    drop_if_default -> projection_omit_if`.
  - Unsupported registry lanes, invalid operator schema entries, non-string
    targets, and rewrite/default collisions fail closed.
  - Exact parity divergences are classified and repeated runtime firings are
    aggregated.

- Expected report/count/coverage changes: Parity gains the explicit
  `value_rewrite` accepted classification; value-rewrite runtime diagnostics
  gain a distinct-firing count. No provider-readiness count changes are
  expected.
- Expected generated-output changes: None outside runtime adoption diagnostics
  and parity classification output.
- Expected no-op areas: Transform/write behavior, provider source mappings,
  provider packs, the deferred F7 walker implementation, and the separately
  tracked loop.

## Invariants Claimed

- Evidence must not be silently dropped: A rewrite applies only to an exact
  whole string value at a valid projected path, and a non-matching parity
  difference remains unclassified.
- Generic matcher evidence must not outrank source-backed evidence: No generic
  matcher or provider-source mapping changed.
- Source precedence/provenance must remain explicit: The pack and operator
  policy entry is the source of the rewrite and its `why` evidence; runtime
  validation uses the provider schema for the target path and enum.
- Ambiguity must stay classified instead of being coerced to success: Parity
  classification requires the same resource type, exact path, exact `from`,
  and exact `to`; all other differences retain the unclassified outcome.
- Provider-readiness counts must stay explainable: No readiness or coverage
  accounting changed.
- Adoption safety invariants:

  - A rewrite can apply only to generated adopt types, not derive-delegated or
    data-referent transform-batch types.
  - The terminal attribute must be a writable string.
  - A rewrite cannot resolve to the same-path `drop_if_default` value.
  - The rewrite runs before pack default dropping.
  - Repeated firings are aggregated by `(resource type, path, from, to)` and
    retain the count.
  - The vendor-normalization precondition is documented as a hard operational
    precondition because static pack validation cannot observe live Read
    behavior.

## Tests Run

- Commands:

  - `cd go && GOCACHE=/private/tmp/infrawright-value-rewrite-gocache go test ./internal/metadata ./internal/adopt ./internal/authoring/transformadoptparity -count=1`
  - `cd go && GOCACHE=/private/tmp/infrawright-value-rewrite-gocache go build ./...`
  - `cd go && GOCACHE=/private/tmp/infrawright-value-rewrite-gocache go vet ./internal/metadata ./internal/adopt ./internal/authoring/transformadoptparity`
  - `gofmt -l` over every changed Go file
  - `git diff --check`
  - `cd go && GOCACHE=/private/tmp/infrawright-value-rewrite-gocache go vet ./...`
  - `cd go && GOCACHE=/private/tmp/infrawright-value-rewrite-gocache go test ./... -count=1`

- Relevant output summary: The focused metadata, adoption, and parity suites,
  the build, and vet for all touched implementation packages pass. A fresh
  repository-wide `go vet ./...` also passes. Formatting and whitespace checks
  are clean.

- Focused regression and pre-fix/unsafe-mutation proof:

  - Disabling the exact parity classifier caused the new parity test to fail
    before restoration.
  - Disabling the generated-adoption validation caused both derive and
    data-referent refusal tests to fail.
  - Disabling the diagnostic aggregator caused the 400-item test to observe
    400 duplicate lines instead of one counted line.
  - Disabling operator schema validation allowed the invalid operator policy
    to load, causing its focused test to fail.
  - Disabling the terminal-string guard allowed bool and number targets,
    causing both focused cases to fail.
  - Disabling the drop-default collision guard allowed the invalid pack entry,
    causing its focused test to fail.
  - Removing `value_rewrite` from the stale-order fixture caused the expected
    entry to be missing, proving the default-mode ordering assertion.

  Every temporary mutation was restored before the final focused run.

- Promotion efficiency: Wall-clock elapsed time was not measured. One focused
  run covered the final implementation after the unsafe-mutation checks; one
  repository-wide build, vet attempt, and test-gate attempt were made. No
  duplicate full-corpus test sweep was run on the same final state.
- Tests not run or not green and why:

  - The repository-wide `go test ./... -count=1` attempt exits nonzero only
    because two unrelated listener-dependent tests cannot bind the loopback
    `httptest` listener at `[::1]:0` in this restricted sandbox:
    `cmd/iw/TestFetchRecordedTransport` and
    `internal/httptransport/TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds`.
    The error is `operation not permitted`; the touched implementation
    packages and the repository-wide vet pass.

## Known Deferrals

- Deferred work: A fresh-context adversarial review and any fixes it identifies.
- Reason it is safe to defer: This is the mandated builder stop point; the
  change is explicitly not self-approved. F7 walker duplication and the
  separate state-project loop were explicitly deferred by the request.
- Follow-up owner or trigger: A fresh Codex reviewer should verify the complete
  uncommitted diff, especially the schema/registry gates, policy merge/load
  seam, parity exact-match classifier, projection/drop order, and diagnostic
  aggregation. Accepted findings must map from finding to root cause, fix,
  regression, and verification before acceptance.

## Review Focus

- Highest-risk files or paths:

  - `go/internal/metadata/resources.go`: schema-aware target validation,
    generated-adoption lane restriction, enum handling, and
    `drop_if_default` collision detection.
  - `go/internal/adopt/policy.go`: merged operator-policy validation and
    resource-registry/schema lookup behavior.
  - `go/internal/adopt/state_project.go` and `runner.go`: rewrite ordering,
    exact firing, tuple aggregation, and count emission.
  - `go/internal/authoring/transformadoptparity/parity.go`: exact path/value
    classification and preservation of unclassified differences.
  - `docs/pack-authoring.md` and `docs/import-oracle.md`: machine-delimited
    table integrity, seven-guard documentation, hard convergence rule, and
    actual projection order.

- Specific assumptions to attack:

  - The effective policy entries used by parity are the same validated entries
    that can be projected by adoption.
  - Exact JSON-pointer-to-policy-path matching cannot accidentally classify a
    sibling, indexed, templated, or differently typed difference.
  - The string terminal-type check and enum check agree with the projection
    walker and schema status for required and optional attributes.
  - The default-value comparison agrees with transform default semantics for
    string and integer representations without broadening the primitive's
    exact-match scope.
  - Aggregation cannot merge distinct `(resource type, path, from, to)` tuples
    or emit a count before all projected items have been examined.
  - A successful vendor write really does normalize to the stored `to` value;
    static pack validation cannot establish this and the `why` evidence must
    be reviewed as an operational input.

- Source evidence the reviewer should verify: the registry routing in
  `go/internal/adopt/runner.go`, schema status and projection-path guards,
  `ProjectProviderState` operation order, transform default matching, policy
  merge/load flow, and the exact focused regressions listed above.
- Generated artifacts the reviewer should compare: None.
