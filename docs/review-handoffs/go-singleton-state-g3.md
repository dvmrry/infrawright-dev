# Singleton-state topology G3 builder handoff

Status: APPROVED by fresh adversarial review with no blocking or non-blocking
findings. The coordinator has not yet committed or pushed this parcel.

## Intent

- Retire the adoption runner's unreachable logical-root batching mode now that
  every generated type is its own state root.
- Keep the one-resource loader layered over the existing plural Oracle
  transaction substrate; this parcel removes dead orchestration, not the
  fail-closed import/plan/show machinery.
- Make state-aware import staging snapshot each selected singleton in its own
  Terraform directory and backend key, only when an imports artifact exists.
- Remove the plan lifecycle's unreachable partial-group diagnostic.
- Require new saved-plan assessment transactions to contain exactly one member
  equal to the root label while retaining validation compatibility for already
  emitted v1 grouped assessment reports.
- Preserve artifact/report bytes, complete-plan/freshness enforcement, exact
  Apply behavior, and every provider/live boundary.

## Base / Head

- Base: `8aff06c85c4844f2602971bbb29394b1bbaa18f4`
  (`Promote singleton cross-state references`).
- Head: uncommitted working tree on
  `feature/go-authority-singleton-state-v2`; no implementor commit exists.
- Diff command: `git diff 8aff06c85c4844f2602971bbb29394b1bbaa18f4`.
- Frozen Node executable remains unchanged at SHA-256
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`.

## Files Changed

- Adoption runner and CLI composition:
  `go/internal/adopt/{runner.go,runner_loaders.go,runner_test.go}` and
  `go/cmd/iw/{commands_adopt_apply.go,commands_adopt_apply_test.go}`.
- Import staging and plan lifecycle:
  `go/internal/adopt/{import_staging.go,import_staging_test.go}` and
  `go/internal/plan/{lifecycle.go,lifecycle_test.go}`.
- Assessment input boundary and compatibility tests:
  `go/internal/assessment/{assessment.go,assessment_test.go,semantics_test.go}`.
- Active operator documentation:
  `docs/provider-labs/zia-adoption-followup-runbook.md` and
  `docs/review-handoffs/go-live-qualification-runbook.md`.
- Intentionally untouched: Oracle transaction/validation internals,
  `report.go`, exact-Apply production, plan contract/fingerprint logic,
  canonjson/tfrender, pack metadata, catalogs, generated goldens, Node source
  and bundle, release routing, provider authentication, and live-state code.

## Source Inputs Consulted

- Provider schemas: unchanged; exercised by the full Go suite.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: current singleton root topology and its seven reference
  fields; declarations are unchanged.
- Existing docs or design records:
  `docs/singleton-state-topology-v2.md` G3,
  `docs/go-runtime-v2.md`, and G1/G2 handoffs.
- Other source evidence: the pre-G1 adoption runner, the retained
  `ImportProviderStates` transaction, plan lifecycle, assessment schema
  validator, and active qualification runbooks.

## Generated Artifacts

- Reports: no report renderer or report schema changed. A regression explicitly
  proves historical grouped v1 assessment evidence remains valid.
- Schemas: none.
- Fixtures: focused in-memory/temp-directory lifecycle fixtures only.
- Snapshots/goldens: none changed.
- Demo or lab outputs: none changed.
- Artifact drift intentionally expected: none.

## Expected Delta

- `RunAdoptBatchOptions` no longer exposes a batch loader or batch-mode
  environment. `INFRAWRIGHT_ORACLE_BATCH_MODE` and its active runbook wiring
  are retired.
- Adoption visits the selected types in reference order and invokes only the
  per-resource `AdoptionStateLoader`. A failure remains isolated to its type.
- The single-resource default loader continues to call
  `ImportProviderStates` with one `OracleBatchResourceRequest`; plural Oracle
  types and their safety checks remain available inside that substrate.
- Adoption artifacts always use the singleton variable name `items`.
- State-aware staging performs one init/list pair per selected type that has an
  imports artifact. Move-only staging performs no Terraform work.
- Planning independently uses each singleton directory, one var file, and
  `<tenant>/<resource-type>.tfstate` backend key. A root with no config still
  skips exactly as before; a partial grouped root can no longer exist.
- New assessment transactions fail deterministically unless
  `members == [label]`. Other invalid-root inputs retain the previous generic
  diagnostic. The saved-report validator intentionally continues to accept
  historical grouped evidence.

## Invariants Claimed

- Evidence must not be silently dropped: report generation and validation
  remain unchanged except for an explicit compatibility regression.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success: N/A.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: pending-move checks still bracket provider reads
  and artifact publication; one type cannot publish another type's artifacts;
  unsupported/derived resources retain preflight/count behavior; the default
  loader retains the complete-plan-gated Oracle transaction.
- Staging cannot reuse one type's state addresses, directory, or backend input
  for another type.
- No test can reach credentials, a provider API, a remote backend, or Apply.

## Tests Run

- `gofmt` over every changed Go file: clean.
- `git diff --check`: pass.
- `go vet ./...`: pass.
- `go test -count=1 ./...`: pass, all packages.
- `go test -race -count=1 ./internal/adopt ./internal/plan ./internal/assessment`:
  pass.
- Focused normal/race tests for adopt, plan, assessment, and `cmd/iw`: pass.
- `make v2-authority`: pass.
- `make differential`: pass without rebuilding Node.
- `make check-root-catalog`: pass; v2 catalog remains unchanged.
- Not run: credentials, provider APIs, remote backends, live state, Kubernetes,
  or Terraform Apply. Kubernetes is unavailable at the current location and
  live qualification remains separately gated.

## Known Deferrals

- Credential-free 151-type topology/backend-key and seven-edge command
  qualification follows this parcel.
- Live cross-state ZPA/ZIA/ZCC qualification, backend inventory, and the
  disposable Kubernetes exact-Apply qualification remain access/human gated.
- The plural Oracle transaction naming is retained because it is still the
  tested transaction substrate used by the single-resource loader; renaming or
  replacing it has no value in this parcel.
- Release-default routing and Node archive remain governed by
  `docs/go-cutover-roadmap.md`.

## Review Focus

- Prove the removed batch path was unreachable under singleton topology and
  that the default behavior was already per-resource. Reject any accidental
  removal of `ImportProviderStates`, its complete-plan gate, or Oracle
  freshness/cleanup checks.
- Attack selection/reference ordering, failure isolation, pending-move
  ordering, unsupported/derived handling, unselected resource publication,
  and stable artifact bytes after the deletion.
- Verify no production Go or active runbook still reads or advertises
  `INFRAWRIGHT_ORACLE_BATCH_MODE`.
- Prove staging uses the correct singleton directory/backend configuration and
  cannot bleed state addresses across types. Check missing-import and move-only
  laziness.
- Compare the removed `MISSING_GROUP_CONFIG` branch with the retained no-config
  skip path and confirm diagnostic priority for reachable inputs is unchanged.
- Attack the assessment boundary: reject multi-member and label-mismatch
  transactions, preserve all pre-existing generic invalid-root diagnostics,
  and keep historical v1 grouped report validation intact.
- Confirm report, fingerprint, exact-Apply, canonjson/tfrender, catalogs,
  goldens, and frozen Node bytes are unchanged.

## Adversarial Review Result

- Verdict: **Approve**.
- Findings: none blocking and none non-blocking.
- The reviewer independently inspected the complete diff and reran formatting,
  vet, focused/race/full Go tests, the retained differential, and the v2 catalog
  check without editing the tree.
- The review specifically confirmed the removed batch path was unreachable,
  the single-loader/plural-Oracle safety substrate remains intact, and no
  report, artifact, fingerprint, exact-Apply, catalog, golden, or frozen-Node
  byte drift occurred.
