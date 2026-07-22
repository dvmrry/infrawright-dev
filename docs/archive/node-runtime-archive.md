# Node runtime archive record

Archived: 2026-07-22.

The Go command tree had already become product authority before this archive.
The dev repository then removed the live Node rollback/build lane directly,
based on completed downstream testing and the explicit decision that any
needed recovery can use Git history. This record does not recast the existing
credential-free tests as live-provider or live-Apply qualification.

Removed current-tree surfaces:

- `node-src/` and executable files under `node-tests/`;
- `package.json`, `package-lock.json`, and TypeScript build configuration;
- JavaScript build, verification, profile-materialization, performance, and
  release scripts;
- Node build/test/artifact jobs and downloads in CI;
- Make routing variables, bundle prerequisites, rollback targets, and the
  package-root fallback;
- the compatibility `packsets/` directory.

Retained evidence:

- the immutable `node-oracle-v1-final` tag at
  `047e39e5f2d0d0a1a5415587255200dea775ac0b`;
- frozen bundle SHA-256
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`;
- JSON oracle and provider fixtures under `node-tests/fixtures/`;
- reviewed Go goldens, source-bound authoring evidence, historical handoffs,
  and provenance documentation;
- opt-in differential harnesses, disabled unless
  `INFRAWRIGHT_FROZEN_NODE_ORACLE` names a separately recovered bundle.

Go comments that cite `node-src/*.ts` are retained provenance pointers, not
current-tree source dependencies. Resolve those paths against the immutable
`node-oracle-v1-final` tag above; they are intentionally absent from the
working tree after archive.

The pack layout was simplified in the same parcel: each former
`packsets/<name>.json` file moved literally to `packs/<name>.packset.json`.
There is no compatibility copy, indirection, generated manifest, or second
authority.
