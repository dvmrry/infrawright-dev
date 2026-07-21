# Singleton-state topology G2 builder handoff

Status: APPROVED after fresh adversarial review, blocking remediation, and a
targeted recheck with no remaining findings. The coordinator has not yet
committed or pushed this parcel.

## Intent

- Make declared pack references resolve through singleton remote state by
  default. Only an explicit `cross_state_references: false` retains literal
  IDs.
- Remove the unreachable same-root/module binding path left after G1 retired
  grouped roots.
- Reject every declared reference cycle during pack validation, with a stable
  full cycle path and the prescribed literal-ID/operator-expression remedy.
- Fail closed if an invalid programmatically constructed cyclic root bypasses
  metadata validation; the old Node behavior that continued by alphabetically
  breaking cycles is intentionally retired.
- Replace the topology-dependent portion of the frozen-Node transform gate
  with complete Go-v2 command goldens while retaining the full Node transform
  differential in explicit legacy literal-ID mode.
- Preserve artifact rendering, Apply safety, the complete-plan gate, frozen
  Node bytes, catalog bytes, and all behavior outside reference promotion.

## Base / Head

- Base: `cf5854b5be8d94024552b886478faabb3837e4a8`
  (`Implement singleton-state topology v2 core`).
- Head: uncommitted working tree on
  `feature/go-authority-singleton-state-v2`; no implementor commit exists.
- Diff command: `git diff cf5854b5be8d94024552b886478faabb3837e4a8`
  plus `git status --short` for the new authority/cycle fixtures.
- Frozen Node executable for retained differentials:
  `dist/infrawright-cli.mjs` SHA-256
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`.

## Files Changed

- Runtime behavior:
  `go/internal/deployment/deployment.go`,
  `go/internal/envgen/{environment_generator,reference_topology}.go`,
  `go/internal/metadata/packs.go`,
  `go/internal/tfrender/transform_artifacts.go`, and
  `go/internal/transform/selection.go`.
- Focused tests in the same packages plus
  `go/internal/assessment/{inputs_test,exact_plan_apply_test}.go`.
- Command gates:
  `go/cmd/iw/transform_differential_test.go`,
  `go/cmd/iw/v2_vertical_slice_test.go`, and new
  `go/cmd/iw/v2_transform_authority_test.go`.
- New cycle corpus:
  `go/internal/metadata/reference_cycles_test.go`.
- New exact Go-v2 command goldens:
  `go/cmd/iw/testdata/v2_transform/**` (46 complete output-tree files plus
  exact stdout/stderr; 48 files and 56,909 content bytes total).
- Intentionally untouched: pack reference declarations, provider schemas,
  root catalogs, Node/TypeScript source and bundle, canonjson, Terraform
  invocation, evidence/report renderers, live-provider code, release routing,
  and G3 adopt/staging/plan lifecycle cleanup.

## Source Inputs Consulted

- Provider schemas: committed schemas were exercised by full transform,
  envgen, assessment, and command suites; none changed.
- OpenAPI/API contracts: N/A; this parcel changes declared topology semantics,
  not API collection or source evidence.
- Provider source files: N/A.
- Pack metadata: all active `pack.json` reference declarations. The committed
  graph has exactly seven directed fields: four ZPA, one ZIA, and two ZCC.
- Existing docs/design: `docs/singleton-state-topology-v2.md` D2/D3 and G2,
  `docs/go-runtime-v2.md`, and the G1 authority handoff.
- Other evidence: the exact frozen Node executable for the retained
  literal-ID transform differential and the committed full demo pull corpus.

## Generated Artifacts

- Reports: none.
- Schemas: none changed.
- Fixtures: new synthetic self, mutual, long, duplicate-edge, and
  cross-manifest cycle fixtures; default/explicit/disabled reference fixtures;
  a positive exact-Apply reference-output fixture.
- Snapshots/goldens: new complete Go-v2 default-cross-state transform
  transcript and 46-file output tree. The command is run twice before golden
  comparison to prove repeatability. Only exact repository/workspace path
  prefixes are normalized.
- Demo/lab outputs: the committed demo input is transformed into test-owned
  temporary directories only. No checked-in demo output outside the new golden
  directory changes.
- Expected artifact drift: with omitted roots, inferred lookup sidecars and
  remote-state generated-expression artifacts now appear where declared
  references have resolvable demo data. With explicit false, output remains
  byte-identical to the frozen Node literal-ID mode.

## Expected Delta

- Omitted roots, an omitted provider, an omitted setting, and explicit true
  all select cross-state mode. Explicit false alone disables it.
- All seven current edges resolve as singleton cross-state dependencies by
  default. The ZIA-only explicit false fixture filters only its edge.
- Generated bindings use only `data.terraform_remote_state`; no same-root
  `module.<referent>.items` branch or same-root mode constant remains.
- Disabling references removes stale generated binding files and emits the
  updated literal-ID diagnostic.
- Declared cycles fail pack validation before transform/envgen. A defensive
  runtime boundary also fails rather than returning a partial order.
- Generic exact-Apply safety fixtures explicitly disable unrelated ZIA
  references; a separate omitted/default ZPA test proves reference-output
  authorization reaches exactly one Apply.
- No catalog, backend-key, provider-read, Terraform command, REPORT,
  fingerprint, evidence, or non-reference artifact behavior changes.

## Invariants Claimed

- Evidence must not be silently dropped: N/A; evidence paths are unchanged.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success: N/A.
- Provider-readiness counts must stay explainable: N/A; no readiness output
  changes.
- Adoption safety invariants: the complete-plan gate remains unchanged;
  reference outputs require authorized planned root-module values; generic
  Apply tests do not bypass that contract, and the new positive test exercises
  it directly. No live Apply or provider call is present in any test.
- Reference iteration and cycle discovery are deterministic under
  `canonjson` Python-string ordering.
- A declared cycle cannot be converted into a partial transform order or an
  alphabetic recovery note.
- Explicit false is a real compatibility escape: no remote-state edge,
  generated binding, or stale binding artifact survives it.
- The frozen Node differential is not weakened: exit, stdout, stderr, and the
  complete output tree are still compared, with both runtimes given the same
  explicit false deployment.
- The new Go-v2 authority is self-contained and byte-exact; it does not depend
  on a digest or delta from the v1 oracle.

## Tests Run

- `gofmt -l cmd/iw internal/{assessment,deployment,envgen,metadata,tfrender,transform}`:
  empty.
- `go vet ./...`: pass.
- `go test ./... -count=1`: pass, all packages.
- `go test -race ./internal/deployment ./internal/metadata ./internal/transform ./internal/tfrender ./internal/envgen ./internal/assessment -count=1`:
  pass.
- Focused package and remediation tests in deployment, metadata, transform,
  tfrender, envgen, assessment, and cmd/iw: pass.
- `make v2-authority`: pass; includes the new complete transform authority.
- `make differential`: pass; includes the retained full transform Node
  differential with explicit false on both sides and does not rebuild Node.
- `make check-root-catalog`: pass; v2 catalog bytes remain unchanged.
- Golden path leak scan: no private temp or user path remains; only the exact
  `<REPOSITORY>` and `<V2_TRANSFORM_WORKSPACE>` placeholders appear in the
  normalized transcript.
- `git diff --check`: pass.
- Not run: credentials, provider APIs, remote backends, live Terraform Apply,
  state inventory, Zscaler live qualification, or Kubernetes. Those remain
  outside G2; Kubernetes is inaccessible from the current location.

## Known Deferrals

- G3 still owns adopt/staging memoization and the remaining lifecycle sweep
  over assessment/plan/report state-unit consumers.
- Credential-free qualification over all 151 generated types and exact backend
  key-set comparison follows G3.
- The live cross-state ZPA/ZIA/ZCC checks and Kubernetes exact-Apply
  qualification remain access/human gated.
- `cross_state_references: true` warning/removal is a later release policy;
  this parcel continues accepting it and proves it equals omission.
- Node archive and release-default routing remain governed by
  `docs/go-cutover-roadmap.md`.

## Review Focus

- Reconstruct the seven committed reference edges from pack metadata and
  verify the default topology includes exactly all seven, sorted and without
  provider leakage under explicit false.
- Attack cycle validation priority and graph completeness: self, mutual, long,
  duplicate field, cross-manifest, malformed-reference precedence, and a
  cyclic root fabricated after validation.
- Verify no same-root/module binding path or constant remains and every emitted
  reference expression is remote-state based.
- Compare every new golden byte and file path to an independently rerun full
  demo transform. Confirm path normalization cannot mask a non-path drift.
- Confirm the frozen Node transform differential still compares identical
  modes and retains full exit/transcript/tree strength.
- Inspect stale binding cleanup under explicit false and ensure ignored stale
  inputs cannot leak into generated HCL.
- Verify exact-Apply tests have not weakened reference-output authorization:
  the generic opt-out must be limited to fixture setup and the default ZPA
  positive path must pass the real contract before one Apply.
- Check that G2 did not silently absorb G3 lifecycle or release/cutover work.

## First adversarial review remediation

The fresh review returned **Request changes** on one blocking merge-precedence
defect and one non-blocking normalization risk. The reviewer independently
accepted default/false behavior, all generated drift, the retained frozen-Node
gate, and Apply safety.

1. Aggregate cycle validation originally unioned every raw declaration. A
   declaration shadowed by a later manifest could therefore invent a cycle
   absent from the effective reference graph. The loader now structurally
   validates each manifest, merges reference fields in deterministic manifest
   order with the same later `referrer+field` overwrite used by transform, and
   runs cycle detection only on that effective graph. Standalone
   `ValidatePackManifest` still validates its own graph immediately. Exact
   regressions prove both a genuine cross-manifest cycle and the reviewer's
   `A.target -> B`, shadowed `A.target -> C`, `B.back -> A` case; the latter
   loads and orders exactly `C, A, B`.
2. Transform transcript normalization checked only a matched path's right
   boundary. It now requires a valid left delimiter/start too. Positive
   start/space/quote cases and negative left- and right-adjacent token cases
   pass; all 48 committed golden files remain byte-unchanged.
