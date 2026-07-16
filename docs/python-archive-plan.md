# Python implementation archive plan

Status: active cleanup plan after the Node runtime integration merge
(`4f2ece4979d1de837c5d4637ab1bd91b6c458a61`).

## Goal

Remove Python from the repository's runtime, build, test, release, and pack
authoring paths without discarding the behavioral evidence that qualified the
Node migration. The end state has no tracked `.py` files and can run the full
repository gate with `python`, `python3`, and `PYTHON` pointed at failing
tripwires.

Git history is the archive for the retired implementation. Python source is
not copied into a second in-tree archive.

## Current inventory

The retained compatibility implementation contains approximately:

| Surface | Files | Lines | Disposition |
|---|---:|---:|---|
| `engine/` | 57 | 23,884 | Remove after differential evidence is frozen. |
| `tests/` | 57 | 31,484 | Replace live-oracle comparisons, then remove. |
| pack collectors | 10 | 383 | Remove after collector fixtures no longer invoke them. |
| `tools/zpa_provider_evidence.py` | 1 | 440 | Remove after its Node provider-probe replacement is fixture-bound. |

The shipped `iw` CLI is already Python-independent. The remaining dependency
is a qualification dependency: 20 Node test files still import the live Python
oracle during `npm run test:all`, CI still installs Python, and release guards
still require representative Python files. The first six direct contracts are
frozen with their resurrection procedure in
[Frozen Python oracle contracts](python-oracle-contracts.md).

Names such as `python-compatible`, `python-number`, and
`python-lower-15.1` describe frozen byte and Unicode semantics. They are not
runtime Python dependencies and are not renamed as part of the archive.

## Ordered removal

### 1. Replace Python-owned generated authorities

Every committed artifact still generated only by Python must have a Node
derivation and freshness gate before Python is removed. The first blocker is
`catalogs/zscaler-root-catalog.v1.json`; `iw root-catalog` and
`make check-root-catalog` replace `engine.root_catalog` without changing its
bytes or source digest.

Other retained generated authorities must be found by searching Python tests,
Make recipes, CI, and release guards before the deletion commit.

### 2. Freeze migration-oracle evidence

Do not delete an entire mixed Node test file merely because one test invokes
Python. Preserve direct Node tests and replace the live comparison with the
smallest durable authority:

- inline exact strings/objects for small JSON, path, policy, query, and error
  contracts;
- compact versioned corpora for plan evaluation, imports/moves, reports, and
  authoring classifications;
- normalized tree manifests plus content hashes for environment generation,
  provider probes, topology/scope, staging, and adoption artifacts;
- the reviewed exhaustive digest for Unicode lowercasing and binary64 JSON
  rendering rather than a sampled replacement.

Frozen fixtures record the producing baseline commit, supported Python/UCD
oracle, normalization rules, and SHA-256. Tests compare exact bytes or complete
semantic objects; parsed-JSON equality must not replace a byte contract.

After conversion, every former oracle-importing test joins the normal Node
suite. The suite selector's Python-oracle exclusion and resolver are then
removed.

### 3. Remove Python and switch the complete gate

Delete the Python implementation, Python tests, product collector shims, and
superseded Python tool. Then remove:

- `PYTHON`, `test-python-legacy`, and Python recipes from the Makefile;
- Python setup and inline Python scripts from GitHub Actions;
- Python file requirements from release packaging;
- stale generated comments and active documentation that instruct users to
  run `python -m engine.*`;
- the Python-engine vendor-boundary allowlist after the Node boundary remains
  the sole enforced authority.

Inline CI data transformations must be replaced by small Node scripts or
existing `iw` commands, not shell JSON rewriting.

### 4. Prove the archive

The deletion is acceptable only when all of these hold:

1. `npm run check:all` passes with Python tripwires.
2. `make check-all` passes with Python tripwires.
3. The stripped runtime-release smoke passes without `.py`, npm, or
   `node_modules`.
4. Pack-profile and physically reduced-pack jobs remain green.
5. Generated modules, demo artifacts, root catalog, and frozen differential
   fixtures are current.
6. Repository search finds no executable Python invocation and no tracked
   `.py` file.
7. A fresh adversarial review verifies that no evidence was silently dropped,
   weakened, remapped, or replaced with a self-comparison.

## Explicit non-goals

- Renaming historical compatibility semantics solely to remove the word
  `python`.
- Changing Terraform, provider, pack, projection, or artifact behavior.
- Re-recording fixtures to make a failing Node implementation pass.
- Keeping a hidden Python fallback or vendoring a Python interpreter.
- Moving the retired implementation to another directory in `main`.
