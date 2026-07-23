# Builder handoff: Python plan report contract

## Intent

- Replace all five live-CPython executions in `plan-report.test.ts` with one
  provenance-bound frozen authority.
- Preserve path formatting, complete report objects, exact pretty JSON bytes,
  and float lexical provenance while joining the Python-disabled Node suite.
- Leave production/runtime behavior and retained Python unchanged.

## Base / Head

- Base: `ef8b4622e79bdc2e8b3c54a52bc18c6c379ef13c`
- Implementation head: `16735a6ccb3dfe4b00f8df7ff2e0f01a1249948f`
- Diff: `git diff ef8b4622e79bdc2e8b3c54a52bc18c6c379ef13c..16735a6ccb3dfe4b00f8df7ff2e0f01a1249948f`
- The next commit changes only this handoff; bind review to actual HEAD.

## Files Changed

- `node-tests/plan-report.test.ts`
- `node-tests/fixtures/python-plan-report-v1.json`
- `node-tests/node-test-suite-selector.test.ts`
- `docs/python-oracle-contracts.md`
- `docs/python-archive-plan.md`
- This handoff.
- Intentionally untouched: production TypeScript, Python sources/tests, packs,
  schemas, Make targets, CLI bundle, and the Python-oracle resolver.

## Source Inputs Consulted

- Provider/OpenAPI/pack inputs: N/A; the contract is provider-independent.
- Producing baseline: `ef8b4622e79bdc2e8b3c54a52bc18c6c379ef13c`,
  CPython 3.13.13 / UCD 15.1.0.
- Original test blob: `c93c39d46e0e354cf9096acfaf5c68b4c2f80bc2`.
- Python blobs: `engine/ops.py`
  `f160a796f6078d96ee423d1ca7f1d169598c8160`, `engine/paths.py`
  `63ffb562172405c27a880345cd85b93af7b1ba94`, and
  `engine/plan_eval.py` `f15e4f44193d517384065a1d320533ea74a47a15`.
- Node blobs: plan report `4077ba595ab6e58ad51265102b1166b925c3cdf4`,
  validators `2e29d8025f857c38af48627ef67c03385af91679`, and renderer
  `a95ef511c10bb1c727ca6a5f9616909acdea12c3`.

## Generated Artifacts

- `node-tests/fixtures/python-plan-report-v1.json`: 15,180 bytes, SHA-256
  `df9d09b903bf60d34ad567f213bd1ddbb1e8bf2aaf1fc71c49be9a050a3e343c`.
- Five logical cases: one six-vector path case, three exact report cases, and
  one exact float-provenance report. Normalization is `none`.
- No existing report, schema, snapshot, runtime artifact, or golden changed.

## Expected Delta

- `plan-report.test.ts` starts no Python and is selected by the default suite.
- Live Python-oracle consumers decrease 14 to 13; frozen contracts increase
  12 to 13.
- Production behavior and generated outputs remain unchanged.

## Invariants Claimed

- No evidence is dropped: every removed subprocess and all input/output bytes
  are represented.
- Source provenance is explicit and test-locked by full fixture digest,
  baseline, CPython/UCD, and source blobs.
- The frozen evidence is not a Node self-comparison: report inputs must equal
  the exact recorded Python inputs, while current Node objects and bytes equal
  complete recorded Python outputs.
- Float `1.0`, path escaping/index normalization, findings, guidance, stale
  policy, and summary statuses retain their exact authority.
- No provider, Terraform, backend, credentials, or network operation occurs.

## Tests Run

- `npm run typecheck`: passed.
- `npm run build:test`: passed.
- Python-disabled focused report and selector suite: 16 passed.
- `git diff --check`: passed.
- Full Node gate is deferred until after required review, then run once.

## Known Deferrals

- Thirteen Node tests still import the live Python oracle.
- No Python file can be deleted in this slice because the shared authorities
  still serve other retained compatibility tests.
- Transform/adopt parity remains the next high-leverage archive subsystem.

## Review Focus

- Reproduce all five logical cases from the exact baseline and source blobs.
- Verify the six path outputs, three compact input JSON strings, complete
  pretty output bytes, and `1.0` provenance output.
- Reject any parsed-object-only comparison where the contract is exact bytes.
- Verify selector inclusion and count changes cannot hide live Python use.
