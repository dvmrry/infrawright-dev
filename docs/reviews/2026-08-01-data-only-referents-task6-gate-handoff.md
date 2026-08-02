# Builder Review Handoff: data-only referents final-gate findings 2–6 and risk 2

This handoff follows `docs/review-handoff-template.md`. It covers the scoped
implementation worker's uncommitted changes for the final-gate report. A
fresh-context reviewer must inspect the source and diff; this builder does not
self-approve the change.

## Intent

- What problem does this change solve?

  It closes the assigned final-gate defects around data-only referent roots:
  published `plan-roots` schema drift, exact lifecycle selection, default
  fetch/transform omission, unsafe adoption entry, and silent acceptance or
  omission of references declared by a data referent. It also removes the
  misleading “generated resource” qualification terminology.

- What user-visible or maintainer-visible behavior should change?

  `plan-roots` is a schema-v2 document whose every root has a required
  `data_referents` string array. An exact data-referent selector can address
  the singleton data root through roots, environment generation, plan, and
  refresh. A no-selector transform processes active data pulls before their
  generated referrers and publishes configuration plus lookup artifacts only.
  Adoption refuses data-referent types at its command boundary with a
  classified error. Invalid data-referent referrer declarations fail during
  metadata loading and also fail defensively in refedge resolution and
  transform ordering.

- What behavior must stay unchanged?

  Bare product/provider selectors continue to expand the generated lane only;
  data referents remain out of generated-referrer enumeration and adoption.
  Existing root-topology and changed-path-scope documents remain schema v1.
  Cross-state reference fields declared by valid generated referrers continue
  to use the existing merged-manifest precedence and deterministic ordering.
  No data-referent imports or pending moves are produced.

## Base / Head

- Base: `49f916ef9fa0419b31ea1b7454f14422a2e32459` (current HEAD before this
  worker's uncommitted changes)
- Head: uncommitted working tree on `claude/data-only-referents`; no commit,
  stage, push, or other git write was performed
- Diff command: `git diff 49f916ef9fa0419b31ea1b7454f14422a2e32459 --` followed
  by inspection of the untracked scoped tests

## Files Changed

- Files:

  - `docs/schemas/plan-roots.schema.json`
  - `go/cmd/iw/data_referent_lifecycle_test.go`
  - `go/cmd/iw/data_referent_transform_test.go`
  - `go/cmd/iw/plan_roots_schema_test.go`
  - `go/cmd/iw/testdata/v2_topology/plan-roots.stdout`
  - `go/cmd/iw/testdata/v2_transform/transform.stderr`
  - `go/internal/adopt/runner.go`
  - `go/internal/adopt/runner_test.go`
  - `go/internal/envgen/data_referent_test.go`
  - `go/internal/envgen/pack_scope_test.go`
  - `go/internal/envgen/reference_topology_test.go`
  - `go/internal/metadata/loader.go`
  - `go/internal/metadata/loader_test.go`
  - `go/internal/metadata/packs.go`
  - `go/internal/refedges/refedges.go`
  - `go/internal/refedges/refedges_test.go`
  - `go/internal/roots/full_surface_qualification_test.go`
  - `go/internal/roots/planroots.go`
  - `go/internal/roots/render.go`
  - `go/internal/roots/render_test.go`
  - `go/internal/roots/roots.go`
  - `go/internal/roots/roots_test.go`
  - `go/internal/transform/selection.go`
  - `go/internal/transform/selection_test.go`
  - this handoff

- Files intentionally left untouched:

  - Concurrent worker scope: `go/internal/plan`,
    `go/internal/assessment`, `go/internal/transform/data_referent.go`,
    `go/internal/transform/data_referent_test.go`, `go/internal/tfrender`,
    and `go/internal/transformrun`.
  - `go/internal/collectors` production code; its existing fetch selector was
    sufficient and the end-to-end regression exercises it without modifying
    it.
  - All provider packs, provider schemas, OpenAPI files, and source adapters.

## Source Inputs Consulted

- Provider schemas: The synthetic provider-neutral schemas used by the new
  focused fixtures, plus the existing loaded-pack metadata contracts. No
  provider schema was modified in this worker.
- OpenAPI/API contracts: N/A; no provider operation mapping changed.
- Provider source files: N/A; no provider adapter changed.
- Pack metadata: The loaded manifest/registry/schema merge behavior in
  `go/internal/metadata`, the synthetic data-root fixtures, and the committed
  full-profile metadata exercised by the existing qualification and CLI
  tests.
- Existing docs or design records: The complete raw gate report at
  `/private/tmp/claude-501/-Users-dm-src-gh-dvmrry-infrawright-dev--claude-worktrees-provider-sdk-openapi-lineage-be2449/5fb83b2b-ce7d-4bae-b46d-aedc395902c6/scratchpad/solmax-gate-report.md`,
  `AGENTS.md`, `docs/adversarial-review.md`, and
  `docs/review-handoff-template.md`.
- Other source evidence: Existing merged-reference implementations in
  `refedges` and `transform`, root selector callers used by plan/refresh and
  environment generation, and the committed CLI golden tests.

## Generated Artifacts

- Reports: None.
- Schemas: `docs/schemas/plan-roots.schema.json` is updated to schema v2,
  requiring `data_referents` as an array of strings on every root.
- Fixtures: New synthetic CLI fixtures are created in temporary directories by
  tests; no generated temporary directories are retained.
- Snapshots: `go/cmd/iw/testdata/v2_topology/plan-roots.stdout` is bumped to
  schema version 2. `go/cmd/iw/testdata/v2_transform/transform.stderr` gains
  the deterministic no-pull skip for the now-selected data referent.
- Demo or lab outputs: None retained. The CLI module tree generated during
  tests was removed from the exact `go/cmd/iw/modules` path after each run.
- Artifact drift intentionally expected: Only the schema-v2 plan-roots
  output and the default-transform diagnostic line above. Existing root and
  scope v1 bytes remain unchanged.

## Expected Delta

- Expected behavior change:

  - Exact selectors recognize data referent types as root targets.
  - Empty-selector transform selection adds active data referents, then uses
    referent-first reference ordering.
  - Adoption and invalid data-referrer declarations fail closed at explicit
    boundaries.

- Expected report/count/coverage changes: The qualification test terminology
  now says “root-bearing resource” and “root-bearing resource_roots”; the
  checked count remains 152. No provider-readiness count was changed.
- Expected generated-output changes: `plan-roots` emits schema version 2 and
  the required field; the existing v2 transform stderr golden records the
  newly selected data pull's skip when its file is absent.
- Expected no-op areas: Product/provider/bare generated selector expansion,
  generated-referrer enumeration, adoption of generated resources, and root
  topology/scope schema-v1 renderers.

## Invariants Claimed

- Evidence must not be silently dropped: Data-referent roots are represented
  in the public plan-roots contract, selected explicitly when requested, and
  included in the default transform lane when active. Unsafe references from
  a data referent are refused rather than omitted.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or provider-source evidence path changed.
- Source precedence/provenance must remain explicit: Effective metadata
  references follow active manifest order and field overwrite semantics, while
  refedges and transform keep deterministic sorted walks.
- Ambiguity must stay classified instead of being coerced to success: An
  unsafe data-referrer declaration is a load-time/defensive refusal naming the
  referrer and field; adoption uses `UNSUPPORTED_ADOPTION_RESOURCE` with a
  domain category.
- Provider-readiness counts must stay explainable: The qualification message
  uses root-bearing terminology; no count or provider coverage assertion is
  silently reinterpreted.
- Adoption safety invariants: The data-referent refusal occurs immediately
  after selector expansion and before diagnostics, root topology loading,
  pull reads, identity derivation, Terraform resolution, state loading, or
  artifact callbacks.

## Tests Run

- Commands (with `GOCACHE` and `GOTMPDIR` set to the workspace's ignored
  `.gocache` and `.gotmp` directories):

  - `gofmt -l $(rg --files -g '*.go' go)` — clean.
  - `git diff --check` — clean.
  - `go test ./internal/roots/ ./internal/refedges/ ./internal/metadata/ ./internal/envgen/ ./internal/adopt/ ./internal/transform/ ./internal/collectors/ -count=1` — pass.
  - `go test ./cmd/iw/ -skip 'TestFetchRecordedTransport' -count=1` — pass.
  - `go test ./cmd/iw/ -count=1` — the only failure is the documented
    sandbox restriction in `TestFetchRecordedTransport`: `httptest` cannot
    bind `tcp6 [::1]:0` (`operation not permitted`).
  - Focused CLI regressions for schema validation, default transform, exact
    plan, and exact refresh — pass.

- Relevant output summary: The requested internal package tail is green; the
  full CLI package is green with only the loopback test excluded; formatting
  and whitespace checks are clean.
- Focused regression and pre-fix/unsafe-mutation proof:

  - Finding 2: a controlled mutation of the committed schema's
    `schema_version` const from 2 to 1 made the real CLI-output/schema
    validation fail in both empty and non-empty cases (`value must be 1`).
    Restoring v2 made the focused test pass.
  - Finding 3: before the exact-selector expansion, the focused root and
    gen-env data-selector regressions failed with the existing
    “unknown or non-generated resource selector” behavior. Restoring the
    generated-or-data exact branch makes plan-roots, gen-env, plan, and
    refresh select the single data root; the no-selector root expansion still
    returns generated resources only.
  - Finding 4: removing the new no-selector data lane as a faithful pre-fix
    mutation made the synthetic transform regression publish only
    `sample_rule` and leave the data pull unprocessed. Restoring the lane
    passes with publication order `sample_groups_data`, `sample_rule`, a data
    config and lookup, and no data imports/moves.
  - Finding 5: removing the adopt boundary guard as a faithful pre-fix
    mutation made the focused spy test return nil instead of a classified
    refusal. Restoring it yields `UNSUPPORTED_ADOPTION_RESOURCE`, names the
    type, and records zero loader, diagnostic, output, or side-effect calls.
  - Finding 6: the pre-fix metadata fixture loaded successfully; defensive
    refedges, envgen topology, and transform-order tests silently returned or
    skipped the invalid data referrer. Restoring semantic load-time validation
    plus defensive refusals makes the focused tests fail closed and names the
    referrer (and `references.<type>.<field>` at load time). The old envgen
    fixture arm that required silent omission was removed accordingly.

- Promotion efficiency: Wall-clock time from the first candidate tip was not
  measured. The CLI package promotion gate was attempted four times in this
  session: the initial diagnostic run, a corrected skip run, the final exact
  run (blocked only by the sandbox loopback listener), and the final corrected
  skip run. Focused regressions preceded the final promotion gate.
- Tests not run and why: No live provider/API tests were run; no external
  network or Terraform service is needed for the scoped synthetic regressions.
  The repository-wide `go test ./...` was not requested for this worker and
  would include concurrent worker scope.

## Known Deferrals

- Deferred work: The separate non-blocking risk concerning the
  `PlanRootsFromResourceSet` data-referent projection remains outside this
  assignment; this worker addressed only risk 2's qualification terminology.
  Concurrent findings 1 and 7 remain in the other worker's files.
- Reason it is safe to defer: The coordinator explicitly assigned those
  surfaces to another worker or excluded them from this scope. The CLI
  plan-roots path covered here uses the loaded-pack projection and is schema
  validated end to end.
- Follow-up owner or trigger: The coordinator/other implementation worker;
  the fresh adversarial reviewer should verify the scope boundary before
  accepting this handoff.

## Review Focus

- Highest-risk files or paths:

  - `go/internal/metadata/packs.go` and `loader.go`: effective active-manifest
    merge and the semantic data-referrer refusal.
  - `go/internal/refedges/refedges.go` and
    `go/internal/transform/selection.go`: defensive refusal ordering and
    generated/data lane partitioning.
  - `go/internal/roots/roots.go`, `planroots.go`, and `render.go`: exact
    selector admission, generated-only product expansion, and schema-v2
    rendering.
  - `go/internal/adopt/runner.go`: refusal placement relative to every
    observable callback.
  - `docs/schemas/plan-roots.schema.json` and the CLI schema/transform
    goldens.

- Specific assumptions to attack:

  - The active manifest list and the metadata validator's merge order exactly
    match the downstream refedges/transform merge semantics.
  - Every data-referent type in the loaded root is meant to enter the default
    transform lane, while no-selector root/product expansion remains
    generated-only.
  - Exact data selectors cannot accidentally widen product/provider/bare
    selectors or generated-referrer enumeration.
  - The adopt guard runs before all current and future side-effect callbacks.
  - The portable tenant regex is equivalent to the prior schema intent for
    the allowed tenant alphabet while permitting the Go validator to compile
    the committed schema.
  - The transform stderr golden's added skip is the only intentional full-
    profile default-selection drift.

- Source evidence the reviewer should verify: `expandResources`,
  `activeDataReferentTypes`, `validateDataReferentReferences`, the three
  defensive error sites, the `RunAdoptBatch` guard, `PlanRoots`/renderer
  schema version constants, and the actual CLI schema-validation helper.
- Generated artifacts the reviewer should compare: the v2 plan-roots schema
  and stdout golden, the v2 transform stderr golden, and the temporary
  synthetic CLI output for empty/non-empty data referent arrays.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  later active manifest field overwrites, an unsafe data referrer not included
  in an explicit transform selection, mixed data/generated selector lists,
  empty pulls, data roots with no import/move files, unknown selectors,
  provider/product selectors, and adoption calls with a valid pull plus a
  non-nil state loader.

Status: ready for adversarial review. Do not treat this builder handoff as an
acceptance verdict; the reviewer must use a fresh Codex context and the
repository's adversarial-review run prompt.
