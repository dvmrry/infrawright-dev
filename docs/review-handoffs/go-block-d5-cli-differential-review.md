# Block D5 CLI and Differential Corpus — Builder Review Handoff

## Intent

- Solve: wire the landed Block D domain packages into the Go CLI for `adopt`,
  `stage-imports`, `unstage-imports`, and exact saved-plan `apply`, including
  Node-compatible parsing, environment/path precedence, lazy Terraform
  construction, legacy Apply usage-code mapping, diagnostics, and exit status.
- Behavior change: the Go binary now exposes the full authorized Block D
  command surface and proves classifications, generated/staged artifact bytes,
  and exit codes against the exact frozen Node bundle.
- Must stay unchanged: `canonjson` and `tfrender` remain the only committed-byte
  renderers; the existing bounded `terraformcmd` path remains in place; no
  credentials, provider/API traffic, host Terraform lookup, or real/live Apply
  is permitted in the corpus. Controlled live Apply remains separately gated.

This is a builder handoff, not approval. The change is ready for a fresh-context
adversarial review and has not been committed or pushed.

## Base / Head

- Base: `02cdab8a3cd111366c1c9c2bf3c78ff457f5924a` on
  `feature/go-canonjson-foundation` (reviewed D4 tip).
- Head: uncommitted working tree atop that base.
- Diff command after staging only the files listed below:
  `git diff --cached -- go/cmd/iw/main.go
  go/cmd/iw/commands_adopt_apply.go
  go/cmd/iw/commands_adopt_apply_test.go
  go/cmd/iw/block_d5_differential_test.go
  go/internal/adopt/adoption_meta.go go/internal/adopt/runner.go
  go/internal/assessment/exact_plan_apply.go
  go/internal/assessment/exact_plan_apply_test.go
  docs/review-handoffs/go-block-d5-cli-differential-review.md`.

## Files Changed

- D5 production:
  - `go/cmd/iw/commands_adopt_apply.go`: 591 new lines.
  - `go/cmd/iw/main.go`: +11/-3 lines for dispatch and slice description.
- D5 tests:
  - `go/cmd/iw/commands_adopt_apply_test.go`: 303 new lines.
  - `go/cmd/iw/block_d5_differential_test.go`: 442 new lines.
- Differential-exposed D3 diagnostic corrections, explicitly reported and
  authorized before editing:
  - `go/internal/adopt/adoption_meta.go`: +69/-2 lines.
  - `go/internal/adopt/runner.go`: +9/-6 lines.
- Differential-exposed D4 operator-visible parity correction, explicitly
  reported and authorized before editing:
  - `go/internal/assessment/exact_plan_apply.go`: +4/-5 lines.
  - `go/internal/assessment/exact_plan_apply_test.go`: +7/-1 lines.
- This builder handoff.
- Files intentionally left untouched: `go.mod`, `go.sum`, `canonjson`,
  `tfrender`, `terraformcmd`, D1 Oracle scratch implementation, D2 staging
  implementation, D3 classification/orchestration behavior, D4 Apply order
  and cleanup, Node source, frozen bundle, committed packs, and committed
  fixtures.

## Source Inputs Consulted

- Provider schemas: only a synthetic minimal provider schema in a temporary D5
  successful-adoption fixture; no committed provider schema changed.
- OpenAPI/API contracts: N/A. D5 performs no provider/API operation.
- Provider source files: N/A.
- Pack metadata: synthetic temporary `sample_resource` registry/adoption
  metadata plus the existing pack/deployment loading contracts.
- Existing docs or design records:
  `docs/review-handoffs/go-block-d-adopt-apply.md`, D1-D4 builder and
  adversarial-review handoffs, `docs/adversarial-review.md`, and the builder
  handoff template.
- Other source evidence:
  - `node-src/cli/main.ts` for the four command parsers, dispatch order,
    environment/path asymmetries, lazy adapters, and Apply-only legacy shim.
  - `node-src/domain/adopt-runner.ts` for exact skipped/unsupported diagnostic
    spelling.
  - `node-src/domain/plan-contract.ts:463-465` for the complete-gate message.
  - Landed Go D1-D4 public APIs in `internal/adopt` and
    `internal/assessment`.
  - Frozen `dist/infrawright-cli.mjs`, hard-pinned at SHA-256
    `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`.

## Generated Artifacts

- Reports: none committed.
- Schemas: none committed; one temporary synthetic schema is created under
  `t.TempDir`.
- Fixtures: none committed; the D5 corpus builds all pack, deployment, plan,
  input, Terraform-fake, and state fixtures under `t.TempDir`.
- Snapshots: none committed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. The successful accepted-plan
  adoption compares the complete relevant `config/` and `imports/` subtrees
  against frozen Node, including `sample_resource.auto.tfvars.json` and
  `sample_resource_imports.tf`; missing, changed, or extra files fail. Ordinary
  and state-aware staging separately compare copied import/move bytes against
  Node.

## Expected Delta

- Expected behavior change:
  - `adopt`, `stage-imports`, `unstage-imports`, and `apply` are dispatched by
    the Go CLI instead of the pre-port “not yet ported” guard.
  - pack/deployment defaults retain per-command `||` environment semantics;
    explicit CLI values win.
  - Terraform resolution is lazy for adoption loaders, state-aware staging,
    and exact Apply; ordinary staging and unstage never resolve Terraform.
  - Apply resolves backend, policy, and deployment controls against the
    absolute workspace, uses D4 branch detection, and alone routes through the
    legacy plan-lifecycle usage shim.
  - D3 diagnostic bytes now match the reachable frozen scalar vectors:
    `<`, `>`, and `&` are not HTML-escaped on plain-text stderr; U+2028/U+2029
    remain literal UTF-8; lossless numeric name/id values use
    `{"isLosslessNumber":true,"value":"<lexeme>"}`; and an absent skipped-item
    name/id spells `undefined` rather than Go `null`.
  - D4's independent terraform-json typed Complete gate remains before raw
    classification but now returns Node's plain
    `plan must be complete before assessment` error instead of an invented
    ProcessFailure envelope.
- Expected report/count/coverage changes: no report/count semantic change.
  Adoption classification/count diagnostics are byte-identical to Node on the
  D5 string, HTML-sensitive, U+2028/U+2029, lossless-number, and absent-label
  vectors. This does not claim byte parity for arbitrary object-valued labels.
- Expected generated-output changes: the Go CLI can now publish the existing
  D3 renderer output. Output bytes themselves do not change.
- Expected no-op areas: provider transport/auth, raw evidence, plan/apply
  safety order, temporary cleanup, renderer internals, dependencies, and every
  pre-D5 command.

## Invariants Claimed

- Evidence must not be silently dropped: no evidence path changes; successful
  adoption still requires exact Oracle state coverage before artifact publish.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: pack/user adoption policy
  remains the reviewed D3 implementation; D5 only loads and passes it.
- Ambiguity must stay classified instead of being coerced to success:
  unsupported adoption exits 1 with exact counts and never constructs an
  Oracle Terraform loader; malformed/unknown CLI input remains fail-closed.
- Provider-readiness counts must stay explainable: the frozen differential
  checks the complete skipped/unsupported/eligible/published/failed diagnostic
  bytes on its bounded scalar corpus.
- Adoption safety invariants:
  - the frozen bundle digest mismatch or absence is a hard test failure, never
    a skip;
  - accepted-plan adoption uses an explicit temporary fake executable and
    `INFRAWRIGHT_ORACLE_STATE_SOURCE=accepted-plan`; its fake exits 97 if an
    Apply command is attempted, and both Node and Go logs prove none occurred;
  - state-aware staging uses only an explicit temporary fake and compares its
    init/state-list argv with Node;
  - incomplete saved-plan Apply uses ephemeral local saved-plan state, compares
    Node/Go stderr, exit, and init/show transcript with both
    `--allow-destroy` and `--allow-plan-changes`, and proves `arg=apply` is
    absent;
  - the typed Complete gate remains before classification and Apply;
  - unit tests invoke only injected fake interfaces, including the method named
    `Apply`; no test resolves or runs host Terraform for Apply;
  - no credential variable, real API host, tenant, provider, network request,
    or real/live infrastructure Apply is present or reachable in this parcel.

## Dependency Use

- `go.mod` and `go.sum` have zero D5 delta.
- The current reviewed Block D dependency set remains
  `github.com/hashicorp/terraform-json v0.28.0` plus its existing minimal
  transitive set (`go-textseg/v15`, `go-version`, `go-cty`, `x/text`). D5
  consumes it only indirectly through the landed D1/D4 typed structs.
- D5 adds no speculative `terraform-exec`, `jsonschema`, or Zscaler SDK import.
  The reviewed D1 decision retained the bounded existing `terraformcmd` path;
  D5 does not reopen the future unified-invocation decision.
- `canonjson` and `tfrender` remain hand-rolled and unchanged for every
  committed artifact byte.

## Tests Run

- Commands:
  - `gofmt -d .`
  - `go vet ./...`
  - `go test ./... -count=1`
  - `go test -race ./internal/adopt ./internal/assessment -count=1`
  - `go test -race ./cmd/iw -count=1`
  - `go test ./internal/assessment -run
    'TestApplyExactSavedPlansCleanFlowAndCompleteGate' -count=1`
  - `go test ./cmd/iw -run
    'Test(AdoptCommand|ImportStagingCommands|ApplyCommand|BlockDCommandUsageContract)'
    -count=1`
  - `go test ./internal/adopt -run 'Test(Adoption|RunAdoptBatch)'
    -count=1`
  - `go test ./cmd/iw -run
    'RootCatalog|Transform|Topology|Generation' -count=1`
  - `go test ./cmd/iw -run 'TestBlockD5' -count=1`
- Relevant output summary:
  - gofmt: clean, no output.
  - vet: clean, no output.
  - full module: all packages green; final `cmd/iw` 36.651s,
    `internal/adopt` 5.860s, `internal/assessment` 17.300s.
  - race: `internal/adopt` 6.642s, `internal/assessment` 23.039s, isolated
    `cmd/iw` 31.727s, all green.
  - four standing byte gates: green, 12.006s.
  - post-review focused CLI units: green, 0.258s; focused adoption units:
    green, 0.233s.
  - final D5 frozen-oracle corpus: green, 6.101s. It covers seven dispatch/exit
    cases, unsupported adoption classification, successful accepted-plan
    generated artifact bytes, ordinary and state-aware staging artifact bytes
    and argv, unstage removal/exit, incomplete Apply classification and
    transcript, and the no-live-surface source guard.
- Test-run note: one initial attempt parallelized full `cmd/iw` and race
  `cmd/iw`; two older differential tests raced on their shared
  `dist/iw-go-diff` filename, producing a transient “no such file” failure.
  After all parallel commands stopped, the required isolated `cmd/iw` race
  rerun passed. D5's own candidate uses a unique filename and did not cause a
  semantic or race-detector failure.
- Tests not run and why: no real provider/API call, credentials, real Terraform
  Apply, or controlled live-tenant Apply was run; those are explicitly outside
  this authorization. No Node source test was changed.

## Known Deferrals

- Allowed divergence: an arbitrary object-valued adoption diagnostic label can
  retain Go's sorted `map[string]any` property order rather than the original
  JavaScript insertion order. Exact insertion order is already unrecoverable
  once the Go lossless decoder materializes an object as a map. This is bounded
  to plain stderr label spelling: the label serializer does not feed
  classification, exit status, generated artifacts, evidence, reports, or
  automation decisions. D5 therefore ports the reachable scalar/LosslessNumber
  vectors and deliberately does not build an ordered-map parser/runtime
  emulation for this inert diagnostic-only case.

- Deferred work: controlled Apply qualification against real/live provider
  state.
- Reason it is safe to defer: it remains a separate explicit human-gated event;
  all current tests fail closed on incomplete plans and use only fake/local
  ephemeral state.
- Follow-up owner or trigger: user authorization after adversarial review and
  separate controlled-apply planning.
- Deferred work: any future unification of `terraformcmd` with another
  invocation library.
- Reason it is safe to defer: the current bounded path is reviewed, working,
  and byte/behavior qualified; D5 needed no replacement.
- Follow-up owner or trigger: a separately reviewed dependency/ROI decision.

## Review Focus

- Highest-risk files or paths:
  - `go/cmd/iw/commands_adopt_apply.go` lazy adapter state and path/env
    precedence.
  - `go/cmd/iw/block_d5_differential_test.go` frozen digest enforcement,
    accepted-plan fake, artifact-tree comparison, and incomplete Apply proof.
  - `go/internal/assessment/exact_plan_apply.go` typed-gate error parity.
  - `go/internal/adopt/adoption_meta.go` JSON diagnostic spelling.
- Specific assumptions to attack:
  - no path reaches resolver construction before the domain operation actually
    needs Terraform;
  - Apply's `LoadInputs` remains lazy and branch refusal precedes it;
  - accepted-plan extraction cannot reach the fake's Apply branch;
  - `complete:false` cannot reach fake Apply even with broad allow flags;
  - absent versus explicitly-null skipped labels, literal U+2028/U+2029, and
    numeric name/id values preserve the bounded JSON.stringify semantics;
  - no claim accidentally expands to arbitrary object property-order parity;
  - the hard-pinned oracle cannot silently skip on missing Node/bundle.
- Source evidence the reviewer should verify: the four Node CLI functions and
  dispatch block, `plan-contract.ts:463-465`, D3 diagnostic source lines, and
  the exact frozen bundle digest.
- Generated artifacts the reviewer should compare: accepted-plan adoption
  tfvars/import bytes and ordinary/state-aware staged import/move bytes.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  empty-but-set pack/TF environment variables; duplicate/repeated selectors;
  missing name/id versus explicit null; HTML-sensitive diagnostic values;
  missing/false typed Complete; branch refusal before inputs; backend/policy
  relative path resolution; state-aware adapter use when no source artifact is
  present; and accidental live/host Apply reachability.
