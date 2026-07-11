# Builder Review Handoff: ZCC Refresh Acknowledgement

## Intent

- Add one ZCC-only machine operation that retires an exact safe-move artifact
  and its pending marker after a trusted pipeline states that Terraform apply
  succeeded.
- Keep the trust claim honest: the engine does not observe Terraform apply and
  the result explicitly records `apply_observed_by_engine: false`.
- Require both a fixed request statement and the separate host-only
  `INFRAWRIGHT_ALLOW_EXTERNAL_APPLY_ACK=1` capability before filesystem I/O.
- Close the transition gap where legacy Python transform/adopt could delete an
  unapplied move while the Node pending marker existed.

## Base / Head

- Base: `7141356c18f6df7af92c9f048b504e2ca6819bc2`
- Head: the checked-out commit on `feature/node-zcc-refresh-ack`; resolve it
  with `git rev-parse HEAD` after the review checkpoint is committed.
- Diff: `git diff 7141356c18f6df7af92c9f048b504e2ca6819bc2...HEAD`

## Files Changed

- Process documentation and integration sequence:
  `docs/node-process-api.md`, `docs/integration-validation.md`.
- Versioned contracts: process request/response schemas and
  `docs/schemas/zcc-pull-refresh-acknowledgement.schema.json`.
- Contract implementation: `node-src/contracts/validators.ts` and
  `node-src/contracts/zcc-pull-refresh-acknowledgement-semantics.ts`.
- Domain/process implementation: refresh fingerprints/materialization,
  `node-src/domain/zcc-pull-refresh-acknowledgement-operation.ts`, and process
  types/dispatch/main.
- Transition guard: `engine/artifacts.py`, `engine/transform.py`,
  `engine/adopt.py`.
- Focused Node/Python tests in the existing refresh materializer, transform,
  and adopt suites.

## Source Inputs Consulted

- PR #171 publisher contracts, exact pending marker, assertion and publication
  receipt schemas, and imports-last filesystem transaction.
- Python move generation/removal in `engine/transform.py` and `engine/adopt.py`.
- Python staging/unstaging/apply behavior in `engine/ops.py`.
- Existing strict raw-candidate preparation and current source/control/parent
  binding in `node-src/domain/zcc-pull-operation.ts`.
- Provider/OpenAPI source: N/A; this slice does not interpret provider fields or
  API responses.

## Generated Artifacts

- One new strict v1 result schema:
  `infrawright.zcc_pull_refresh_acknowledgement`.
- One new process request/response operation: `acknowledge_pull_refresh`.
- No generated provider data, fixtures, snapshots, or pack metadata changes.

## Expected Delta

- A request must contain the complete ready/equal parity assertion, its complete
  `awaiting_apply` publication receipt, the fixed external acknowledgement, and
  exact matching tenant/resource/context coordinates.
- The process host must also supply an output authority and exact external-ack
  capability; neither can be granted by request data.
- Exact move + marker retires in `moves -> pending_moves` order. A crash after
  move deletion retries from `retirement_prefix`; both absent is idempotent
  `already_retired`.
- Marker-absent/move-present, foreign payloads, foreign marker/move, changed
  source/control/parent bindings, or reserved artifacts fail closed.
- Python transform/adopt refuse before reading/deriving/writing when the pending
  marker exists; stage and unstage remain available.

## Invariants Claimed

- The acknowledgement is a trusted caller assertion, not apply proof. The
  result cannot claim Node observed Terraform or state convergence.
- Policy, acknowledgement object, assertion, publication, and callbacks are
  inertly snapshotted; host capability is checked before preparation I/O.
- Request hash, assertion hash, baseline fingerprint, transition fingerprint,
  complete publication receipt digest, desired artifact descriptors, current
  raw candidate, and control/source/parent bindings remain joined.
- The canonical move and marker are revalidated as the exact expected regular
  files immediately before their path-based unlinks. Portable Node has no
  descriptor-relative unlink primitive, so the final check-to-unlink interval
  relies on the documented trusted, serialized single-writer boundary rather
  than an atomic inode guarantee. Bound directory handles are identity-checked
  before and after synchronization.
- Move-parent durability is re-established before marker retirement, including
  on `retirement_prefix` retries. An `already_retired` retry synchronizes and
  re-reads the absent entries before returning success. The exact marker remains
  the recovery fence until every input and payload is rechecked.
- Any error after a deletion by the current invocation is retryable and
  indeterminate; earlier deterministic errors remain precise.
- Final verification reads imports last and emits only content-free states and
  hashes—no paths, bytes, keys, IDs, state addresses, credentials, or output.

## Tests Run

- `npm run typecheck`
- `npm run build:test`
- `npm test`: 451 tests, 450 passed, 1 platform skip, 0 failed.
- `python3 -m unittest tests.test_transform tests.test_adopt`: 191 passed.
- `git diff --check`
- Focused acknowledgement cases cover retirement, durable prefix recovery,
  idempotence, final-boundary move/marker replacement no-clobber, bound-parent
  rebind detection, capability-before-I/O, exact pending-marker joins, result
  semantics, process success/error, receipt digest, and content-free evidence.
- Focused Python cases preserve exact sentinel artifacts and prove both early
  and late-arriving markers stop mutation without secret-bearing diagnostics.

## Known Deferrals

- Node does not execute Terraform, inspect `previous_address`, or verify post-
  apply state in this slice. A future credentialed apply executor is separate
  work and can invoke the same retirement kernel after observing its own exit 0.
- The pipeline remains responsible for stage, saved-plan policy review, apply,
  and unstage before acknowledgement.
- No second terminal acknowledgement file is retained. Crash safety relies on
  the exact existing marker and deletion order: move first, marker last. Once
  both are absent, replay is non-destructive and validates the retained caller
  assertion/receipt plus current desired artifacts before returning idempotent
  success.
- The process is not a security boundary against a hostile same-UID process
  racing pathname operations. Pipelines must serialize publisher,
  acknowledgement, Terraform, Python, and cleanup mutations for the selected
  artifact paths.

## Review Focus

- Whether trusting the external statement is represented honestly and gated by
  the host capability on every public path.
- Cross-document semantic joins among request, assertion, publication, current
  candidate, marker bytes, and result receipt digest.
- TOCTOU windows around exact move/marker unlink, directory rebinding, sync,
  and final imports-last verification.
- Safety of `already_retired` without a second fence and whether any replay can
  cause destructive action or overclaim apply observation.
- Result/schema agreement, removed-role ordering, lookup resource semantics,
  bounded validation, and secret/path absence.
- Python guard placement before artifact mutation while preserving the valid
  stage/apply/unstage/ack sequence.
