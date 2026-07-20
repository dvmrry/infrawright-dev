# Block C3 Control-Evidence Review Handoff

## Intent

- Port `node-src/domain/control-evidence.ts` into a neutral Go package that binds required and optional assessment control files to stable content and, when requested, filesystem identity.
- Preserve caller-order serial rechecks and exact redacted mutation failures without exposing sensitive contents or paths.
- Keep plan evidence, assessment/report generation, lifecycle, CLI, existing artifacts, and Node behavior unchanged.

## Base / Head

- Base: `98682a739af92011beb71ddd54872aac23860e3f`
- Head: uncommitted working-tree parcel; review only `go/internal/controlevidence/**` against the base.
- Diff command: `git diff --no-index /dev/null go/internal/controlevidence` (or inspect every file under that new directory).

## Files Changed

- Files: all files under `go/internal/controlevidence/**`.
- Files intentionally left untouched: `go/internal/artifacts/**`; `go/internal/plan/**`; `go/internal/assessment/**`; CLI; Node oracle sources; reports; schemas; fixtures.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/control-evidence.ts`; its direct Node tests and assessment call sites; existing Go `artifacts` stable-read and budget substrate; `procerr`.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains bound required/optional control-text reads, defensive control-file copying, and serial mutation rechecks.
- Expected report/count/coverage changes: None yet; assessment/report orchestration is outside this parcel.
- Expected generated-output changes: None.
- Expected no-op areas: Node behavior; existing Go packages and artifact bytes; plan evidence/lifecycle; fetch/transport; CLI.
- Intentional Go strengthening: after the source-equivalent first stable hash, Go performs a second independently bounded stable hash to close a same-inode post-hash mutation window present in Node. Caller-visible logical budget charging remains one 64-MiB set, while physical recheck I/O is fixed-bounded at up to 128 MiB for a maximum-size set.

## Invariants Claimed

- Evidence must not be silently dropped: every bound input is rechecked in caller order; missing optional inputs are bound as absence and later appearance rejects.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: caller-provided path/digest/identity/follow-symlink options are defensively copied and preserved.
- Ambiguity must stay classified instead of being coerced to success: identity mismatch, content mutation, symlink swap, absence appearance, TOCTOU, and unsupported platform fail closed.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: recheck failures collapse to fixed `ASSESSMENT_CONTROL_CHANGED` diagnostics without leaking secret text or raw underlying errors.

## Tests Run

- Commands: focused tests; focused tests repeated 20 times; focused `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`.
- Relevant output summary: all passed; zero third-party Go dependencies.
- Tests not run and why: no real provider/API test applies; this is credential-free stable-file evidence.

## Known Deferrals

- Deferred work: assessment orchestration and report/CLI consumption.
- Reason it is safe to defer: no current production Go path consumes this package; this parcel only establishes the evidence primitive.
- Follow-up owner or trigger: C3 assessment parcel after adversarial acceptance; its integration tests must validate deadline/latency behavior for an exact 64-MiB logical control set under the intentional two-pass recheck.

## Review Focus

- Highest-risk files or paths: optional absence binding; identity extraction; follow/no-follow handling; serial recheck and redaction.
- Specific assumptions to attack: ENOENT-only absence; BOM stripping; exact budget limits; same-byte inode replacement behavior with and without identity; symlink final-component behavior; input-copy isolation; unsupported-platform behavior.
- Source evidence the reviewer should verify: `node-src/domain/control-evidence.ts` and direct tests, rather than this handoff.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: treating permission/read errors as absence; accepting an appeared optional control; retaining mutable caller slices/pointers; leaking raw path/error text; parallelizing rechecks and changing failure precedence.
