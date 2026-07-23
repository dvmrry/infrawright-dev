# Builder handoff: Python import/move derivation contract

## Intent

- Replace the retained live-Python import/move differential with a frozen,
  provenance-bound corpus produced by the original Python authority.
- Move `import-moves-differential.test.ts` into the default Python-disabled
  Node suite without weakening exact import bytes, move bytes, parsing, safe
  move derivation, or unsafe suppression diagnostics.
- Keep production/runtime code, packs, artifacts, Terraform operations, and
  provider behavior unchanged.

## Base / Head

- Base: `71da6c267119c8f8531accce4906414a8c7c1e84`
- Implementation head: `2933746a9925416813fd01b7f140152d60f20725`
- Diff command:
  `git diff 71da6c267119c8f8531accce4906414a8c7c1e84..2933746a9925416813fd01b7f140152d60f20725`
- The subsequent commit changes only this handoff. The reviewer must bind the
  final verdict to the actual requested candidate head.

## Files Changed

- `node-tests/import-moves-differential.test.ts`
- `node-tests/fixtures/python-import-moves-v1.json`
- `node-tests/node-test-suite-selector.test.ts`
- `docs/python-oracle-contracts.md`
- `docs/python-archive-plan.md`
- This handoff.
- Files intentionally left untouched: production TypeScript, retained Python,
  packs, schemas, generated runtime artifacts, Make targets, pack-test
  requirements, and the shared `node-tests/python-oracle.ts` resolver.

## Source Inputs Consulted

- Provider schemas: N/A; no provider projection or schema behavior changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A; the import/move corpus is pack-independent.
- Existing docs or design records: `docs/python-archive-plan.md`,
  `docs/python-oracle-contracts.md`, and the prior archive handoffs.
- Other source evidence: the live comparison at base
  `71da6c267119c8f8531accce4906414a8c7c1e84`, test blob
  `952071f1aae881d9c361b40b9e44dfe2bee0d384`, and Python transform blob
  `ba382610c45ab6c0a7f870599c133edc69c5199a`, executed with CPython
  3.13.13 / UCD 15.1.0. The exact resurrection worktree and test command are
  recorded in `docs/python-oracle-contracts.md`.

## Generated Artifacts

- Reports: this handoff only.
- Schemas: none.
- Fixtures: `node-tests/fixtures/python-import-moves-v1.json`, produced from
  the original unchanged 12-case corpus by the original embedded Python
  authority. It records authority metadata and every exact result field.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: only the new frozen fixture. No
  operational artifact or committed golden changed.

## Expected Delta

- Expected behavior change: `import-moves-differential.test.ts` no longer
  invokes Python and joins the default Node suite.
- Expected report/count/coverage changes: full-profile selection increases
  from 51 to 52 files; live Python-oracle exclusions decrease from 17 to 16.
  The default selected suite increases from 506 to 514 tests.
- Expected generated-output changes: none.
- Expected no-op areas: production/runtime code, Terraform behavior, packs,
  provider behavior, artifact bytes, release bundle, and the 16 remaining live
  Python differential files.

## Invariants Claimed

- Evidence must not be silently dropped: all 12 original cases and all seven
  fields per result remain in the fixture; tests compare exact old/new import
  text, parsed pairs, complete move/suppression objects, and exact moved-block
  text.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matching or source-evidence behavior changed.
- Source precedence/provenance must remain explicit: the fixture names its
  baseline and CPython/UCD authority; docs name the exact test and transform
  blobs. The 15,981-byte fixture SHA-256 and 11,001-byte ordered-results
  SHA-256 are independently locked.
- Ambiguity must stay classified instead of being coerced to success:
  ambiguous old IDs, duplicate sources, key swaps, occupied destinations, and
  a three-member occupied cycle preserve their complete suppression records.
- Provider-readiness counts must stay explainable: N/A; suite selection counts
  are exact and regression-locked.
- Adoption safety invariants: only safe one-to-one renames emit moved blocks;
  unsafe candidate classes remain suppressed and no test contacts Terraform,
  a provider, a backend, or the network.

## Tests Run

- `npm run build:test`: passed.
- Python-disabled focused import/move and selector suite: 15 passed.
- `PYTHON=/usr/bin/false npm test`: 514 passed; 52 selected and 16 live-oracle
  files excluded.
- `npm run test:all`: 663 total, 662 passed, one platform skip, zero failed.
- Full and reduced selector checks both select
  `import-moves-differential.test.js`; full selection is exactly 52/16.
- `git diff --check`: passed.
- Tests not run: live credentials, provider calls, remote backends, or
  deployment Apply; no affected behavior requires them.

## Known Deferrals

- Sixteen test files still import the live Python oracle. They are unchanged
  and remain excluded from the Python-free default suite.
- The shared Python oracle resolver and its own test remain until the last live
  consumer is frozen.
- The retained Python implementation, Python tests, collector shims, CI setup,
  and release tripwires remain until all live authorities are frozen.
- Follow-up trigger: continue with the next coherent authority cohort selected
  by the remaining-consumer map; do not delete Python or the resolver early.

## Review Focus

- Highest-risk files: the frozen fixture and its comparison test.
- Reproduce the fixture values from the exact base, test blob, transform blob,
  and CPython/UCD authority rather than trusting the builder summary.
- Attack whether the fixture was generated from Node output or whether any
  field became a Node-versus-Node self-comparison.
- Verify the hostile escape/Unicode case preserves exact Python/HCL bytes and
  code-point ordering.
- Verify all safe move orderings and complete unsafe suppression objects,
  including reason precedence and the three-cycle case.
- Verify the fixture and ordered-results byte counts/digests.
- Verify full and reduced selector behavior and the exact 52/16 count.
- Reject any fixture update that can be regenerated solely from the Node
  implementation under test without re-adjudicating the recorded authority.
