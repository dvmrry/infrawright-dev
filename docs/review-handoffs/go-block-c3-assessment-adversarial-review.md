# Block C3 Assessment Transaction Adversarial Review

## Blocking Findings

None remaining after two independent review/fix loops.

Both initial reviewers found a post-verification `RemoveAll` directory-swap race; one also found that post-finalization failures could return a success-looking ordinary result. Cleanup was replaced with descriptor-bound `os.Root` manifest validation and no pathname-destructive operations, and every error now zeroes the ordinary result while retaining completed roots only in `Failure.Partial`. Direct swap/composite/result regressions were re-reviewed.

## Non-Blocking Risks

Successful assessments deliberately retain a private `0700` directory and one descriptor-scrubbed, zero-length snapshot inode per prepared root. This is the reviewed portable fail-closed tradeoff. Runner/CLI adoption is explicitly blocked until a bounded trusted-janitor or equivalent remnant lifecycle is chosen and tested.

## Source Evidence Review

- Diff inspected: transaction, tests, descriptor cleanup/platform files, and unsupported test against approved report base `814f6dc`.
- Handoff inspected: Yes, including final cleanup scope, remnant contract, barrier timeout, and adoption trigger.
- Provider schemas inspected: N/A.
- OpenAPI/API contracts inspected: Node assessment transaction and accepted Go seams.
- Provider source inspected: N/A.
- Pack metadata inspected: accepted guidance/policy seams only.
- Fixtures or snapshots inspected: mutation barriers, partial roots, directory swaps, exact manifests, replaced/unexpected snapshots, cleanup composites, zero-result failures, and 64-MiB control set.
- Missing evidence or review gaps: real provider leg remains deferred; unsupported cleanup was source/cross-compile reviewed.

Both reviewers confirmed preflight precedence, tenant-NUL-label order, per-root/final evidence barriers, complete:false rejection, limits, partial roots, synchronous finalizer, redaction, descriptor-bound cleanup, replacement survival, primary+cleanup composites, zero ordinary result on error, and barrier-based timeout behavior. Focused/repeated/race/full/vet/formatting, cross-compilation, zero dependencies, and Node assessment 17/17 passed.

## Generated Artifact Review

- Reports reviewed: success/error wrapper and partial-root integration through approved report APIs.
- Schemas reviewed: None changed.
- Fixtures reviewed: runtime transaction/cleanup fixtures.
- Snapshots reviewed: exact original/replacement inode and zero-length manifest behavior.
- Count/coverage deltas reviewed: result/root/finding/path/metadata ceilings.
- Artifact drift accepted or rejected: no tracked drift; scrubbed inode remnants accepted behind the pre-runner gate.

## Verdict

Approve.

Verdict rationale: Both independent reviewers found the transaction safety blockers resolved with direct regressions. The remaining operational remnant lifecycle is explicit and blocks runner/CLI adoption until bounded.
