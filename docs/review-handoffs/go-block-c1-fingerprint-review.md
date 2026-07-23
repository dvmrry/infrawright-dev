# Builder Handoff: Go Block C1 Fingerprinting

Review status: **Approve**. The fresh-context result is recorded in
`docs/review-handoffs/go-block-c1-fingerprint-adversarial-review.md`.

## Intent

- Port the saved-plan source fingerprint contract from
  `node-src/domain/plan-fingerprint.ts` into the Go runtime.
- Preserve payload bytes, SHA-256 values, filesystem traversal, bounded-read
  charging, generated-root HCL recognition, and failure behavior exactly where
  they feed saved-plan evidence.
- Keep the existing Node runtime, frozen Python authority, standing artifact
  gates, and all non-plan Go packages unchanged.

## Base / Head

- Base: `edf51beff45a3ffeba907bdfca2adf6868b835a6`.
- Head: uncommitted working tree on `feature/go-canonjson-foundation`.
- Diff command: `git diff --no-index /dev/null go/internal/plan/<file>` for each
  file listed below; all five files are new.

## Files Changed

- Files:
  - `go/internal/plan/doc.go`
  - `go/internal/plan/fingerprint.go`
  - `go/internal/plan/fingerprint_files.go`
  - `go/internal/plan/fingerprint_hcl.go`
  - `go/internal/plan/fingerprint_test.go`
- Files intentionally left untouched: evidence, contract, lifecycle,
  assessment, CLI, Node sources, frozen fixtures, schemas, and existing Go
  packages.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records:
  - `docs/go-runtime-v2.md`
  - `docs/review-handoffs/go-block-c-plan-lifecycle.md`
- Other source evidence:
  - `node-src/domain/plan-fingerprint.ts`
  - `node-tests/plan-fingerprint.test.ts`
  - `node-tests/fixtures/python-plan-fingerprint-v1.json`
  - existing `go/internal/artifacts`, `canonjson`, `pypath`, and `procerr`
    contracts.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: no fixture changed; the existing Python authority is consumed
  read-only and pinned to SHA-256
  `69ebf724f468e72c37ffaac33f78055e37cc944397fa923a31ff08331030a1b6`.
- Snapshots: none committed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: the Go module gains deterministic plan-source and
  init-source fingerprint primitives; no CLI command calls them yet.
- Expected report/count/coverage changes: one new Go package and its focused
  tests; no product report or readiness count changes.
- Expected generated-output changes: none outside values returned directly by
  the new package.
- Expected no-op areas: existing four artifact byte-gates, Fetch/Transform,
  Terraform transport, metadata, and Node runtime behavior.

## Invariants Claimed

- Evidence must not be silently dropped: every eligible root/module/backend/
  var file is charged and hashed in deterministic source order; malformed
  filenames and unsupported HCL fail closed.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the frozen Python fixture
  and Node TypeScript are the authorities; fixture integrity is checked before
  use.
- Ambiguity must stay classified instead of being coerced to success: missing
  module sources, remote sources, duplicate modules, heredocs, malformed
  generated-root blocks, invalid names, and budget failures reject.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: canonical JSON is compact Python-compatible
  `ensure_ascii` with no trailing newline; plan fingerprint version remains 2;
  the complete payload and digest must match the frozen authority exactly;
  one caller-supplied `ReadBudget` is charged serially through composed reads.

## Tests Run

- Commands:
  - `gofmt -l internal/plan`
  - `go test -count=1 ./internal/plan`
  - `go test -count=1 ./...`
  - `go vet ./...`
  - `go list -m all`
- Relevant output summary: focused authority parity passes; all Go packages
  pass; vet is clean; the module still has zero third-party dependencies.
- Tests not run and why: C1 evidence, C2/C3 lifecycle/report, and C4 CLI tests
  do not exist yet and are explicitly outside this fingerprint-only parcel.

## Known Deferrals

- Deferred work: evidence preparation/recheck/cleanup, plan contract and
  lifecycle, assessment/report, and CLI differential corpus.
- Reason it is safe to defer: no existing production path calls the new
  fingerprint package; each downstream parcel has an explicit dependency and
  acceptance gate.
- Follow-up owner or trigger: C1 evidence begins only after this fingerprint
  parcel passes fresh adversarial review and is committed.

## Review Focus

- Highest-risk files or paths: `fingerprint_hcl.go`, filesystem traversal in
  `fingerprint_files.go`, and the compact encoder in `fingerprint.go`.
- Specific assumptions to attack:
  - Python Unicode/code-point ordering and `ensure_ascii`, especially DEL,
    non-ASCII BMP characters, and astral surrogate pairs.
  - root symlink following versus nested directory-symlink refusal.
  - budget charging of irrelevant directory entries and root-config files that
    are later filtered out.
  - Python `splitlines(keepends=True)` and whitespace behavior in the HCL
    scanner.
  - stable basename-only var-file ordering and same-basename ties.
  - nil default budget versus one shared caller-supplied budget.
- Source evidence the reviewer should verify: every exported behavior against
  `plan-fingerprint.ts` and the corresponding Node tests, rather than relying
  on this summary.
- Generated artifacts the reviewer should compare: decoded frozen payloads,
  canonical bytes, plan/init digests, module paths, scanner results, FEFF
  names, and symlink-tree results.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  invalid UTF-8 names, disappearing files between enumeration and stat,
  ignored-directory symlinks, missing local modules, nonlocal sources,
  comments/heredocs/unbalanced braces, and backend or var-file symlinks.
