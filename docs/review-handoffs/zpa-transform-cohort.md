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

- Base: `c1ad94b8657bf1371f5d84d948e80c55382c80a4`, the exact reviewed
  ZIA head at PR #180 when the two ZPA commits were replayed with `--onto`.
- Head: the clean `ready for adversarial review` checkpoint on
  `feature/node-zpa-transform-cohort`; resolve its exact immutable hash with
  `git rev-parse HEAD` when starting the review.
- Diff command:
  `git diff c1ad94b8657bf1371f5d84d948e80c55382c80a4...HEAD`.

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
  - `engine.transform.transform_items` and `engine.transform.render_tfvars` are
    the independent live runtime oracles for the complete result envelope and
    exact tfvars bytes.
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
  - Seven new Node tests and six Python catalog tests.
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
  - JSON object insertion order is not semantic for projection attribute/block
    maps: reordered semantic copies are accepted and replaced with the embedded
    canonical snapshot. Ordered arrays such as `source_files`, resource order,
    key fields, import segments, and projection lists retain their exact order.
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
  - `npm run build`
  - `npm run build:test`
  - `node --test .node-test/node-tests/zpa-transform-cohort.test.js .node-test/node-tests/zia-transform-cohort.test.js .node-test/node-tests/zcc-pull-artifacts-differential.test.js`
  - `npx --yes node@24.18.0 --test .node-test/node-tests/zpa-transform-cohort.test.js .node-test/node-tests/zia-transform-cohort.test.js .node-test/node-tests/zcc-pull-artifacts-differential.test.js`
  - `node --test .node-test/node-tests/*.test.js`
  - `npx --yes node@24.18.0 --test .node-test/node-tests/*.test.js`
  - `python3 -m unittest -v tests.test_transform_catalog tests.test_zia_transform_cohort_catalog tests.test_zpa_transform_cohort_catalog`
  - `make test`
  - `python3 -m engine.audit_vendor_boundary`
  - `python3 tools/zpa_transform_cohort_catalog.py --check catalogs/zpa-transform-cohort-catalog.v1.json`
  - `python3 -m engine.transform_catalog --product zia --resource zia_admin_roles --resource zia_traffic_forwarding_static_ip --resource zia_url_categories --check catalogs/zia-transform-cohort.v1.json`
  - `python3 -m engine.transform_catalog --product zcc --check catalogs/zcc-transform-catalog.v1.json`
  - `node tools/generate-python-lower-151.mjs --ucd-root /tmp/infrawright-ucd --check`
  - `git diff --check`
- Relevant output summary:
  - Focused ZPA Node: 7/7 pass, including complete result-envelope and exact
    `render_tfvars` byte comparison against live Python, nested list/set blocks,
    HTML unescape, lossless integers, booleans, scalar/empty/set coercion,
    duplicate-preserving set order, semantic projection-map reordering,
    Unicode 15.1 snake/dot boundaries, and production bundle exclusion.
  - Combined live-Python ZCC/ZIA/ZPA Node differentials: 14/14 pass on both
    Node 24.15.0/Unicode 16.0 and Node 24.18.0/Unicode 17.0.
  - Combined generic/ZIA/ZPA Python catalog checks: 33/33 pass.
  - Full Node on each of Node 24.15.0/Unicode 16.0 and Node
    24.18.0/Unicode 17.0: 631 total, 630 pass, 1 expected platform skip,
    0 failures. Typecheck and production bundle build pass.
  - Full Python: 1,388 total, 1,387 pass, 1 opt-in provider-source skip,
    0 failures.
  - Vendor boundary: 187 allowed matches, 0 violations.
  - ZPA, ZIA, and ZCC transform catalog freshness checks, the generated Python
    lowercase compatibility check, and whitespace check pass.
  - The ZPA source digest remains
    `e1dbc94cd82cfb824e88cfa2db3cc7398787369557d16dc23b660a1c2302a149`;
    exact catalog SHA-256 remains
    `eab7f5ce8f3e508629cd6a3cebd344332f57647442741717762e7373e2ae5694`.
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
- Reason it is safe to defer:
  - No public or mutating runtime path can reach this cohort. Unsupported
    resources and catalog changes fail closed, and Python remains the oracle.
  - The generated ZPA catalog is unchanged by the consolidation: source digest
    remains `e1dbc94c...` and exact file SHA-256 remains `eab7f5ce...`.
    The ZPA 4.4.6 evidence binding and override-absence gate remain local.
- Follow-up owner or trigger:
  - A fresh read-only adversarial reviewer must assess the exact checkpoint
    before any push or PR. Public reachability and additional ZPA resources
    remain separate, independently reviewed slices.

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
  - Reordered projection attribute/block maps are accepted only as semantic
    copies and return the canonical embedded snapshot, while ordered arrays
    still fail when reordered.
  - The production process bundle contains no ZPA catalog, provider schema,
    private wrapper, evidence commit/hash/status, or resource marker.
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
    source drift, newly introduced raw provider fields, U+A7CB lowercase drift,
    U+2028 regex-dot behavior, and insertion-order-only contract copies.
