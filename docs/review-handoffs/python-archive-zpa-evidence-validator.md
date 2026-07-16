# Builder handoff: Python archive — ZPA provider evidence validator

## Intent

- Replace the unique Python validator for the committed ZPA provider v4.4.6
  evidence matrix with a typed Node library and `iw` command.
- Preserve the exact curated matrix and all local/source-binding checks.
- Delete `tools/zpa_provider_evidence.py` and its Python test without weakening
  source provenance, count accounting, or fail-closed behavior.
- Leave provider, pack, Oracle, adoption, and generated artifacts unchanged.

## Base / Head

- Base: `397a30c1dc6996283729648d16c1e258ec3627ec`
- Implementation head: `77c825af9b816c627244dcf8df120080ee059076`
- Review head: the final commit on
  `feature/archive-python-zpa-evidence-validator`; resolve with
  `git rev-parse HEAD` before review.
- Diff: `git diff 397a30c1dc6996283729648d16c1e258ec3627ec...HEAD`

## Files Changed

- Added `node-src/authoring/zpa-provider-evidence.ts`.
- Added `node-tests/zpa-provider-evidence.test.ts`.
- Added the `iw zpa-provider-evidence` authoring route and usage.
- Updated Node/Python pack-test requirement manifests.
- Updated active evidence/archive documentation.
- Deleted `tools/zpa_provider_evidence.py` and
  `tests/test_zpa_provider_evidence.py`.
- Intentionally untouched: `docs/evidence/zpa-provider-v4.4.6.json`, provider
  schemas, pack metadata, overrides, operational runtime semantics, and
  historical review handoffs.

## Source Inputs Consulted

- Provider schema: `packs/zpa/schemas/provider/zpa.json`.
- Pack metadata: `packs/zpa/pack.json`, `packs/zpa/registry.json`, and the
  relevant committed ZPA overrides named by the matrix.
- Provider source: the 17 source files and 45 inclusive source ranges pinned in
  `docs/evidence/zpa-provider-v4.4.6.json` at tag `v4.4.6`, commit
  `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`.
- Retired authority: obtain the Python validator and test from the base commit
  with `git show 397a30c:tools/zpa_provider_evidence.py` and
  `git show 397a30c:tests/test_zpa_provider_evidence.py`.
- Active design record: `docs/zpa-provider-evidence.md`.

## Generated Artifacts

- Reports: no report bytes changed.
- Schemas: none.
- Fixtures/snapshots: the canonical matrix is intentionally byte-unchanged.
- Demo/lab output: none.
- Expected artifact drift: none.

## Expected Delta

- `iw zpa-provider-evidence` replaces the Python command with success exit 0,
  evidence failure exit 1, and CLI usage failure exit 2.
- The same 16 resources, 12 local input bindings, 17 provider source-file
  bindings, 45 source anchors, six summary counts, import/read identities, and
  runtime-evidence gates must remain enforced.
- Two tracked Python files are removed; all remaining Python compatibility and
  live-oracle surfaces remain unchanged.

## Invariants Claimed

- Evidence is neither regenerated nor rewritten; the committed matrix remains
  the authority being validated.
- Local pack/schema hashes, exact fetch set/order, derived Terraform state
  shapes, import metadata, read identities, source files, source ranges, and
  summary counts all fail closed.
- Canonical committed input hashes remain bound to the repository, while all
  derived metadata is evaluated against the effective `INFRAWRIGHT_PACKS`
  root selected by the caller.
- Provider source paths must additionally be safe relative paths; this
  hardening does not alter the accepted matrix.
- Static source evidence does not upgrade any row beyond
  `terraform_runtime_evidence_required`.
- No provider, credentials, Terraform, Oracle, or deployment Apply is invoked.

## Tests Run

- `npm run typecheck`
- `npm run build`
- focused Node validator and selector tests: 41 pass, 1 optional source-checkout
  skip
- bundled CLI subprocess test with `python`, `python3`, and `PYTHON` tripwires,
  covering exits 0/1/2 and help exposure
- missing and independently copied effective pack roots, including registry,
  schema, and adoption-identity override drift
- `PYTHON=/usr/bin/false npm test`: 556 pass, 2 skip
- `make test-python-legacy`: 1,345 pass from a neutral worktree path
- `git diff --check`

## Known Deferrals

- The optional clean-checkout audit against the pinned provider commit was not
  rerun locally because the available provider checkout does not contain that
  commit. Full-file and range verification remain tested through an injected
  Git host and synthetic exact bytes; the original source-bound matrix is
  unchanged.
- Other Python engines/tests/collector shims remain for later archive slices.

## Accepted review finding

- Finding: the first Node implementation ignored `INFRAWRIGHT_PACKS` and could
  validate bundled metadata instead of the selected distribution.
- Root cause: canonical committed-input binding and effective-root semantic
  derivation used one unconditionally bundled `LoadedPackRoot`.
- Fix: preserve the canonical root for matrix input hashes, load the effective
  root separately, and derive fetch, schema, override, and adoption metadata
  from that selected root.
- Regression tests: missing effective root, copied registry drift, copied
  provider-schema drift, copied import-identity override drift, direct CLI,
  bundled CLI, and ZPA-profile selection.
- Verification: focused suite passes with 45 tests and one optional external
  source-checkout skip; patch-focused adversarial re-review required.

## Review Focus

- Compare every check in the retired Python tool against
  `node-src/authoring/zpa-provider-evidence.ts`; reject any missing authority.
- Verify the matrix remains byte-unchanged and is not replaced by a Node
  self-comparison.
- Attack exact-key validation, strict type/equality behavior, local input
  derivation, state-shape hashing, source path containment, Git pin/status
  checks, full-file hashes, inclusive range hashes, and summary recomputation.
- Verify bundled CLI dispatch and exit translation do not mask evidence errors.
- Confirm the Python test requirement was removed only because its complete
  replacement is selected under the same ZPA/shared-pack conditions.
