# Go product-authority handoff

Status: COMPLETE. This record closes G0 in
[singleton-state-topology-v2.md](../singleton-state-topology-v2.md) and
transfers product-source authority from the frozen Node v1 implementation to
Go. It does not switch the released/default operator route, authorize a live
Apply, or archive Node; those remain governed by
[go-cutover-roadmap.md](../go-cutover-roadmap.md).

## Frozen Node v1 authority

- Immutable source tag: `node-oracle-v1-final`
- Tagged commit: `047e39e5f2d0d0a1a5415587255200dea775ac0b`
- Final Node bundle SHA-256:
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`
- Bundle checksum-file SHA-256:
  `b955f56a128a590f7811472959ce580cb344ed4fe400906377e6a2e30263f63e`
- Authoring-authority manifest SHA-256:
  `c9485be8b0c7a805247d54250c700c562ba8f32fa60f9e35ceb1b6c6e6671612`
- Final full-corpus main run:
  `https://github.com/dvmrry/infrawright-dev/actions/runs/29786786008`

The final main run completed all 14 jobs: complete repository and Node gates,
runtime-release staging, ten pack profiles, and both pruned checkouts. The
retiring `zpa-provider-evidence` validator remains part of the frozen Node test
corpus. The candidate culminating at
`c3e18a67e4b61b90860e02b782342b3e98ebbd80` also completed the requested Opus,
GPT-5.6 Pro, and Fable review sequence with no blocking findings.

The tag is immutable. Node source, its v1 catalog, and its authority fixtures
do not change after this point. They remain executable only for the bounded
oracle/rollback window described by the cutover roadmap, then become
provenance-only artifacts.

The recorded bundle and complete v1 suite are rebuilt from the immutable tagged
tree, not from a post-handoff working tree. Singleton-state v2 changes the
current pack inputs and active catalog while the frozen Node tests remain bound
to their v1 distribution. The unversioned root-catalog schema remains the
frozen v1 contract; Go v2 uses `root-catalog.v2.schema.json`. Differential runs
materialize the tagged `ce48c2c6...` artifact and verify its digest; they never
rebuild the oracle from current v2 distribution inputs.

## Authority declaration

Go is the product-source authority after this record lands. The Go `iw` binary
exposes the retained operator surface and all six retained
authoring commands with no executable Node fallback inside the binary. New
product behavior is implemented in Go. Node v1 may prove retained compatibility
behavior; it does not define new behavior and is never updated to understand
singleton-state topology v2.

This source-authority transfer is distinct from release routing. The current
released/default operator path may remain Node during the candidate and
rollback window, but it cannot authorize or redefine v2 topology.

## Differential-gate disposition

The frozen tag remains the oracle for behavior that is independent of root
topology:

- Transform artifact/output-tree behavior;
- Fetch and metadata command behavior that does not consume root topology;
- saved-plan, assessment, staging, and Apply cases after their fixtures are
  expressed as implicit singleton state units;
- the retained authoring command differentials and immutable source-bound
  authority fixtures.

At the first singleton-state v2 parcel, these v1 comparisons retire because
their outputs are topology-dependent:

- `RootCatalog`;
- `Topology` (`roots`, `scope-paths`, and `plan-roots`);
- `Generation` cases that produce environment/root topology;
- any C4/D5 case whose fixture still declares `groups`, `strategy`, or
  `bind_references`.

They are replaced by committed, adversarially reviewed Go v2 goldens. A test is
not silently skipped: each retired case is either deleted with its replacement
named in the same change or rewritten to a topology-independent singleton
fixture. `canonjson`, `tfrender`, reports, fingerprints, and other unchanged
byte-reaching contracts remain exactly gated.

## Transitional v1 catalog rule

`catalogs/zscaler-root-catalog.v1.json` remains frozen at the tagged bytes until
the Node rollback lane is archived because the frozen Node build imports that
path. It is not a second product authority. Singleton-state v2 introduces
`catalogs/zscaler-root-catalog.v2.json`, and the active Go `ROOT_CATALOG` gate
moves to v2. The v1 file is deleted with the Node executable/archive phase,
not modified or regenerated after this handoff.
