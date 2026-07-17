# Builder handoff: Python plan evaluation contract

## Intent

- Replace all 16 retained live-CPython plan-classifier comparisons with one
  provenance-bound frozen authority.
- Move `plan-eval.test.ts` into the default Python-disabled Node suite without
  weakening classifier, drift-policy, stale-entry, or lossless-number checks.
- Keep production code, Terraform operations, packs, providers, and generated
  runtime artifacts unchanged.

## Base / Head

- Base: `c344647dc12f10e862af147565ef580f039cfc70`
- Implementation head: `b54f568dc07d887ef61e9ea5b2e12abf896181fb`
- Diff: `git diff c344647dc12f10e862af147565ef580f039cfc70..b54f568dc07d887ef61e9ea5b2e12abf896181fb`
- The subsequent commit changes only this handoff. Bind the verdict to the
  actual candidate head returned by `git rev-parse HEAD`.

## Files Changed

- `node-tests/plan-eval.test.ts`
- `node-tests/fixtures/python-plan-eval-v1.json`
- `node-tests/node-test-suite-selector.test.ts`
- `docs/python-oracle-contracts.md`
- `docs/python-archive-plan.md`
- This handoff.
- Intentionally untouched: production TypeScript, retained Python, packs,
  schemas, Make targets, runtime bundle, and `node-tests/python-oracle.ts`.

## Source Inputs Consulted

- Provider schemas, OpenAPI contracts, provider source, and pack metadata:
  N/A; this contract is pack- and provider-independent.
- Existing records: `docs/python-archive-plan.md` and
  `docs/python-oracle-contracts.md`.
- Frozen authority baseline:
  `397a30c1dc6996283729648d16c1e258ec3627ec` under CPython 3.13.13 / UCD
  15.1.0.
- Original test blob: `396c74bb12ab34b66a7bac2ba4944a93f1bf4abe`.
- Python source blobs: `engine/plan_eval.py`
  `f15e4f44193d517384065a1d320533ea74a47a15`,
  `engine/drift_policy.py` `852517958dc18f37019f369a08ab9bfbd91441c9`,
  and `engine/paths.py` `63ffb562172405c27a880345cd85b93af7b1ba94`.
- Node source blobs under comparison: `node-src/domain/plan-eval.ts`
  `af72faf37582142d51f1bf3e854ae94ccb9fdc0a` and
  `node-src/domain/drift-policy.ts`
  `ac6f61ece107213e23a5ef9533fa2477448915d1`.

## Generated Artifacts

- Fixture: `node-tests/fixtures/python-plan-eval-v1.json`, 14,579 bytes,
  SHA-256 `83924f81dc073e2dc9fef5f20ec96331fa674db09de9ab3bfac9b8770df0eaf8`.
- It records 16 exact input JSON lexemes, complete classifier results, and the
  policy case's complete stale-entry result. Normalization is `none`.
- No existing report, schema, snapshot, runtime artifact, or golden changed.

## Expected Delta

- `plan-eval.test.ts` no longer starts Python and is selected by the default
  Python-disabled Node suite.
- Live Python-oracle consumers decrease from 15 to 14; direct frozen contracts
  increase from 11 to 12.
- No production behavior or generated output changes.

## Invariants Claimed

- Evidence is not silently dropped: all four prior live-comparison sites and
  all 16 cases are preserved.
- Provenance stays explicit: fixture digest, baseline, interpreter/UCD, and all
  source blobs are asserted by the test and documented with a resurrection
  command.
- The fixture does not become a Node self-comparison: exact results were
  produced at the recorded baseline by the retired CPython implementation;
  current Node code parses every recorded input and compares the complete
  result.
- Policy ambiguity remains visible through exact classifier status, finding
  paths, and stale-entry accounting.
- Adoption safety is unchanged; no provider, Terraform, backend, credential,
  pack, or network path is touched.

## Tests Run

- `npm run typecheck`: passed.
- `npm run build:test`: passed.
- `PYTHON=/usr/bin/false node --test .node-test/node-tests/plan-eval.test.js .node-test/node-tests/node-test-suite-selector.test.js`:
  24 passed, zero failed or skipped.
- `git diff --check`: passed.
- Not run before review: full Node/legacy suites, provider/Terraform/backend
  tests, or live credentials. The change is test evidence only; one final
  broad Node gate follows accepted review findings.

## Known Deferrals

- Fourteen Node test files still import the live Python oracle.
- The shared resolver, Python implementation/tests, collector shims, CI setup,
  and release guards remain until all live authorities are replaced.
- Next coherent authority is plan-report; transform/adopt parity follows after
  the compact plan contracts.

## Review Focus

- Independently reconstruct or source-check all 16 fixture cases at the exact
  baseline; reject a Node-versus-Node self-comparison.
- Verify exact input lexemes are retained for arbitrary-size integers,
  integer-versus-float equality, booleans versus numbers, signed zero, and
  non-finite JSON exponents.
- Verify the prototype-key, valid-plan, policy/stale, and numeric cohorts cover
  every removed subprocess call with complete result equality.
- Verify selector accounting cannot hide the file or a remaining live Python
  import, and that no production behavior changed.
