# A3-R reconciliation adversarial review

## Blocking findings

The initial review requested changes for three source-verifiable parity gaps:

1. An explicitly supplied empty provider source was collapsed into absence,
   permitting unqualified provider discovery that Node rejects.
2. Non-finite authoring numbers could reach reconciliation instead of failing
   at the authoring boundary.
3. Snake-case key collisions selected Go's code-point-sorted winner even
   though the Node winner depends on source encounter order, which Go maps no
   longer retain.

The builder preserved provider-source presence with a `*string`, added a
recursive authoring-input validator, and made collisions fail closed. The
first remediation review then found one introduced regression: OPTIONS
validation inspected the entire envelope, including methods and values that
Node ignores. Validation was narrowed to object-valued POST, PUT, and PATCH
metadata immediately before normalization. The same reviewer approved that
final changed surface.

There are no remaining blocking findings for A3-R.

## Non-blocking risks

None. The two OpenAPI-derived metadata vectors and the command/publication
surface are explicit A3-O and A6 deferrals, not omissions from A3-R.

## Source evidence review

- Diff inspected: the new `go/internal/authoring/reconcile` package, its tests,
  the builder handoff, and the A3 coordinator roadmap/manifest changes.
- Handoff inspected:
  `docs/review-handoffs/go-authoring-a3-reconcile.md`.
- Provider schemas inspected: frozen embedded schemas, Go Terraform-schema
  selection helpers, and Node provider-source selection behavior.
- OpenAPI/API contracts inspected: this parcel consumes normalized API
  metadata but does not parse or qualify OpenAPI.
- Provider source inspected: Node's distinction between an absent and supplied
  `providerSource` is preserved explicitly.
- Pack metadata inspected: not applicable to this isolated package.
- Fixture inspected:
  `node-tests/fixtures/python-reconcile-schema-api-v1.json`, SHA-256
  `464121fe2e7edcc09861ea046c10aa54d4d101145803d5af13adb41b56c5cbd7`.
- Missing evidence or review gaps: none inside A3-R's boundary.

The final tests cover absent versus empty provider sources, all three
non-finite float cases, top-level and nested normalized-key collisions, and
Node's OPTIONS processing fence. Ignored GET/unknown/non-object values remain
ignored; only consumed POST/PUT/PATCH metadata is validated.

## Generated artifact review

- Reports reviewed: all 2 Node-live and 7 retained reconciliation reports.
- Schemas reviewed: no generated schemas.
- Fixtures reviewed: the frozen authority is hash-locked and unchanged.
- Snapshots reviewed: none.
- Count/coverage deltas reviewed: none; A3-R creates no source-readiness or
  provider-coverage counts.
- Artifact drift: rejected. A3-R adds no files to the six-artifact bundle and
  does not alter CLI, publication, source precedence, or readiness behavior.

## Verification

The coordinator and reviewer independently ran the focused formatting, test,
race, vet, module-tidy, and diff checks. The coordinator also ran the complete
offline Go suite, the full authoring race suite, and the standing
RootCatalog/Transform/Topology/Generation byte gates successfully after the
first remediation; the same focused gates passed after the final
OPTIONS-boundary correction.

No credentials, provider calls, Terraform operations, or live systems were
used.

## Verdict

Approve.

The frozen authorities replay exactly, the review-discovered edge cases now
fail or pass at the same semantic boundary as Node, and the package remains
isolated from OpenAPI parsing, CLI behavior, filesystem publication, and live
systems.
