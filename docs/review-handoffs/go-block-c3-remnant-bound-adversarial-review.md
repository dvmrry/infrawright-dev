# Block C3 Bounded Assessment Remnants Adversarial Review

## Blocking Findings

None remaining.

The initial fresh review found one blocker: after atomically claiming a
deterministic slot with `Mkdir(0700)`, the accepted transaction still performed
a separate pathname `Chmod`. A same-UID concurrent rename/symlink replacement
could redirect that permission mutation. The pathname `Chmod` was removed.
Production now requests permissions only in the atomic `Mkdir` call, with
umask permitted to make the slot stricter.

A direct regression replaces the claimed leaf with a symlink to a `0755`
directory before returning it to the transaction. Assessment rejects the
symlink as `UNSAFE_SNAPSHOT_DIRECTORY`, never calls evidence preparation,
leaves the symlink intact, and leaves the target at `0755`. The reviewer
re-ran the updated parcel and approved the fix.

## Non-Blocking Risks

A same-UID process can move the newly created directory before its first
identity observation and install another same-UID-owned private directory.
This is accepted as the existing temporary-namespace threat-model boundary:
pre-existing entries are never opened or reused, symlinks/non-directories are
rejected without mutation, and after initial binding the existing
descriptor/path identity checks guard evidence creation and cleanup. The bound
is operational, not a security quota against a malicious same-UID process.

The 33rd assessment transaction under one stable temporary root fails closed
until an operator removes retained slots. There is no automatic janitor or
slot reuse. A restrictive umask can make a claimed slot unusable; that slot is
then safely consumed rather than widened with a pathname permission change.

## Source Evidence Review

- Diff inspected: production hook, finite-slot implementation, focused tests,
  and builder handoff against base `4754e29`.
- Handoff inspected: yes, including the exact 32-slot/32,000-snapshot bound,
  restrictive-umask behavior, operator-cleanup requirement, and deliberate
  divergence from Node's unbounded `mkdtemp` lifecycle.
- Provider schemas, OpenAPI, provider source, and pack metadata: N/A.
- Runtime entries inspected: deterministic slot names, private requested mode,
  zero-length retained snapshots, pre-seeded file/symlink/directory fixtures,
  concurrent claims, and exhaustion ordering.
- Missing evidence or review gaps: no credentialed provider call is relevant;
  unsupported runtime targets were cross-compiled/source-reviewed rather than
  executed.

Verification passed after the fix: focused tests repeated 20 times, focused
race tests, full Go suite, `go vet`, formatting/diff checks, Linux/FreeBSD/
Windows assessment-package cross-compilation, and Node assessment 17/17. The
coordinator independently re-ran the focused and race regressions.

## Generated Artifact Review

- Reports, schemas, tracked fixtures, and snapshots: unchanged.
- Runtime remnants: at most 32 directories and 32,000 scrubbed zero-length
  snapshot inodes, derived from the existing 1,000-root ceiling.
- Artifact drift accepted or rejected: no tracked drift; the bounded runtime
  remnant lifecycle is the explicit accepted operational delta.

## Verdict

Approve.

Verdict rationale: the pathname-mutation blocker is resolved with a direct
swap regression. Capacity, concurrency, pre-seeding, error redaction,
pre-evidence exhaustion, zero results, and retained-inode accounting are
fail-closed and bounded without introducing a janitor or filesystem layer.
