# Go Wave 7 Drift-Policy Runtime Matching — Builder Handoff

## Review status

- Adversarial review identified the runtime-matching expansion as an
  unreviewed diff hidden inside another parcel's file glob. This handoff
  isolates it as a self-contained four-file parcel.
- This is a builder handoff, not approval. Scoped gates and the escalated
  host-network full-module gate are green against the frozen hashes below. The
  parcel is ready for a fresh-context adversarial review.

## Intent

- Complete the previously deferred runtime half of
  `node-src/domain/drift-policy.ts` and the path helpers it consumes from
  `node-src/domain/policy-paths.ts`.
- Expose only the Go API needed by future plan evaluation, assessment, state
  projection, generated-config, and adopt consumers.
- Preserve declaration-order matching, canonical exact-selector aliases,
  exact/wildcard precedence, WeakSet-style identity accounting, stale
  ordering/filter defaults, and existing validation diagnostics.
- Add no consumer wiring, CLI behavior, report change, or generated artifact.

## Base / Head

- Base: `c32479d6fdee4ce44944c4b8b8971900b97beda3`.
- Head: shared uncommitted working tree on
  `feature/go-canonjson-foundation`.
- Origin: `https://github.com/dvmrry/infrawright-dev`.
- Review scope is exactly the four files under “Files Changed.” Every other
  dirty-worktree path belongs to another parcel.
- Review commands:
  - `git diff -- go/internal/metadata/driftpolicy.go`
  - `git diff --no-index /dev/null go/internal/metadata/driftpolicy_runtime_test.go`
  - `git diff --no-index /dev/null go/internal/metadata/driftpolicy_node_oracle_test.go`
  - `git diff --no-index /dev/null docs/review-handoffs/go-wave7-drift-policy-runtime.md`

## Frozen parcel manifest

```text
ca66ac0e19a0ff9a346b73df48fe882ea99067c24967ef0a280a2f2bdf05c814  go/internal/metadata/driftpolicy.go
0cd2f2165ca7b39fb485e2769270cd2aee76b201c1a38e05039a4865c5456a7f  go/internal/metadata/driftpolicy_runtime_test.go
b0c23c755dff3b2e51d6964667833c3727a8abaaa4fadbed1c87669312100111  go/internal/metadata/driftpolicy_node_oracle_test.go
```

The handoff is frozen separately because including its own digest here would
be self-referential. The builder report accompanying review supplies that
SHA-256.

## Files Changed

- `go/internal/metadata/driftpolicy.go`: completes runtime compilation,
  path parsing/matching/normalization, matching methods, identity-safe stale
  accounting, defensive snapshots, and the bounded exported API.
- `go/internal/metadata/driftpolicy_runtime_test.go`: ports all discovered
  runtime/helper vectors, records the semantic probes, and pins Go boundary,
  ordering, identity, snapshot, nil/zero, and concurrency behavior.
- `go/internal/metadata/driftpolicy_node_oracle_test.go`: freezes seven exact
  Node v24.15.0 matcher/stale results as JSON bytes.
- `docs/review-handoffs/go-wave7-drift-policy-runtime.md`: this handoff.
- Intentionally untouched: `go/internal/resthttp`, `go/cmd/iw`, `Makefile`,
  `docs/go-runtime-plan.md`, `go/internal/artifacts`, every other metadata
  file, and all consulted Node source/tests.

## Source Inputs Consulted

- Provider schemas, OpenAPI/API contracts, provider source, and pack metadata:
  N/A.
- Node implementation at base `c32479d6fdee4ce44944c4b8b8971900b97beda3`:
  - `node-src/domain/drift-policy.ts`, SHA-256
    `8479aa9faf93ece45238265435072b10ef88a43101da75f4ed6827b01bbc68c9`;
  - `node-src/domain/policy-paths.ts`, SHA-256
    `101330ac110e462baa7d2b98651b51e5ed4abf06c23f0fa3b4a19d0638865289`.
- Production-consumer evidence: `state-project.ts`,
  `generated-config-policy.ts`, `plan-eval.ts`, `plan-assessment.ts`,
  `assessment-guidance.ts`, `import-oracle.ts`, `plan-policy.ts`, and
  `metadata/packs.ts`.
- Direct-import test inventory from
  `rg -l 'domain/(drift-policy|policy-paths)\.js' node-tests/*.test.ts`:
  `drift-policy.test.ts`, `plan-eval.test.ts`, `state-project.test.ts`,
  `generated-config-policy.test.ts`, `import-oracle.test.ts`, and
  `adoption-meta.test.ts`.
- Additional requested/runtime-method test inventory:
  `plan-assessment.test.ts` and `plan-policy.test.ts`.
- Test source SHA-256 values:
  - `drift-policy.test.ts`:
    `99ca0c3803fd8e203f381f5c46d874ab0ca05a12e8f6576c6b675e3323cafd63`;
  - `plan-eval.test.ts`:
    `baa414fc7ad25cd1985f334216f77424f57451d3227ba15cb61b42abfce8bd08`;
  - `plan-assessment.test.ts`:
    `bafaad16c2564b4016ef8a9178e84352e00dbd88688ad132044685273e94a7c3`;
  - `plan-policy.test.ts`:
    `e41f933bf04add7c3a8bfda7f02d1a23242b97082c7ffa6ebb1b757546689cae`;
  - `state-project.test.ts`:
    `61fd9cdc7b4895900d6331fd0e66d6828eb0d04a4626345bc3a622e9e9d0c744`;
  - `generated-config-policy.test.ts`:
    `b9eace2f1cc8819f70ea8a0a77684829131d1d48badf9655d2df0608bdce5e60`;
  - `import-oracle.test.ts`:
    `1e10f90536f5f24554404c4cb02ef4f9bb93da73f3863dada64e51eaf7fa69b2`;
  - `adoption-meta.test.ts`:
    `4421ecb7efaf7fb2784f62ed0d81a132ba2968caa56d6a43f411ab6f21c31227`.
- Existing Go conventions: `go/internal/metadata/packs.go`,
  `go/internal/metadata/validation.go`, and
  `docs/review-handoffs/go-wave7-bounded-files.md`.
- Live semantic evidence: Node v24.15.0/esbuild 0.25.12 probes. Each exact
  command, the mandatory `--external:lossless-json` flag, temporary external
  dependency resolution, and observed result are pinned beside the relevant
  Go test.

## Generated artifacts

- Reports, schemas, committed fixtures, snapshots, and demo/lab outputs: none.
- Artifact drift intentionally expected: none.
- The exact Node runtime oracle is reviewed test source, not generated
  production output.

## Expected Delta

- `NewDriftPolicy` and `ParsePolicyPath` are the only new recovery boundaries;
  both use the package-standard `recoverMetadataError` convention. Pure
  matching/accessor wrappers do not recover.
- `PolicyMode` and its five constants expose the source mode union without
  exporting mutable mode-order state.
- `PolicyEntry`, `PolicyEntry.Data`, `DriftPolicy.Entries`, and
  `DriftPolicy.MarkMatched` expose entry-driven consumer behavior while hiding
  identity and storage internals.
- `StalePolicyEntry`, `StaleEntriesOptions`, and `DriftPolicy.StaleEntries`
  expose assessment/accounting output and filters.
- `DriftPolicy.ProjectionOmits` and `DriftPolicy.ToleratesPlanPath` expose the
  state-projection and plan-evaluation matchers.
- `ParsePolicyPath`, `PolicySelectorMatches`, and `NormalizePolicyPath` are
  exported because future state, generated-config, and assessment-guidance
  consumers call their Node counterparts directly.
- `formatPolicyPath`, marker/equality helpers, compiled entries, raw policy
  data, error implementation details, and mode ordering remain unexported
  because no future plan/adopt consumer calls them.
- Every export is a PascalCase wrapper over camelCase logic and has a doc
  comment naming the exact Node source file and symbol/property.
- The Go boundary snapshots validated input and detaches returned entry data
  and slices. This is deliberately narrower than TypeScript's runtime-mutable
  `readonly` surface and is a primary review target.
- Existing metadata validation behavior remains intact. No consumer output
  changes until later parcels wire this foundation.

## Vector Inventory

- Ported source runtime/helper occurrences: 66/66.
  - 42/42 from all eight tests in `node-tests/drift-policy.test.ts`;
  - 24/24 downstream unit-boundary occurrences: 2 plan-assessment, 8
    plan-eval, 3 state-project, 7 generated-config, 2 import-oracle, and 2
    plan-policy.
- Probe-pinned semantic observations: 34, intentionally overlapping some
  source vectors: 7 exact matcher/stale JSON cases, 9 normalize/format
  outputs, 5 wildcard numeric-category outcomes, 11 mutation/snapshot
  outcomes, and 2 cross-policy object-identity outcomes.
- Node tests/assertions judged outside this metadata unit parcel:
  - `generated-config-policy.test.ts`: HCL/schema/value predicates, edit
    counts, and the consumer's decision about which entry to mark; all seven
    explicit stale-accounting results are ported here;
  - `import-oracle.test.ts`: Terraform sequencing and corrected-plan
    authorization; both explicit policy stale results are ported here;
  - `adoption-meta.test.ts`: injects an empty policy into legacy-metadata
    failure plumbing but exercises no matcher, entry, mark, or stale result;
  - remaining `state-project.test.ts`: schema traversal, conditional value
    equality, projection effects, and projection errors;
  - remaining `plan-eval.test.ts`: higher-level classification, findings, and
    diff ordering; identity/sensitivity matcher inputs are ported here;
  - remaining `plan-assessment.test.ts`: bounded reads, hashes, Terraform
    execution, and report assembly;
  - remaining `plan-policy.test.ts`: byte binding, hashing, replacement,
    symlink, and recheck behavior.

## Invariants Claimed

- Validation limits, duplicate/conflict checks, accepted modes, error text,
  and manifest integration remain compatible with the earlier validation-only
  port.
- A selector matches only an equal-length concrete path. Wildcards match
  JavaScript integer-valued numbers, including negative integers and negative
  zero, but not strings, fractions, NaN, Infinity, `json.Number`, or
  `*big.Int` concrete segments.
- Exact indexes beyond `Number.MAX_SAFE_INTEGER` parse as bigint and never
  equal a JavaScript-number analogue.
- Canonical-equivalent exact selectors retain their first declaration. Exact
  and wildcard overlaps resolve by original declaration order.
- Projection omission stops at and marks only the first matching declaration.
- Match accounting follows source-entry object identity, not value equality.
  The same raw entry object crosses policy instances and modes; separately
  allocated equal entries, zero handles, and invalid handles do not mark.
- Strong references keep raw source objects alive for as long as their
  identities can be compared, preventing map-pointer reuse.
- Stale order is resource type by source code point, requested mode order
  (source `MODES` when nil/empty), then declaration order. Empty resource and
  mode filters select all entries.
- Constructor input, returned entry data, and returned slices cannot mutate Go
  policy meaning. Nil policy receivers fail closed; zero entry data is nil.
- Match mutation and stale reads are race-free; a stale result uses one copied
  snapshot of the matched-ID set.
- Generic/source precedence, provenance, ambiguity classification, generated
  evidence, reports, and provider-readiness counts are N/A to this parcel.

## Tests Run

- `gofmt -l internal/metadata/driftpolicy.go internal/metadata/driftpolicy_runtime_test.go internal/metadata/driftpolicy_node_oracle_test.go`
  produced no output and exited 0.
- `GOCACHE=/tmp/infrawright-go-cache go vet ./internal/metadata` produced no
  output and exited 0.
- `GOCACHE=/tmp/infrawright-go-cache go test ./internal/metadata` produced:

  ```text
  ok  	github.com/dvmrry/infrawright-dev/go/internal/metadata	(cached)
  ```
- `GOCACHE=/tmp/infrawright-go-cache go test -race ./internal/metadata`
  produced:

  ```text
  ok  	github.com/dvmrry/infrawright-dev/go/internal/metadata	2.594s
  ```
- The managed-sandbox `go test ./...` attempt reached all packages but failed
  only where untouched `cmd/iw` and `internal/resthttp` tests tried to bind
  loopback listeners (`bind: operation not permitted`). The same required gate
  was immediately rerun outside that network sandbox; no sibling code changed.
- Final `GOCACHE=/tmp/infrawright-go-cache go test ./...` rerun produced:

  ```text
  ok  	github.com/dvmrry/infrawright-dev/go/cmd/iw	37.009s
  ok  	github.com/dvmrry/infrawright-dev/go/internal/artifacts	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/canonjson	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/cliargs	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/collectors	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/deployment	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/envgen	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/metadata	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/modulesgen	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/nodefserr	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/procerr	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/pyoserr	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/pypath	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/pyunicode	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/resthttp	6.029s
  ok  	github.com/dvmrry/infrawright-dev/go/internal/roots	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/terraformcmd	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/tfrender	(cached)
  ok  	github.com/dvmrry/infrawright-dev/go/internal/transform	(cached)
  ?   	github.com/dvmrry/infrawright-dev/go/internal/transformrun	[no test files]
  ```
- Tests intentionally not run: provider, Terraform, CLI, HCL/schema, and
  report integration tests that belong to unwired future consumers. The
  existing full Go module is still covered by the final gate.

## Known deferrals

- Plan evaluation, plan assessment, state projection, generated-config, and
  adopt wiring remain in their own future parcels.
- Exporting `formatPolicyPath` is deferred until a concrete production
  consumer appears.
- Consumer-owned classification, HCL/schema traversal, Terraform sequencing,
  hashing, file binding, and report assembly need their own vector ports when
  those consumers are implemented.

## Review Focus

- Verify the frozen four-file scope before reviewing semantics.
- Attack whether every export is required by an identified future consumer,
  remains a thin wrapper, and names the exact Node symbol/property in its doc
  comment.
- Attack the package-standard `recoverMetadataError` boundary and unchanged
  validation error text.
- Reproduce every esbuild probe with `--external:lossless-json`; bundling that
  dependency duplicates its class and invalidates `instanceof` observations.
- Attack JavaScript-number modeling: negative integers/zero, fractional and
  non-finite values, every Go integer family, `json.Number`, safe-integer
  boundaries, and bigint selectors.
- Attack quoted `"*"`/`"[]"` ambiguity, escaping, segment-marker collisions,
  exact/wildcard precedence, and canonical aliases such as `field[0]` versus
  `field[00]`.
- Attack raw-map identity across policies/modes, the strong-reference lifetime
  argument, equal-but-distinct maps, zero/invalid handles, and pointer reuse.
- Attack the deliberate immutable-snapshot divergence. Node's projection and
  stale paths observe later entry/list mutations while its plan indexes retain
  constructor-time selectors; Go freezes all three uniformly.
- Attack stale ordering/filter defaults, repeated/unknown filters, nil
  receivers, and concurrent match/stale snapshots.
- Verify that every direct-import/runtime Node test is either represented by a
  unit vector or excluded only for explicitly consumer-owned HCL/schema,
  Terraform, classification, hashing, file-binding, or report behavior.
