# A5 Transform/Adopt parity: builder review handoff

## Intent

- Port the retained Transform/Adopt parity diagnostic as a pure Go package.
- Exercise the existing production Transform and Adopt kernels against the four
  source-backed fixtures and reproduce the frozen Node report bytes exactly.
- Keep CLI argument parsing, environment selection, stdout/stderr, exit codes,
  help, and command routing in A6.
- Preserve every existing operator artifact and command path; A5 constructs a
  detached diagnostic report only.

## Base / Head

- Base: `52643592dcb14c01b666d278b4da90448ba8d311`
- Head: uncommitted A5 working tree on
  `feature/go-canonjson-foundation`
- Diff command: inspect the untracked
  `go/internal/authoring/transformadoptparity/` package, the eight-line seam in
  `go/internal/transformrun/adopt_seams.go`, and this handoff. The shared
  working tree is the review authority.

## Files Changed

- Files:
  - `go/internal/authoring/transformadoptparity/parity.go`
  - `go/internal/authoring/transformadoptparity/parity_test.go`
  - `go/internal/transformrun/adopt_seams.go`
  - this handoff
- Files intentionally left untouched: `cmd/iw`, Makefiles, Node sources and
  tests, frozen fixtures and authorities, `go.mod`, `go.sum`, Transform/Adopt
  kernels, artifact renderers, and public artifact publishers.

## Source Inputs Consulted

- Provider schemas: the active pack schemas loaded by the existing metadata
  root and real Transform/Adopt kernels for the four fixtures.
- OpenAPI/API contracts: not applicable.
- Provider source files: no provider source was executed; the fixture
  provenance contains the existing version-pinned provider and SDK source
  URLs.
- Pack metadata: active resource metadata, provider pins, overrides, adoption
  policy, and manifest `unescape_products` behavior from the existing Go
  loaders.
- Existing docs or design records:
  - `docs/go-authoring-port-roadmap.md`, A5 and A6
  - `docs/python-oracle-contracts.md`, Transform/Adopt parity contract
  - the accepted Transform and Adopt port records and standing differential
    gates
- Other source evidence:
  - `node-src/domain/transform-adopt-parity.ts`
  - `node-src/domain/transform-runner.ts`
  - `node-src/domain/adopt-runner.ts`
  - `node-tests/transform-adopt-parity.test.ts`
  - `node-tests/fixtures/python-transform-adopt-parity-v1.json`, exactly
    13,486 bytes, SHA-256
    `87f4ef2c299c413fd87193a6f2e312fcbbcbef0f501af3ebeab32f54942127a8`
  - the four existing fixtures under `tests/fixtures/parity/`

## Generated Artifacts

- Reports: the detached
  `infrawright.transform_adopt_parity` v1 report. The four-fixture output is
  exactly the 7,887-byte report embedded in the frozen authority, with derived
  SHA-256
  `3bddcdc5dd39d691e87b5904d66044e8bbe8817a1f1ec8fadc926296dabf4445`.
- Schemas: none generated or changed.
- Fixtures: no fixture changed.
- Snapshots: no snapshot changed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. A5 writes no artifact and the
  four standing Node-oracle artifact gates remain byte-identical.

## Expected Delta

- Expected behavior change: a Go library caller can losslessly load and
  validate parity fixtures, execute the real Transform and Adopt kernels,
  compare their `{items}` payloads, reconcile source-backed expectations,
  verify replay completeness, build the report, and render its frozen bytes.
- Expected report/count/coverage changes: none against the frozen authority.
  The accepted summary remains four fixtures, three equal, one evidence-gated,
  one classified difference, and zero review-required or unaccounted cases.
- Expected generated-output changes: none.
- Expected no-op areas: CLI/help/exit behavior, Make routing, pack metadata,
  Transform, Adopt, Terraform, provider I/O, dependencies, and every committed
  operator artifact.

## Invariants Claimed

- Evidence must not be silently dropped: every actual structural difference is
  either matched to exact declared evidence or remains unclassified; unused
  expectations remain stale and force review.
- Generic matcher evidence must not outrank source-backed evidence: not
  applicable; this package accepts only evidence declared by validated fixture
  provenance.
- Source precedence/provenance must remain explicit: fixtures require an active
  provider pin, syntactically version-pinned GitHub sources, existing
  repository-local evidence, `sanitized: true`, and closed key sets.
- Ambiguity must stay classified instead of being coerced to success:
  unclassified/stale differences, Transform drops, or incomplete replay force
  `review_required`.
- Provider-readiness counts must stay explainable: not applicable; A5 does not
  enter readiness accounting.
- Adoption safety invariants: provider-state IDs must exactly equal the real
  Adopt kernel's requested import IDs; each fixture gets a fresh adoption
  policy; no live oracle, Terraform, provider, or credentials path exists.
- Byte-completeness invariant: reported differences are replayed over the
  Transform payload and the reconstructed `{items}` bytes must equal Adopt;
  zero or partial comparator output cannot hide an unreported byte difference.
- Numeric invariant: booleans never equal numbers; integer/float kind, numeric
  spelling, and signed zero remain significant.
- Execution boundary: processing is sequential and synchronous. There are no
  goroutines, host processes, network calls, environment reads, or public
  filesystem mutations.
- Platform boundary: Darwin and Linux only. Windows is out of scope and has no
  compile or execution gate in A5.

## Tests Run

- Commands:
  - `gofmt -l internal/authoring/transformadoptparity
    internal/transformrun/adopt_seams.go`
  - `go vet ./...`
  - `go test ./...`
  - `go test -count=1 ./internal/authoring/transformadoptparity`
  - `go test -race -count=1 ./internal/authoring/transformadoptparity`
  - `go test -count=1 ./cmd/iw -run
    'RootCatalog|Transform|Topology|Generation'`
  - `git diff --check`
- Relevant output summary:
  - formatting and vet are clean; the full Go module passes.
  - the focused package and race run pass uncached.
  - the exact frozen A5 report, all embedded output hashes, DEL boundary, and
    validation/diff/replay failure vectors pass.
  - all four standing artifact differentials pass uncached.
- Tests not run and why:
  - A6 CLI/output/exit tests are deferred with the CLI wiring.
  - no network, credentials, live provider, Terraform, or real artifact
    publication is part of A5.
  - no Windows gate was run because Windows is outside the declared platform
    scope.

## Known Deferrals

- Deferred work: A6 command argument/env parsing, stdout/stderr, result-to-exit
  mapping, help, Make routing, complete-set publication, and the final
  authority handoff.
- Reason it is safe to defer: A5 exposes detached validated results and a
  result classification; it cannot publish or execute a public command by
  itself.
- Follow-up owner or trigger: A6, after A5 is accepted and committed.
- Error type names and exact validation prose are Go-native except where tests
  depend on a phase-identifying substring. Those strings are not report or
  artifact bytes; A6 must freeze only the command-visible boundary it actually
  exposes.
- The fixture-local evidence containment check deliberately follows the frozen
  Node lexical/stat behavior; it is evidence validation, not a symlink-safe
  repository sandbox claim.
- The frozen Node JSON-pointer grammar is intentionally loose: expectation
  paths require only empty-or-leading `/`, and replay accepts signed and
  leading-zero list indexes. A5 does not strengthen those quirks because the
  completeness report is byte-gated against the frozen authority.
- `ResultClassification` is a narrow accessor for maps returned directly by
  A5 `Compare` or `Build`, not a validator for arbitrary external maps. A6 must
  keep that provenance or replace it with a typed result before command
  wiring; contradictory external report validation is not an A5 surface.

## Initial Adversarial Review Remediation

- Blocking finding: canonical-render failures were collapsed to an empty
  string by strict comparison and expectation-key construction, which could
  erase distinct unrenderable numeric values. Remediation: `Differences`,
  strict equality, recursive comparison, comparator injection, and difference
  keys now propagate errors. Regressions cover native fractions, non-finite
  floats, invalid/overflowing numeric tokens, valid lossless fractions, and
  programmatic unrenderable expectations.
- Blocking finding: exact provider-state coverage lived only inside the
  injected loader, but Adopt does not call the loader when every item is
  skipped. Remediation: a fixture-local state tracker records requested IDs
  and performs a mandatory post-Adopt exact-coverage check. A real
  `zia_url_filtering_rules` skip fixture now rejects retained state `202`.
- Non-blocking boundary risk: returned report provenance aliased the caller's
  fixture map. Remediation: comparison deep-clones provenance before returning
  the detached report; a mutation regression proves isolation.
- Determinism correction: missing required keys are selected with the existing
  CPython-compatible string ordering.
- Focused read-only re-review found no remaining blocking or non-blocking
  findings and returned **Approve**. The exact frozen report, focused/race
  package tests, full uncached Go suite, vet, module tidy, and four standing
  artifact differentials all remained green.

## Review Focus

- Highest-risk files or paths:
  `go/internal/authoring/transformadoptparity/parity.go`, especially fixture
  validation, strict numeric comparison, pointer replay, exact state coverage,
  expectation reconciliation, and summary construction.
- Specific assumptions to attack:
  - The exact authority pass genuinely executes both existing production
    kernels rather than replaying or copying the embedded report.
  - The shared `ShouldUnescapeForTransform` seam delegates to the single
    production manifest implementation and cannot drift independently.
  - Wide integers remain accepted while non-finite floats fail closed.
  - Boolean/numeric, integer/float, signed-zero, missing-value, array
    insertion/deletion, escaped-key, and DEL cases cannot collapse.
  - Missing or extra provider-state IDs fail before a misleading report is
    produced.
  - A partial comparator result is detected by replayed byte mismatch.
  - Stale expectation ordering and all map/report rendering use the existing
    CPython-compatible renderer and string comparator.
  - No fixture mutation, map aliasing, or adoption-policy reuse can make later
    comparisons depend on prior fixtures.
- Source evidence the reviewer should verify: the frozen Node source/tests,
  authority identity, active pack pins, and the already-accepted Transform,
  Adopt, metadata, and canonjson APIs.
- Generated artifacts the reviewer should compare: the full rendered report,
  all Transform/Adopt SHA fields, and the DEL one-report/side hashes.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  unsupported JSON values; multiple missing/unknown keys; duplicate evidence;
  path traversal; source/dependency pin parsing; numeric overflow; duplicate
  names; stale/unclassified precedence; root replacement/deletion during
  replay; and Transform drops.
