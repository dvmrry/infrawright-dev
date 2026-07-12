# Builder Review Handoff: Private ZIA Transform Cohort

## Intent

- Add one closed, private Node transform cohort for exactly
  `zia_admin_roles`, `zia_traffic_forwarding_static_ip`, and
  `zia_url_categories`.
- Consume already-pulled lossless JSON through the product-neutral kernel from
  PR #175 and reproduce the real Python `engine.transform` result plus rendered
  tfvars bytes for finite floats, provider string sets, string maps, the
  URL-category list-sort override, and unexpected-drop reporting.
- Keep the public process API, exact five-resource ZCC catalog/schema/bytes,
  collectors, artifact publication, adoption/oracle, Terraform execution, and
  release bundle unchanged.

## Base / Head

- Base: `bfa764a4530042fd597a11a7264b74fc0bd558d9` (`origin/main`, merge
  of PR #177).
- Head: the review checkpoint on `feature/node-zia-transform-cohort`; resolve
  with `git rev-parse HEAD`.
- Diff command: `git diff <base>...HEAD` using the base recorded above.

## Files Changed

- Compact catalog authoring and exact committed artifact:
  `engine/transform_catalog.py` and
  `catalogs/zia-transform-cohort.v1.json`.
- Private catalog contract and validator:
  `docs/schemas/transform-resource-cohort.schema.json`,
  `node-src/domain/zia-transform-cohort-validator.ts`, and
  `node-src/domain/zia-transform-cohort.ts`.
- Existing internal product-neutral kernel seam:
  `node-src/domain/pull-transform.ts` and
  `node-src/domain/transform-catalog.ts`.
- Differential fixtures and tests:
  `node-tests/fixtures/zia-transform-cohort.v1.json`,
  `node-tests/zia-transform-cohort.test.ts`, and
  `tests/test_zia_transform_cohort_catalog.py`.
- Boundary documentation: `docs/node-process-api.md` and this handoff.
- Files intentionally left untouched: `engine/transform.py`, the shared public
  `docs/schemas/transform-catalog.schema.json`, shared
  `node-src/contracts/validators.ts`, process request/response schemas and
  dispatch, collectors, HTTP/auth, file materializers/publishers,
  import/move/lookup artifact compilers, adoption/oracle code, Terraform
  execution, pack metadata/provider schema/overrides, and release scripts.

## Source Inputs Consulted

- Provider schemas:
  `packs/zia/schemas/provider/zia.json` at pack pin `4.7.26`, specifically the
  projections for all three selected resource types.
- OpenAPI/API contracts: N/A; this slice accepts already-pulled JSON and makes
  no HTTP or operation-mapping claim.
- Provider source files: N/A; no provider CRUD/read identity behavior is
  implemented or claimed.
- Pack metadata: `packs/zia/pack.json`, `packs/zia/registry.json`,
  `packs/zia/overrides/zia_traffic_forwarding_static_ip.json`, and
  `packs/zia/overrides/zia_url_categories.json`.
- Existing docs or design records: `docs/node-process-api.md`,
  `docs/review-handoffs/zscaler-transform-contract-lift.md`, and the
  adversarial-review workflow/templates.
- Other source evidence: `engine/transform.py` is invoked live as the
  independent result and rendered-byte oracle. `engine/gen_module.py` is the
  only engine consumer of the static-IP override's `sample` key; that
  authoring-only key is explicitly removed only in cohort catalog compilation,
  while the complete override file remains bound by `sources_sha256`.

## Generated Artifacts

- Reports: none.
- Schemas: new private
  `infrawright.transform_resource_cohort` v1 schema; the existing exact ZCC
  transform schema is byte-unchanged.
- Fixtures: one sanitized six-case ZIA corpus covering representative and edge
  behavior for every selected resource.
- Snapshots: none.
- Demo or lab outputs: none; no tenant or provider execution is claimed.
- Artifact drift intentionally expected: new 6,261-byte ZIA cohort catalog
  only. Its committed SHA-256 is
  `f6046978afeb80eab82fad183892011cec61aa076bc640efefa4a3ca7b04caf0`.
  The existing ZCC catalog must remain byte-identical.

## Expected Delta

- Expected behavior change: private callers can transform already-pulled JSON
  for exactly the three selected ZIA resource types using the committed
  catalog's exact semantics. Differently serialized semantic copies validate
  and resolve to the canonical embedded snapshot; serialized-byte freshness is
  an authoring-time gate only.
- `zia_traffic_forwarding_static_ip` preserves finite lossless float semantics;
  `zia_url_categories` canonicalizes provider `set(string)` fields and applies
  its authored all-string `urls` sort; `zia_admin_roles` coerces string-map
  values and string-set members like Python.
- Non-`id` computed fields remain reported drops rather than silently ignored.
  Acknowledged API-only paths remain omitted from the returned drop report.
- Expected report/count/coverage changes: N/A; no readiness report or count is
  changed.
- Expected generated-output changes: only the new private catalog and fixture.
- Expected no-op areas: Python transform/runtime output, all ZCC catalog bytes
  and public operations, every non-selected resource, artifact layout, and
  release contents.
- Reusable private extension points for a later product cohort are deliberately
  limited to: repeated `--resource` catalog authoring, the existing
  `TransformCatalogResource` plus `transformPullItemsKernel` value seam, and
  the pattern of a product-owned schema/embedded exact-catalog loader. A ZPA
  cohort should generate its own source-bound artifact, schema, exact resource
  set, digest, and loader; it must not widen or reuse the exact ZIA acceptance
  gate.

## Invariants Claimed

- Evidence must not be silently dropped: catalog generation binds the complete
  selected source files; non-`id` computed or unknown fields flow to the exact
  Python-compatible drop reporter unless explicitly acknowledged.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  evidence matcher or readiness classification is changed.
- Source precedence/provenance must remain explicit: Python pack/registry/
  provider-schema/override loaders remain authoritative at authoring time;
  Node schema-validates and accepts only exact semantic copies of the embedded
  contract, then returns the canonical embedded snapshot. Generator freshness
  and the test-only known digest bind committed bytes separately.
- Ambiguity must stay classified instead of being coerced to success: malformed
  or semantically changed catalogs, resource types outside the exact three,
  unsupported schema encodings, native JavaScript numeric input, non-finite
  lossless numeric input, malformed set members, and unexpected fields fail or
  report explicitly.
- Provider-readiness counts must stay explainable: N/A; no counts change.
- Adoption safety invariants: no adoption/oracle operation is introduced and no
  provider or Terraform process runs.
- Public closure: `transformPullItems` still accepts only the exact embedded
  ZCC catalog. The ZIA function is not imported by process dispatch or the
  release bundle, and its schema is not registered with the shared process AJV
  validator graph.
- Default authoring closure: default `engine.transform_catalog` generation
  still rejects extra computed-only top-level attributes and every override
  key outside its original encoded vocabulary. Cohort-only allowances are
  enabled only by explicit repeated `--resource` selection.

## Tests Run

- Post-rebase `npm run check`: 612 total, 611 passed, one platform-specific
  skip, zero failures. The first loaded-machine pass hit the repository's
  timing-sensitive Terraform descendant-PID fixture; its isolated 18-test file
  passed immediately and the complete rerun passed.
- Post-rebase `make test`: 1,382 selected, 1,381 passed, one optional
  external-source skip, zero failures.
- `npm run build`: passed.
- `python3 -m engine.audit_vendor_boundary`: zero violations.
- `python3 -m engine.transform_catalog --product zcc --check
  catalogs/zcc-transform-catalog.v1.json`: exact byte gate passed.
- Explicit three-resource ZIA `--check`: exact byte gate passed.
- Committed ZIA catalog SHA-256 matches the test-only authoring fixture.
- The existing shared ZCC transform schema has zero diff from the base.
- No process-dispatch or release-script path imports the ZIA cohort.
- Focused real-Python differential: complete transform result and embedded
  `render_tfvars` bytes match for all six committed cases.
- Findings 2/3 remediation: `npm run typecheck` and `npm run build` passed;
  the focused Node semantic/byte/differential/bundle suite passed 5/5; the
  focused Python ZIA/ZCC catalog suites passed 27/27; both catalog freshness
  checks and the vendor-boundary audit passed.
- `git diff --check`: passed.
- Tests not run and why: no live tenant/provider/Terraform tests are applicable
  to this pure private transform slice.

## Initial Review Findings / Remediation

- Finding 2: the first checkpoint accepted a caller-supplied
  `catalogSha256`, but the caller could assert the known value independently of
  the object supplied. Remediation: remove the parameter and production
  constant. Runtime closure is now exactly the contract it can prove: AJV
  validation plus semantic equality with the embedded catalog, returning only
  the canonical embedded snapshot. A regression accepts a minified semantic
  copy, while mutated semantics still fail. Exact generator bytes and the
  known SHA-256 remain authoring-time tests.
- Finding 3: the first checkpoint registered the private cohort schema in
  `node-src/contracts/validators.ts`, making it reachable from the production
  process bundle even though no operation used it. Remediation: a local AJV
  instance, imported only by `zia-transform-cohort.ts`, reuses the established
  transform-schema definitions and validates the private schema. The shared
  validator returns to its base state. A real esbuild production-entry test
  proves the cohort module, validator, catalog, schema, kind, schema ID, and
  unique source markers are absent from both the bundle graph and output.
- Finding 1 concerns shared snake-case semantics. It is deliberately not
  addressed on this checkpoint; it will land separately, after which this
  branch must rebase and receive a changed-surface review.

## Known Deferrals

- No ZIA HTTP collector, public process adapter, file/artifact publisher,
  imports/moves/lookup compiler, adoption/oracle, Terraform runner, SDK, or
  framework is included.
- The other ZIA fetch-backed resources and override vocabulary (`divide`,
  defaults, skip predicates, nested drops, value maps, and so on) remain
  unsupported until separately bounded cohorts land.
- The cohort does not claim provider-read or multi-apply parity; it proves only
  exact compatibility with the existing Python raw-pull transform and rendered
  JSON tfvars behavior for the selected source-bound contract.
- Generated-config repair and live tenant evidence remain separate work.
- This is not a general ZIA catalog loader, Zscaler SDK, Terraform replacement,
  orchestration framework, or API server. It introduces no output directory,
  persistence, retry, authentication, concurrency, or transaction semantics.

## Review Focus

- Verify the catalog projection directly against the pinned ZIA schema,
  registry, pack, and both present override files; regenerate and compare exact
  catalog bytes and `sources_sha256`.
- Confirm the default ZCC generator remains fail-closed and the existing ZCC
  schema/catalog bytes are unchanged.
- Attack the explicit cohort-only handling of top-level computed fields,
  `sort_lists`, and authoring-only `sample`; ensure none can widen the default
  catalog path or silently suppress an unexpected drop.
- Compare all-string versus mixed `urls` ordering, provider-set Unicode/null/
  duplicate/scalar behavior, map scalar/large-integer/prototype-like keys, and
  finite float spelling directly to live Python output.
- Verify authoring-time byte freshness is not presented as runtime evidence;
  the exact semantic gate must canonicalize differently serialized copies,
  reject mutations, and keep an unsupported fourth ZIA type out of the kernel.
- Confirm native/non-finite numbers and malformed collection members fail
  closed without adding a public parser or process boundary.
- Confirm the shared validator has no cohort import/registration and the real
  production-entry bundle contains none of the private cohort inputs or unique
  markers.
