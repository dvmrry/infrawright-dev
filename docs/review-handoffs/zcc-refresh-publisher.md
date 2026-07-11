# Builder Review Handoff: ZCC Refresh Publisher

## Intent

- Solve the missing write seam between a ready two-phase ZCC refresh parity
  assertion and canonical refresh artifacts.
- Add one machine-only, assertion-bound refresh mode to
  `materialize_pull_artifacts` that replaces or verifies exact payloads in the
  fixed `lookup -> moves -> tfvars -> imports` order.
- Make crashes retry-forward with a durable, content-free pending-transition
  fence and an exhaustive classifier for baseline/desired prefixes.
- Preserve the existing bootstrap materializer, read-only refresh compiler,
  parity contracts, Python byte oracle, and process behavior outside the new
  refresh request branch.

## Base / Head

- Base: `5f539dfff4487aef4f8826e2381ff43239387f94`
- Head: the checked-out commit on `feature/node-zcc-refresh-publisher`; resolve
  it with `git rev-parse HEAD` so the locally checkpointed review head remains
  unambiguous without embedding a self-referential commit hash in this file.
- Diff command: `git diff 5f539dfff4487aef4f8826e2381ff43239387f94...HEAD`

## Files Changed

- `docs/node-process-api.md`
- `docs/schemas/process-request.schema.json`
- `docs/schemas/process-response.schema.json`
- `docs/schemas/zcc-pull-refresh-materialization.schema.json`
- `docs/schemas/zcc-pull-refresh-pending-transition.schema.json`
- `node-src/contracts/validators.ts`
- `node-src/contracts/zcc-pull-refresh-materialization-semantics.ts`
- `node-src/domain/zcc-pull-operation.ts`
- `node-src/domain/zcc-pull-refresh-materialization.ts`
- `node-src/domain/zcc-pull-refresh-parity.ts`
- `node-src/domain/zcc-pull-refresh-publisher-operation.ts`
- `node-src/domain/zcc-pull-refresh-transition.ts`
- `node-src/process/execute.ts`
- `node-src/process/types.ts`
- `node-tests/zcc-pull-refresh-materialization-semantics.test.ts`
- `node-tests/zcc-pull-refresh-materializer.test.ts`
- `node-tests/zcc-pull-refresh-transition.test.ts`
- `docs/review-handoffs/zcc-refresh-publisher.md`
- Files intentionally left untouched: the Python engine and transform writer,
  bootstrap materializer/contracts, pack files, and downstream pipeline code.

## Source Inputs Consulted

- Provider schemas: N/A; this slice consumes already-compiled ZCC transform
  evidence and does not change provider schema interpretation.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: the embedded exact ZCC transform catalog and all-Zscaler root
  catalog through the existing compiler/parity path; neither catalog changes.
- Existing docs or design records: `docs/adversarial-review.md`,
  `docs/review-handoff-template.md`, `docs/node-process-api.md`, existing
  bootstrap materialization contracts/tests, read-only refresh compiler,
  refresh parity seed/assertion schemas, and fingerprint contracts.
- Other source evidence: actual `python3 -m engine.transform` output for all
  five supported ZCC resources is the byte oracle in the focused tests.

## Generated Artifacts

- Reports: `infrawright.zcc_pull_refresh_materialization` v1, content-free.
- Schemas: refresh materialization result and pending-transition marker, plus
  process request/response union additions.
- Fixtures: no committed static fixture; tests construct isolated Python/Node
  twins and compare their actual bytes.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: none outside a successfully asserted
  refresh publication; no generated repository artifact changes.

## Expected Delta

- Expected behavior change: a ready `infrawright.zcc_pull_refresh_parity` v1
  assertion can authorize the new
  `replace_or_verify_exact_imports_last` refresh publication policy.
- Expected report/count/coverage changes: one additional process request/result
  branch, two standalone v1 schemas, semantic validators, an exhaustive
  classifier oracle, five-resource Python-byte publication coverage, and crash
  recovery/race coverage.
- Expected generated-output changes: no-move refreshes finish with exact Python
  bytes and no marker; safe moves finish with the exact move file plus an exact
  content-free marker and `awaiting_apply` receipt.
- Expected no-op areas: bootstrap publication, compilation/parity output bytes,
  assessment, root/scoping, and all non-ZCC paths.

## Invariants Claimed

- Evidence must not be silently dropped: publication rechecks the ready parity
  assertion, candidate request binding, raw/control inputs, baseline
  fingerprint, transition fingerprint, neutral evidence, desired descriptors,
  move derivation when old imports remain available, and seven final states.
- Generic matcher evidence must not outrank source-backed evidence: N/A; the
  operation accepts only the existing exact ZCC compiler/parity profile.
- Source precedence/provenance must remain explicit: the protected parity
  assertion is authority evidence; `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` is the
  host-only write authority; request/deployment data cannot grant write scope.
- Ambiguity must stay classified instead of being coerced to success: foreign
  marker/payload, changed common role, non-prefix state, markerless advanced
  move transition, and reserved artifacts fail closed without overwrite.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - Effective payload order is `lookup`, `moves`, `tfvars`, `imports`, with
    imports last even when its baseline equals desired and is only verified.
  - The only accepted in-flight vector is a desired prefix followed by a
    baseline suffix after equality-collapsed roles are removed.
  - An exact marker is durable before any changed payload becomes visible.
  - Absent-to-present uses no-overwrite link; present-to-present uses same-parent
    rename under the documented serialized trusted-runner model.
  - Every staged inode and byte sequence is rebound immediately before atomic
    publication; hook-mutated/replaced aliases cannot publish foreign bytes.
  - Marker replacement immediately before removal is detected and not unlinked.
  - No-move completion removes only the exact marker; safe moves retain marker
    and move bytes until future apply evidence is explicitly acknowledged.
  - Final verification reads imports last and emits no paths, contents, import
    IDs, move keys, staging names, or output-root data.

## Tests Run

- Commands:
  - `npm run build:test`
  - `node --test .node-test/node-tests/zcc-pull-refresh-transition.test.js .node-test/node-tests/zcc-pull-refresh-materialization-semantics.test.js .node-test/node-tests/zcc-pull-refresh-materializer.test.js`
- Relevant output summary: typecheck and build/test compilation passed; 29 focused tests
  passed, 0 failed. The classifier test covers all 16 baseline-equality masks,
  all `3^4` baseline/desired/foreign observation vectors, all three marker
  states, each common-role mutation, both reserved roles, precedence, and
  imports-last order (3,888 exhaustive cases). Publisher tests cover all five
  Python byte finals, unchanged imports, safe rename, request hash join, every
  durable prefix, pre/post-sync crash boundaries, exact-marker retry,
  desired-import retry, foreign/reserved/early states, no-clobber marker races,
  marker-removal swap, staging mutation/replacement, cleanup, inert getters,
  final input and payload CAS before marker removal, precise permanent-error
  propagation with a pre-existing marker, rehashed assertion tampering, and
  actual process success/error exits.
- Tests not run and why: the broad Node/Python suite and GitHub CI are deferred
  until after fresh-context adversarial review, per the requested checkpoint.

## Known Deferrals

- Deferred work: Terraform apply evidence and explicit move acknowledgement;
  only that future contract may retire a safe move file and pending marker.
- Reason it is safe to defer: this slice returns `awaiting_apply`, retains both
  artifacts, and provides no automatic cleanup or success claim.
- Follow-up owner or trigger: next migration slice after this publisher is
  adversarially approved and merged.
- Deferred work: import-oracle/provider-read adoption, generated bindings, HCL
  tfvars, other products, and Python removal.
- Reason it is safe to defer: the process schemas accept only the five-resource
  JSON ZCC profile already covered by independent Python-byte parity.
- Follow-up owner or trigger: subsequent parity slices; Python remains the
  oracle until downstream dual-run evidence is complete.
- Deferred work: abrupt subprocess/host-termination recovery fixtures in
  addition to the exhaustive thrown-boundary crash-prefix tests.
- Reason it is safe to defer: source ordering, fsync boundaries, the exhaustive
  classifier, and every durable prefix are independently reviewed and covered;
  the fixture is extra platform evidence, not a missing recovery branch.
- Follow-up owner or trigger: add it to the final ZCC cutover gate instead of
  expanding this publisher slice further.

## Review Focus

- Highest-risk files or paths:
  `node-src/domain/zcc-pull-refresh-materialization.ts`,
  `node-src/domain/zcc-pull-refresh-transition.ts`, request/result semantic
  validators, and process dispatch.
- Specific assumptions to attack: marker durability before payload visibility;
  stage-alias identity/byte recheck; directory identity refresh after intended
  writes; hard-link alias crash handling; present-to-present rename semantics;
  imports-equal and imports-already-desired lanes; exact-marker removal race;
  cleanup before versus after a durable boundary; and hook error sanitization.
- Source evidence the reviewer should verify: the accepted parity assertion's
  seed/candidate/request joins, refresh fingerprint functions, import-move
  derivation, and Python transform bytes used by the focused corpus.
- Generated artifacts the reviewer should compare: both new schemas against
  every emitted marker/result field and their semantic keywords against the
  state machine.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  baseline equals desired for imports or moves; markerless all-desired moves;
  crash after link/rename but before parent sync; crash after marker removal;
  a foreign staging alias with the original random name; a desired imports file
  that prevents rederiving old move identities; and a structurally valid outer
  assertion hash around inconsistent nested evidence.
