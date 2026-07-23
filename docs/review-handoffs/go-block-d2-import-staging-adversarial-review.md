# Block D2 Import Staging — Adversarial Review

## Blocking Findings

None remain after correction.

The initial review requested changes because ordinary staging opened and
truncated the final Terraform artifact before copying completed. A copy or
close failure could therefore leave an empty or partial `_imports.tf` or
`_moves.tf` while returning failure. The first correction prepared a
randomized same-directory temporary file through `os.Root`, verified its
identity, and published it with a descriptor-relative rename.

The second review found one remaining transaction-boundary issue: a successful
rename was followed by `return root.Close()`, which could falsely report
failure after publication had committed. The final correction defines rename
as the commit point and cannot convert a later descriptor-close error into an
operation failure. The corrected diff was reviewed a third time.

## Non-Blocking Risks

- D2's focused topology tests use synthetic loaded metadata rather than every
  committed pack profile.
- This is non-blocking because staging delegates selection and whole-root
  expansion to the already-qualified `roots.LoadedRootTopology`, and the D2
  tests cover the relevant whole-root integration and ordering.
- Add a committed-pack CLI smoke case to D5's differential corpus.

## Source Evidence Review

- Diff inspected: the four D2 Go files against `fcce466`.
- Handoff inspected: `go-block-d2-import-staging-review.md`.
- Node authority inspected: `node-src/domain/import-staging.ts`, the generated
  import filter in `node-src/domain/import-moves.ts`, and their Node tests.
- Byte behavior verified: exact ordinary copies, state-aware import filtering,
  Python whitespace and newline behavior, BOM handling, state-list separators,
  diagnostics, and traversal order.
- Terraform boundary verified: D2 can invoke only bounded `init -input=false`
  and `state list`; backend preflight occurs before Terraform and the
  environment is snapshotted.

The final failure matrix proves that injected failures after copy, chmod, and
close preserve an existing destination's bytes and mode, do not create an
absent destination, preserve the source, and leave no temporary remnant. A
post-rename close-error test proves successful publication is reported as
success. A hostile temporary-name rebind test proves the regular-file and
`os.SameFile` checks refuse a symlink swap, remove only the descriptor-relative
temporary name, and leave both the existing destination and outside victim
unchanged.

## Generated Artifact Review

- Reports, schemas, and committed fixtures: none changed.
- Import and moved artifacts continue through existing `tfrender` path
  computation and retain exact bytes.
- RootCatalog, Transform, Topology, and Generation passed byte-identically
  against the frozen Node oracle.

Independent checks passed: gofmt, vet, focused tests, focused race tests, the
full Go suite, module tidy/verification, and all four standing differential
gates. No real Terraform binary, provider API, credentials, tenant, or Apply
operation was invoked during implementation or review.

## Verdict

**Approve**

The final D2 closes both publication blockers and preserves the fail-closed
artifact boundary without changing successful artifact bytes.
