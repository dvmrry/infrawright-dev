# Builder Handoff: Private ZPA Transform Cohort

## Intent

- Add one deliberately small, private Node transform cohort for already-pulled
  ZPA JSON without widening the product-neutral transform kernel.
- Prove exact rendered-byte parity with `engine.transform` for two resources
  that use only already-supported scalar, boolean, set-string, and nested-block
  shapes.
- Keep every public process operation, collector, publisher, Terraform lane,
  adoption/oracle lane, and generated-config qualification unchanged.
- Preserve the exact public ZCC catalog and transform behavior.

## Base / Head

- Base: `f812740c9f786be9ce436f558251d3dff82c14bd` (the local reviewed ZIA
  cohort head used for consolidation; it was not yet `origin/main`).
- Head: the checkpoint commit on `feature/node-zpa-transform-cohort`; resolve
  with `git rev-parse HEAD` before review.
- Diff command:
  `git diff f812740c9f786be9ce436f558251d3dff82c14bd...HEAD`.

## Files Changed

- Files:
  - `catalogs/zpa-transform-cohort-catalog.v1.json`
  - `docs/node-process-api.md`
  - `docs/review-handoffs/zpa-transform-cohort.md`
  - `node-src/domain/zpa-pull-transform.ts`
  - `node-src/domain/zpa-transform-cohort-catalog.ts`
  - `node-tests/fixtures/zpa-transform-cohort.v1.json`
  - `node-tests/zpa-transform-cohort.test.ts`
  - `tests/pack-test-requirements.json`
  - `tests/test_zpa_transform_cohort_catalog.py`
  - `tools/zpa_transform_cohort_catalog.py`
- Files intentionally left untouched:
  - The process request/response schemas and all public operation routing.
  - ZPA collectors, HTTP adapters, artifact publication, import/adoption,
    Terraform execution, and generated-config paths.
  - The generic transform kernel and the public five-resource ZCC catalog.
  - The reviewed generic cohort compiler/schema supplied by the ZIA base.
  - ZPA pack overrides, provider schema, registry, and provider evidence.

## Source Inputs Consulted

- Provider schemas:
  - `packs/zpa/schemas/provider/zpa.json`, pinned by the ZPA pack at 4.4.6.
- OpenAPI/API contracts:
  - None. This slice accepts already-pulled JSON and makes no collection or
    endpoint claim.
- Provider source files:
  - No new direct provider-source interpretation. The catalog binds the
    already-reviewed `docs/evidence/zpa-provider-v4.4.6.json` bytes and its
    pinned provider commit
    `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`.
- Pack metadata:
  - `packs/zpa/pack.json`
  - `packs/zpa/registry.json`
  - Absence of overrides for `zpa_pra_console_controller` and
    `zpa_pra_portal_controller`.
- Existing docs or design records:
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
  - `docs/review-handoffs/zscaler-transform-contract-lift.md`
  - `docs/review-handoffs/zpa-provider-v4.4.6-evidence.md`
  - `docs/review-handoffs/zia-transform-cohort.md`
- Other source evidence:
  - `engine.transform.transform_items` is the independent runtime oracle.
  - Public `engine.transform_catalog.transform_resource_cohort` constructs the
    core resource/projection contracts; the ZPA tool only decorates those
    resources with product-specific evidence after enforcing its pins.
  - `catalogs/zcc-transform-catalog.v1.json` supplies the reviewed Python
    `html.unescape` compatibility table and is included in the cohort source
    digest.

## Generated Artifacts

- Reports:
  - None.
- Schemas:
  - No new schema. The reviewed ZIA base supplies the generic private-cohort
    schema/compiler. The ZPA evidence envelope remains governed by its exact
    private validator because its provider, absent-override, compatibility,
    and per-resource evidence fields are intentionally outside that generic
    core contract.
- Fixtures:
  - `node-tests/fixtures/zpa-transform-cohort.v1.json` is sanitized input for
    the real Python/Node rendered-byte differential. It contains no live
    tenant values or credentials.
- Snapshots:
  - `catalogs/zpa-transform-cohort-catalog.v1.json` is generated from the
    committed ZPA pack, registry, provider schema, provider-evidence matrix,
    and existing Python-compatibility catalog.
- Demo or lab outputs:
  - None.
- Artifact drift intentionally expected:
  - One new private two-resource catalog and one sanitized differential input.
    Existing ZCC catalogs and fixtures remain byte-identical.

## Expected Delta

- Expected behavior change:
  - Internal TypeScript callers can transform already-pulled JSON for exactly
    `zpa_pra_console_controller` and `zpa_pra_portal_controller` through the
    existing pure kernel.
- Expected report/count/coverage changes:
  - Five new Node tests and six Python catalog tests.
  - No provider-readiness or generated-config qualification count changes.
- Expected generated-output changes:
  - Only the new private catalog. It continues to record
    `terraform_runtime_evidence_required` for both resources.
- Expected no-op areas:
  - Every public process operation and all ZCC behavior.

## Invariants Claimed

- Evidence must not be silently dropped:
  - Unknown provider fields remain in `originals` and appear in the sorted
    unacknowledged `drops` result. The wrapper documents that any non-empty
    drop list is a failed evidence gate.
- Generic matcher evidence must not outrank source-backed evidence:
  - No matcher or provider-readiness behavior is added. Each catalog resource
    carries the merged evidence matrix's exact state-shape hash and gated
    generated-config status.
- Source precedence/provenance must remain explicit:
  - Catalog bytes bind the exact pack, registry, provider schema, provider
    evidence, and Python-compatibility catalog bytes with one source digest.
  - Removing `provider_evidence` from each decorated ZPA resource must produce
    exact equality with public `transform_resource_cohort` output.
  - A newly added resource override makes generation fail rather than silently
    changing transform semantics.
- Ambiguity must stay classified instead of being coerced to success:
  - Unsupported resource types fail with
    `UNSUPPORTED_ZPA_TRANSFORM_RESOURCE`; catalog drift fails the exact gate.
- Provider-readiness counts must stay explainable:
  - No readiness counts or qualification states are changed.
- Adoption safety invariants:
  - This slice performs no import, adoption, state projection, Terraform, or
    publication.
  - Raw JSON numbers must be losslessly parsed; native JavaScript numbers and
    non-finite numeric values fail before projection.
  - The internal wrapper is not reachable from the public process host.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm test`
  - `npm run build`
  - `node --test .node-test/node-tests/zpa-transform-cohort.test.js`
  - `python3 -m unittest -v tests.test_zpa_transform_cohort_catalog`
  - `make test`
  - `python3 -m engine.audit_vendor_boundary`
  - `python3 tools/zpa_transform_cohort_catalog.py --check catalogs/zpa-transform-cohort-catalog.v1.json`
  - `python3 -m engine.transform_catalog --product zia --resource zia_admin_roles --resource zia_traffic_forwarding_static_ip --resource zia_url_categories --check catalogs/zia-transform-cohort.v1.json`
  - `python3 -m engine.transform_catalog --product zcc --check catalogs/zcc-transform-catalog.v1.json`
  - `git diff --check`
- Relevant output summary:
  - Focused Node: 5/5 pass, including exact rendered bytes from the real Python
    transform for nested list/set blocks, HTML unescape, lossless integers,
    booleans, scalar/empty/set coercion, and duplicate-preserving set order.
  - Combined ZIA/ZPA Node cohort checks: 10/10 pass.
  - Combined generic/ZIA/ZPA Python catalog checks: 33/33 pass.
  - Full Node: 617 pass, 1 platform skip, 0 failures on the isolated final
    run; typecheck and production bundle build pass.
  - Full Python: 1,388 pass, 1 opt-in provider-source skip, 0 failures.
  - Vendor boundary: 187 allowed matches, 0 violations.
  - ZPA, ZIA, and ZCC transform catalog freshness checks and whitespace check
    pass.
  - One full Node attempt overlapped another agent's full Node run and hit the
    existing Terraform descendant-PID timeout; Node counts both the failed
    subtest and parent suite. Every ZIA/ZPA test passed in that attempt. After
    the other process exited, the isolated full suite passed completely.
- Tests not run and why:
  - No live tenant, HTTP, provider, Terraform, import, plan, apply, state, or
    generated-config test was run because none of those capabilities is
    introduced or claimed.

## Known Deferrals

- Deferred work:
  - A public process operation, collector, publisher, adoption/oracle lane,
    Terraform lane, and generated-config qualification for ZPA.
  - `zpa_app_connector_group` and `zpa_application_server`, which require
    reviewed `drop_if_default` behavior; connector group also carries range
    policy.
  - `zpa_application_segment`, which additionally requires object-list
    attributes, merged blocks, and enum-to-boolean value mapping.
  - A later rebase onto merged ZIA plus the shared snake/string-semantics fix;
    this checkpoint deliberately uses the reviewed local ZIA head so the
    consolidation can be assessed independently.
- Reason it is safe to defer:
  - No public or mutating runtime path can reach this cohort. Unsupported
    resources and catalog changes fail closed, and Python remains the oracle.
  - The generated ZPA catalog is unchanged by the consolidation: source digest
    remains `e1dbc94c...` and exact file SHA-256 remains `eab7f5ce...`.
    The ZPA 4.4.6 evidence binding and override-absence gate remain local.
- Follow-up owner or trigger:
  - After ZIA and shared string semantics are both on `origin/main`, rebase
    again and run a fresh changed-surface review before any push or PR.

## Review Focus

- Highest-risk files or paths:
  - `tools/zpa_transform_cohort_catalog.py`
  - `catalogs/zpa-transform-cohort-catalog.v1.json`
  - `node-src/domain/zpa-transform-cohort-catalog.ts`
  - `node-src/domain/zpa-pull-transform.ts`
  - `node-tests/zpa-transform-cohort.test.ts`
- Specific assumptions to attack:
  - The selected resources truly have no override and use only current kernel
    encodings.
  - Stripping the ZPA evidence decoration yields the exact public generic
    compiler resources without private-helper imports or semantic rewrites.
  - `pra_application` and `pra_portals` cardinality/projection matches Python
    for object and list-shaped raw values.
  - Reusing the exact ZCC catalog's compatibility table preserves ZPA's
    two-pass top-level `name`/`description` unescape behavior.
  - Computed top-level `id` stays silently omitted while new provider fields
    remain visible as unacknowledged drops.
  - The private TypeScript catalog gate cannot authorize a third resource or a
    changed projection.
- Source evidence the reviewer should verify:
  - The two registry fetch rows, provider-schema projections, pack pin, evidence
    resource rows/state-shape hashes, and absence of both override files.
- Generated artifacts the reviewer should compare:
  - Regenerate the catalog and compare exact bytes.
  - Run the real Python differential rather than treating the generated catalog
    or builder summary as the oracle.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - Nested reference-object ID unwrapping, list/set shape coercion, Unicode set
    ordering with duplicates, empty/scalar sets, HTML double-unescape,
    arbitrary-size integer string coercion, native/non-finite numbers, catalog
    source drift, and newly introduced raw provider fields.
