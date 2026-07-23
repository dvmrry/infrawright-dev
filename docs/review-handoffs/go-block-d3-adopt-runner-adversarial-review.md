# Block D3 Adopt Runner — Adversarial Review

## Blocking Findings

None remain after correction.

The first review requested changes for infrastructure-affecting and fail-closed
defects:

1. A nil drift-policy pointer silently disabled projection policy.
2. Normalized key collisions could select a different identity, import ID, or
   state address than Node because Go map order had already discarded source
   insertion order.
3. Classified terminal counts were lost when identity derivation failed.
4. Runner and default-loader environment fallbacks did not preserve the
   source's per-entry-point asymmetry.
5. Default loaders deferred Oracle timeout validation until a command happened.
6. Logical-root refusal, fallback, coverage, pending-move, and isolation claims
   lacked direct safety tests.

The corrected implementation rejects nil policy before work, recursively
rejects raw normalized-key collisions, rejects normalized metadata aliases,
preserves classified counts and Node result ordering on derivation failure,
uses process environment only for a nil `RunAdoptBatch` environment, requires
explicit loader-constructor environments, and validates timeout eagerly. The
corrected diff was re-reviewed after the final constructor-asymmetry fix.

## Non-Blocking Risks

- A skipped item with neither `name` nor `id` renders `null` in Go where Node's
  template interpolation renders `undefined`. This affects only the diagnostic
  label; D5's exact stderr corpus should either close or explicitly accept it.
- Adoption diagnostic JSON uses Go HTML escaping for rare `<`, `>`, and `&`
  values. This is also a D5 diagnostic-byte watch item, not an identity or
  publication defect.
- State-projection tests are primarily synthetic. Add a committed-pack smoke
  vector when that corpus is expanded.

## Source Evidence Review

- Diff inspected: all D3 production, seam, and test files against `9b669a0`.
- Handoff inspected: `go-block-d3-adopt-runner-review.md`.
- Node authority inspected: `adoption-meta.ts`, `state-project.ts`,
  `adopt-runner.ts`, PR 247's policy merge, and their focused tests.
- Pack evidence inspected: active manifest order, registry adoption metadata,
  logical roots/references, legacy fallback, and the committed ZIA adoption
  classification fixture.
- Provider/API evidence: N/A; D3 consumes injected raw items and state loaders
  and contains no provider transport.

Targeted independent reviews verified:

- state projection preserves schema projection followed by sync, fill,
  pack-default dropping, and conditional omission, including recursive masks
  and collection resizing;
- pack/user policy merge preserves exact version-one semantics, manifest order,
  base-before-user entries, and independent user validation;
- logical-root preflight precedes loading, output coverage is exact, pending
  moves are checked in every window, compilation precedes atomic publication,
  and failed batch isolation publishes nothing;
- recursive raw and metadata-alias collisions fail before state loading;
- loader construction rejects nil environment and invalid timeout without
  invoking Terraform.

## Generated Artifact Review

- Reports, schemas, committed fixtures, and snapshots: none changed.
- Ephemeral logical-root tests compile and publish through the existing
  `canonjson`/`tfrender` byte renderers only.
- RootCatalog, Transform, Topology, and Generation remain byte-identical to the
  frozen Node oracle.

Independent checks passed against the final files: gofmt, vet, focused tests,
full Go suite, `internal/adopt` race tests, module verification, and all four
standing differential gates. No Terraform binary, provider API, credentials,
tenant, or live/local Apply operation was invoked during D3 implementation or
review.

## Verdict

**Approve with nits**

All safety-significant defects and proof gaps are closed. The remaining items
are diagnostic-byte watch points for D5 and do not affect classification,
identity, state coverage, or artifact publication.
