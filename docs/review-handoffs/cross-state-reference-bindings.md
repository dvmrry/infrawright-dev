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
  `node-src/domain/reference-backend.ts`, `node-src/domain/plan-lifecycle.ts`,
  `node-src/domain/plan-contract.ts`, `node-src/domain/plan-assessment.ts`,
  `node-src/domain/plan-assessment-inputs.ts`, and
  `node-src/domain/exact-plan-apply.ts`.
- Focused Node and real local-Terraform tests under `node-tests/`.
- Source-backed nested ZPA reference declarations in `packs/zpa/pack.json`;
  required lookup sidecars are derived from those declarations only while a
  reference-binding mode is enabled.
- Operator documentation and live-qualification runbook under `docs/`.
- Files intentionally left untouched: provider code, provider schemas, Fetch,
  modules, import staging, saved-plan fingerprint schema, deployment Apply
  mechanics, and deployment defaults.

## Source Inputs Consulted

- Provider schemas: committed ZIA and ZPA provider schemas were read only. ZPA
  establishes ordered outer lists with required `set(string)` ID leaves for
  `server_groups`, `app_connector_groups`, and `servers`; ZIA establishes that
  `cbi_profile` is a list but does not make provider 4.7.26 import Read complete.
  No schema was changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A; existing provider-observed IDs remain
  authoritative.
- Pack metadata: the pre-existing top-level declarations plus the source-backed
  ZPA cohort `zpa_application_segment.server_groups.id -> zpa_server_group`,
  `zpa_server_group.app_connector_groups.id -> zpa_app_connector_group`, and
  `zpa_server_group.servers.id -> zpa_application_server`.
- Existing docs or design records: `docs/adoption-command-surface.md`,
  `docs/terraform-expression-bindings.md`, root topology and saved-plan
  lifecycle documentation.
- Other source evidence: retained raw/projected application-segment and
  server-group fixtures under `tests/fixtures/transform/`; Terraform
  `terraform_remote_state` documentation,
  azurerm backend documentation, the existing referent lookup sidecars,
  existing same-root binding behavior, and the generated module `items` output.

## Generated Artifacts

- Reports: the downstream scalar ZPA qualification is summarized without IDs,
  credentials, tenant values, state, or plan contents in the lab runbook.
- Schemas: None.
- Fixtures: test-owned temporary deployment/pack trees only.
- Snapshots: local test-only Terraform state for built-in `terraform_data`;
  removed with its temporary directory.
- Demo or lab outputs: a local producer state, dependent consumer Apply, and
  second consumer no-op plan with no external provider or credentials.
- Artifact drift intentionally expected only when the new flag is enabled:
  reference-derived lookup sidecars, generated binding JSON, remote-state data
  blocks, minimal sensitive ID outputs, backend input variable, and smoke-test
  data overrides.

## Expected Delta

- Expected behavior change: `cross_state_references: true` permits pack-declared
  top-level and exact indexed-list references across singleton state roots.
  Same-root references remain module expressions. The option is mutually
  exclusive with the legacy `bind_references` switch.
- Expected report/count/coverage changes: assessment now accepts one
  mechanically reconstructed engine-owned output create/update/no-op; it produces no
  finding and changes no report schema.
- Expected generated-output changes: opted-in referrer roots read the exact
  referent root and opted-in referent roots publish only stable-key-to-ID maps.
  Predefined/system IDs absent from managed lookup evidence remain literals
  with existing visible skip notes.
- Expected no-op areas: a deployment without the option, explicit groups,
  transform/adopt identity, tfvars/import/lookups/moves, arbitrary output
  rejection, fingerprint format, and exact saved-plan Apply execution.

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
  and assessment remain the convergence gates; output acceptance is bound to
  loaded topology and exact provider-observed planned IDs on initial and
  second-run plans. Empty referents require the expected generated module
  resource in Terraform configuration. No remote mutation is added.

## Tests Run

- `npm run build` and `npm run typecheck` passed.
- Focused expression-binding, deployment, Transform/Adopt artifact,
  environment, topology, metadata/profile, and real local-Terraform tests
  passed. Real Terraform covers local remote-state consumption at an indexed
  leaf, multiple indexed edits preserving siblings, local Apply, and a second
  no-op plan.
- The complete Python generated-root differential passed all 9 profiles,
  including the full 151-root tree; legacy bytes are unchanged.
- Focused assessment/input/exact-Apply tests and a real Terraform 1.15
  import-plan fixture prove that only the topology-bound, sensitive, fully
  known output map is accepted; absent contracts, arbitrary names, wrong IDs,
  unknowns, sensitivity changes, duplicate modules, and deletes fail closed.
- `npm test` passed all 838 selected Node tests after remediation.
- `git diff --check` passed.
- Tests not run: no repository-side live credentials, azurerm backend, remote
  provider, or deployment Apply. A downstream local-state-only scalar ZPA run
  reached an import-clean referrer plan; the updated assessor still needs a
  downstream rerun.

## Known Deferrals

- Wildcard/identity list selectors, unordered multi-element set traversal, and
  references requiring producer output other than `item.id` remain deferred.
- Only the three fixture/schema-backed ZPA nested edges are declared. Other ZPA
  nested fields and ZIA firewall group edges require their own source/live
  evidence before pack expansion.
- Indexed root bindings do not repair ZIA provider 4.7.26 `ISOLATE` import Read;
  the version-scoped `unsupported_if` classification remains required before
  Oracle execution.
- Additional source-backed ZIA URL-category consumers are a separate pack-data
  change after this engine mode is accepted.
- A missing nested referent lookup currently counts one skipped concrete field
  path even when that terminal field contains several IDs. Literals and
  per-field diagnostics remain correct; per-ID summary accounting is a
  non-blocking follow-up.
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
- Attack exact-index parsing, list sibling preservation, missing/out-of-range
  failure, schema distinction between ordered lists and unordered sets, HCL
  validation, and indexed-path canonicalization back to pack metadata.
- Verify nested derivation stops at the declared ID collection leaf, emits one
  list expression rather than per-ID target indexes, and retains unresolved
  literals with visible diagnostics.
- Verify the generated smoke override cannot conceal an invalid production
  expression and the local real-Terraform test proves second-run convergence.
- Verify mixed ZIA lists bind managed custom categories while retaining
  predefined/system tokens literally and visibly.

## Prior Review Result

- Fresh-context verdict on `9545bd970ac815871c52eb4026bf5854deeec083`:
  **Request changes**, with three blocking findings.
- Finding 1: operator-authored remote-state selectors could activate generated
  data blocks without the opt-in and the selector parser accepted suffixes.
  Fix: automatic inference now runs only in cross-state mode, requires the exact
  canonical selector, and validates referrer field/root/referent/root against a
  committed pack-declared edge. Regression tests cover the no-flag legacy path,
  noncanonical selectors, suffixes, and undeclared targets.
- Finding 2: selecting only a referrer omitted its producer root/output.
  Fix: `gen-env` expands selected roots through the cross-state referent closure
  before writing. A consumer-only ZPA test now requires both singleton roots.
- Finding 3: azurerm smoke tests omitted the required non-secret backend-address
  variable. Fix: every generated run supplies a safe test-only address object
  while `override_data` prevents external state access. A real Terraform 1.15
  test executes the resulting variable/override shape without credentials.
- Patch-focused verification: production build passed; 69 affected tests
  passed; the complete full-profile generated-root tree remained byte-identical
  to Python; typecheck and whitespace checks passed.
- Patch re-review target: `9545bd970ac815871c52eb4026bf5854deeec083..feature/reference-binding-qualification`.

The prior review later approved exact head
`eacca5305c96cbfb48fbde57e7adc03d2e111079`. The indexed-list and ZPA pack
delta after that head invalidates that approval. A fresh-context adversarial
review is required for the new frozen commit; no current approval is claimed.

Fresh-context review of indexed-list head
`d765067e88315ec1e3e1c1b6c85da0872722f3f8` requested changes for two blocking
findings: indexed HCL rendering duplicated the prior expression exponentially,
and three new unconditional ZPA lookup declarations changed the no-option
artifact tree. The remediation renders all indexed edits from one stable base
and makes lookup evidence inferred from reference metadata mode-scoped, while
retaining explicit historical `lookup_sources`. Regression coverage includes
large indexed edit sets, strict provider-shaped Terraform values, a complete
disabled artifact tree, enabled lookup bytes, and enabled-to-disabled cleanup.
Patch-focused re-review is required before this handoff claims approval.
