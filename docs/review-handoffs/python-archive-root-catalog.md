# Builder handoff: Node root-catalog authority

## Intent

- Replace the last Python-only derivation/freshness authority for
  `catalogs/zscaler-root-catalog.v1.json` before the Python implementation is
  archived.
- Add `iw root-catalog`, `make root-catalog`, and
  `make check-root-catalog` while preserving the committed catalog bytes,
  source-file provenance, and SHA-256.
- Do not change root topology, pack metadata, assessment behavior, or the
  bundled catalog.

## Base / Head

- Base: `4f2ece4979d1de837c5d4637ab1bd91b6c458a61`
- Implementation head: `9bd4c06f3c01675825ae82cc4d9d7dae4243d62c`
- Remediated implementation head:
  `0f612c660ebba2899c1801bb97a87648116f56fa`
- Diff command: `git diff 4f2ece4979d1de837c5d4637ab1bd91b6c458a61..0f612c660ebba2899c1801bb97a87648116f56fa`

## Files Changed

- `node-src/metadata/root-catalog.ts`
- `node-src/cli/main.ts`
- `node-tests/root-catalog.test.ts`
- `node-tests/pack-test-requirements.json`
- `Makefile`
- `docs/python-archive-plan.md`
- `docs/repo-surface.md`
- Files intentionally left untouched: the bundled root catalog, all pack
  manifests/registries, Python implementation/tests, and runtime semantics.

## Source Inputs Consulted

- Provider schemas: none.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: every committed Zscaler `pack.json` and `registry.json` via
  the validated Node metadata loader.
- Existing docs or design records: `docs/repo-surface.md`, the adversarial
  review workflow, and the retained Python `engine/root_catalog.py` behavior.
- Other source evidence: `catalogs/zscaler-root-catalog.v1.json` and
  `docs/schemas/root-catalog.schema.json`.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. The Node renderer must equal the
  existing catalog byte-for-byte.

## Expected Delta

- Expected behavior change: maintainers can generate/check the compatibility
  catalog without Python; `make check-all` now checks its freshness.
- Expected report/count/coverage changes: one new four-case Node test file.
- Expected generated-output changes: none.
- Expected no-op areas: topology resolution, assessment, pack selection,
  Terraform behavior, provider behavior, and the committed catalog.

## Invariants Claimed

- Evidence must not be silently dropped: every selected pack manifest and
  existing registry file contributes `relative-path + NUL + bytes + NUL` to
  the source digest in deterministic order.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: `source_files` and
  `sources_sha256` exactly match the Python-produced catalog.
- Ambiguity must stay classified instead of being coerced to success: unknown
  providers fail; duplicate requested providers are deterministically
  deduplicated like the former Python producer.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: no adoption or Terraform path is touched.

## Tests Run

- `npm run typecheck`
- `npm run build:test && node --test .node-test/node-tests/root-catalog.test.js`
- `npm run build && make check-root-catalog`
- `npm test`
- `npm run test:all`
- `make check-all`
- Relevant output summary: all commands passed; the new renderer exactly
  reproduced the 151-resource all-Zscaler catalog and its existing digest.
- Review-remediation additions: the six-case focused suite passed against both
  the full pack root and a physically reduced Zscaler-only pack root. It now
  covers exact Python-compatible non-ASCII escaping and a selected pack with
  no registry file.
- Tests not run and why: no live provider/backend tests; this change reads only
  repository metadata and does not contact external systems.

## Adversarial Review Disposition

The initial fresh-context review requested changes at the implementation head.
Both blocking findings were accepted and fixed in
`0f612c660ebba2899c1801bb97a87648116f56fa`:

1. The root-catalog tests previously hardcoded the full `packs/` root and
   profile. They now resolve the effective pack root, profile, and catalog from
   the same environment used by the pruned distribution jobs, and CLI tests
   pass that root explicitly. The focused suite passes with only ZCC, ZIA,
   ZPA, ZTC, and the shared Zscaler component physically present.
2. The initial renderer used `JSON.stringify`, which emits literal non-ASCII
   characters. It now uses the existing Python-compatible JSON renderer, and
   an exact-byte fixture proves ASCII escaping for non-ASCII provider,
   resource, product, and slug strings.

The review's nonblocking absent-registry coverage note was also accepted and
added. Empty `--providers` remains a CLI usage error; that malformed-input
behavior is intentionally not part of catalog compatibility.

## Known Deferrals

- Deferred work: freeze/replace the 26 remaining live Python-oracle Node test
  files, then delete Python/CI/Make/release dependencies.
- Reason it is safe to defer: this change removes one prerequisite without
  weakening or deleting the live parity lane.
- Follow-up owner or trigger: the next Python archive slice after this review
  is accepted.

## Review Focus

- Highest-risk files or paths: `node-src/metadata/root-catalog.ts` and the
  catalog freshness test.
- Specific assumptions to attack: provider selection, longest-prefix slug
  derivation, `derive`/`generate` truthiness, optional `slug_group` omission,
  missing registry behavior, portable relative paths, source sort order, and
  exact digest framing.
- Source evidence the reviewer should verify: compare the former
  `engine/root_catalog.py`, pack loader behavior, and committed catalog.
- Generated artifacts the reviewer should compare: run `make
  check-root-catalog` and generate to a temporary file for byte comparison.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  non-Zscaler packs in the full profile, duplicate/unknown provider selection,
  absent registry files, overlapping provider prefixes, and a stale catalog.
