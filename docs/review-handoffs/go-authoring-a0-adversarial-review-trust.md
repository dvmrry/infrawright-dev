# Go authoring A0 adversarial review: trust and provenance

Review target: staged A0 diff from
`cbb8f01a5d7aae4785645a8c75a0eb3ad55fcc4f` on
`feature/go-canonjson-foundation` (50 files, 8,746 additions).

Reviewer context: fresh, read-only Terra High reviewer. No implementation
conversation was inherited and no files were edited.

## Blocking Findings

- Finding: **A Create-only request can be emitted as Read-rooted
  `observed_http` evidence.**
- Source evidence: `contracts/validate.go` requires an `observed_http` row to
  have registration, Read callback, endpoint chain, and no reason, and requires
  the endpoint to match terminal `raw_http`; it does not bind the first step to
  the declared Read callback or validate caller-to-callee edges. The synthetic
  unresolved fixture intentionally contains an unresolved Read and a separate
  Create-only POST request.
- Impact: a mapper or consumer can confer verified endpoint/readiness credit on
  a Create/Delete-only request, violating the central Read-rooted trust boundary.
- Required change: represent and validate the chain root and every caller-to-
  callee edge, retaining terminal raw-HTTP co-location.
- Suggested regression: forge `sourcefirst_unresolved` as `observed_http` using
  its Create-only raw request and require both standalone and input-bound report
  validation to reject it.

- Finding: **SDK call evidence can be attributed to the wrong overlapping
  module/version.**
- Source evidence: the validator checks only that some bound SDK contains an
  import path; it discards the selected longest-prefix result and validates the
  separately claimed `SDKCall` against any compatible parent module prefix.
- Impact: evidence can bind to a different SDK module, revision, version, and
  source file than Go module resolution actually selects.
- Required change: resolve every SDK import to exactly one manifest SDK using
  Go longest-prefix rules, require that exact module/version on `SDKCall`, and
  bind locations to the expected source root/module.
- Suggested regression: bind parent and nested SDK modules with overlapping
  package paths and reject a chain that claims the parent for a nested import.

## Non-Blocking Risks

- Root containment still depends on the documented immutable-root precondition:
  parent components are checked before a later pathname open rather than held
  through descriptor-rooted traversal. Stable reads, exact hashes, and pre/post
  Git snapshots mitigate changed bound bytes, but descriptor-rooted capture is
  still required before hostile mutable roots enter qualified production use.
- Git executable selection trusts inherited `PATH`.
- Internal Artifactory availability for the new `golang.org/x/mod` dependency
  was not established on this host.

## Source Evidence Review

- Diff inspected: full staged diff against `cbb8f01`.
- Handoff inspected: `go-authoring-a0-contract-corpus.md`.
- Provider schemas inspected: all staged contract schemas and typed semantic
  validators.
- OpenAPI/API contracts inspected: isolated diagnostics contract; no parser is
  present in A0.
- Provider source inspected: complete synthetic provider/SDK fixture, including
  the unresolved Read and Create-only decoy.
- Pack metadata inspected: frozen Node-v1 authority and ZPA matrix gate.
- Missing evidence: no external ZPA checkout or internal Artifactory
  verification was available; both are declared deferrals.

## Generated Artifact Review

- All four synthetic artifact digests reproduced.
- Source and OpenAPI partition totals recompute correctly for the checked data.
- Artifact drift is rejected pending the two provenance fixes.

## Commands Run

The reviewer ran staged diff/status checks, focused authoring tests, authoring
vet, offline full-module tests, `go mod tidy -diff`, and independent SHA-256
reproduction. All commands passed; the findings are semantic validator gaps.

## Verdict

**Request changes.** The opaque loader, defensive-copy boundary, canonical-byte
groundwork, Git hardening, and artifact bindings are sound, but qualified
endpoint evidence does not yet prove Read reachability or exact SDK-module
attribution.

## Corrected-Surface Recheck

Target: current staged diff from `cbb8f01` (52 files, 9,822 additions).

Both blocking findings are closed. The reviewer verified explicit Read-rooted
caller/callee adjacency, terminal raw/unresolved rules, the successful repeated
`lookupA` path, exact SDK longest-prefix ownership and first-callee identity,
origin/module endpoint checks, nil-callee rejection, and the new hostile
regressions. Focused, race, offline, vet, tidy, and diff checks all passed.

Residual risks remain as documented: immutable-root traversal boundary, trusted
`PATH`, diagnostic-only OpenAPI basis until A3, and unverified internal
Artifactory availability for `golang.org/x/mod`.

Recheck verdict: **Approve.**
