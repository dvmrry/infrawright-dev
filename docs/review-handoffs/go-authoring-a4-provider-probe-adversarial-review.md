# Adversarial review: Go authoring A4 provider probe

This record transcribes the final verdict of the fresh, read-only A4 reviewer.
The coordinator implemented accepted findings but did not supply the approval
verdict. A separate read-only gap sweep attacked the pinned ref-parser boundary;
neither reviewer edited the repository or performed a live operation.

## Blocking Findings

No blocking finding remains.

The review/fix loop closed these initially blocking classes:

- Reusable legacy work paths admitted symlink and pathname-rebind escapes.
  The final implementation binds a private mode-0700 root with `os.Root`, uses
  descriptor-relative verified replacement, and refuses outside-target swaps.
- Legacy recipes did not initially preserve Node validation phase/order and
  explicit-empty `api_prefix` semantics. Cross-invalid precedence and full
  five-artifact differentials now cover them.
- HTTP, Git, and Terraform cancellation initially stopped at orchestration
  boundaries. Context now reaches requests, retry waits, and isolated process
  groups; killed children are drained and reaped.
- The first candidate omitted SwaggerParser validation. The final legacy-only
  validator pins the official schemas and source hashes, disables external
  resolution, and covers version, pointer/dereference, schema, OAS 3.1 patch,
  numeric, and Swagger 2 supplemental semantics.
- Focused re-review found URI-token/raw-slash pointer handling, primitive and
  array extended refs, discarded-sibling traversal, circular Swagger 2
  required traversal, non-pointer hash recognition, intermediate-ref pointer
  traversal, and repeated-ref work amplification. Each was corrected and
  protected by a pinned-Node or focused compatibility regression.

## Non-Blocking Risks

- The Go validator intentionally rejects some documents accepted by Node,
  including JavaScript-native pointer properties, traversal/cache-order-
  dependent invalid extended refs, and documents exceeding defensive depth or
  visit limits. These differences are explicit, tested, and fail closed; they
  cannot silently overclaim or weaken provider evidence. Preserve them in A6
  release notes and add a real-provider fixture if one is encountered.
- A private randomized legacy work directory can remain after a preparation
  failure. It is private and unbounded in naming; avoiding unsafe recursive
  pathname cleanup is preferable until A6 declares lifecycle ownership.
- Windows behavior was cross-compiled but not run on a Windows host. Guarded
  Git/unsupported Terraform paths fail closed; run the focused suite in
  Windows CI before enabling those execution paths.

## Source Evidence Review

- Diff inspected: all A4 provider-probe additions, HTTP and Terraform context
  changes, tests, vendored schemas, provenance, licenses, and the builder
  handoff.
- Handoff inspected:
  `docs/review-handoffs/go-authoring-a4-provider-probe.md`.
- Provider schemas inspected: vendored Swagger 2.0, OpenAPI 3.0, and OpenAPI
  3.1 schemas; their hashes match the pinned package sources.
- OpenAPI/API contracts inspected: SwaggerParser 12.1.0,
  json-schema-ref-parser 14.0.1, swagger-methods 3.0.2, and
  openapi-schemas 2.1.0 behavior, plus the independently reproduced Node
  oracle.
- Provider source inspected: frozen provider fixture and the accepted
  source-operation/OpenAPI paths; live-provider integration was outside A4.
- Pack metadata inspected: not applicable; provider probe is recipe-based.
- Fixtures or snapshots inspected: frozen artifact authority and the new
  offline validator corpora. Existing fixtures and snapshots did not change.
- Missing evidence or review gaps: no live HTTP, remote Git, real Terraform,
  credentials, provider, or native Windows execution. Local injected seams,
  failure tests, and cross-compilation cover the accepted slice.

## Generated Artifact Review

- Reports reviewed: exact legacy five-artifact comparisons and qualified
  six-core-artifact sealing, including optional diagnostic-map isolation.
- Schemas reviewed: three vendored validation-input schemas; no emitted schema
  changed.
- Fixtures reviewed: frozen provider artifacts and the validator oracle
  corpus.
- Snapshots reviewed: no existing snapshot changed.
- Count/coverage deltas reviewed: no readiness/report count changes;
  `providerprobe` statement coverage is 82.7%.
- Artifact drift accepted or rejected: no artifact drift. The explicit
  validator acceptance differences are accepted because they are
  deterministic, tested, and fail closed.

## Verdict

**Approve.**

The initial workspace, validation, recipe-ordering, and cancellation findings,
and every focused recheck finding, are closed. The stable tree passes gofmt,
full `go vet` and tests, repeated and race tests, the four standing artifact
gates, Windows cross-compilation, module-tidiness checks, and the independent
Node oracle. A4 is ready for coordinator commit; it authorizes no live action.
