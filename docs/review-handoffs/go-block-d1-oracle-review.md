# Block D1 Oracle Foundation — Builder Review Handoff

## Intent

- Implement the Go scratch import Oracle and generated-configuration policy
  substrate specified by `import-oracle.ts` and
  `generated-config-policy.ts` before adoption orchestration is wired.
- Use the existing bounded `internal/terraformcmd` process boundary for every
  Terraform phase and `terraform-json` only for typed plan/state decoding.
- Preserve exact request argv, import-only classifications, complete-field
  fail-closed behavior, lossless provider evidence, deterministic scratch HCL,
  generated-config policy order, and operator-visible refusal text.
- Keep all tests credential-free and incapable of forwarding to host
  Terraform. No real provider, API, state, tenant, or Apply is in scope.

## Base / Head

- Base: `b6f6e66ed59dee6e54e21864664cab4d0febe531`
- Head: uncommitted D1 working tree on
  `feature/go-canonjson-foundation`; no commit or push was made.
- Diff command after staging for review:
  `git diff --cached -- go/go.mod go/go.sum go/internal/adopt docs/review-handoffs/go-block-d1-oracle-review.md`

## Files Changed

- Files:
  - `go/go.mod`, `go/go.sum`
  - `go/internal/adopt/doc.go`
  - `go/internal/adopt/oracle.go`
  - `go/internal/adopt/oracle_runner.go`
  - `go/internal/adopt/oracle_transaction.go`
  - `go/internal/adopt/oracle_validate.go`
  - `go/internal/adopt/generated_config_policy.go`
  - `go/internal/adopt/generated_config_schema.go`
  - `go/internal/adopt/oracle_test.go`
  - `go/internal/adopt/oracle_runner_test.go`
  - `go/internal/adopt/oracle_fixture_test.go`
  - `go/internal/adopt/generated_config_policy_test.go`
  - this handoff
- Files intentionally left untouched: CLI wiring, D2 staging, D3 adoption
  runner/state projection, D4 exact-plan Apply, D5 differential harness,
  existing `terraformcmd`, `canonjson`, and `tfrender` implementations.
- Go LOC before this handoff: 2,205 production lines and 917
  test lines. `go.mod` adds one direct dependency; `go.sum` has 14 lines.

## Source Inputs Consulted

- Provider schemas: the existing `internal/metadata` Terraform provider-schema
  API; tests use a minimal local schema and the committed core fixture.
- OpenAPI/API contracts: None.
- Provider source files: None.
- Pack metadata: loaded provider source/pin, resource override
  `drop_if_default`, and optional `oracle/<provider>.tf` configuration.
- Existing docs or design records:
  - `docs/go-runtime-v2.md`
  - `docs/review-handoffs/go-block-d-adopt-apply.md`
  - `docs/adversarial-review.md`
- Other source evidence:
  - `node-src/domain/import-oracle.ts`
  - `node-src/domain/generated-config-policy.ts`
  - the schema/fill functions in `node-src/domain/state-project.ts`
  - `node-tests/import-oracle.test.ts`
  - `node-tests/generated-config-policy.test.ts`
  - `node-tests/fixtures/terraform-import-structure-v1.15.4.json`
  - the already-qualified `go/internal/terraformcmd` process and trust tests.

## Generated Artifacts

- Reports: None.
- Schemas: None generated.
- Fixtures: no new fixture bytes; the Go tests consume the already committed
  Terraform 1.15.4 plan/state structural fixture and construct focused inline
  refusal vectors.
- Snapshots: None changed.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None. `canonjson` and `tfrender` stay
  unchanged and remain the only committed-artifact renderers.

## Expected Delta

- Expected behavior change: Go gains a fake-injectable provider import Oracle,
  exact plan/state validation and extraction, a bounded production command
  adapter, and generated-config fill/omit policy rewriting. No CLI reaches D1
  yet.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: only ephemeral Oracle scratch
  configuration may be rewritten. No committed artifact changes are expected.
- Expected no-op areas: fetch, transform, root catalog, topology, generation,
  assessment, Block C, staging, CLI dispatch, and live-provider behavior.

## Invariants Claimed

- Evidence must not be silently dropped: every plan/state document is decoded
  both into `terraform-json` typed structs and a canonjson lossless raw
  sidecar. Exact address coverage, values, sensitivity masks, and numeric
  lexemes are retained.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: generated policy loads the
  provider schema, applies fills first, then pack defaults, projection omits,
  and conditional omits in source order.
- Ambiguity must stay classified instead of being coerced to success: malformed,
  incomplete, errored, non-applyable, create/update/replace/destroy, drifted,
  deferred, output-changing, wrong-provider, wrong-ID, duplicate, or
  coverage-incomplete plans fail closed.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - `tfjson.Plan.Complete != nil && *Complete` and the raw literal
    `complete === true` are both mandatory before the scratch Apply phase.
  - Accepted-plan mode cannot request `apply-imports` or `show-state`.
  - Every production phase uses `terraformcmd.RunTerraformCommand` with the
    exact request argv, complete explicit environment, timeout, 8-MiB stdout
    cap, 1-MiB stderr cap, trusted absolute executable check, supported-platform
    gate, and process-group containment.
  - Every subprocess failure is immediately mapped to a fixed redacted
    `ProcessFailure`; raw provider diagnostics and import IDs never escape.
  - Oracle roots reject backend and Terraform Cloud configuration.
  - All transaction tests inject an in-memory fake. Production-runner tests use
    temporary non-forwarding shell fixtures only; they cannot locate or invoke
    host Terraform.

## Tests Run

- Commands run on the D1 working tree:
  - `GOWORK=off go test -count=1 ./internal/adopt`
  - `GOWORK=off go test -race -count=1 ./internal/adopt`
  - `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh ./internal/adopt`
  - `bash /Users/dm/.codex/skills/go-code-review/scripts/pre-review.sh --force ./internal/adopt`
  - `GOWORK=off go mod tidy -diff`
  - `GOWORK=off go mod verify`
- Relevant focused coverage:
  - committed Terraform 1.15.4 plan/state typed decode and exact extraction;
  - exact applied-state and accepted-plan fake transcripts;
  - missing/false complete rejection before Apply;
  - create/update/replace/destroy, drift, deferred, diagnostics, output,
    coverage, provider, and import-ID refusals;
  - multi-resource plan and state coverage;
  - generated-plan failure followed by exactly one corrected plan;
  - projection fill followed by omit-if, pack-default precedence, required
    omission refusal, compound expressions, and entry accounting;
  - cleanup and keep-workdir warning;
  - actual bounded runner argv/environment/output forwarding, relative path,
    symlink/non-executable rejection, stderr overflow redaction, timeout, and
    descendant process containment.
- Tests not run by this builder after the runner correction: the coordinator
  owns the final full-module and four standing artifact-byte gates before
  commit. No real Terraform transaction, provider API call, credentials, or
  live Apply was run or made reachable.

## Known Deferrals

- Deferred work: D2 staging, D3 adoption orchestration/full state projection,
  D4 exact-plan Apply, and D5 CLI/differential corpus.
- Reason it is safe to defer: no command dispatch or later parcel calls D1 yet.
- Follow-up owner or trigger: only after fresh adversarial D1 approval and a
  coordinator-reviewed commit.
- The first D1 draft used `terraform-exec`. Review rejected it because it
  reconstructs argv, internally buffers stderr without the existing bound, and
  weakens the qualified Darwin/process-group contract. It is now completely
  removed from source, `go.mod`, and `go.sum`; the sunk bounded
  `terraformcmd` path is reused for every phase.
- The only direct dependency is `github.com/hashicorp/terraform-json v0.28.0`.
  Its minimal indirect set is `go-textseg/v15 v15.0.0`, `go-version v1.9.0`,
  `go-cty v1.18.1`, and `x/text v0.31.0`. `jsonschema/v6` and
  `zscaler-sdk-go/v3` have no honest D1 consumer and were not padded into the
  module.
- D1 intentionally retains Node's recursive cleanup for the Oracle's arbitrary
  Terraform work tree. The descriptor-bound randomized cleanup mandated for
  D4 applies to its closed saved-plan snapshot; safely deleting an arbitrary
  plugin/cache tree is a different problem and is not claimed here.

## Review Focus

- Highest-risk files or paths: `oracle_validate.go`,
  `oracle_transaction.go`, `oracle_runner.go`, and
  `generated_config_policy.go`.
- Specific assumptions to attack:
  - every route to scratch Apply crosses the typed and raw complete gates;
  - accepted-plan comparison uses exact change/planned/prior values and masks;
  - request argv is forwarded without library-added or reordered flags;
  - every command failure is bounded and redacted while retaining its code;
  - fill precedes omit and policy match accounting is source-faithful;
  - empty transactions return before reading Oracle environment variables.
- Source evidence the reviewer should verify: exact condition order and error
  strings in the two TypeScript files and their tests; `terraformcmd` trust,
  timeout, output-bound, and process-group contracts.
- Generated artifacts the reviewer should compare: Oracle root/import HCL,
  generated-config rewrite bytes, and the standing RootCatalog/Transform/
  Topology/Generation gates run by the coordinator.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  missing/nonliteral complete; diagnostics/errors/checks/drift/deferred/action
  invocation collections; duplicate/wrong addresses; unknown values; sensitivity
  mask shape; wide JSON numbers; generated HCL heredocs/compound values;
  required or sensitive fill/omit targets; corrected-plan retry count;
  cleanup after primary failure; and kept-workdir disclosure warnings.
