# Node runtime archive adversarial-review handoff

## Intent

- Remove the executable Node runtime, build, test, CI, rollback, and release
  surfaces after Go became product authority and the user accepted Git history
  as the recovery path.
- Route every Make command through `IW ?= dist/iw` and make the Go-only gate the
  current repository contract.
- Move pack-set documents literally from `packsets/<name>.json` to
  `packs/<name>.packset.json`, with no compatibility copies or indirection.
- Preserve frozen fixtures, bundle digest, immutable tag, historical handoffs,
  reviewed Go goldens, and an explicitly opt-in differential resurrection
  path.
- Do not change pack selections, provider metadata, authoring evidence meaning,
  resource counts, adoption decisions, Terraform addresses, or safety gates.

## Base / Head

- Base: `origin/main` at `5b9abaeae23dfcc457aa3aeb8416249375eb97d7`.
- Head: implementation commit
  `27f730076ad76dab2c54b138e0eb16f702ee3639`.
- Diff command: `git diff --find-renames 5b9abae..27f7300`.

## Files Changed

- Files: Make/CI routing; runtime and archive documentation; Go package-root,
  profile-path, archive-tripwire, and opt-in differential tests; four Go-v2
  demo sidecars; literal pack-set moves; deletion of `node-src/`, executable
  `node-tests/` files, package/build configuration, and JavaScript/release
  scripts.
- Files intentionally left untouched: `node-tests/fixtures/*.json`, existing
  Go golden/testdata corpora, provider schemas, pack manifests, registries,
  overrides, root catalogs, Terraform modules, and untracked `reports/`.

## Source Inputs Consulted

- Provider schemas: committed schemas selected by existing pack metadata; no
  schema bytes changed.
- OpenAPI/API contracts: none changed.
- Provider source files: none changed.
- Pack metadata: every committed `pack.json`, especially `vendor` and
  `requires_shared`, as read by `TestCommittedPackProfilesAreDerivable`.
- Existing docs or design records: `docs/go-cutover-roadmap.md`,
  `docs/go-runtime-plan.md`, `docs/go-runtime-v2.md`,
  `docs/go-post-archive-compatibility-cleanup.md`, and the adversarial-review
  workflow/templates.
- Other source evidence: `node-oracle-v1-final` at
  `047e39e5f2d0d0a1a5415587255200dea775ac0b`; frozen bundle SHA-256
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`;
  existing Go v2 transform authority tests and stderr manifest.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: frozen JSON fixtures under `node-tests/fixtures/` retained
  byte-for-byte.
- Snapshots: none.
- Demo or lab outputs: added
  `zpa_app_connector_group.lookup.json`,
  `zpa_application_server.lookup.json`,
  `zpa_server_group.lookup.json`, and
  `zpa_server_group.generated.expressions.json` under `demo/config/demo/`.
- Artifact drift intentionally expected: exactly those four files. They are
  named by `TestV2TransformDefaultCrossStateAuthority` and its exact output manifest;
  a clean committed `make check` regenerated no further demo drift.

## Expected Delta

- Expected behavior change: all active commands build and execute `dist/iw`;
  active CI no longer builds, downloads, or executes the frozen bundle; package
  root discovery requires `packs/full.packset.json` rather than `packsets/`
  plus a `package.json` fallback.
- Expected report/count/coverage changes: none. Full metadata still loads 151
  resources and 74 overrides through the unchanged Go loader tests.
- Expected generated-output changes: the four ZPA Go-v2 sidecars above; pack-set
  bytes are unchanged across their literal renames.
- Expected no-op areas: provider/OpenAPI/source analysis, pack metadata,
  selection semantics, topology v2, Terraform rendering, saved-plan
  assessment, and Apply authorization.

## Invariants Claimed

- Evidence must not be silently dropped: all frozen JSON evidence remains; the
  tag, digest, and recovery boundary are recorded in
  `docs/archive/node-runtime-archive.md`.
- Generic matcher evidence must not outrank source-backed evidence: no matcher,
  source analyzer, or evidence precedence code changed.
- Source precedence/provenance must remain explicit: unchanged; historical
  oracle execution now requires explicit `INFRAWRIGHT_FROZEN_NODE_ORACLE`.
- Ambiguity must stay classified instead of being coerced to success: no
  classification code or fixture changed.
- Provider-readiness counts must stay explainable: full-profile tests retain
  151 registry entries and 74 overrides; profile derivation uses only manifest
  identity, `vendor`, and `requires_shared`.
- Adoption safety invariants: plan binding, freshness/TOCTOU checks,
  assessment, exact saved-plan Apply, state addresses, and artifact renderers
  are unchanged.

## Tests Run

- Commands:
  - `test -z "$(gofmt -l go/cmd go/internal)"`
  - `cd go && go vet ./...`
  - `cd go && go test -count=1 ./...`
  - `env -u INFRAWRIGHT_FROZEN_NODE_ORACLE make check`
  - `make check-core`
  - `make archive-tripwire`
  - `PATH=<failing-node-and-npm-interceptors>:$PATH make check-distribution check-root-catalog`
  - local reduced-root simulations of the CI profile job for `empty` and
    `zscaler`, including focused profile-load and derivability subtests.
  - `env -u INFRAWRIGHT_FROZEN_NODE_ORACLE go test -count=1 ./cmd/iw -run '^TestA6' -v`.
- Relevant output summary: all commands passed. The A6 run visibly skipped only
  the two archived differential tests; Go help, sole-Make-lane, and
  no-external-executable tests passed. The full Go suite passed all packages.
  `make check` regenerated the committed demo with no drift. Both reduced roots
  passed the full distribution gate.
- Tests not run and why: GitHub Actions has not yet run on the pushed branch;
  live provider/backend/Apply qualification is separately human-gated and not
  claimed; the opt-in frozen-bundle differential was not rerun because the
  archive deliberately removes it from the current gate and retains its
  accepted tag/digest evidence.

## Known Deferrals

- Deferred work: cross-platform signed Go release artifacts and an `iw version`
  contract remain roadmap work; the obsolete Node release script was not
  replaced inside this archive parcel.
- Reason it is safe to defer: this dev repository's supported current-tree
  build is `make dist/iw`; deletion of the obsolete publisher cannot redirect
  runtime behavior or weaken Apply gates.
- Follow-up owner or trigger: release owner before publishing a new standalone
  Go distribution.

## Review Focus

- Highest-risk files or paths: `Makefile`, `.github/workflows/check.yml`,
  `go/cmd/iw/main.go`, differential harness setup, profile derivability tests,
  and the four demo sidecars.
- Specific assumptions to attack: every active Make target truly uses `$(IW)`;
  CI's shell copy faithfully materializes all eleven profiles; removal of the
  package marker does not break relocated binaries; archived oracle gating does
  not skip Go-only authority tests; no deleted JavaScript script remains a
  current documented workflow dependency.
- Source evidence the reviewer should verify: tag/commit/digest in the archive
  record; pack `vendor` and `requires_shared` closure; existing v2 transform
  manifest naming the four sidecars.
- Generated artifacts the reviewer should compare: the four new demo files
  against fresh `make demo` output and the v2 transform authority manifest.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  profile files accidentally treated as packs; empty/shared-only reduced roots;
  missing profile names accepted by derivation; stale local bundle causing a
  default differential; package-root symlink/file-marker behavior; docs that
  still instruct use of a deleted current-tree script.
