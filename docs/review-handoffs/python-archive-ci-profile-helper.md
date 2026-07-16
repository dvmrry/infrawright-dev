# Builder handoff: replace CI pack-profile Python helpers

## Intent

- Replace the two inline `python3` pack-profile materializers in the check
  workflow with one checked-in Node 24 helper.
- Preserve the selected pack/shared directory contract for copied pack roots
  and pruned checkouts.
- Leave Python setup, floor testing, legacy testing, and production engine
  behavior unchanged.

## Base / Head

- Base: `a00510b46b04767d371bf7c05286d13b52784253`
- Head: pending review commit
- Diff: `git diff a00510b46b04767d371bf7c05286d13b52784253...HEAD`

## Files changed

- `.github/workflows/check.yml`
- `scripts/materialize-pack-profile.mjs`
- `node-tests/ci-pack-profile.test.ts`
- This handoff.

## Expected delta and invariants

- CI invokes Node, rather than inline Python, to copy or prune a pack profile.
- Pack-set kind, version, keys, component names, ordering, uniqueness, selected
  directories, and required shared dependencies fail closed before mutation.
- Copy rejects recursively nested symlinks in every selected pack/shared tree.
- Prune recursively preflights the complete packs tree before its first removal,
  so an unsafe symlink cannot leave a partially pruned checkout.
- No production runtime, pack metadata, generated artifact, Terraform, provider,
  credential, or network behavior changes.

## Tests run

- `npm run typecheck`
- `npm run build:test`
- `PYTHON=/usr/bin/false node --test .node-test/node-tests/ci-pack-profile.test.js`
- Real `zscaler` copy and `empty` prune smoke checks.
- `git diff --check`

## Review focus

- Attempt traversal, malformed profiles, missing shared dependencies, and nested
  symlinks in selected and unselected trees.
- Verify every validation and symlink failure occurs before filesystem mutation.
- Verify copy/prune results match the previous CI selection behavior.
- Confirm the remaining Python workflow gates are deliberately untouched.
