# Builder handoff: Node Transform/Adopt parity authority

## Intent

- Replace the retired Python Transform/Adopt parity diagnostic and four live
  Python artifact invocations with one typed Node implementation and a frozen,
  source-bound CPython authority.
- Add the maintainer command `iw transform-adopt-parity`.
- Preserve every report byte, classification, artifact byte, pack pin,
  provenance check, and exit status while deleting the two superseded Python
  files.

## Base / Head

- Base: `55f6189efe888564b515a6c2f5a505348f921f6e` (draft PR #239 head).
- Head: working tree on `feature/archive-python-transform-adopt-parity`.
- Diff: `git diff 55f6189efe888564b515a6c2f5a505348f921f6e` plus untracked files shown by `git status --short`.

## Files changed

- Node parity core, Transform production seam, authoring CLI dispatch, tests,
  pack-test requirements, frozen authority, archive docs, and this handoff.
- Deleted `engine/transform_adopt_parity.py` and
  `tests/test_transform_adopt_parity.py`.
- Terraform, provider, pack, projection, fixture inputs, and operational
  Transform/Adopt semantics are intentionally untouched.

## Source inputs consulted

- Existing four `tests/fixtures/parity/*.json` fixtures and their pinned
  provider/SDK/local-pack evidence.
- Retired Python diagnostic/test and production Transform/Adopt sources.
- Existing Node Transform, Adopt, metadata, projection, and artifact renderers.
- `docs/transform-adopt-parity.md` and `docs/python-archive-plan.md`.

## Generated artifacts

- `node-tests/fixtures/python-transform-adopt-parity-v1.json`: complete CPython
  report and all four exact Transform tfvars, Adopt tfvars, Adopt imports, and
  drop results. Size 13,486 bytes; SHA-256
  `87f4ef2c299c413fd87193a6f2e312fcbbcbef0f501af3ebeab32f54942127a8`.
- No pack, module, root, Terraform, provider, or deployment artifacts change.

## Expected delta

- New Node maintainer command; four retained fixtures still return the exact
  Python report SHA-256
  `3bddcdc5dd39d691e87b5904d66044e8bbe8817a1f1ec8fadc926296dabf4445`
  and expected exit 1 because the DLP evidence gate remains open.
- Live Python-oracle Node consumers decrease from 13 to 12.
- Tracked Python decreases by 830 engine lines and 343 test lines.

## Invariants claimed

- Exact provider-state import-ID coverage and active pack pins remain
  fail-closed.
- Fixture provenance remains sanitized, exact-ref pinned, locally resolvable,
  and closed over classification evidence.
- JSON comparison distinguishes booleans, numeric kinds, signed zero, list
  edits, and RFC 6901 escaped keys.
- Reported differences must reconstruct exact Adopt bytes; comparator misses,
  stale expectations, unclassified changes, unacknowledged drops, and open
  evidence gates cannot become success.
- Frozen evidence is compared to production Node paths; it is not regenerated
  from Node output or reduced to parsed-JSON equality.

## Tests run

- `npm run typecheck`
- `npm run build`
- `npm test`: 618 passed, 2 optional skips, 0 failed.
- `make test-python-legacy`: 1,324 passed, 0 failed; the deleted Python test
  and its pack-requirements rule leave no stale discovery entry.
- Python-disabled focused Node parity, Adopt, and suite-selection tests: 39
  passed, 0 failed.
- Exact Node report equals the frozen CPython `report` string and digest.
- `make audit-vendor-boundary`: 159 allowed matches, 0 violations.
- `git diff --check`.

## Known deferrals

- Twelve unrelated Node test files still use the live Python oracle. They are
  the next archive slices and remain honestly excluded from the Node-only
  selected suite.
- The existing DLP name evidence gate is unchanged; this archive PR must not
  resolve or weaken it.

## Review focus

- Attack evidence completeness, provenance binding, strict JSON equality,
  RFC 6901 replay, stale-classification accounting, report/exit parity, and
  whether the frozen fixture could become a Node-to-Node self-comparison.
- Verify reduced-pack selection excludes both newly Node-owned Zscaler tests
  rather than evaluating them without their required packs.
