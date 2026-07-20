# A3-M adversarial review — frozen generic OpenAPI map kernel

## Blocking findings

None outstanding.

The initial review identified a valid registry-parity class: the first
candidate did not preserve Node's strict product comparison, status
truthiness, nullish reason fallback, or nested fetch-pagination selection.
That class was remediated together with coordinator-found sibling cases for
omitted versus explicit-empty API prefixes, missing/non-object path inventory,
exact detail-path shape, and signed half-even rounding.

The initial uppercase-method finding was withdrawn on recheck. Node retains
the original uppercase spelling after filtering, but every report consumer
then tests membership against lowercase literals. The detached Go view's
lowercase-only inventory is therefore report-equivalent; no report-byte
counterexample exists.

## Non-blocking risks

None.

## Source evidence review

- Diff inspected: the A3-M owned files against
  `4d8b0deb37ac654b7308eda29a318812a737e5a8`.
- Handoff inspected: `docs/review-handoffs/go-authoring-a3-openapi-map.md`.
- Provider schemas inspected: all 19 frozen inputs and the focused
  provider-selection paths.
- OpenAPI/API contracts inspected:
  `node-src/authoring/openapi-resource-map.ts`, including optional/default
  handling, exact detail matching, registry behavior, and ratio semantics.
- Provider source inspected: none; correctly outside this generic parcel.
- Pack metadata inspected: the default registry aggregation used by the
  frozen replay.
- Fixtures or snapshots inspected:
  `node-tests/fixtures/python-openapi-resource-map-v1.json`, verified at
  SHA-256 `e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c`.
- Missing evidence or review gaps: none material. The dependency and consumer
  audit found no sourceanalysis, sourceoperation, readiness, or command
  dependency on `openapimap`.

## Generated artifact review

- Reports reviewed: all 19 frozen reports replayed byte-for-byte.
- Schemas reviewed: none generated.
- Fixtures reviewed: the frozen authority hash and every report case.
- Snapshots reviewed: none.
- Count/coverage deltas reviewed: frozen totals remain identical; focused
  optional/default registry vectors pass.
- Artifact drift accepted or rejected: accepted as absent.

## Verification

The same fresh reviewer rechecked the localized remediation. Focused frozen,
optional/default, detail, signed-rounding, detached-view, malformed-path, and
cancellation tests passed, together with package and race tests, `go vet`,
`gofmt -l`, `go mod tidy -diff`, `git diff --check`, and the Go error-handling
checker.

## Verdict

Approve.

The source-parity findings are remediated and covered; generic output remains
sealed, byte-exact to its frozen authority, and disconnected from source-first
readiness.
