# Builder handoff: archive Python source-operation and SDK-path mapping

## Intent

- Retire Python source-operation and SDK-path authoring after freezing the
  complete CPython test authority.
- Keep `iw source-operation-map`, exact reports/artifacts, ambiguity, source
  precedence, and provider-specific evidence heuristics unchanged.

## Base / Head

- Base: `7d90752` (`feature/archive-python-authoring-leaves`)
- Head: pending review commit
- Diff: `git diff 7d90752...HEAD`

## Files changed

- Complete source-operation and SDK frozen authorities and Node replays.
- Frozen source-operation CLI output wired into the mixed authoring CLI test.
- Selector accounting, Python archive records, and vendor allowlist cleanup.
- Deleted four Python source/test files and the dead Python-only registry
  fixture.

## Source inputs consulted

- Baseline `7d90752ac4b800c5509b380d02dc828749f891a6`.
- CPython 3.13.13 / UCD 15.1.0.
- All 42 source-operation and 18 SDK-path Python tests.
- Exact Python, Node, test, and transitive source hashes recorded in fixtures.

## Generated artifacts

- `python-source-operation-map-v1.json`: 39 reports, 3 CLI cases, and 7
  helper authorities, plus all 10 former live differential reports.
- `python-sdk-path-evidence-v1.json`: 13 complete scanner/integration/CLI
  authorities.
- `generate-source-operation-authority.py`: pinned temporary reproduction
  script; delete with the final Python archive.
- No production artifact changes are expected.

## Expected delta

- Two fewer live Node Python-oracle consumers.
- 2,682 Python implementation lines and 3,372 Python test lines removed.
- `make source-operation-map` remains Node-backed and byte-compatible.

## Invariants claimed

- Every report-producing retired Python case has a complete frozen authority.
- Every former live Node/Python differential has a complete frozen authority;
  the AST comparison is compared to Python rather than to another Node result.
- CLI file outputs and stdout/stderr are exact byte contracts.
- Source precedence, ambiguity, provider heuristics, and false-positive
  suppression are not replaced with selected-field assertions.
- SDK action and unresolved diagnostics retain complete report coverage.
- Reduced profiles select both frozen tests because neither requires a pack.

## Tests run

- Python-disabled source-operation suite: 57/57 passed.
- Python-disabled SDK-path suite: 10/10 passed.
- Reduced-profile selector suite: 7/7 passed.
- Complete Node/legacy/final gates passed before the initial review; focused
  patch gates passed after remediation.

## Known deferrals

- Six unrelated Node tests still import the live Python oracle.
- Historical compatibility names and comments remain byte-contract debt.

## Review focus

- Attack the 42-test and 18-test corpus completeness claims.
- Verify CLI output/file contracts and temporary-root normalization.
- Verify source precedence, ambiguity, provider heuristics, and negative cases.
- Confirm deleted tests were not replaced by Node self-comparisons.
