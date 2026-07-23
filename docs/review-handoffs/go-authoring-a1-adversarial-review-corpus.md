# A1 source analysis — corpus adversarial review

## Blocking Findings

None.

## Non-Blocking Risks

None.

## Source Evidence Review

- Diff inspected: base `31895dbe0c45b834b637e152f031d1dcd1f250d1`
  through the complete tracked and untracked working-tree candidate.
- Handoff inspected: `go-authoring-a1-source-analysis.md`, as orientation only.
- Provider schema inspected: the pinned digest is
  `d25ad0fa…9202e6`; all 16 selected resources exist.
- Provider source inspected: the local checkout is exactly ZPA v4.4.6 commit
  `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`. The 133 provider bindings equal
  `main.go` plus every tracked, non-test, top-level `zpa/*.go` file.
- SDK source inspected: the local module is exactly zscaler-sdk-go v3.8.40;
  all 20 file hashes and its subset-tree digest were verified.
- OpenAPI/API contracts: no OpenAPI binding exists and no OpenAPI evidence is
  claimed.
- Pack metadata: selection contains exactly the 16 report resources and no
  filters.
- Missing evidence or review gaps: none material.

## Generated Artifact Review

- All 16 registration/Read anchors and every provider→SDK and SDK request
  anchor were checked against the pinned source.
- Manifest, input-provenance, and report canonical bytes and digests passed.
- The source-reviewed partition is 15 `observed_sdk_call`, one `ambiguous`
  (`zpa_policy_access_rule`), zero `observed_http`, and endpoint coverage 0/16.
- `NewRequestDo` remains `endpoint_not_recovered`; no wrapper-derived HTTP
  endpoint is invented.
- The synthetic drift is limited to its direct `net/http.NewRequest` proof and
  corresponding source/provenance hashes.
- External pinned-source, full authoring, formatting, vet, and diff-hygiene
  gates passed.

## Verdict

**Approve.** The independently transcribed corpus identities, complete
provider closure, source anchors, canonical digest chain, policy ambiguity,
and count accounting are source-verifiable and internally consistent.

## Recheck

The engine fixes were rechecked against both corpora. The independently
transcribed ZPA authority remains byte-identical at manifest digest
`577eaf74544f0d24a52205a13922ca4cc3803701cfda7557da6367e68ead55bc`
and report digest
`4897d20a680c433473b34459c885c20c2067de12c860640b3730111bbd279039`;
its partition remains 15 `observed_sdk_call`, one `ambiguous`, and zero raw
HTTP endpoints. The refreshed synthetic authority and direct-HTTP proof were
also rechecked after the fixes.

Final verdict: **Approve**, with no blocking findings or non-blocking risks.
