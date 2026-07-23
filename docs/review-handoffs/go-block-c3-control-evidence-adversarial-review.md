# Block C3 Control-Evidence Adversarial Review

## Blocking Findings

None remaining.

The initial review found two blockers: an in-place same-inode mutation could occur after the first hash, and absent-only bindings could bypass the unsupported-platform gate. The builder added a second independently bounded stable observation plus a same-inode overwrite regression, and moved the unsupported-platform check ahead of every nonempty recheck with unsupported-target coverage. Changed-surface re-review accepted both fixes.

## Non-Blocking Risks

None remaining. The initial operational nit about the intentional two-pass Go strengthening is now recorded in the builder handoff with its maximum physical I/O and required integration trigger.

## Source Evidence Review

- Diff inspected: all files under `go/internal/controlevidence/**` against `98682a739af92011beb71ddd54872aac23860e3f`.
- Handoff inspected: Yes, including the revised divergence record.
- Provider schemas inspected: N/A.
- OpenAPI/API contracts inspected: `node-src/domain/control-evidence.ts` and stable-file substrate.
- Provider source inspected: N/A.
- Pack metadata inspected: N/A.
- Fixtures or snapshots inspected: N/A.
- Missing evidence or review gaps: unsupported tests cross-compiled rather than executed on the Darwin host; their source explicitly pins the contract.

Direct review confirmed the same-inode regression, unsupported absent-binding failure and empty-set success, exact 16-MiB/64-MiB boundaries, ENOENT-only absence, caller-order short-circuit, BOM/copy/identity/symlink/TOCTOU behavior, and fixed diagnostic redaction. Focused/repeated/race/full/vet and multi-target cross-compilation passed.

## Generated Artifact Review

- Reports reviewed: N/A.
- Schemas reviewed: N/A.
- Fixtures reviewed: N/A.
- Snapshots reviewed: N/A.
- Count/coverage deltas reviewed: N/A.
- Artifact drift accepted or rejected: no drift.

## Verdict

Approve.

Verdict rationale: Both evidence-integrity blockers are fixed with direct regression coverage. The intentional Go-only second stable hash is serial, fixed-bounded, documented, and has a mandatory integration latency/deadline follow-up.
