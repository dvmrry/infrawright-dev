# Adversarial review record: Go source-first authoring roadmap

Two independent fresh-context reviewers inspected the full staged diff from
base `2224ffd66143899a4a0fdee54e03a45d2de0d35b`. Neither reviewer edited files
or implemented fixes.

## Blocking Findings

No blocking finding remains.

The first reviewer initially requested four changes:

| Finding | Root cause | Fix | Required implementation regression |
|---|---|---|---|
| Go command boundary contradicted itself | Operator-only wording survived the full-archive amendment | One released `iw` binary now owns the operator surface and all six authoring commands | A6 help/routing smoke proves all six names and no Node path |
| Optional OpenAPI could still suppress source evidence | Failure/artifact/exit behavior was not specified | Report-level adapter states, strict standalone `openapi-map`, and source-artifact isolation are normative | Absent/invalid/partial/conflicting fixtures prove artifacts, diagnostics, exits, and atomic replacement |
| Endpoint/count accounting was incomplete | SDK-symbol evidence was not separated from HTTP evidence | Closed seven-state source partition, exact numerator/denominator, and legacy projection | Exhaustive classification transitions recompute every total |
| ZPA command retirement conflated two qualifications | Curated semantic anchors were described as analyzer evidence | Static source-binding and provider+SDK endpoint gates are independent | Independent binding and endpoint mutation matrices fail closed |

After those fixes the first reviewer returned **Approve with nits**. Its sole
terminology nit was fixed by describing the ZPA matrix as the static
source-binding corpus and the new provider+SDK fixture as endpoint authority;
the reviewer confirmed closure and then approved the later full revision.

The second reviewer independently found three more gaps:

| Finding | Root cause | Fix | Required implementation regression |
|---|---|---|---|
| Source pinning existed only in fixture prose | Bare local roots could produce apparently qualified evidence | Shared local `source-provenance-v1` manifest; explicit unverified mode is excluded from readiness/handoff | Every command rejects revision, schema, file, SDK module, and tree drift without network access |
| `source-operation-map` had no atomic bundle destination | Its legacy interface exposes independent files/stdout | `--artifact-dir` defines v2 complete-set mode; the old interface is frozen legacy-only | Pre-existing directory tests prove atomic set replacement and stale optional-file removal |
| OpenAPI qualifier counts mixed document and row states | `unavailable` and `absent` could not enter a per-resource partition | One report document state plus a closed six-state row comparison partition | Exact sums cover absent, unavailable, degraded, conflicts, and zero rows |

The second reviewer then returned **Approve with nits**. Its sole nit was closed
by stating that either `--source-manifest` or `--allow-unverified-source`
selects v2 `source-operation-map` mode and therefore requires
`--artifact-dir`, with usage exit 2 when omitted. The reviewer confirmed
closure.

## Non-Blocking Risks

None remains from either review. `kin-openapi` is explicitly conditional: A3
must validate and record its exact Artifactory version before adding it.

## Source Evidence Review

- Diff inspected: full staged seven-document design/handoff diff from
  `2224ffd`.
- Handoff inspected: builder handoff and both remediation iterations.
- Provider schemas and pack metadata inspected: ZPA matrix plus current
  pack/schema/registry/override validator behavior.
- OpenAPI/API contracts inspected: current Node CLI, validation,
  source-operation, evaluation, and provider-probe flows.
- Provider source logic inspected: current Go AST collector, Node source/SDK
  mapper, and ZPA source-binding validator.
- Fixtures inspected: frozen Node authoring inventory and ZPA matrix counts.
- Missing evidence: v2 schemas, endpoint fixtures, and implementation do not
  exist yet; A0–A6 create and review them.

## Generated Artifact Review

- Reports, schemas, fixtures, snapshots: none changed.
- Count/coverage deltas: design-only; the controlling roadmap now defines the
  exact future partitions and invariants.
- Artifact drift: none; `git diff --cached --check` passed after every review
  remediation.

## Verdict

**Approve.** Both independent reviewers found no unresolved blocking or
non-blocking issue after the review/fix loop. This verdict approves the roadmap
for A0 implementation; it does not approve any future generated schema,
fixture, mapper output, authority handoff, degrouping, or Node archive without
their own required gates.
