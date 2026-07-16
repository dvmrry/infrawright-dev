# Builder handoff: frozen direct Python oracle contracts

## Intent

- Remove six small live-Python comparisons from the Node migration suite while
  retaining their exact byte, value, path, metadata, and diagnostic contracts.
- Move those six files into the ordinary Python-disabled Node suite.
- Preserve every production behavior, pack artifact, provider interaction,
  Terraform operation, and remaining live Python parity test unchanged.

## Base / Head

- Base: `7d54261c33a63d248c2ec80dfe7776eab5dc054b`
- Implementation head: `3b818ef94e90e1859243c8ccde866ffad612620c`
- Remediated implementation head:
  `61fe13c4d0fbd7ed8a74c13d5528a6f42e768646`
- Diff command: `git diff 7d54261c33a63d248c2ec80dfe7776eab5dc054b..61fe13c4d0fbd7ed8a74c13d5528a6f42e768646`

## Files Changed

- Direct contracts: `node-tests/drift-policy.test.ts`,
  `node-tests/exact-plan-apply.test.ts`, `node-tests/json.test.ts`,
  `node-tests/paths.test.ts`,
  `node-tests/rest-collector-python-parity.test.ts`, and
  `node-tests/zscaler-assessment.test.ts`.
- Suite routing: `node-tests/node-test-suite-selector.test.ts` and
  `node-tests/pack-test-requirements.json`.
- Documentation: `docs/python-archive-plan.md` and
  `docs/python-oracle-contracts.md`.
- Files intentionally left untouched: production Node code, all Python source,
  all generated artifacts, pack metadata, provider schemas, Make/CI/release
  routing, and the 20 larger live-oracle Node test files.

## Source Inputs Consulted

- Provider schemas: none.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: active raw `pack.json` manifests for ZCC, ZIA, ZPA, and ZTC.
- Existing docs or design records: `docs/python-archive-plan.md` and the
  adversarial-review workflow.
- Other source evidence: the six live Python comparisons at the base commit,
  `engine.drift_policy.DriftPolicy`, `engine.packs` guidance accessors,
  `engine.ops.cmd_apply`, CPython `json.dumps`, `os.path.realpath`, and
  `urllib.parse.urlencode` under CPython 3.13.13 / UCD 15.1.0.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: no external fixture. Small expected authorities remain inline next
  to the Node behavior they constrain.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.
- Provenance and exact baseline resurrection command:
  `docs/python-oracle-contracts.md`.

## Expected Delta

- Expected behavior change: six test files no longer invoke Python and now run
  in the default Node suite.
- Expected report/count/coverage changes: default full-profile selection moves
  from 42 selected / 26 Python-oracle-excluded files to 48 / 20.
- Expected generated-output changes: none.
- Expected no-op areas: every production/runtime path, Python compatibility
  implementation, artifact byte, pack, provider, Terraform, and deployment
  behavior.

## Invariants Claimed

- Evidence must not be silently dropped: each former comparison is replaced by
  its complete decision vector, exact bytes, exact diagnostic prefix, semantic
  path result, or an independent inspection of raw pack metadata.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the producing commit,
  Python/UCD authority, original implementations, normalization rule, and
  exact baseline test invocation are recorded.
- Ambiguity must stay classified instead of being coerced to success: malformed
  guidance metadata still fails; policy invalidity remains the full 16-item
  vector rather than a sampled happy path.
- Provider-readiness counts must stay explainable: the Zscaler test directly
  counts raw active manifest lanes; it does not compare Node output with
  another Node rendering.
- Adoption safety invariants: the real Node Apply/Make fake-Terraform path still
  executes and remains fail-closed; only its retired Python diagnostic prefix
  is frozen.

## Tests Run

- `npm run typecheck`
- Focused seven-file suite with `PYTHON=/definitely/missing/python`: 50 passed.
- `PYTHON=/definitely/missing/python npm test`: passed; 48 selected, 20
  Python-oracle-excluded.
- `npm run test:all`: 662 tests, 661 passed and one platform skip, zero failed.
- `PYTHON=/definitely/missing/python make check-all`: passed, including module,
  pack, vendor-boundary, demo, and root-catalog gates.
- Empty-profile selected suite with `--test-concurrency=1`: 237 passed, zero
  skipped or failed.
- `git diff --check`: passed.
- Tests not run: live credentials, provider calls, remote backends, and
  deployment Apply; no production or provider behavior changed.
- Environmental note: the default concurrent `make check-core` exposed an
  existing 500 ms timeout-test race twice (`descendant.pid` had not yet been
  written). The identical test passed in `make check-all`, in isolation (23/23),
  and in the serialized empty-profile suite (237/237). This diff does not touch
  `terraform-command.test.ts` or its implementation.

## Adversarial Review Disposition

The initial fresh-context review requested changes at implementation head
`3b818ef94e90e1859243c8ccde866ffad612620c`.

The blocking finding was accepted. The first Zscaler replacement incorrectly
grouped guidance rules by the provider owning their containing manifest. The
retired accessors and production Node guidance instead scan every manifest and
attribute each rule by its explicit provider, falling back only when the
manifest has exactly one provider. The remediated raw-manifest counter now
preserves that three-lane, per-rule effective-provider behavior. Regression
cases cover an explicit ZIA rule in an AWS-owned manifest, an implicit rule in
a single-provider manifest, and an unattributed rule in a multi-provider
manifest.

The nonblocking routing note was also accepted. The selector test now asserts
all six files are selected in the full profile, locks the 48 selected / 20
Python-oracle-excluded migration count, and proves the four pack-independent
contracts remain selected while the two full-pack contracts are honestly
excluded in a reduced ZIA profile.

## Known Deferrals

- Twenty larger Node test files still import the live Python oracle. They need
  compact, versioned frozen corpora or normalized tree manifests before the
  Python tree and selector exclusion can be removed.
- CI, release guards, Python tests, and the Python implementation remain until
  all retained evidence is frozen.
- The empty-profile process-test race is unrelated test-harness debt and is not
  changed in an evidence-archive slice.

## Review Focus

- Attack whether any replacement is weaker than the original comparison,
  especially JSON byte equality, the 16-item policy vector, float query
  spelling, and the exact Apply diagnostic prefix.
- Verify the symlink-loop expectation preserves Python semantics without
  hardcoding a platform-specific temporary-directory spelling.
- Verify the Zscaler guidance replacement inspects source pack metadata rather
  than becoming a Node-vs-Node self-comparison.
- Verify pack requirement routing is exact and does not silently skip these
  tests in the full profile or run full-pack tests in reduced profiles.
- Verify no production code, generated artifact, pack metadata, provider
  schema, Terraform behavior, or remaining live oracle changed.
