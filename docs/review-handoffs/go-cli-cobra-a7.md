# Go CLI A7: Cobra migration contract

Status: APPROVED AFTER FRESH ADVERSARIAL REVIEW on top of the pushed A6
review point `a8673c6`. The candidate remains unpushed.

## Intent

Replace the byte-parity-era `internal/cliargs` reimplementation and static
`usage.txt`/dispatch switch with one typed Cobra command tree. This is an
orchestration-layer correction: use a validated library rather than preserve
Node `parseArgs` idiosyncrasies that do not protect infrastructure or evidence.

## Compatibility boundary

Must remain stable:

- command names and all documented valid invocation shapes;
- environment read/default/precedence behavior for each command;
- successful stdout report, evidence, and committed artifact bytes;
- exit-code classifications, including usage status 2 and the
  `check-pack-set` status-3 skip contract;
- all fail-closed assessment, saved-plan freshness, exact-Apply, branch, and
  `ALLOW_*` gates;
- no credential, live-provider, live-Terraform, or infrastructure execution in
  the migration tests.

Intentional Cobra-native changes:

- generated root and per-command help text;
- parse-error wording and suggestions;
- standard `--flag=value` and `--` handling;
- `-h`/`--help` at every command boundary;
- deterministic shell-completion and generated Markdown reference surfaces.

No automation may parse help/error prose after A7. Machine-consumed command
data remains on stdout and diagnostics remain on stderr.

## Parcels

1. Establish the live Cobra root and complete command vocabulary. A temporary
   `DisableFlagParsing` bridge may delegate to existing handlers only while
   typed family migration is in progress.
2. Port metadata, fetch/transform, and topology/module commands to typed Cobra
   flags and arguments.
3. Port adopt, plan, assessment, staging, and exact Apply. Re-prove every
   fail-closed gate and exit classification.
4. Port the six authoring commands, including repeatable source/SDK inputs and
   qualified/unverified exclusivity.
5. Delete `internal/cliargs`, `usage.txt`, the static dispatch helpers, and every
   `DisableFlagParsing` use. Generate and drift-gate the Markdown CLI reference.

## Acceptance gates

- `rg 'DisableFlagParsing|internal/cliargs' go/cmd/iw go/internal` returns no
  production-code matches.
- `gofmt`, `go vet`, `go mod tidy -diff`, full Go tests, and relevant race tests
  pass.
- Successful frozen-Node differentials and every artifact/report byte gate
  remain green. Help and parser-error byte cases are replaced by explicit
  Cobra contract tests rather than normalized away.
- Tests prove usage status 2, status-3 skip, stdout/stderr separation,
  environment precedence, no-live-test reachability, complete-gate rejection,
  freshness/TOCTOU rejection, and exact-Apply gating.
- A fresh read-only adversarial review approves the completed A7 change before
  the coordinator commits it.

The final Node freeze/tag and Go product-authority declaration remain separate
and still wait for the user's Opus, GPT-5.6 Pro, and Fable review sequence.

## Builder review handoff

### Base / head

- Base: `a8673c6cd1d0f24be4e3dc9704f173f9e1740391`
- Head: the current uncommitted worktree on
  `feature/go-canonjson-foundation`
- Diff: `git diff a8673c6 --`

### Files changed

- Added the Cobra root, tests, and generated-doc drift gate in
  `go/cmd/iw/cobra*.go`.
- Converted every command adapter in `go/cmd/iw/commands_*.go` and the root
  catalog/transform adapters in `main.go` to typed Cobra input.
- Removed `go/internal/cliargs/**` and `go/cmd/iw/usage.txt`.
- Reclassified frozen-Node differential cases so only CLI presentation/parser
  text left the byte gate; domain outputs, artifacts, reports, and exit
  classifications remain compared.
- Added `docs/cli-reference.md` and updated the v2 dependency/compatibility
  record.
- Added Cobra `v1.10.2`; pflag and mousetrap are its only new `go.mod`
  requirements.

No pack, provider schema, OpenAPI input, source-analysis kernel, artifact
renderer, plan/apply kernel, deployment metadata, or generated infrastructure
fixture was changed.

### Source inputs consulted

- `node-src/cli/main.ts` and `node-src/authoring/cli.ts` for command names,
  valid options, repeatability, environment precedence, and exit contracts.
- The removed `internal/cliargs` implementation/tests for the behaviors being
  intentionally retired.
- `docs/go-runtime-v2.md`, the A6 coordinator record, and the Block C/D/E
  differential corpora for the compatibility and safety boundary.
- Cobra `v1.10.2`/pflag behavior for help, completion, `--flag=value`, `--`,
  bool values, and parse errors.

### Generated artifacts

- `docs/cli-reference.md` is deterministically generated from the live command
  tree. `TestCLIReferenceCurrent` fails on drift and documents the explicit
  update command.
- No infrastructure artifact, evidence report, provider-readiness output,
  schema, pack fixture, or Terraform golden changed.

### Expected delta

- CLI help, completion, suggestions, parser-error prose, `--flag=value`, `--`,
  and help placement are Cobra-native.
- `check-pack` now enforces the documented exclusive selector shape instead of
  Node encounter-order mixing; `deployment` rejects ignored extra positionals.
- Every valid command shape, environment/default rule, stdout data/report
  bytes, artifact bytes, and exit classification remains stable.
- All complete/freshness/TOCTOU, branch, policy, and exact-Apply gates are
  unchanged below the parser boundary.

### Invariants claimed

- Cobra passes exact repeated values to domain adapters; empty strings are
  rejected as usage errors except for the five source-declared tenant/path
  allowances, where omission remains distinguishable from an explicit empty.
- Authoring source options retain the exact repeatability split for API,
  provider-file, SDK-file, and SDK-root inputs; qualified/unverified
  exclusivity remains domain-validated.
- `check-pack-set` status 3 crosses Cobra without being rendered as an error.
- ProcessFailure-to-usage classification remains on every plan/adopt/apply
  lifecycle path.
- Help never runs command dependencies or the Terraform platform gate.
- No test or generated-doc path can reach credentials, a real provider, a live
  backend, or real Terraform Apply.

### Tests run

- `gofmt` and `git diff --check`: clean.
- `go vet ./...`: clean.
- `go mod tidy -diff`: clean.
- `go test -count=1 ./...`: green, including frozen-Node domain/report/artifact
  differentials.
- `go test -race -count=1 ./internal/adopt ./internal/assessment ./cmd/iw`:
  green.
- `go test -count=1 ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'`:
  green; the four standing artifact byte gates remain byte-identical.
- `rg 'DisableFlagParsing|internal/cliargs' go/cmd/iw go/internal`: no
  production matches.

No live credentials, provider API, Kubernetes cluster, remote backend, or real
Terraform Apply was used or reachable.

### Known deferrals

- The Make/operator default still points at the Node bundle until the separate
  authority-handoff and cutover roadmap gates are approved.
- The Node freeze/tag and formal Go product-authority declaration remain after
  the user's planned Opus, GPT-5.6 Pro, and Fable reviews.
- Singleton-state topology work remains paused behind that handoff.

### Review focus

1. Reconstruct every command/flag/repeatability mapping from the Node source;
   do not trust the generated help as its own evidence.
2. Attack `exactStringValues`, duplicate handling, empty values, bool false,
   `--`, and flags after positionals for silent option loss or remapping.
3. Verify the removed differential cases are presentation-only and that no
   artifact/report/domain classification comparison was weakened.
4. Trace status 3/4/1 outcomes and ProcessFailure usage mapping through
   `cobraCommandStatus` and the top-level renderer.
5. Verify help/completion cannot run dependency reads, Terraform preflight, or
   any lifecycle action.
6. Re-run the safety-focused exact-Apply, complete-field, freshness/TOCTOU, and
   authoring qualified/unverified tests from the diff rather than relying on
   this summary.

### First-review findings and remediation

The first fresh read-only review requested changes for two issues. Both are
remediated in the candidate and require a changed-surface recheck:

1. The initial adapter accepted empty strings for every value flag. The typed
   spec now rejects empties before its domain callback unless that command
   explicitly declares the source's allowed-empty policy. An exhaustive Cobra
   tree test permits only `--tenant` on roots/plan-roots/clean-plans/plan/the
   two assessments/apply and `--path` on scope-paths. A focused regression
   proves `TF=/fake` plus `plan --terraform=` exits as usage without reaching
   plan dependencies.
2. The initial root scanned raw argv separately to decide whether help skipped
   Terraform platform preflight. The raw scanner is deleted. Cobra's resolved
   command and effective parsed bool values now drive a root persistent
   pre-run gate; Cobra help and parse failures never reach it. Regressions
   cover help consumed as a value, help after `--`, unknown-before-help,
   repeated help true/false, both repeated `--state-aware` orders, and the
   source-required `gen-env` and `modules generate` gates.

The same read-only reviewer rechecked only those remediations and returned
**APPROVE** with no remaining blocking findings or non-blocking risks. Focused
remediation/help/completion tests and `git diff --check` were independently
green in that recheck; the coordinator's full post-remediation gates are
recorded above.
