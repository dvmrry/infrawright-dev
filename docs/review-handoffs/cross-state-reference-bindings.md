# Cross-state Reference Bindings Review Handoff

## Intent

- Add an opt-in deployment mode that keeps resource types in separate state
  roots while compiling pack-declared references into Terraform expressions.
- Preserve same-root `module.*` bindings and use minimal
  `terraform_remote_state` ID outputs only when a declared reference crosses a
  root boundary.
- Let new deployments use singleton roots instead of automatic slug groups,
  without splitting, migrating, or changing existing grouped state.
- Keep the entire legacy path byte-compatible when the option is absent.

## Base / Head

- Base: `676c12cbdce7960b2dd4d24c88157a103060f153` (draft PR #224 head)
- Head: `feature/reference-binding-qualification` (reviewer must record and
  freeze the exact commit before review)
- Diff command:
  `git diff 676c12cbdce7960b2dd4d24c88157a103060f153...feature/reference-binding-qualification`

## Files Changed

- Deployment option and topology: `node-src/domain/types.ts`,
  `node-src/domain/deployment.ts`, `node-src/domain/reference-topology.ts`.
- Binding compilation and parsing: `node-src/domain/transform-artifacts.ts`,
  `node-src/domain/transform-runner.ts`, `node-src/domain/adopt-runner.ts`,
  `node-src/domain/expression-bindings.ts`.
- Root/plan integration: `node-src/domain/environment-generator.ts`,
  `node-src/domain/reference-backend.ts`, `node-src/domain/plan-lifecycle.ts`.
- Focused Node and real local-Terraform tests under `node-tests/`.
- Operator documentation and live-qualification runbook under `docs/`.
- Files intentionally left untouched: pack reference metadata, provider code,
  provider schemas, Fetch, modules, import staging, assessment semantics,
  saved-plan fingerprint schema, exact-plan Apply, and deployment defaults.

## Source Inputs Consulted

- Provider schemas: committed ZIA and ZPA provider schemas were read only to
  bound the current top-level ID-reference scope; no schema was changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A; existing provider-observed IDs remain
  authoritative.
- Pack metadata: committed transform-reference declarations, currently
  `zia_url_filtering_rules.url_categories -> zia_url_categories` and
  `zpa_application_segment.segment_group_id -> zpa_segment_group`.
- Existing docs or design records: `docs/adoption-command-surface.md`,
  `docs/terraform-expression-bindings.md`, root topology and saved-plan
  lifecycle documentation.
- Other source evidence: Terraform `terraform_remote_state` documentation,
  azurerm backend documentation, the existing referent lookup sidecars,
  existing same-root binding behavior, and the generated module `items` output.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: test-owned temporary deployment/pack trees only.
- Snapshots: local test-only Terraform state for built-in `terraform_data`;
  removed with its temporary directory.
- Demo or lab outputs: a local producer state, dependent consumer Apply, and
  second consumer no-op plan with no external provider or credentials.
- Artifact drift intentionally expected only when the new flag is enabled:
  generated binding JSON, remote-state data blocks, minimal sensitive ID
  outputs, backend input variable, and smoke-test data overrides.

## Expected Delta

- Expected behavior change: `cross_state_references: true` permits top-level
  pack-declared references across singleton state roots. Same-root references
  remain module expressions. The option is mutually exclusive with the legacy
  `bind_references` switch.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: opted-in referrer roots read the exact
  referent root and opted-in referent roots publish only stable-key-to-ID maps.
  Predefined/system IDs absent from managed lookup evidence remain literals
  with existing visible skip notes.
- Expected no-op areas: a deployment without the option, explicit groups,
  transform/adopt identity, tfvars/import/lookups/moves, plan classification,
  fingerprint format, and exact saved-plan Apply.

## Invariants Claimed

- Evidence must not be silently dropped: missing referent lookup evidence keeps
  the literal and emits the existing skip diagnostic; no guessed key is made.
- Generic matcher evidence must not outrank source-backed evidence: only
  committed pack reference declarations are eligible.
- Source precedence/provenance must remain explicit: raw/adopted lookup
  sidecars decide the stable configuration key; provider IDs remain values.
- Ambiguity must stay classified instead of being coerced to success: missing
  roots/resources, cycles, unsafe backend data, and malformed selectors fail.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: referent state is applied first; deployment plan
  and assessment remain the convergence gates; no remote mutation is added.

## Tests Run

- `npm run build` passed.
- Focused deployment, Transform/Adopt binding, environment, plan-lifecycle,
  topology, and local Terraform tests passed (42 focused tests plus the real
  local producer/consumer convergence test).
- The complete Python generated-root differential passed all 9 profiles,
  including the full 151-root tree; legacy bytes are unchanged.
- `npm test` ran 826 tests: 825 passed. One untouched ZCC timing test missed its
  simultaneous protection-mutation window; its immediate isolated rerun passed
  2/2. The failure is recorded rather than represented as a clean full gate.
- `git diff --check` passed.
- Tests not run: no live ZIA/ZPA credentials, azurerm backend, remote provider,
  or deployment Apply. Those are explicitly downstream qualification.

## Known Deferrals

- Nested reference paths and references requiring an ID representation other
  than `item.id` are deferred. The current binding engine supports top-level
  fields only; broad ZIA/ZPA mapping would overclaim coverage.
- Additional source-backed ZIA URL-category consumers are a separate pack-data
  change after this engine mode is accepted.
- Existing grouped state is not auto-split or migrated. Existing operators must
  make a deliberate state migration; new deployments omit slug grouping.
- Remote-state contents are not included in the dependent root's saved-plan
  fingerprint. Pipelines must prevent referent mutation between dependent
  plan/Apply and regenerate dependent plans after any referent-state change.
- `terraform_remote_state` requires authorization to read the full state
  snapshot even though generated HCL exposes only the minimal ID output.
- The base branch pins `@apidevtools/swagger-parser@12.1.0`; the separately
  reported internal-registry coordinate `swagger-parser@8.4.2` does not match a
  public package/version. This inherited source-build issue is not changed or
  guessed here; the prebuilt runtime remains unaffected.

## Review Focus

- Highest-risk files: `reference-topology.ts`, `transform-artifacts.ts`,
  `expression-bindings.ts`, `environment-generator.ts`,
  `reference-backend.ts`, and `plan-lifecycle.ts`.
- Attack whether legacy bytes really remain exact; operator-authored bindings
  can smuggle unsupported remote-state selectors; output ID types match all
  currently declared referents; and selected generation cannot omit a needed
  referent root/output.
- Attack local and azurerm state-key derivation, tenant/root isolation,
  credential-key filtering, error redaction, backend-file mutation windows,
  and whether init/plan/Apply receive the required variable at the right time.
- Attack cycle detection, explicit-group collapse, dependency ordering, missing
  state on first adoption, and stale referent state between plan and Apply.
- Verify the generated smoke override cannot conceal an invalid production
  expression and the local real-Terraform test proves second-run convergence.
- Verify mixed ZIA lists bind managed custom categories while retaining
  predefined/system tokens literally and visibly.
