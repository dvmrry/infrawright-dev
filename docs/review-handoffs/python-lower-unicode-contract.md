# Builder Review Handoff: Python Lowercase And Snake Semantics

## Intent

- Make Node 24 transform snake-case and transform/adoption slug bytes match the
  authoritative Python 3.12/3.13 behavior instead of silently following Node's
  newer Unicode tables.
- Correct Python regex-dot parity in the first snake-case pass for CR, U+2028,
  U+2029, astral characters, and lone surrogates.
- Freeze a small, source-derived Unicode 15.1 compatibility delta with no
  runtime Unicode dependency and fail closed when the Node Unicode version
  changes.
- Preserve all ordinary ZCC outputs and the exact public five-resource catalog
  gate. Only previously divergent edge names/drop paths/identities should
  change.
- Do not add ZIA/ZPA cohort catalogs, HTTP, provider execution, publication,
  Terraform orchestration, an SDK/framework, or a general Unicode API.

## Base / Head

- Base: `f8148687197db378f8ba0643d5867005813cbe8a`
- Head: checked-out checkpoint on `feature/node-python-snake-semantics`; resolve
  with `git rev-parse HEAD`.
- Diff command:
  `git diff f8148687197db378f8ba0643d5867005813cbe8a...HEAD`

## Files Changed

- Files:
  - `docs/node-process-api.md`
  - `docs/python-lower-unicode-contract.md`
  - `docs/review-handoffs/python-lower-unicode-contract.md`
  - `node-src/domain/pull-transform.ts`
  - `node-src/generated/python-lower-15.1.ts`
  - `node-src/json/python-lower-151.ts`
  - `node-tests/python-lower-151.test.ts`
  - `node-tests/zcc-pull-artifacts-differential.test.ts`
  - `tools/generate-python-lower-151.mjs`
- Files intentionally left untouched:
  - ZIA and ZPA private cohort catalogs, source, fixtures, and tests.
  - The exact public ZCC transform/adoption catalogs and their schemas.
  - Python transform/adoption implementation; Python remains the independent
    differential oracle.
  - Provider schemas, overrides, collectors, HTTP, Terraform, publication, and
    process request/response schemas.
  - Existing committed ZCC demo and corpus fixtures; their ordinary bytes do
    not drift.

## Source Inputs Consulted

- Provider schemas: Existing ZCC catalog projections only for integration
  coverage; no schema claim or schema file changes.
- OpenAPI/API contracts: None.
- Provider source files: None.
- Pack metadata: Existing exact ZCC transform and adoption catalogs only; no
  pack metadata changes.
- Existing docs or design records:
  - `AGENTS.md`
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
  - `docs/node-process-api.md`
  - `docs/review-handoffs/zscaler-transform-contract-lift.md`
  - Python `engine/transform.py` and `engine/adoption_meta.py`
- Other source evidence:
  - Official Unicode 15.1.0 `UnicodeData.txt`, `SpecialCasing.txt`, and
    `DerivedCoreProperties.txt`.
  - Official Unicode 16.0.0 copies of the same three files.
  - Exact official URLs and SHA-256s are embedded in the generator, generated
    artifact, and `docs/python-lower-unicode-contract.md`.
  - An additional nonproduction comparison of official Unicode 15.0.0 versus
    15.1.0 proved no lower-mapping, `Cased`, or `Case_Ignorable` delta, matching
    the successful live Python 3.12 and 3.13 exhaustive runs.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None retained.
- Generated production data:
  - `node-src/generated/python-lower-15.1.ts`, generated from the six pinned
    official inputs by `tools/generate-python-lower-151.mjs`.
  - The artifact records source URLs/hashes and four compact inclusive-range
    tables: 27 Unicode 16-only lowercase sources, 52 Unicode 16-only `Cased`
    points, 43 Unicode 16-only `Case_Ignorable` points, and U+1171E as the sole
    Unicode 15.1-only `Case_Ignorable` point.
- Artifact drift intentionally expected:
  - One new compact generated TypeScript artifact only. No existing catalog,
    schema, fixture, snapshot, demo, or golden artifact changes.

## Expected Delta

- Expected behavior change:
  - `snakeName` uses Python regex-dot semantics through
    `([^\n])([A-Z][a-z]+)` with Unicode code-point matching.
  - Snake-case and slug generation use the internal `pythonLower151` helper.
  - The helper requires Node runtime Unicode `16.0`, preserves the 27 new
    Unicode 16 lowercase sources, and evaluates Final Sigma with Unicode 15.1
    `Cased`/`Case_Ignorable` semantics.
  - A public ZCC unknown field starting with U+A7CB now reports the Python drop
    path containing U+A7CB instead of Node 24's U+0264 mapping.
  - Final Sigma drop-path context uses the Python property set, including the
    Unicode 16-only deltas and U+1171E reverse delta.
  - Private ZCC adoption identity keys retain Python slug expansion, fallback,
    and collision behavior.
- Expected report/count/coverage changes:
  - One new exhaustive/directed Node test file and one new public artifact
    differential case. No production report counts change.
- Expected generated-output changes:
  - Only Unicode edge inputs that previously exposed a Python/Node mismatch.
  - Ordinary committed ZCC corpus/demo artifact bytes remain identical.
- Expected no-op areas:
  - Transform projection, ordering, numbers, HTML handling, imports, lookup,
    publication, refresh, adoption oracle state projection, and all request/
    response contracts.

## Invariants Claimed

- Evidence must not be silently dropped:
  - Unknown API surface remains review-required. Only its normalized drop-path
    bytes are corrected to Python behavior.
  - The public ZCC artifact differential compares complete tfvars, imports,
    lookup, and drop results against `engine.transform`.
- Generic matcher evidence must not outrank source-backed evidence:
  - N/A; no matcher or provider evidence precedence changes.
- Source precedence/provenance must remain explicit:
  - Every derivation input is an official pinned Unicode file with an exact
    digest. The generator refuses mismatched bytes and never downloads input.
- Ambiguity must stay classified instead of being coerced to success:
  - A Node runtime whose Unicode version is not exactly `16.0` fails closed.
  - The exhaustive Python oracle accepts only Python 3.12/UCD 15.0 or Python
    3.13/UCD 15.1; every other Python/UCD pairing fails before comparison.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - Python key/slug behavior remains the authority during migration.
  - Final Sigma scans `Case_Ignorable` before `Cased`; points carrying both
    properties cannot be mistaken for the nearest significant character.
  - Per-code-point lowercasing retains unconditional full mappings, including
    U+0130 expansion, without delegating Final Sigma context to Unicode 16.
  - Duplicate slugs still fail closed on transform and private adoption paths.
  - Unsupported runtime Unicode cannot silently remap identities or drop paths.

## Tests Run

- Commands:
  - `npm ci --ignore-scripts`
  - `npm run check`
  - `npm run build`
  - `make test`
  - `make audit-vendor-boundary`
  - `python3 -m engine.transform_catalog --product zcc --check catalogs/zcc-transform-catalog.v1.json`
  - `node tools/generate-python-lower-151.mjs --ucd-root /tmp/infrawright-ucd --check`
  - Focused Node test runs for the new lowercase contract, raw transform,
    product-neutral kernel, public ZCC artifact, and private ZCC adoption
    differentials.
  - The new lowercase contract test under both live Python 3.13/UCD 15.1 and
    `/nix/store/65p6ipj712fb5igr9w1h5k5cb7bymj42-python3-3.12.13/bin/python3.12`
    (UCD 15.0).
  - Negative exhaustive-oracle run under `/usr/bin/python3` 3.9/UCD 13.0.
  - A built-bundle public `compile_pull_artifacts` smoke using U+A7CB,
    U+0897/Final Sigma, and U+0130.
  - `git diff --check`.
- Relevant output summary:
  - Full Node: 618 tests, 617 passed, 1 expected platform skip, 0 failed.
  - Full Python: 1,374 passed, 1 external pinned-provider-source skip, 0
    failed.
  - Python 3.12 and 3.13 exhaustive runs both matched the independently
    computed Node digest across every Unicode scalar and five contexts.
  - Deterministic framing is documented in
    `docs/python-lower-unicode-contract.md`; its known-answer SHA-256 is
    `93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1`.
  - The previously supplied `e846dd40496e58417c9f8b79d1af4b7ffbcc82d0bbf9e5b935349de0a963bd80`
    was not used as evidence because its framing was absent and therefore not
    comparable. Output equality under the documented independently computed
    framing is the enforced invariant.
  - Generated artifact check, production typecheck/bundle build, built-bundle
    edge smoke, exact ZCC catalog check, and whitespace check passed.
  - Vendor boundary: 192 allowed matches, 0 violations.
  - Dependency install added no dependency and reported 0 vulnerabilities.
- Tests not run and why:
  - No live provider, tenant, credentials, Terraform, or API calls: this change
    concerns deterministic string semantics and is proven against official UCD
    inputs plus the existing credential-free Python differentials.

## Known Deferrals

- Deferred work:
  - A future Node/ICU Unicode upgrade requires a separately reviewed
    regeneration and helper adjustment; this checkpoint intentionally fails
    closed instead of predicting that delta.
  - No `upper`, `casefold`, locale-sensitive casing, normalization, grapheme,
    collation, or general Unicode utility is implemented.
  - The six multi-megabyte UCD inputs remain external regeneration inputs and
    are not vendored.
  - ZIA/ZPA private cohort integration remains on its own branches and is not
    mixed into this shared prerequisite.
- Reason it is safe to defer:
  - The runtime contract is narrow, version-guarded, source-derived, and
    exhaustively compared to both supported Python versions. Broader Unicode
    behavior is outside current transform/adoption semantics.
- Follow-up owner or trigger:
  - Node runtime upgrade owner when `process.versions.unicode` changes.
  - ZIA/ZPA cohort builders after this shared prerequisite is adversarially
    approved and merged.

## Review Focus

- Highest-risk files or paths:
  - `tools/generate-python-lower-151.mjs`
  - `node-src/generated/python-lower-15.1.ts`
  - `node-src/json/python-lower-151.ts`
  - `node-src/domain/pull-transform.ts`
  - `node-tests/python-lower-151.test.ts`
- Specific assumptions to attack:
  - The generator parses UnicodeData simple lower mappings plus only
    unconditional SpecialCasing lower mappings correctly.
  - Source hashes, cardinalities, and compact inclusive ranges match the pinned
    official files exactly.
  - All 27 Unicode 16-only direct lowercase sources are preserved.
  - UCD16-only `Cased` and `Case_Ignorable` points are subtracted, and U+1171E
    is added back, before Final Sigma context is decided.
  - `Case_Ignorable` is tested before `Cased` in both scan directions.
  - Per-code-point `toLowerCase` retains every unconditional full mapping and
    no context-sensitive default mapping other than Final Sigma is missed.
  - UTF-16 code-unit matching was not accidentally retained: the actual first
    regex is `([^\n])...` with `u`, matching Python dot over code points while
    excluding only LF.
  - Runtime and Python oracle version guards cannot silently accept unsupported
    Unicode tables.
  - Exhaustive digest framing is unambiguous and both implementations build it
    independently in the exact order documented.
- Source evidence the reviewer should verify:
  - Download the six official files independently, verify their SHA-256s, and
    run the generator in `--check` mode.
  - Compare the generated deltas directly rather than trusting the builder
    summary or known-answer digest.
  - Inspect Python `engine.transform.snake`/`slugify` and
    `engine.adoption_meta.derive_key_from_identity` as the migration oracle.
- Generated artifacts the reviewer should compare:
  - Regenerated `node-src/generated/python-lower-15.1.ts` must be byte-exact.
  - Confirm no pre-existing catalog, fixture, schema, snapshot, or demo output
    changed.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - CR versus LF, U+2028/U+2029, astral values, and lone surrogates in the regex
    pass.
  - U+A7CB and every direct-mapping range.
  - U+0130 full expansion.
  - Final Sigma with Cased/Case_Ignorable points on both sides, especially a
    point carrying both properties.
  - Slug collisions/fallback and public unexpected-drop ordering/bytes.
  - Unsupported Node or Python Unicode versions.
