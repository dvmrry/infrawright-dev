# Go Wave 6 Terraform Command Foundation — Builder Review Handoff

## Intent

- Port `node-src/io/terraform-command.ts` at
  `f3a86f2d24dddd4ebf95362d55718a81137800f2` and
  `terraform-show.ts` at `66e9c2d1668b89d772bf6218bbce82172c774a41`
  into a
  shell-free Go package that preserves executable resolution, environment
  snapshotting, output limits, timeouts, process-group cleanup, and structured
  failure behavior.
- Provide the process/show foundation required by later plan and adoption
  slices without wiring a new CLI command in this parcel.
- Preserve Node 24.15 lexical path behavior on every host, including
  `path.win32` candidate expansion used by portability tests.

## Base / Head

- Base: `f36f51bc5d8bf03f64371975747f3360cd014563`.
- Head: shared uncommitted working tree on
  `feature/go-canonjson-foundation`.
- Diff command: `git diff -- go/internal/terraformcmd` plus all untracked files
  under that directory from `git status --short`.

## Files Changed

- `go/internal/terraformcmd/api.go`
- `go/internal/terraformcmd/validation.go`
- `go/internal/terraformcmd/executable.go`
- `go/internal/terraformcmd/unicode_lower.go`
- `go/internal/terraformcmd/platform.go`
- `go/internal/terraformcmd/runner.go`
- `go/internal/terraformcmd/show.go`
- platform-specific access and process-group files under
  `go/internal/terraformcmd/`
- package tests, helpers, and
  `testdata/node24-unicode-lower-oracle.mjs` under the same directory
- This handoff.
- Intentionally untouched: plan lifecycle, assessment, adopt/oracle, import
  staging, Apply, CLI dispatch, provider/backend state, and Terraform binary
  contents.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `docs/go-runtime-plan.md`, the adversarial
  review workflow, and the handover's Block B contract.
- Other source evidence:
  - `node-src/io/terraform-command.ts` and `terraform-show.ts`;
  - their retained Node tests and process fixtures;
  - Node 24.15 `node:path` win32 implementation and live probes;
  - Node 24.15 / V8 13.6 / Unicode 16.0 lowercasing oracle;
  - POSIX process-group and signal behavior on the local Darwin host.

## Generated Artifacts

- Reports, schemas, snapshots, and demo outputs: None.
- Fixture: a runnable Node oracle script included in this uncommitted parcel;
  it hashes every Unicode scalar lowercase mapping plus the three-way class
  controlling Final_Sigma. The script is mode `0644` and is invoked with
  `node`, rather than installed as an executable.
- Artifact drift intentionally expected: None.

## Expected Delta

- Go callers can resolve a trusted Terraform executable, snapshot a closed
  child environment, run bounded shell-free commands, inherit/discard/capture
  output, attempt termination of the isolated process group, reap the direct
  child, wait for pipe-holding group members to terminate, and parse Terraform
  show JSON.
- Windows execution remains rejected exactly as the source requires, while
  Windows lexical resolution is available for deterministic portability tests.
- No existing CLI behavior changes in this parcel.
- No Terraform plan/state bytes or diagnostics may enter a structured process
  failure.

## Invariants Claimed

- Evidence must not be silently dropped: stdout/stderr size violations,
  timeout, spawn, JSON, and UTF-8 failures retain their source-defined fixed
  codes. Ordinary nonzero exit and child signal termination deliberately share
  `TERRAFORM_COMMAND_FAILED`; a host termination signal is re-sent to the host
  process instead of becoming a structured command failure.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: this package owns
  PATH/PATHEXT resolution, omitted-versus-explicit cwd handling, and the closed
  child-environment rules. CLI and `TF` selection belong to later callers and
  are not claimed by this parcel.
- Ambiguity must stay classified instead of being coerced to success:
  untrusted executables, malformed UTF-8/NUL, unsupported platforms, invalid
  limits, unexpected Unicode base tables, malformed UTF-8, and malformed show
  JSON fail closed. Process-group kill and host re-signal failures remain the
  owning Node source's best-effort cleanup limitation rather than a structured
  fail-closed result.
- Provider-readiness counts: N/A.
- Adoption safety invariants:
  - no shell is invoked;
  - only a resolved absolute regular executable is run;
  - the child does not inherit the ambient environment;
  - capture buffers are bounded and cleared on failure;
  - the direct child is reaped; the isolated process group is sent SIGKILL on
    timeout/stream failure and after direct-child exit, and pipe-holding group
    members must terminate before success is returned;
  - group-kill errors are best-effort, descendants that create a new session
    escape the group boundary, and this process does not reap descendants;
  - host-output backpressure cannot postpone timeout or admit queued bytes
    after the abort boundary.

## Tests Run

- Root verification:
  - `go vet ./internal/terraformcmd`
  - `go test -count=1 ./internal/terraformcmd`
  - `go test -race -count=1 ./internal/terraformcmd`
  - `go test -shuffle=on -count=5 ./internal/terraformcmd`
- Fresh-review verification also ran the full package and race suites, the
  focused path/Unicode suite at `-count=100`, and the full shuffled suite at
  `-count=10`.
- Lifecycle/signal/descendant cleanup tests, including success, nonzero exit,
  timeout, stream limits, blocked host writers, queued-output cancellation,
  and cross-run isolation.
- Temporary builder probes replayed 23/23 direct Node command cases and
  compared 720 `path.win32.resolve` combinations plus 465 `extname`
  combinations. These probes were run with Node v24.15.0 against the package
  tests; they are not retained as a durable harness in this parcel. The
  UNC/drive/cwd/PATHEXT regression cases themselves remain in Go tests.
- Unicode oracle: all 1,112,064 Unicode scalar lowercase mappings and the
  Node-derived Final_Sigma class, SHA-256
  `6ad4680180cd0875945037489e01452934e65cf1156ece8ed386829a524d4b9f`.
- Deleted-current-directory, signal resubscription, process-group descendant,
  hostile environment, and output-zeroing tests.
- `go vet`, `gofmt`, `git diff --check`, and test cross-builds for Windows,
  Linux, Plan 9, JS/wasip1, AIX, Darwin, DragonFly, FreeBSD, NetBSD, OpenBSD,
  and Solaris targets.
- Tests not run: live tenant/provider/backend operations and real plan/Apply;
  none is in this package's scope.

## Review Remediation

- The first lifecycle review rejected a cancel-versus-ready `select` whose
  choice was nondeterministic after cancellation. The output pump now uses one
  writer plus a mutex/condition queue; abort and queued-to-in-flight handoff
  linearize on the same mutex. Abort never waits for a writer already admitted
  before that boundary.
- Deterministic hooks prove queued bytes cannot leak after failure or into a
  later command; at most one already-in-flight blocked write per inherited
  stream remains, matching the documented constraint.
- The first resolution review rejected an ad hoc Windows path implementation.
  The replacement directly ports Node's root parsing, tail normalization,
  `resolve`, and `extname`, preserving UNC devices, roots, repeated separators,
  drive-relative behavior, PATHEXT, and omitted-versus-explicit-empty cwd.
- Re-review found a flaky descendant test: shell redirection could create an
  empty PID file before `printf`. The hook now waits for a parsed positive PID
  before arming timeout and still lets command cleanup finish before reporting
  synchronization failure.
- Re-review found single-leading `/` missing from win32 absolute detection and
  Go simple lowercase diverging from ECMAScript for Unicode PATHEXT. Both were
  corrected; lowercase uses a frozen Unicode-16 implementation with an
  exhaustive Node digest and fails closed unless Go's reviewed Unicode-15
  base tables are present.
- Deleted-cwd cleanup now keeps the saved directory descriptor alive through
  restoration and reports restoration/close failures.

## Known Deferrals

- Deferred work: CLI consumers; plan-contract shape validation, including
  `complete === true`; plan lifecycle; adopt/oracle; import staging; exact-plan
  Apply; and performance-report integration.
- Reason it is safe to defer: this parcel exposes only the package foundation;
  it cannot authorize or execute one of those product workflows by itself.
- Follow-up owner or trigger: Blocks C/D/E wire their reviewed domain contracts
  onto this package.

## Review Focus

- Highest-risk paths: process-group watcher, timeout/stream arbitration,
  host-output pump, executable trust/path resolution, environment snapshot,
  Unicode PATHEXT lowering, and strict show parsing.
- Assumptions to attack: queued versus in-flight writes, descendants retaining
  pipes, timeout at spawn boundaries, signal races, deleted cwd, UNC and
  drive-relative resolution, exact ECMAScript full/contextual lowercase,
  response buffer clearing, and JSON/UTF-8 failure precedence.
- Source evidence to verify: owning Node source and live Node oracles; a local
  Go helper is not independent evidence for itself.
- Generated artifacts to compare: execute the included Node Unicode oracle
  under exactly Node 24.15 and compare its corpus identity/digest.
- Silent-overclaim risks: waiting on backpressured host output, running an
  untrusted path, inheriting ambient secrets, returning captured bytes on
  failure, accepting a newer Unicode table, treating malformed JSON as show
  output, claiming descendants are reaped, or claiming best-effort cleanup
  failures become structured errors.
