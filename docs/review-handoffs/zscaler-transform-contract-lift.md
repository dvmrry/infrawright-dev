# Builder Review Handoff: Product-Neutral Transform Contract Lift

## Intent

- Lift three schema-driven transform primitives needed by later ZIA/ZPA
  catalogs without adding either product catalog or widening the public process
  resource set: finite lossless float lexemes, `set(string)`, and
  `map(string)`.
- Preserve the exact embedded five-resource ZCC catalog gate and every existing
  ZCC artifact byte.
- Keep native JavaScript numbers, non-finite float results, unsupported
  collection encodings, and malformed set members fail-closed.

## Base / Head

- Base: `7fe7b6c` (`origin/main` when the branch was created).
- Head: the checked-out commit on `feature/node-zscaler-contract-lift`; resolve
  it with `git rev-parse HEAD` after this review checkpoint is committed.
- Diff command: `git diff 7fe7b6c...HEAD`.

## Files Changed

- Number and rendering compatibility:
  `node-src/json/python-number.ts`,
  `node-src/json/python-lossless-artifact.ts`, and
  `node-src/json/python-compatible.ts`.
- Product-neutral pure transform seam and catalog types:
  `node-src/domain/pull-transform.ts` and
  `node-src/domain/transform-catalog.ts`.
- Authoring contract and schema:
  `engine/transform_catalog.py` and
  `docs/schemas/transform-catalog.schema.json`.
- Differential and hostile tests:
  `node-tests/product-neutral-transform-kernel.test.ts`,
  `node-tests/pull-transform.test.ts`,
  `node-tests/python-lossless-artifact.test.ts`,
  `node-tests/transform-catalog.test.ts`, and
  `tests/test_transform_catalog.py`.
- Contract documentation: `docs/node-process-api.md` and this handoff.
- Files intentionally left untouched: the committed ZCC transform catalog,
  pack manifests/registries/overrides/provider schemas, process request and
  response contracts, artifact schemas, public process dispatch, collectors,
  materializers, and adoption/oracle code.

## Source Inputs Consulted

- Provider schemas:
  - `packs/zia/schemas/provider/zia.json` at the pack pin `4.7.26` for
    `zia_traffic_forwarding_static_ip.latitude/longitude`,
    `zia_url_categories.db_categorized_urls`, and
    `zia_admin_roles.feature_permissions`.
  - `packs/zpa/schemas/provider/zpa.json` at the pack pin `4.4.6` for
    `zpa_app_connector_group.user_codes`.
- OpenAPI/API contracts: N/A; this slice consumes already-pulled JSON and
  committed provider-schema encodings.
- Provider source files: N/A; no provider Read/write or operation mapping is
  changed or claimed.
- Pack metadata: the corresponding ZIA/ZPA override files plus the exact
  embedded ZCC transform catalog and its source digest.
- Existing implementation oracle: `engine/transform.py`, especially
  `_coerce_primitive` and `_coerce_by_encoding`, and Python
  `json.loads`/`json.dumps` float behavior.
- Existing docs: `docs/node-process-api.md`, the adversarial-review workflow,
  and the prior ZCC transform/adoption handoffs.

## Generated Artifacts

- Reports: none.
- Schemas: the existing exact-ZCC transform-catalog schema gains only
  `set(string)` and `map(string)` encoding alternatives.
- Fixtures: no product catalog or static fixture was added; the differential
  uses small inline sanitized ZIA/ZPA shapes and the real Python transform.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. The committed
  `catalogs/zcc-transform-catalog.v1.json` remains byte-identical to its Python
  generator, and existing all-five ZCC Python/Node byte differentials pass.

## Expected Delta

- Losslessly parsed integer tokens retain arbitrary precision and canonical
  Python integer spelling exactly as before.
- Losslessly parsed JSON float lexemes are converted through the same finite
  binary64 value model as Python `json.loads` and rendered using the spelling
  selected by Python `repr(float)`/`json.dumps`, including negative zero,
  exponent padding, fixed/scientific thresholds, subnormals, underflow, and
  maximum finite values.
- Native JavaScript numbers remain rejected at the raw transform boundary;
  overflow to infinity and non-JSON/non-finite `LosslessNumber` values fail.
- Provider `set(string)` values use Python code-point ordering after schema
  coercion, preserve duplicates and stable ties like the existing Python path,
  and reject a non-string/non-null post-coercion member.
- Provider `map(string)` values coerce each value and retain prototype-like
  keys as inert own data in a null-prototype map.
- The Python catalog producer and JSON Schema accept exactly
  `set(string)`/`map(string)` beyond the existing primitive/list vocabulary;
  `set(number)`, `map(bool)`, and other unimplemented encodings still fail.
- The public `transformPullItems` path still requires the exact immutable ZCC
  catalog. The exported product-neutral kernel seam is internal and requires a
  structurally validated resource contract from a future catalog integration.

## Invariants Claimed

- Evidence must not be silently dropped: no source catalog or provider schema
  is regenerated; the exact ZCC source digest and semantic-equality gate remain
  unchanged.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or provider-evidence classification changes.
- Source precedence/provenance must remain explicit: committed provider schema
  and pack metadata remain the authoring source; Python remains the independent
  behavior/byte oracle in the differential.
- Ambiguity must stay classified instead of being coerced to success:
  unsupported encodings, native numbers, non-finite results, and malformed set
  members fail instead of being stringified generically.
- Provider-readiness counts must stay explainable: N/A; no counts or readiness
  reports change.
- Adoption safety invariants: no adoption/oracle behavior changes. Public ZCC
  resource and catalog closure remain exact; no ZIA/ZPA process operation is
  introduced.
- Numeric fidelity: integer versus float lexemes remain distinct; 2,048
  deterministic finite binary64 values and explicit notation boundaries are
  compared to Python bytes.
- Collection fidelity: set ordering is Python code-point order with stable
  duplicate retention; map keys cannot mutate `Object.prototype`.

## Tests Run

- `npm run check`: 584 total, 583 passed, 1 platform skip, 0 failed.
- `make test`: 1,365 passed, 0 failed.
- `npm run build`.
- `python3 -m unittest tests.test_transform_catalog`: 19 passed.
- `python3 -m engine.transform_catalog --product zcc --check catalogs/zcc-transform-catalog.v1.json`.
- `python3 -m engine.audit_vendor_boundary`: 0 violations.
- Focused real-Python transform differential for representative ZIA float,
  ZIA set/map, and ZPA set shapes: exact rendered bytes match.
- Focused 2,048-value deterministic finite-binary64 Python rendering
  differential: exact compact numeric tokens match.
- `git diff --check`.

## Known Deferrals

- No ZIA or ZPA catalog, public operation, collector, artifact compiler,
  publisher, or adoption executor is included. Those must consume this kernel
  through their own generated and reviewed product contracts.
- `set(number)`, object/nested collection attributes, semantic ZIA/ZPA
  overrides, HCL tfvars, reference binding, and generated-config oracle policy
  remain separate slices.
- Float-valued identity/key fields remain intentionally unsupported; current
  product identities are strings or integral IDs.
- Non-finite Python JSON extensions (`NaN`, `Infinity`) are intentionally not
  reproduced because the raw input contract is finite JSON.
- No live provider or tenant evidence is claimed; structural test cases use
  committed schemas and sanitized inline values only.

## Review Focus

- Attack `node-src/json/python-number.ts` at the Python fixed/scientific
  threshold, exponent padding, negative zero, underflow/overflow, subnormal,
  and rounding-tie boundaries.
- Verify that arbitrary-size integer behavior is unchanged and no native
  JavaScript number can enter through the new float path.
- Compare `set(string)` ordering, null/empty-string ties, duplicates, Unicode,
  scalar coercion, and max-one block merging directly with
  `engine.transform._coerce_by_encoding`.
- Verify map coercion and prototype-like keys against Python dict behavior.
- Confirm the TypeScript type, Python catalog producer, and JSON Schema accept
  the same encoding set and reject every broader one.
- Confirm `transformPullItemsKernel` cannot widen the public process boundary:
  all production callers still enter through the exact ZCC catalog gate.
- Re-run the all-five ZCC differential and confirm the committed catalog and
  artifact bytes have no drift.
