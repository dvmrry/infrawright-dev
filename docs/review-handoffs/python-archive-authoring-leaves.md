# Builder handoff: archive Python authoring leaf harnesses

## Intent

- Retire the Python provider-probe and source-evidence-evaluation leaves after
  freezing their complete CPython output authorities.
- Keep the Node authoring commands, exact emitted artifacts, classifications,
  Markdown, and provider-recipe checks unchanged.

## Base / Head

- Base: `501bd09` (`feature/archive-python-environment-roots`)
- Head: pending review commit
- Diff: `git diff 501bd09...HEAD`

## Files changed

- Frozen provider-probe and source-evidence authorities and their Node tests.
- Node suite-selector accounting and provider-recipe regression coverage.
- Python archive documentation.
- Deleted `engine/provider_probe.py`, `tests/test_provider_probe.py`,
  `engine/source_evidence_eval.py`, and
  `tests/test_source_evidence_eval.py`.

## Source inputs consulted

- Baseline `501bd09384aa2e825342083141abc11789ed9bb1`.
- CPython 3.13.13 / UCD 15.1.0.
- Exact Python, Node, helper, and test source blobs recorded in each fixture.
- Committed GitHub and DigitalOcean provider recipes.

## Generated artifacts

- `python-provider-probe-v1.json`: exact five-file artifact sets for both
  retained provider-probe cases.
- `python-source-evidence-eval-v1.json`: complete evaluation objects, exact
  Markdown, and exact authoring-CLI artifact bytes.
- No production artifact changes are expected.

## Expected delta

- Two fewer live Node Python-oracle consumers.
- 1,232 Python implementation lines and 717 Python test lines removed.
- `iw provider-probe` and `iw source-evidence-eval` remain Node-backed and
  byte-compatible.

## Invariants claimed

- Provider-probe comparison covers every emitted artifact, not a parsed
  projection.
- Source-evidence comparison preserves complete classifications,
  shortcomings, row-capping, explicit nulls, Markdown, and CLI artifacts.
- Temporary-path normalization replaces only the exact fixture root.
- Fixture digests and source blobs prevent either authority from becoming a
  Node-to-Node self-comparison.
- Reduced test profiles select both frozen tests because neither requires a
  product pack.

## Tests run

- `PYTHON=/usr/bin/false node --test .node-test/node-tests/provider-probe.test.js .node-test/node-tests/provider-probe-parity.test.js .node-test/node-tests/authoring-source-evidence-eval.test.js .node-test/node-tests/node-test-suite-selector.test.js`
- `node --test .node-test/node-tests/authoring-cli.test.js` (three unrelated
  comparisons in this mixed test file still use the supported live oracle).
- `PYTHON=/usr/bin/false npm test`: 61 selected tests passed; 9 live-oracle
  files were honestly excluded.
- `make test-python-legacy`: passed after removing the two retired suites.
- `npm run typecheck`, `npm run build`, `make audit-vendor-boundary`, and
  `git diff --check`: passed.

## Known deferrals

- Provider-probe marker bytes retain the historical `engine.provider_probe`
  wording because the marker is part of the frozen compatibility contract.
- Eight unrelated live Python-oracle consumers remain for later archive
  slices.

## Review focus

- Attack fixture provenance, normalization, and complete artifact coverage.
- Verify unique Python provider-recipe and source-evaluation cases remain.
- Verify the deletions do not hide live Python calls or weaken exact bytes.
- Confirm neither frozen authority is a self-comparison.
