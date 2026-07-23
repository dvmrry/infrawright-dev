# Go authoring A6: builder review handoff

## Intent

- Complete the final authoring-port slice by routing the six retained authoring
  commands through the existing Go `iw` binary and the Go-backed Make targets.
- Preserve frozen Node v1 command bytes, artifacts, diagnostics, and exits for
  overlapping legacy inputs while composing the already-accepted source-first
  Go capabilities for qualified and explicitly unverified v2 inputs.
- Add a cooperative, complete-set artifact publisher so a successful v2
  publication replaces the whole declared artifact vocabulary and removes
  stale optional artifacts.
- Retire `zpa-provider-evidence` from the active command surface while retaining
  its frozen matrix as qualification evidence for the generic analyzer.
- Keep singleton-state degrouping, release packaging, the final authority tag,
  Node deletion, and the global operator cutover out of this change.

## Base / Head

- Base: `6c31ebf6b7a4b73fe1f1867df8385deb8bc85480`
- Head: uncommitted A6 working tree on `feature/go-canonjson-foundation`
- Diff commands: `git diff 6c31ebf --` plus `git status --short`; new files are
  intentionally untracked until the coordinator accepts the review.

## Files Changed

- Command composition and tests:
  `go/cmd/iw/commands_authoring_{core,source,probe}.go` and matching tests.
- Integration and frozen differential corpus:
  `go/cmd/iw/{main.go,usage.txt,authoring_a6_differential_test.go}` plus the
  existing CLI differential helpers whose usage normalization now recognizes
  the intentionally changed six-command authoring block.
- Complete-set publication:
  `go/internal/authoring/artifactpublish/**`.
- Narrow sealed decision/copy accessors:
  `go/internal/authoring/sourceoperation/{v2.go,decision_test.go}` and
  `go/internal/authoring/providerprobe/{types,legacy,qualified}.go` with tests.
- Exact legacy adapters/fixes:
  `go/internal/authoring/providerprobe/legacy_api.go` and the one-newline
  `go/internal/authoring/openapimap` renderer correction.
- Build routing and contract:
  `Makefile` and
  `docs/review-handoffs/go-authoring-a6-coordinator-manifest.md`.
- Files intentionally left untouched: Node source and bundle, frozen fixtures,
  `go.mod`/`go.sum`, operator-command routing, release configuration,
  singleton-state topology, and all live-provider/lab material.

## Source Inputs Consulted

- Provider schemas: local authoring fixtures only, including
  `tests/fixtures/authoring/source-first-v2/provider-schema.json`.
- OpenAPI/API contracts: frozen authoring fixtures and
  `node-src/authoring/{cli,json,openapi-resource-map,provider-probe}.ts`.
- Provider source files: explicit fixture-local provider and SDK roots only.
- Pack metadata: existing checked-in packs for the frozen Transform/Adopt
  parity vectors; no pack output changed.
- Existing design records:
  `docs/go-authoring-port-roadmap.md`, `docs/go-runtime-v2.md`, and
  `docs/review-handoffs/go-authoring-a6-coordinator-manifest.md`.
- Other source evidence: frozen Node bundle SHA-256
  `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`.

## Generated Artifacts

- Reports: fixture-temporary command outputs only; none committed.
- Schemas: none.
- Fixtures: none changed or added.
- Snapshots: none changed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none for legacy inputs. V2 bundle
  publication is new command behavior and uses the already-accepted sealed
  source-first bytes.

## Expected Delta

- The Go binary exposes exactly `reconcile`, `openapi-map`,
  `source-operation-map`, `source-evidence-eval`, `provider-probe`, and
  `transform-adopt-parity` as retained authoring commands.
- Those Make targets use `IW_MAINTAINER ?= dist/iw`; global `IW_OPERATOR` and
  release routing remain deferred.
- `zpa-provider-evidence` is absent from active Go help/routing.
- Legacy source-evidence evaluation requires explicit `--source-facts`; the
  retired automatic `go run tools/source-evidence-ast` convenience and
  `--ast-tool-dir` are not carried into the single-binary authority.
- Qualified mode consumes manifest-bound roots; unverified mode requires an
  explicit bounded file/module grammar and cannot mint verified trust.
- No report/count/coverage or checked-in generated-output delta is expected.

## Invariants Claimed

- Evidence must not be silently dropped: CLI composition publishes only sealed
  bundle/probe artifacts and never rebuilds evidence from decoded maps.
- Generic matcher evidence must not outrank source-backed evidence: the CLI
  calls the accepted source-analysis and source-operation capabilities without
  changing their precedence.
- Source precedence/provenance must remain explicit: qualified and unverified
  modes use distinct typed load/analyze/compile chains.
- Ambiguity must stay classified instead of being coerced to success: no CLI
  inference or fallback is added.
- Provider-readiness counts must stay explainable: A6 does not compute them.
- Publication must not expose a mixed artifact set under the declared
  cooperative/exclusive ownership model. A failed promotion attempts rollback;
  a cleanup failure after promotion is reported as committed.
- A usage failure runs no domain logic or publication. Post-publication exits
  remain ordered: reconcile status 4, source regression status 1, probe status
  2.
- No command clones, downloads, scrapes, invokes Node, invokes a second
  authoring executable, or contacts a provider.

## Tests Run

- `go test -count=1 ./cmd/iw -run
  'TestA6|TestProviderProbeCommand|TestAuthoring|TestReconcile|TestOpenAPIMap|TestTransformAdoptParity'`
  passed.
- `go test -count=1 -race ./internal/authoring/artifactpublish
  ./internal/authoring/providerprobe ./internal/authoring/sourceoperation
  ./cmd/iw` passed.
- `go test -count=1 ./internal/authoring/a0fixture` passed when run alone.
- `go vet ./...` passed.
- `go mod tidy -diff` produced no output.
- The final serial `go test -count=1 ./...` passed all packages.
- The first broad `go test -count=1 ./...` was run concurrently with the broad
  race command; both reached the fixture's nested 30-second compile timeout in
  `internal/authoring/a0fixture/sdk` and were killed. Every other package passed,
  and the exact failed package and final full suite passed when rerun without
  competing compiler load.
- No network, credentials, real provider, live Terraform, or infrastructure
  operation was run or reachable from these tests.

## Known Deferrals

- Formal Node freeze/tag and product-authority declaration wait for the user's
  external Opus, GPT-5.6 Pro, and Fable review sequence.
- Global operator routing, release artifacts/signing, stable-tag cutover, Node
  archive, and singleton-state degrouping remain later roadmap phases.
- `INFRAWRIGHT_PERFORMANCE_REPORT` remains the existing whole-Go CLI deferral;
  A6 does not implement it selectively.
- The complete-set publisher deliberately makes no same-UID hostile-writer,
  NFS/remote-lock, or power-loss durability claim. Its lock is cooperative and
  never stolen.

## Adversarial Review Outcome

The fresh read-only review initially requested changes for three blocking
issues. The coordinator remediated them in one bounded pass:

- V2 source-bundle warnings and conflict decisions now occur only after a
  successful publication; failure tests cover unavailable and degraded status.
- Legacy source command validation now rejects invalid arguments before any
  file read, with frozen-Node usage-priority differentials and direct no-read
  tests.
- Qualified provider-probe now preflights the required work root, creates and
  verifies its private work directory, and passes an expected recipe mode into
  execution so a categorical mode change fails closed.

The same pass pinned the exact authoring help block, restored the operator help
differentials, added a static Make-routing assertion, and proved the optional
provider-probe artifact present-to-absent lifecycle plus failed-replacement
preservation. The fresh reviewer rechecked those changes, reported no findings,
and returned **Approve**. The recorded review is
[go-authoring-a6-adversarial-review.md](go-authoring-a6-adversarial-review.md).

## Review Focus

- Attack legacy command parsing, env semantics, output ordering, and post-write
  exits directly against `node-src/authoring/cli.ts` and the frozen bundle.
- Verify that the v2 CLI cannot cross qualified/unverified capabilities,
  silently discover source, accept unverified OpenAPI, or reconstruct sealed
  status from artifact maps.
- Verify OpenAPI unavailable/degraded warnings and conflict regression exits
  happen only after the complete source bundle is successfully published.
- Attack publisher preflight, sibling lock/backup handling, two-rename failure,
  rollback, committed cleanup, stale optional removal, and cancellation.
- Verify provider-probe's legacy `--markdown` copy intentionally differs from
  its published `summary.md` appendix exactly as Node does.
- Confirm usage normalization changes only the authoring help block and cannot
  hide unrelated stdout/stderr drift in existing operator differentials.
- Confirm the new help and PATH-tripwire tests prove the six-command surface and
  absence of an executable Node/second-authoring fallback.
