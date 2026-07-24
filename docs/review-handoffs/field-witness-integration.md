# Builder Review Handoff: Field Witness Integration

## Intent

- What problem does this change solve? Consolidate the field-lineage AST spike into the existing in-process Go source analyzer and remove the retired standalone collector, Python spike, and generated corpus from the branch.
- What user-visible or maintainer-visible behavior should change? `sourceanalysis` gains qualified and explicitly unverified APIs that derive corroborating Terraform-schema, provider `schema.Schema`, `ResourceData.Set`, acceptance-HCL, and `TestCheckResourceAttr` witnesses for selected fields. Unsupported or ambiguous source shapes are diagnostic instead of being treated as success.
- What behavior must stay unchanged? `source-evidence-report-v1`, source-operation bundles, CLI/Make/CI surfaces, readiness classifications/counts, pack behavior, and existing authoring output remain unchanged. The new report is diagnostic and cannot change qualification or readiness.

## Base / Head

- Base: `origin/main` at `5db8ff747f5cdbe768e60905d581d0738ea24127`.
- Head: `feature/field-lineage-spike`, published as the consolidated implementation plus focused review-hardening commits; resolve the exact head with `git rev-parse HEAD`.
- Diff command: `git diff origin/main...HEAD`.

## Files Changed

- Files: `README.md`; `go/internal/authoring/sourceanalysis/analyze.go`; new `go/internal/authoring/sourceanalysis/field_witnesses.go`; new `go/internal/authoring/sourceanalysis/field_witnesses_test.go`; this handoff; deletion of all five tracked files under `tools/source-evidence-ast/`.
- Files intentionally left untouched: frozen contracts and validators; `sourceoperation`; commands; Make targets; CI; packs; generated reports. The pre-existing untracked `reports/` directory is user-owned and untouched.

The historical branch-only Python scripts, generated field-lineage corpus, provider-lab notes, and three old review handoffs are absent from the consolidated `origin/main...HEAD` diff.

## Source Inputs Consulted

- Provider schemas: Focused Terraform provider-schema JSON in `field_witnesses_test.go` plus the checked-in `packs/zia/schemas/provider/zia.json` for the real-source probe; no new live provider-schema dump was generated.
- OpenAPI/API contracts: None; field witnesses do not infer API behavior.
- Provider source files: `zscaler/terraform-provider-zia` tag `v4.7.26`, commit `6e6509f001ca71adcedfd4884250d09227395bf0`: `main.go`, `zia/provider.go`, `zia/common.go`, `zia/common/resourcetype/resource_type.go`, and the resource/test pairs for firewall IP source groups, firewall network services, location management, and URL categories.
- Pack metadata: None.
- Existing docs or design records: `README.md`, `docs/provider-readiness.md`, `docs/adversarial-review.md`, and the review templates/prompts under `docs/`.
- Other source evidence: Existing `sourcebind`, `sourceanalysis`, and source-evidence contract implementations and tests. The retired `tools/source-evidence-ast` collector was read only to identify the small reusable AST concepts before deletion.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: Runtime-only focused test files in `t.TempDir`; no checked-in fixture directory.
- Snapshots: None.
- Demo or lab outputs: None. A temporary local probe ran the analyzer against the ZIA source files listed above and was removed after verification.
- Artifact drift intentionally expected: None. Existing report/count/golden output must not change.

## Expected Delta

- Expected behavior change: Callers may opt into `AnalyzeFieldWitnesses` or `AnalyzeUnverifiedFieldWitnesses`. Fields are `corroborated` only when at least two recovered witness classes are present and no direct conflict is found; direct flag or unambiguous repeated-block count disagreement is `conflicting`; a single class is `untested`. Acceptance config witnesses distinguish attributes from blocks so an attribute declaration count is never compared with a collection element count. Missing acceptance tests remain silence.
- Expected report/count/coverage changes: None in existing reports, readiness counts, or coverage. The new in-memory report is not wired into those authorities.
- Expected generated-output changes: None in existing outputs. The opt-in field-witness report identifies acceptance config syntax as `attribute`, `block`, or `mixed`.
- Expected no-op areas: Existing source evidence, OpenAPI mapping, provider probes, reconciliation, adoption, generation, packs, commands, and CI.

## Invariants Claimed

- Evidence must not be silently dropped: Unsupported constructors/schema helpers, dynamic schema names, unresolved Read callbacks, dynamic `ResourceData.Set` keys, malformed HCL, dynamic check paths, and ambiguous formatted resource blocks produce diagnostics. Missing acceptance files produce no negative claim.
- Generic matcher evidence must not outrank source-backed evidence: No matcher or precedence path is changed; all new witnesses are explicitly source-derived and diagnostic.
- Source precedence/provenance must remain explicit: Analysis consumes only defensive `sourcebind` snapshots and carries source trust, manifest identity when qualified, input-provenance identity, and portable source locations. It performs no path reads, network access, or tool invocation.
- Ambiguity must stay classified instead of being coerced to success: Direct Terraform/provider flag disagreements and one unambiguous repeated-block config/check count disagreement become `conflicting`; attribute cardinality and unpaired/mixed acceptance counts are not inferred from declaration occurrences. Ambiguous source shapes remain diagnostics.
- Provider-readiness counts must stay explainable: Existing counts are unchanged because this spike does not feed readiness policy.
- Adoption safety invariants: No adoption path or output is changed.

## Tests Run

- Commands: `go test ./internal/authoring/sourceanalysis`; `go test -race ./internal/authoring/sourceanalysis`; temporary real-source probe `go test ./internal/authoring/sourceanalysis -run '^TestProbeRealZIAAcceptanceCounts$' -count=1 -v` (probe file removed); `go test ./...`; `go vet ./...`; `make check`; `git diff --check`.
- Relevant output summary: All commands passed. The focused fixture mirrors the real ZIA authority shape (`main.go` `plugin.Serve` -> `zia.ZIAProvider`, with `p := &schema.Provider{...}; return p`) and covers schema/helper flags and validators, qualified `ResourceData.Set` receiver filtering, dynamic-key diagnostics, formatted HCL, three collection blocks with one `end`, exact count assertion association, attribute declaration versus collection-cardinality separation, unrelated-resource assertion rejection, direct conflict reporting, cancellation, and deterministic JSON rendering. A removed temporary probe against the real source and checked-in ZIA schema recovered provider-schema, read-back, and acceptance witnesses for all four probed resources: IP source groups (5 fields), network service (19), location management (66), and URL categories (33). Real `ip_addresses = [three values]` plus `ip_addresses.# == 3` is corroborated without comparing one attribute declaration to three elements; unsupported nested helper values remain explicit diagnostics.
- Tests not run and why: No live Terraform acceptance apply, provider binary execution, live provider-schema generation, or live ZIA API call; those would require external tooling, credentials, or mutation and the new code statically observes bound source only.

## Known Deferrals

- Deferred work: Adding a versioned artifact contract and feeding reviewed field witnesses into source-operation/readiness policy.
- Reason it is safe to defer: No existing authority consumes the new report, so no readiness or generated-output semantics can change before that policy is designed and reviewed.
- Follow-up owner or trigger: A follow-up change that explicitly decides field-witness precedence, artifact versioning, and downstream disposition policy.

Additional parser limits deliberately deferred: plugin-framework schema declarations; indirect/local-variable `*schema.Resource` constructor returns (the real ZIA provider-factory shape `p := &schema.Provider{...}; return p` is supported); dynamically constructed nested schema values such as the ZIA location and URL-category `id` fields; helper-propagated `ResourceData.Set`; non-literal check paths; acceptance tests outside the exact constructor-companion file; and complex `fmt.Sprintf` verbs beyond the length-preserving common cases. Unsupported shapes identified at selected parsing seams are diagnostic; unsearched helper/test locations and absence of a matching test file remain silence.

## Review Focus

- Highest-risk files or paths: `go/internal/authoring/sourceanalysis/field_witnesses.go`, especially provider schema helper resolution, HCL association, state-path normalization, and disposition calculation.
- Specific assumptions to attack: Omitted Go schema booleans equal `false`; Terraform block schemas do not expose Optional/Computed flags; a sole formatted resource block in the exact companion test file is attributable but remains marked by its raw source location; Terraform schema plus provider declaration count as distinct corroborating witness classes without implying runtime round-trip.
- Source evidence the reviewer should verify: ZIA `tag` is Optional+Computed with a validator and is set from `resp.Tag`; `src_tcp_ports` is an Optional set with Optional `start`/`end`; the Read callback sets the port collection; acceptance HCL contains three source-port blocks and only one `end = 5005`; checks assert only `src_tcp_ports.# == 3`, not the nested `end` value. IP source-group HCL declares `ip_addresses` once with three elements and checks `ip_addresses.# == 3`; this is corroboration, not a one-versus-three conflict.
- Generated artifacts the reviewer should compare: Confirm no tracked generated report, schema, fixture, snapshot, or corpus remains in the effective diff and no existing golden output changes.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: Attribute declaration occurrences versus collection cardinality; dynamic resource addresses accepted from companion tests; multiple HCL config literals; repeated or inconsistent count checks; numeric state-path normalization; provider helper ambiguity/recursion; schema fields represented as block types versus nested attributes; direct-only Read callback scanning; and the distinction between configured presence, count assertion, and proven post-read value.
