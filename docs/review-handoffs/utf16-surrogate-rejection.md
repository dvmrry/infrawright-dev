# UTF-16 surrogate rejection: builder review handoff

## Intent

- Reject every JSON string/key containing an unpaired UTF-16 surrogate in the
  Node control/data parsers, Node metadata parsing, and Go canonjson decoding.
- Preserve valid surrogate pairs, literal U+FFFD, normal invalid-UTF-8
  replacement behavior, numeric handling, and valid JSON output bytes.
- Remove the reference-backend-only raw-string preservation workaround because
  the generic parser now rejects the state it existed to preserve.

## Base / Head

- Base: `2397edc` (`exact Apply transcript remediation`), the reviewed
  intervening exact-Apply commit. This surrogate-rejection parcel is the
  uncommitted diff on top of that base; it deliberately does not alter the
  exact-Apply/evidence/terraformcmd implementation in the base.
- Head: uncommitted foundation worktree state.
- Diff command: `git diff 2397edc -- docs/go-runtime-v2.md go/cmd/iw/{authoring_a6_differential_test,block_d5_differential_test}.go go/internal/{authoring/authority,canonjson,plan} node-src/{json/control.ts,metadata/validation.ts} node-tests tests/fixtures/authoring/node-v1/authority.json`.

## Files Changed

- Parser/decoder: `node-src/json/control.ts`, `node-src/metadata/validation.ts`,
  `go/internal/canonjson/control.go`, `go/internal/canonjson/decode.go`.
- Tests: `node-tests/json.test.ts`, `go/internal/canonjson/{control,decode}_test.go`,
  `go/internal/plan/reference_backend_test.go`, and
  `go/internal/plan/lifecycle_test.go`. The lifecycle coverage is replaced
  with a mixed-escape surrogate preflight assertion proving zero Terraform
  initialization/planning calls.
- Reference backend: `go/internal/plan/reference_backend.go`.
- Frozen source-provenance locks: all nine `python-*-v1.json` fixtures, their
  Node SHA/source-hash assertions, `tests/fixtures/authoring/node-v1/authority.json`,
  `go/internal/authoring/authority/authority_test.go`, and the active A6/D5
  bundle-SHA locks in `go/cmd/iw/{authoring_a6_differential_test,block_d5_differential_test}.go`.
  Go fixture-digest consumers in `openapimap`, `providerprobe`, `reconcile`,
  and `sourceoperation` are repinned to the same provenance-only fixtures.
- D5 integration normalization: the differential transcript maps the reviewed
  descriptor-backed `/dev/fd/3` and `/proc/self/fd/3` plan arguments to the
  same `<assessment-snapshot>` marker as Node's randomized snapshot pathname.
  Lower-level exact-Apply tests continue to assert the concrete platform path.
- Active-oracle documentation: `docs/go-runtime-v2.md` records the rebuilt
  bundle SHA.
- Intentionally untouched: exact-Apply/evidence/terraformcmd implementation
  in the `2397edc` base; provider mappings, OpenAPI behavior, and artifact
  rendering outside reference-backend JSON strings.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `docs/adversarial-review.md`,
  `docs/python-oracle-contracts.md`, and the prior reference-backend tests.
- Other source evidence: Node `JSON.parse` behavior; Go `encoding/json`
  behavior; the frozen-authority source-blob conventions.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: nine frozen Python-authority fixtures have provenance-only changes;
  the active authoring authority manifest is repinned to their resulting
  hashes and to the rebuilt Node bundle.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: the rebuilt Node bundle is
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`
  (3,040,955 bytes; checksum-file SHA-256
  `b955f56a128a590f7811472959ce580cb344ed4fe400906377e6a2e30263f63e`).
  Fixture drift is only source SHA-256/Git-blob provenance plus the resulting
  fixture and authority-manifest locks. The final manifest SHA is
  `c9485be8b0c7a805247d54250c700c562ba8f32fa60f9e35ceb1b6c6e6671612`.
  A structural comparison reversing those explicit substitutions found no
  payload change.

## Expected Delta

- Expected behavior change: lone high/low UTF-16 units in JSON keys or values
  fail with `Unpaired UTF-16 surrogate` at the raw UTF-16 source offset.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: provenance hashes only; valid artifacts
  remain byte-identical.
- Expected no-op areas: valid escaped pairs, literal astral characters,
  U+FFFD, ordinary malformed-JSON error priority, and invalid UTF-8
  normalization.

## Invariants Claimed

- Evidence must not be silently dropped: N/A; parser rejects before a lone
  surrogate can be normalized and later misrepresented.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: affected source hashes and
  fixture digest locks were repinned; no artifact payload was changed.
- Ambiguity must stay classified instead of being coerced to success: N/A.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: reference-backend parsing fails before rendering
  or Terraform work when a key/value has an unpaired surrogate.

## Tests Run

- `npm run typecheck` — pass.
- `npm run test:node` — pass after the final active-oracle repin.
- `go test -count=1 ./internal/canonjson ./internal/plan` — pass.
- `go test -count=1 ./internal/authoring/authority` and
  `go test -count=1 ./cmd/iw -run '^TestA6'` — pass.
- The four standing byte gates (`RootCatalog|Transform|Topology|Generation`) —
  pass (final post-remediation run).
- Two consecutive `node scripts/build-metadata-cli.mjs` builds produced the
  identical bundle SHA/size/checksum recorded above.
- `go test -count=1 ./cmd/iw -run '^TestBlockD5'` — pass after normalizing the
  intentionally descriptor-backed Go plan argument to the existing snapshot
  marker used for Node's randomized pathname.
- The repository-wide Go run then identified five stale authoring fixture
  digest locks outside the initial parcel inventory; those locks were repinned
  and the full suite was rerun from the resulting final tree.
- `gofmt -w` on changed Go sources/tests and `git diff --check` — pass.
- Fixture structural gate: all nine changed fixtures parse identically to the
  base after reversing the explicit source/bundle/fixture lock substitutions;
  no payload change.

## Known Deferrals

- Deferred work: none within this parcel.
- Fresh changed-surface adversarial review approved the final tree after the
  ordinary-escape, syntax-priority, key-normalization, lifecycle, authority,
  fixture-consumer, and D5 integration remediations. It found no blocking or
  non-blocking findings.

## Review Focus

- Highest-risk files or paths: raw-token scanners in
  `node-src/json/control.ts` and `go/internal/canonjson/control.go`; post-parse
  Go validation in `decode.go`; lifecycle preflight; active-oracle bundle and
  authority-manifest locks.
- Specific assumptions to attack: ordinary JSON escapes between a high/low
  pair, mixed raw/escaped adjacency, key-vs-value coverage, duplicate invalid
  keys after Go's U+FFFD normalization, raw UTF-16 position mapping after
  astral source text, malformed-syntax priority, and the Go invalid-UTF-8
  normalization path.
- Source evidence the reviewer should verify: `JSON.parse` and `encoding/json`
  accept lone `\uXXXX` escapes but Go replaces them; the new checks occur after
  each runtime's syntax decoder.
- Generated artifacts the reviewer should compare: all nine changed authority
  fixtures, the active authoring manifest, both A6/D5 bundle SHA locks, and
  `docs/go-runtime-v2.md`; verify the two-build deterministic bundle proof.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  a high surrogate followed by a non-low unit must report the high unit; an
  isolated low must report itself; valid pair/U+FFFD must remain accepted; the
  removed backend workaround must not alter valid JSON.stringify-compatible
  escaping.
