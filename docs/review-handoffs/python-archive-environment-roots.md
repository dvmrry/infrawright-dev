# Builder handoff: archive Python environment-root generation

## Intent

- Retire the Python environment-root generator and its direct tests after
  freezing the complete CPython output authority.
- Keep `iw gen-env`, root bytes, grouping, bindings, backend markers, stale
  file behavior, and dangling-symlink behavior unchanged.

## Base / Head

- Base: `b41db74` (`feature/archive-python-transform-adopt-parity`)
- Head: pending review commit
- Diff: `git diff b41db74...HEAD`

## Files changed

- Frozen environment authority and Node environment tests.
- Node/Python pack-test requirements and selector regression.
- Python archive documentation.
- Deleted `engine/gen_env.py` and `tests/test_gen_env.py`; removed the two
  dependent environment cases from `tests/test_group_bindings.py` after their
  behavior was retained in Node.

## Source inputs consulted

- Baseline `55f6189efe888564b515a6c2f5a505348f921f6e`.
- CPython 3.13.13 / UCD 15.1.0.
- Exact source blobs recorded in the frozen fixture.
- Committed ZIA, ZPA, ZCC, and ZTC registries and the full packset.

## Generated artifacts

- `python-environment-roots-v1.json`: exact representative tree bytes,
  complete 453-file manifest, dangling symlink evidence, and provenance.
- `scripts/archive/generate-python-environment-roots-authority.py`: pinned,
  deterministic CPython authority reproduction; delete only with the final
  Python archive after its producing commit remains reachable in git history.
- No production artifact changes are expected.

## Expected delta

- One fewer live Node Python-oracle consumer.
- 617 Python implementation lines and 1,552 Python test lines removed.
- `make gen-env` remains Node-backed and byte-compatible.

## Invariants claimed

- Every full-profile output path remains represented; manifest comparison is
  exact for path, byte length, and SHA-256.
- Representative root bytes are compared without normalization.
- Group-local binding and duplicate-name behavior remain tested in Node.
- Reduced profiles exclude the authority test unless the complete eight-pack
  catalog and shared Zscaler metadata are present.

## Tests run

- `PYTHON=/usr/bin/false node --test .node-test/node-tests/environment-generator.test.js`
- `python3 -m unittest tests.test_group_bindings`
- Final gates pending before review.

## Known deferrals

- Generated comments retain the historical `engine.gen_env` wording because
  those bytes are part of the compatibility contract.
- The remaining live Python-oracle consumers are separate archive slices.

## Review focus

- Attack fixture provenance and completeness, especially the 453-file tree.
- Verify dangling symlink and group-binding evidence was not weakened.
- Verify deletions do not remove unique behavior or break reduced profiles.
- Confirm no self-comparison replaced the retired CPython authority.
