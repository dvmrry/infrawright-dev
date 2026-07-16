# Builder handoff: final Python source removal

## Intent

- Remove the retired Python implementation, tests, collector shims, and
  archive generators after their reviewed behavioral authorities were frozen.
- Make repository build, test, CI, release, pack, and operational surfaces
  Node-only without changing Terraform, provider, pack, projection, or artifact
  semantics.
- Reject `.py`, `.pyc`, and `__pycache__` artifacts in the exact release archive
  before any build step can conceal them.

## Base / Head

- Base: `df301a4` (draft PR #245).
- Implementation commit: `2f064c24fd125da850c27177c761be904c08c25d`.
- Review head: the current branch head (this handoff-only follow-up is stacked
  directly on the implementation commit).
- Diff command: `git diff df301a4..HEAD`.

## Removed Surface

- 49 `engine` Python files.
- 49 Python test/runner files.
- 10 product/shared collector shims.
- 4 retired authority generators.
- The obsolete Python test pack-requirements manifest.

Frozen authorities, their source hashes, embedded generator source,
resurrection commands, and compatibility-semantic Node modules remain intact.
The active demo fixtures and exact transform goldens remain intact.

## Supporting Changes

- CI pack-profile materialization is Node-owned and all Python setup/test jobs
  are removed.
- Module provenance headers name the maintained `iw gen-modules` command.
- The obsolete Python vendor-boundary audit is removed.
- Runtime release verification rejects Python artifacts, including forbidden
  symlink names, before source build and in the final runtime tree.
- npm-package and copied-pack tests assert absence rather than deleting source
  artifacts before testing.
- Every full test entry point routes through the Node suite selector and its
  hardcoded-interpreter tripwire.
- Current-state documentation describes the completed archive; historical and
  frozen provenance remains explicit.

## Invariants Claimed

- No tracked Python source or bytecode artifact remains.
- No Make, CI, release, test, pack, or operational path invokes Python.
- No frozen authority was re-recorded, weakened, remapped, or replaced by a
  Node self-comparison.
- Generated Terraform and operational artifact contracts are unchanged.
- Git history is the implementation archive.

## Tests Run

- `npm run typecheck`
- focused copied-pack and Python-disabled operational smoke tests: 7/7 passed
- `PYTHON=/usr/bin/false npm test`: 784 tests, 782 passed, 2 skipped, 0 failed
- `node scripts/verify-runtime-release.mjs . --artifacts-only`
- `git diff --check`
- `PYTHON=/usr/bin/false make check-all`
- `PYTHON=/usr/bin/false node scripts/test-runtime-release.mjs`
- Git tree, exact Git archive, and `npm pack --dry-run` Python-artifact checks

No live provider, backend, credentials, Terraform deployment Apply, or remote
mutation is part of this change.

## Review Focus

- Verify the frozen authority fixtures still retain their source locks,
  provenance, and resurrection instructions.
- Verify the deleted Python tests are covered by direct Node tests or frozen
  exact replay from the preceding archive PRs.
- Attack release/archive/npm-package paths for artifact concealment, symlink
  evasion, and build-before-check ordering.
- Verify reduced pack roots and collector authority do not rely on deleted
  co-located shims.
- Verify no generated behavior or pack semantics changed with the deletion.
