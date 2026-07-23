# Adversarial review: Go authoring A0.1 trust boundary

## Blocking Findings

The initial review found that the new `reviewed_not_applicable` filter was
authenticated by the manifest digest but not semantically joined to report
rows. A producer could therefore mark any selected resource `not_applicable`,
shrink the coverage denominator, and pass input-bound validation without the
manifest authorizing it.

The accepted fix reserves the filter name, requires all values to name selected
resources, and enforces exact bidirectional correspondence between filter
values and rows classified `not_applicable` with reason
`reviewed_not_applicable`. Verified and unverified input paths use the same
join. Regressions cover missing, extra, mismatched, duplicate, empty, and
unselected authorizations, including an unverified negative case.

No blocking findings remain after re-review.

## Non-Blocking Risks

None.

## Source Evidence Review

- Diff inspected from `86638439a3697af1fc516ed5db422637a0487361`.
- Handoff inspected as orientation only.
- Contract joins, schema/type vocabulary, hostile tests, fixture authorities,
  and canonical digest links inspected independently.
- The `sdk_source_missing` step remains provider-rooted, terminal, non-viable,
  and unable to carry resolved SDK or endpoint evidence.
- Full symbol adjacency includes package identity.

## Generated Artifact Review

- Manifest/input/report/OpenAPI diagnostic digest chain independently checked.
- Evidence rows, call chains, classifications, counts, and endpoint claims are
  unchanged after removing only the authorized filter and digest links.
- Artifact drift accepted.

## Verdict

**Approve.** The initial authority-binding defect and the subsequent unverified
coverage nit are both closed.
