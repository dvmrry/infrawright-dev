# Builder Review Handoff: ZCC Adoption Projection and Bootstrap Artifacts

## Intent

- Add a private, credential-free Node kernel for the deterministic half of ZCC
  adoption: raw identity derivation, exact provider-observation joining,
  provider-schema projection, and bootstrap artifact rendering.
- Freeze the Python-authored adoption identity and projection facts for the
  five fetch-backed ZCC resources in a generated, versioned catalog.
- Prove Node/Python functional and byte parity without representing synthetic
  provider state as live evidence.
- Keep Terraform execution, credentials, provider state, and filesystem
  publication out of the public process API.

## Base / Head

- Base: `1f38bf02b2eb47a8c78dee654ca8fd20656ba8df`
- Head: the checked-out commit on `feature/node-zcc-protected-parity`; resolve
  it with `git rev-parse HEAD` after the review checkpoint is committed.
- Diff: `git diff 1f38bf02b2eb47a8c78dee654ca8fd20656ba8df...HEAD`

## Files Changed

- Generated catalog and authoring compiler:
  `engine/adoption_catalog.py`,
  `catalogs/zcc-adoption-catalog.v1.json`, and
  `docs/schemas/zcc-adoption-catalog.schema.json`.
- Catalog validation and projection kernel:
  `node-src/contracts/validators.ts`,
  `node-src/domain/zcc-adoption-catalog.ts`, and
  `node-src/domain/zcc-adoption-projection.ts`.
- Shared hostile-graph validation and the Python projection source of truth:
  `node-src/json/supported-json-graph.ts` and `engine/state_project.py`.
- Private artifact compiler and shared bootstrap render helpers:
  `node-src/domain/zcc-adoption-artifacts.ts` and
  `node-src/domain/zcc-pull-artifacts.ts`.
- Differential corpus and tests:
  `node-tests/fixtures/zcc-adoption-projection-corpus.v1.json`,
  `node-tests/zcc-adoption-projection-differential.test.ts`,
  `node-tests/zcc-adoption-artifacts.test.ts`, and
  `node-tests/zcc-adoption-projection-security.test.ts`,
  `node-tests/zcc-pull-artifacts.test.ts`,
  `tests/test_adoption_catalog.py`, and `tests/test_state_project.py`.
- Test routing and vendor audit metadata:
  `tests/pack-test-requirements.json` and
  `engine/vendor_boundary_allowlist.json`.
- Files intentionally left untouched: process request/response schemas and
  dispatch, Terraform subprocess/oracle transaction code, Python adoption
  identity behavior, pack registry/overrides/provider schema, filesystem
  materializers, and release bundle entry points.

## Source Inputs Consulted

- Provider schema: `packs/zcc/schemas/provider/zcc.json`.
- Provider source/version: `zscaler/zcc` `0.1.0-beta.1`, pinned by
  `packs/zcc/pack.json`.
- Pack metadata: `packs/zcc/pack.json`, `packs/zcc/registry.json`, and the five
  ZCC override files named in the catalog `source_files` field.
- Python identity/oracle path: `engine/adoption_meta.py`, `engine/adopt.py`,
  `engine/import_oracle.py`, and `engine/state_project.py`.
- Python artifact rendering: `engine/transform.py` and `engine/lookup.py`.
- Existing source-derived evidence:
  `tests/fixtures/parity/zcc_failopen_policy_inversion.json`.
- OpenAPI contracts: N/A; this slice consumes post-Read Terraform state and
  does not map REST operations.

## Generated Artifacts

- One generated catalog:
  `infrawright.adoption_catalog` schema version 1.
- One strict JSON Schema for that catalog.
- One committed differential corpus whose provenance is explicitly
  `synthetic_sanitized`.
- The existing fail-open inversion fixture remains the only source-derived ZCC
  provider-state control used by this slice.
- No live tenant state, credentials, Terraform scratch data, provider output,
  reports, or snapshots are committed.

## Expected Delta

- The generated catalog binds the exact five fetch-backed ZCC resources,
  provider source/version, adoption-only identity/import/skip metadata,
  lookup-source metadata, recursive writable projection, computed-only fields,
  provider-sensitive flags, nested cardinality, and source digest.
- Node derives keys/import IDs without applying raw-transform HTML or value
  overrides, exactly matching Python adoption identity semantics.
- Node requires an exact key and import-ID join before reading provider-state
  values. Each observation must also match the selected resource type, pinned
  provider registry identity, and the exact SHA1-derived Terraform scratch
  address used by the Python oracle.
- Raw items and provider observations are descriptor-read once into inert,
  deeply frozen, acyclic, depth-bounded plain-JSON snapshots before identity
  or projection code can consume them. Proxy objects (including callable
  proxies) are rejected without invoking traps, so one input branch cannot
  mutate a sibling mask while the snapshot is being built.
- Caller-supplied adoption catalogs pass through the same inert graph boundary
  before schema validation, invariant checks, or equality comparison.
- Node projects only writable schema fields, omits absent/null optional fields,
  rejects missing required fields, rejects malformed repeated block members,
  and validates the complete dynamic-sensitive-mask grammar against the
  corresponding provider-value shape and list cardinality before projecting
  writable or computed-only paths.
- Python `state_project` now shares the fail-closed repeated-member and
  sensitive-mask behavior so the compatibility oracle cannot silently discard
  malformed provider evidence.
- The private compiler emits exact Python JSON tfvars, import blocks, and the
  trusted-network lookup sidecar for bootstrap JSON mode.
- Refactored raw-pull bootstrap helpers must remain byte-for-byte unchanged.
- No public CLI/process contract or valid-input behavior changes in this slice;
  malformed provider-state evidence now fails closed instead of being ignored.

## Invariants Claimed

- Evidence must not be silently dropped: generated catalog inputs are hashed,
  the supported resource set is closed, and unsupported schema encodings fail
  generation.
- Generic matcher versus source-backed evidence: synthetic fixtures test
  kernel equivalence only and are never labeled live/provider evidence; the
  source-derived fail-open fixture remains visibly distinct.
- Source precedence/provenance: Python pack metadata and the pinned provider
  schema remain the authoring source; lookup metadata comes only from the
  hashed ZCC manifest, and Node accepts only the exact embedded catalog.
- Ambiguity: missing, extra, duplicate, or key/import-mismatched observations
  fail before projection.
- Sensitive state: values under writable sensitive masks never reach emitted
  artifacts or diagnostics. Malformed masks, including otherwise valid JSON
  scalar nodes, a root list, scalar/collection shape mismatches, unknown value
  members, or list-cardinality mismatches, fail before projection; computed-
  only mask structure is still validated before its value is intentionally
  ignored.
- Provider evidence: resource type, provider name, config key, import ID, and
  scratch address must all agree before state values are read. Repeated nested
  members can no longer disappear from either Python or Node output.
- Adoption safety: projection results are immutable and bound to resource and
  catalog source digest; the artifact compiler invokes the projection kernel
  internally and verifies the binding before rendering.
- Numeric and text fidelity: lossless integer tokens, Python JSON escaping,
  HCL escaping, HTML text, Unicode, newlines, sizes, hashes, and trailing
  newlines are compared to the real Python path.

## Tests Run

- `python3 -m unittest tests.test_adoption_catalog`
- `python3 -m engine.adoption_catalog --product zcc --check catalogs/zcc-adoption-catalog.v1.json`
- `python3 -m engine.audit_vendor_boundary`
- `npm run typecheck`
- `npm run build:test`
- Focused Python/Node projection differential.
- Focused all-five adoption artifact byte differential.
- Existing raw-pull artifact tests after shared-renderer refactoring.
- `npm run check`: 473 tests, 472 passed, 1 platform skip, 0 failed.
- `make test`: 1364 passed, 0 failed.
- `git diff --check`

## Review Remediation

- Finding: raw items, provider values, and sensitive masks could reach recursive
  code without a bounded inert snapshot. Root cause: the adoption compiler did
  not reuse the pull compiler's hostile-graph boundary. Fix: extract the legacy
  pull preflight and add a stricter descriptor-read snapshot for adoption.
  Object and callable proxies are detected without invoking traps, including a
  regression where a proxy attempts to erase an initially true sibling mask.
  Caller catalogs use the same boundary before AJV/equality. Coverage also
  includes cycles, 20,000-deep graphs, accessors, non-plain objects, sparse
  arrays, and secret-safe errors.
- Finding: a malformed sensitive mask could be interpreted as all-false. Root
  cause: Python and Node helpers defaulted unsupported scalar/root shapes to an
  empty object. Fix: validate the same recursive bool/object/list/null grammar
  against the provider-value tree in both engines before projection. Masks now
  require matching scalar/container shapes, known value members, and exact list
  cardinality. Differential and hostile tests retain valid false/null/empty and
  aligned-list controls.
- Finding: caller labels alone could join state from another resource. Root
  cause: observations lacked Terraform resource identity. Fix: bind resource
  type, canonical provider name, and exact scratch address in addition to the
  independently derived key/import ID; cross-resource/provider/address tests
  fail before any state value is read.
- Finding: malformed repeated block members were silently filtered and a null
  ID could rescue an empty non-ASCII key only in Node. Fix: both engines reject
  malformed repeated members, and Node now treats null fallback IDs as absent.
  Both behaviors are locked by the Python/Node differential corpus.
- Finding: lookup metadata was read from the globally merged pack view even
  though the catalog digest covered only ZCC sources. Fix: read it exclusively
  from hashed `packs/zcc/pack.json`; a poisoned-global-pack regression proves
  unrelated packs cannot change the generated catalog. Lookup entries must
  also be objects whose normalized `name_field` is a projected attribute.

## Known Deferrals

- No live ZCC import observation is claimed. Four resource cases are synthetic
  sanitized schema-shape tests; only fail-open uses retained source-derived
  provider evidence.
- No Node Terraform init/plan/import/apply/show transaction, credential
  handling, scratch-root cleanup, timeout, or process-group management.
- No public process operation accepts raw Terraform state or credentials.
- No protected digest-only live comparison lane until an operation-owned
  ephemeral oracle producer exists.
- No projection policy, HCL tfvars, generated reference/group bindings,
  move/refresh derivation, filesystem publication, or downstream cutover.
- These private source modules are not yet reachable from the release CLI
  bundle; the later protected operation will be the first bundled consumer.

## Review Focus

- Whether the catalog generator exactly reflects Python adoption metadata and
  recursive provider-schema input classification, including trusted-network
  identity renames and lookup metadata.
- Whether identity key/import spelling matches Python for LosslessNumber,
  booleans, null, snake-case collisions, skip predicates, and empty-slug
  fallback without accidentally applying raw-transform normalization.
- Whether sensitive-mask traversal matches `engine.state_project` for
  attributes, single blocks, repeated blocks, malformed members, and
  computed-only paths without leaking values in errors.
- Whether the observation join can accept missing, extra, duplicate, or
  cross-key/cross-resource data.
- Whether nested forwarding-profile projection can silently change shape or
  field coverage.
- Whether shared bootstrap renderer exports changed any existing raw-transform
  bytes or relaxed target/source validation.
- Whether artifact catalog/resource bindings, exact byte digest, and immutable
  output prevent accidental cross-contract composition.
- Whether tests overclaim synthetic fixtures, omit a dangerous state shape, or
  compare against a reimplemented oracle instead of real Python functions.
