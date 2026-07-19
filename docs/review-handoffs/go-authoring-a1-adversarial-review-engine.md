# A1 source analysis — engine adversarial review

## Blocking Findings

1. **Unreferenced registration authority.** The initial candidate fell back to
   any root-package `var resources` map when neither a resolved
   `plugin.Serve` authority nor a root `Provider()` factory existed. The same
   scan accepted a syntactic `plugin.Serve` in a dead `if false` branch. A dead
   utility map could therefore manufacture Read-rooted evidence. Required:
   remove the map fallback and make Serve discovery control-flow-aware.
2. **Unbound external operation edges disappeared.** Imported package and
   typed-receiver calls outside a bound/unavailable SDK were silently ignored,
   not merely known standard-library or Terraform-framework operations. A Read
   callback could call an uncaptured service client plus a valid bound endpoint
   and report only the latter. Required: retain unbound external edges as
   unresolved unless they belong to a narrow, source-justified non-operation
   allowlist.
3. **Missing source erased sibling evidence.** If any chain ended in an
   authorized `sdk_source_missing`, `resourceRow` replaced the full trace with
   missing-only chains. Valid endpoint, SDK, dynamic, or unresolved sibling
   chains vanished. Required: retain every canonical chain; use `no_source`
   only when all outcomes are missing, and represent mixed outcomes without
   selecting one.

Source evidence and impacts were verified directly in
`internal/authoring/sourceanalysis/analyze.go`, its hostile tests, and the
roadmap classification contract. The initial focused authoring tests and vet
passed, but they encoded rather than prevented these behaviors.

## Non-Blocking Risks

None in the initial review.

## Source Evidence Review

- Diff inspected: base `31895dbe0c45b834b637e152f031d1dcd1f250d1`
  through the complete tracked and untracked working-tree candidate.
- Handoff inspected: `go-authoring-a1-source-analysis.md`.
- Provider source inspected: pinned ZPA v4.4.6 checkout, including the real
  `main.go` Serve authority and `zpa.ZPAProvider` resource map.
- SDK source inspected: pinned zscaler-sdk-go v3.8.40, including
  `zscaler/zparequests.go`.
- OpenAPI/API contracts: not required for the source-only parcel.
- Fixtures inspected: synthetic source-first and ZPA endpoint corpus.
- Review gap: none for the three blocking trust failures.

## Generated Artifact Review

- Reports, provenance schema, synthetic fixture, ZPA fixture, canonical digest
  checks, and the 15/1/zero-endpoint partition were inspected.
- Artifact acceptance was withheld because the engine could silently
  manufacture, discard, or suppress source-operation evidence.

## Verdict

**Request changes.** Registration authority, unbound external calls, and mixed
missing-source outcomes were not fail-closed in the initial candidate.

## Recheck

The accepted finding → root cause → fix → regression test → verification
loop was completed in three fresh review passes:

1. The first recheck closed the three initial findings by removing the bare
   `resources` fallback, retaining unbound external calls, and preserving
   mixed missing-source candidates. It found two further blockers: nested
   argument calls were not retained and a missing-SDK chain could carry
   fabricated endpoint evidence.
2. The second recheck confirmed strict missing-chain validation and retained
   nested package, typed-receiver, interface-receiver, and bound-closure gaps.
   It found one remaining case: an unbound imported factory/receiver chain such
   as `external.NewClient().Lookup()` could still disappear.
3. The final recheck confirmed that imported factory/receiver chains are
   retained as one outer unresolved candidate, without duplicating the
   receiver call, while resolved SDK constructors remain a single observed
   result. Focused tests and vet passed.

Final verdict: **Approve**, with no blocking findings or non-blocking risks.
