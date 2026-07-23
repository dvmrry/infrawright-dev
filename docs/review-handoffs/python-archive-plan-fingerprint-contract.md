# Builder handoff: Python plan fingerprint contract

## Intent

- Replace the retained live-Python plan-fingerprint differential with frozen,
  provenance-bound evidence produced by the original Python authority.
- Move `plan-fingerprint.test.ts` into the default Python-disabled Node suite
  without weakening exact payload, canonical-byte, digest, root-scanner, or
  invalid-filename behavior.
- Keep production/runtime code, packs, Terraform operations, generated
  artifacts, and provider behavior unchanged.

## Base / Head

- Base: `b999edfb3255c644100935991171ad4fcee003c9`
- Implementation head: `1e685f6f4e12545821cfb08729e8ab745aaf883a`
- Diff command:
  `git diff b999edfb3255c644100935991171ad4fcee003c9..1e685f6f4e12545821cfb08729e8ab745aaf883a`
- The subsequent commit changes only this handoff. The reviewer must bind the
  verdict to the actual requested candidate head.

## Files Changed

- `node-tests/plan-fingerprint.test.ts`
- `node-tests/fixtures/python-plan-fingerprint-v1.json`
- `node-tests/node-test-suite-selector.test.ts`
- `docs/python-oracle-contracts.md`
- `docs/python-archive-plan.md`
- This handoff.
- Files intentionally left untouched: production TypeScript, retained Python,
  packs, schemas, generated runtime artifacts, Make targets, pack-test
  requirements, and `node-tests/python-oracle.ts`.

## Source Inputs Consulted

- Provider schemas: N/A; no provider behavior changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A; this contract is pack-independent.
- Existing docs or design records: `docs/python-archive-plan.md`,
  `docs/python-oracle-contracts.md`, and prior archive handoffs.
- Other source evidence: baseline
  `b999edfb3255c644100935991171ad4fcee003c9`; original test blob
  `40de74a1738ce2d0773a0687e4d102f56d71ce33`; Python `engine/ops.py`
  blob `f160a796f6078d96ee423d1ca7f1d169598c8160`; and Node implementation
  blob `8c57fda681df654f956646b2adbf09d485a689f8`, exercised with CPython
  3.13.13 / UCD 15.1.0. Linux-only invalid-byte filename evidence was
  reproduced with `python:3.13.13` image digest
  `sha256:15a460e69443a42f2fa947b565bfade376510f54400bd9aa44f35c0c5078b7ec`.
  Exact resurrection commands are recorded in
  `docs/python-oracle-contracts.md`.

## Generated Artifacts

- Reports: this handoff only.
- Schemas: none.
- Fixtures: `node-tests/fixtures/python-plan-fingerprint-v1.json`, 13,364
  bytes, SHA-256
  `69ebf724f468e72c37ffaac33f78055e37cc944397fa923a31ff08331030a1b6`.
  It records complete CPython results, not only hashes.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: only the new frozen authority file.
  No operational artifact or committed golden changed.

## Expected Delta

- Expected behavior change: `plan-fingerprint.test.ts` no longer invokes
  Python and joins the default Node suite.
- Expected report/count/coverage changes: repository discovery is 53 selected
  files and 15 live-Python exclusions. Selector tests now derive these counts
  from the compiled suite while independently proving exact selected/excluded
  accounting and exact live-oracle import coverage.
- Expected generated-output changes: none.
- Expected no-op areas: production/runtime code, Terraform behavior, packs,
  provider behavior, artifact bytes, release bundle, and the 15 remaining live
  Python differential files.

## Invariants Claimed

- Evidence must not be silently dropped: the fixture preserves full plan/init
  payloads, canonical bytes, both digests, module sources, symlink traversal,
  U+FEFF filenames, scanner acceptance, all eleven scanner failures,
  duplicate-module failure, and Linux invalid-filename authority.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the fixture locks the
  baseline, source blobs, interpreter/UCD, Linux authority image, and full-file
  SHA-256. Tests compare complete values as well as hashes.
- Ambiguity must stay classified instead of being coerced to success: every
  malformed-root scanner case retains its exact Python error type, line, and
  diagnostic.
- Provider-readiness counts must stay explainable: N/A; suite counts are
  derived from the independently enumerated compiled test set.
- Adoption safety invariants: plan-source fingerprints and generated-root
  validation remain byte- and failure-compatible; no test contacts Terraform,
  a provider, a backend, or the network.

## Tests Run

- `npm run typecheck`: passed.
- Python-disabled focused fingerprint and selector suite: 15 passed and one
  Darwin platform skip.
- `PYTHON=/usr/bin/false npm test`: 523 total, 522 passed, one platform skip,
  zero failed.
- `npm run test:all`: 663 total, 662 passed, one platform skip, zero failed.
- Full profile discovery: 53 selected, 15 live-Python exclusions, 68 total.
- `git diff --check`: passed.
- Tests not run: live credentials, provider calls, remote backends, or
  deployment Apply; no affected behavior requires them. Local Node execution
  of the invalid-byte case is Linux-only and remains CI-covered; its CPython
  authority was independently captured on Linux.

## Known Deferrals

- Fifteen test files still import the live Python oracle. They are unchanged
  and remain excluded from the Python-free default suite.
- The shared resolver and its own test remain until the last live consumer is
  frozen.
- Retained Python implementation/tests, collector shims, CI Python setup, and
  release tripwires remain until all live authorities are frozen.
- Follow-up trigger: freeze the next coherent plan evaluation/report cohort;
  do not delete Python or the resolver early.

## Review Focus

- Highest-risk files: the frozen fixture, fingerprint comparison test, and
  selector-accounting change.
- Independently reconstruct the fixture from the exact source blobs and
  CPython authority; reject a Node-versus-Node self-comparison.
- Verify full payload/canonical/digest equality, Python ASCII escaping, sorting,
  symlink semantics, and U+FEFF filenames.
- Verify all scanner cases and that only the generated environment-root prefix
  is normalized to `{env_dir}`.
- Reproduce or otherwise source-check the Linux invalid-byte filename digests
  and exact Node fail-closed behavior.
- Verify suite accounting cannot hide an unselected test or a live Python
  oracle import, including the intended final zero-exclusion state.
