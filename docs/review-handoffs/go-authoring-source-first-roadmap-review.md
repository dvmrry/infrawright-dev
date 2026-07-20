# Builder review handoff: Go source-first authoring roadmap

Review status: **approved by two independent fresh adversarial reviewers after
the documented remediation loop; ready to commit**.

## Intent

- Correct the Go authoring-port contract before implementation.
- Make provider and pinned SDK source the primary authority for HTTP method/path
  evidence, with OpenAPI optional corroboration rather than a required terminal
  mapping.
- Retain six general authoring commands and retire the one-version
  `zpa-provider-evidence` CLI into a checked generic-analyzer corpus.
- Align the authority-handoff, cutover, v2, and original runtime plans with that
  decision.
- Keep current Node behavior unchanged until reviewed Go parcels land.

## Base / Head

- Base: `2224ffd66143899a4a0fdee54e03a45d2de0d35b` (`Amend roadmaps: full
  Node archival, authority-handoff gate, Go-only degroup`).
- Head: uncommitted documentation working tree on
  `feature/go-canonjson-foundation`.
- Diff command: `git diff 2224ffd -- docs/go-authoring-port-roadmap.md
  docs/go-cutover-roadmap.md docs/go-runtime-plan.md docs/go-runtime-v2.md
  docs/singleton-state-topology-v2.md docs/zpa-provider-evidence.md
  docs/review-handoffs/go-authoring-source-first-roadmap-review.md` plus
  `git diff --no-index /dev/null docs/go-authoring-port-roadmap.md` and the same
  for this untracked handoff until staged.

## Files Changed

- Files:
  - `docs/go-authoring-port-roadmap.md` (new controlling authoring design).
  - `docs/singleton-state-topology-v2.md` (authority gate becomes six retained
    commands plus independently reviewed source-only goldens; ZPA command
    retirement recorded).
  - `docs/go-cutover-roadmap.md` (six-command transitional lane and ZPA corpus
    disposition).
  - `docs/go-runtime-v2.md` (replaces permanent Node authoring boundary with
    full-archive/source-first position).
  - `docs/go-runtime-plan.md` (replaces stale Slice 8 OpenAPI skip/Node-first
    gate with the decided source-first contract and dependency posture).
  - `docs/zpa-provider-evidence.md` (records the planned pre-handoff-to-corpus
    transition without rewriting current behavior as already retired).
  - This handoff.
  - `docs/review-handoffs/go-authoring-source-first-roadmap-adversarial-review.md`
    (two-review findings, remediation mapping, and final verdicts).
- Files intentionally left untouched:
  - All Go and Node implementation and tests.
  - `tools/source-evidence-ast` and its standalone module.
  - Frozen Python/Node fixtures and `docs/evidence/zpa-provider-v4.4.6.json`.
  - Current-behavior user docs (`README`, `operational-runtime`,
    `provider-readiness`, `provider-probes`, command help, recipes). Those must
    change with the implementation parcels, not claim future behavior early.
  - Singleton-state implementation and release routing.

## Source Inputs Consulted

- Provider schemas: checked-in pack schemas were inventoried only through the
  existing ZPA evidence contract; no schema interpretation changed.
- OpenAPI/API contracts:
  - Current Node `openapi.ts`, `openapi-resource-map.ts`, and
    `source-operation-map.ts` behavior.
  - Existing GitHub/DigitalOcean pinned probe recipes and the documented
    scraped Zscaler OpenAPI trial.
- Provider source files:
  - No external checkout was read. The roadmap uses the existing AST collector
    tests and the paths/ranges pinned by the ZPA v4.4.6 matrix as design
    evidence only.
- Pack metadata:
  - ZPA pack/registry/schema/override binding contract described by
    `docs/evidence/zpa-provider-v4.4.6.json` and its prior review.
- Existing docs or design records:
  - `docs/adversarial-review.md` and templates.
  - `docs/go-runtime-plan.md`, `docs/go-runtime-v2.md`,
    `docs/go-cutover-roadmap.md`, and
    `docs/singleton-state-topology-v2.md`.
  - `docs/provider-readiness.md`, `docs/provider-probes.md`,
    `docs/python-oracle-contracts.md`, and
    `docs/zpa-provider-evidence.md`.
- Other source evidence:
  - `tools/source-evidence-ast/collector.go` and tests.
  - `node-src/authoring/provider-source-evidence.ts`.
  - `node-src/authoring/sdk-path-evidence.ts`.
  - `node-src/authoring/source-operation-map.ts`.
  - `node-src/authoring/provider-probe.ts`.
  - `node-src/authoring/zpa-provider-evidence.ts`.
  - Five independent Terra-high read-only audits covering source-chain gaps,
    command disposition, fixture authority, dependencies, and stale docs. The
    audits are orientation, not acceptance evidence.

## Generated Artifacts

- Reports: none.
- Schemas: none; the source-first schema is an A0 implementation deliverable.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none in this documentation change.

## Expected Delta

- Expected behavior change: none yet. The future authoring contract becomes
  source-first and OpenAPI-optional.
- Expected report/count/coverage changes:
  - Future v2 evidence distinguishes observed HTTP, observed SDK call,
    ambiguous, dynamic, unresolved, no-source, and not-applicable outcomes.
  - Generic suggestions never count as mapped evidence.
  - Existing Node-backed reports remain byte authorities for overlapping
    pre-handoff cases.
- Expected generated-output changes: none now. Future source-only reports and
  probe artifact sets are versioned additions reviewed in A0–A4.
- Expected no-op areas: operator runtime; Fetch/Transform/Adopt/Plan/Apply;
  packs, schemas, evidence bytes, topology, state, and live providers.

## Invariants Claimed

- Evidence must not be silently dropped:
  - Every selected resource remains represented, including unresolved and
    ambiguous cases.
  - The ZPA matrix and its exact local/source binding checks remain active as a
    corpus even though its CLI retires.
- Generic matcher evidence must not outrank source-backed evidence:
  - Fuzzy/name/schema candidates are suggestions only. Direct provider HTTP and
    provider-to-pinned-SDK HTTP facts are the only endpoint evidence.
- Source precedence/provenance must remain explicit:
  - Resource registration, Read callback, reachable provider call chain, SDK
    source, HTTP request, and optional OpenAPI match each retain distinct
    provenance.
- Ambiguity must stay classified instead of being coerced to success:
  - Dynamic dispatch/paths, multiple calls, missing SDK source, unsupported
    data flow, and conflicting/partial OpenAPI remain non-success states.
- Provider-readiness counts must stay explainable:
  - Aggregate counts recompute from the closed per-resource vocabulary; a
    suggestion or OpenAPI-only candidate cannot inflate source coverage.
- Adoption safety invariants:
  - No runtime, Oracle, plan, assessment, exact Apply, state, or provider-tenant
    behavior changes. The ZPA matrix's generated-config runtime gate remains
    fail closed and is not upgraded by static endpoint resolution.

## Tests Run

- Commands:
  - Read-only source and documentation inventories with `rg`, `find`, `wc`,
    `sed`, and `jq`.
  - `git diff --check` during drafting.
  - Link target/file existence and final staged-diff checks are still owed
    after review remediation, before commit.
- Relevant output summary:
  - Current Go AST facts are syntactic, not callback-reachability proof.
  - Current SDK parsing is regex/text based and receiver-focused.
  - Current source-operation/probe commands require OpenAPI.
  - Existing frozen corpora are strong for overlapping behavior but contain no
    source-only provider-to-SDK-to-HTTP byte authority.
- Tests not run and why:
  - No Go/Node test suite: no implementation or fixture changed.
  - No live provider, network, credential, Terraform, or Apply operation.
  - No Artifactory lookup: exact future dependencies are not being added yet.

## Known Deferrals

- Deferred work:
  - A0 source-first schema, hand-reviewed goldens, and generic ZPA corpus
    validator.
  - A1–A6 implementation and command routing.
  - Exact `kin-openapi` and any `x/mod`/`x/tools` version validation in
    Artifactory.
  - Updating current-behavior README/provider/probe/help/recipe documentation.
  - Deciding whether `source-evidence-eval` remains a post-archive shipped
    developer command after it has served the handoff; it remains in the six
    retained commands for this roadmap.
- Reason it is safe to defer:
  - This diff changes plans only and explicitly leaves current Node behavior
    authoritative until reviewed Go parcels land.
- Follow-up owner or trigger:
  - A0 begins only after fresh adversarial approval of this roadmap.

## First-review remediation

The first fresh reviewer returned four blocking findings. This revision:

1. makes the final executable boundary unambiguous: one released `iw` binary
   contains the operator surface and all six retained authoring commands;
2. defines optional-OpenAPI document states, artifact-set isolation,
   diagnostics, atomic replacement, and exit behavior while keeping standalone
   `openapi-map` strict;
3. defines the complete per-resource classification partition, endpoint
   denominator, source-call and endpoint numerators, separate OpenAPI qualifier
   counts, and the legacy `mapped` projection; and
4. separates the retained ZPA static source-binding gate from the new
   provider+SDK-pinned endpoint-evidence fixture and its independent mutation
   gates.

The non-blocking dependency note was also closed: `kin-openapi` is explicitly a
conditional candidate with no version or dependency authorized until A3
performs and records Artifactory validation.

The first reviewer approved that revision after one terminology nit, which was
closed by separating the ZPA static source-binding corpus from endpoint
qualification in `docs/zpa-provider-evidence.md`.

The second independent reviewer then found three additional blocking contract
gaps. This revision:

1. adds a command-level, local-only source-provenance manifest shared by all
   source-first commands, plus explicit unverified exploratory mode that is
   ineligible for readiness or handoff;
2. defines `source-operation-map --artifact-dir` as the v2 complete-set bundle
   interface while retaining the current one-file/stdout interface only as the
   byte-exact legacy differential mode; and
3. separates report-level OpenAPI document state from a closed per-resource
   comparison partition with exact eligibility, sum, degraded, and zero-row
   invariants.

Its cleanliness nit was also made exact: the static ZPA gate checks cleanliness
of the matrix-bound tracked source files as the current validator does, not an
unrelated or untracked whole worktree. Curated matrix assertions are explicitly
labeled as curated rather than analyzer-derived.

Both independent reviewers approved the resulting full staged diff. The second
reviewer's final nit—making `--source-manifest` and
`--allow-unverified-source` explicitly require `--artifact-dir` for
`source-operation-map`—was applied and the reviewer confirmed it closed.

## Review Focus

- Highest-risk files or paths:
  - `docs/go-authoring-port-roadmap.md`, especially evidence precedence,
    source-only authority, ZPA command retirement, and A0/A1 gates.
  - Authority-handoff wording in `docs/singleton-state-topology-v2.md`.
- Specific assumptions to attack:
  - Whether retiring the ZPA CLI while retaining its checks in a generic test
    harness genuinely preserves stale-evidence detection.
  - Whether six-command Node parity plus independently reviewed source-only
    goldens is sufficient for authority handoff without modifying Node.
  - Whether callback-rooted bounded AST analysis can prevent Create/Delete-only
    calls from being overclaimed as Read evidence.
  - Whether OpenAPI absence, partiality, conflict, or invalid unrelated content
    can ever suppress or upgrade source evidence.
  - Whether the initial AST scope is bounded enough to avoid rebuilding a Go
    compiler while still handling package functions and explicit receiver
    cases safely.
- Source evidence the reviewer should verify:
  - The named Go collector and Node authoring sources above.
  - The ZPA matrix validator's current local/source checks and hard-coded
    version scope.
  - Frozen corpus descriptions and exact command contracts in
    `docs/python-oracle-contracts.md`.
- Generated artifacts the reviewer should compare:
  - None in this diff. Verify that no fixture, evidence matrix, schema, catalog,
    demo file, or report changed.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - Shared provider files containing non-Read calls; same-named SDK methods in
    different packages; unresolved receivers; helper cycles; dynamic request
    paths; multiple HTTP requests; package functions; OpenAPI path ambiguity;
    candidate-generated goldens; and a stale ZPA pack/schema/source input no
    longer being checked after CLI retirement.
