# Builder Review Handoff: data-only referents second-recheck fixes

This handoff follows docs/review-handoff-template.md. It covers the
uncommitted working tree for the second-recheck findings in
solmax-recheck2-report.md. The builder stops here at ready for adversarial
review and does not self-approve the change.

## Intent

- What problem does this change solve?

  It closes the remaining freshness, evidence-binding, ambiguity, capture
  regeneration, and test-hygiene gaps in the data-only referent assessment
  contract. A saved plan must have a qualified, refresh-enabled creation
  attestation before a non-no-op data reference output can authorize IDs.
  Those IDs must be bound exactly by prior-state instance index to
  output_changes.after key. Data referent names must be unique under
  strings.EqualFold before slugging or ID fallback.

- What user-visible or maintainer-visible behavior should change?

  iw plan passes direct -refresh=true even when TF_CLI_ARGS_plan requests
  -refresh=false. Saved plans created by the engine carry a tfplan.attestation
  sibling containing the qualified Terraform version, exact plan argv,
  refresh policy, and plan SHA-256. Data-kind changed-output authorization
  refuses missing, malformed, stale, refresh-disabled, or unqualified
  attestation evidence, and refuses rekeyed or swapped output maps.
  Duplicate names that case-fold equally, including non-ASCII names and
  names whose slugs are empty, are rejected before key generation.

- What behavior must stay unchanged?

  Managed-kind plans without an attestation retain their pre-branch behavior;
  a managed attestation is validated when present. Data no-op output changes
  still do not require new authorization. Raw capture bytes are not rewritten
  or fabricated. Existing provider packs, source-operation mappings,
  readiness counts, and unrelated assessment multiset logic are untouched.

## Base / Head

- Base: 2e195c6ce76c534c900e05e7852510b10370c927
  (Ground the data evidence contract in Terraform reality and scope
  selection by operation)
- Head: uncommitted working tree on branch claude/data-only-referents
- Diff command: git diff 2e195c6ce76c534c900e05e7852510b10370c927 --
  plus direct inspection of untracked attestation, capture-scenario, and
  handoff files
- Git state: no stage, commit, push, branch, or other git write was performed

## Files Changed

- Files:

  - go/internal/plan/attestation.go (new)
  - go/internal/plan/lifecycle.go and lifecycle_test.go
  - go/internal/plan/evidence.go and evidence_test.go
  - go/internal/plan/contract.go and contract_data_referent_test.go
  - go/internal/assessment/assessment.go, exact_plan_apply.go, and
    data_referent_test.go
  - go/internal/transform/data_referent.go and data_referent_test.go
  - go/cmd/iw/cobra_test.go and commands_plan_test.go
  - go/internal/plan/testdata/provider_double_capture/README.md,
    dev_overrides.tfrc, gen-captures.sh, initial_create/main.tf
  - go/internal/plan/testdata/provider_double_capture/rekey_refusal/
    (the former output_only_change raw capture and scenario)
  - go/internal/plan/testdata/provider_double_capture/stale_refresh_false/
    (scenario configuration only)
  - go/internal/plan/testdata/provider_double_capture/fresh_after_stale/
    (scenario configuration only)
  - this handoff

- Files intentionally left untouched:

  - Provider packs, OpenAPI material, provider source-operation mapping, and
    provider-readiness/count artifacts.
  - The raw show.json bytes. The existing output_only_change/show.json was
    moved to rekey_refusal/show.json byte-for-byte; cmp verified equality.
    No new show.json bytes were fabricated.
  - .gocache, .gotmp, and .provider-double-bin. They are local ignored
    artifacts only. The earlier module-local generated caches were moved
    outside the workspace; final validation used the repository-level
    .gocache and .gotmp requested by the coordinator.

## Source Inputs Consulted

- Provider schemas:

  The sibling test-only provider-double module at
  go/internal/plan/testdata/provider-double/. It exposes capture_item with a
  required name and computed string id.

- OpenAPI/API contracts:

  None changed or required for these engine-only evidence fixes.

- Provider source files:

  The provider-double implementation and its deterministic v1/v2 ID behavior.

- Pack metadata:

  Existing data-referent metadata and the production reference-output contract
  materialization in assessment and exact-plan Apply.

- Existing docs or design records:

  The full raw report at
  /private/tmp/claude-501/-Users-dm-src-gh-dvmrry-infrawright-dev--claude-worktrees-provider-sdk-openapi-lineage-be2449/5fb83b2b-ce7d-4bae-b46d-aedc395902c6/scratchpad/solmax-recheck2-report.md,
  AGENTS.md, docs/adversarial-review.md,
  docs/adversarial-review-run-prompt.md,
  docs/review-handoff-template.md, and the previous data-only referent
  handoffs.

- Other source evidence:

  The committed provider-double captures, lifecycle fake-Terraform harness,
  raw Terraform plan-container characterization already present in the
  branch, and the existing artifact stable-file/digest conventions.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures:

  The scenario HCL for stale_refresh_false and fresh_after_stale is new.
  initial_create/main.tf now contains two items. The former
  output_only_change scenario is renamed to rekey_refusal. Its raw show.json
  is unchanged.

- Snapshots: None regenerated.
- Demo or lab outputs:

  The capture regeneration script builds a local provider binary under the
  ignored worktree .provider-double-bin directory. It was not executed in
  this sandbox because the request forbids Terraform/provider IPC.

- Artifact drift intentionally expected:

  The existing initial_create/show.json remains the old one-item capture until
  the coordinator runs the committed regeneration script. The test skips it
  loudly rather than accepting a fixture whose bytes do not match the new
  two-item scenario. stale_refresh_false/show.json and
  fresh_after_stale/show.json are intentionally absent pending coordinator
  capture generation.

## Expected Delta

- Expected behavior change:

  1. Saved-plan creation uses direct -refresh=true and emits the attestation
     sibling.
  2. Saved-plan evidence binds and rechecks the optional attestation.
  3. Data-kind non-no-op authorization requires a qualified attestation and
     exact prior-state index-to-ID map equality with output_changes.after.
  4. Managed-kind attestation absence remains compatible, while a present
     managed attestation is checked.
  5. Raw data names are rejected when any earlier name is EqualFold-equal.
  6. The modules generate test writes under its test workspace and asserts that
     the package source directory gains no modules/ entry.

- Expected report/count/coverage changes: None.
- Expected generated-output changes:

  Only the three coordinator-generated raw captures are expected to appear:
  refreshed initial_create, stale_refresh_false, and fresh_after_stale. The
  implementation does not generate or rewrite show.json.

- Expected no-op areas:

  Data no-op output validation, managed evidence without an attestation,
  provider packs, generic assessment multiset calculations, and all unrelated
  module-generation outputs.

## Invariants Claimed

- Evidence must not be silently dropped:

  A non-no-op data output claim cannot pass without a present, well-formed
  attestation whose plan_sha256 equals the saved plan, refresh is true, the
  Terraform version is 1.15.x, and the attested argv contains direct
  -refresh=true. The data output map must equal the exact map reconstructed
  from prior_state.values.root_module data instances.

- Generic matcher evidence must not outrank source-backed evidence:

  No generic matcher or provider source mapping changed. Data IDs come only
  from refreshed prior-state data instances; the prior-state engine-output
  projection, planned data resources, and resource_changes are not evidence.

- Source precedence/provenance must remain explicit:

  Managed kinds read planned_values.root_module. Data kinds read
  prior_state.values.root_module. The attestation is attached to the evidence
  object and carried into both the normal assessment classifier and exact-plan
  Apply classifier.

- Ambiguity must stay classified instead of being coerced to success:

  Data names are checked with strings.EqualFold on the raw name before
  SlugifyTransformKey or ID fallback. Exact key-to-ID equality rejects swaps,
  renamed keys, and rekeyed output captures. Distinct names that both produce
  empty slugs remain valid when their ID fallback keys are distinct.

- Provider-readiness counts must stay explainable:

  No readiness report, coverage count, generated module count, or provider pack
  contract vocabulary changed.

- Adoption safety invariants:

  These changes affect saved-plan evidence authorization only. No adoption
  side-effect lane was widened, and no data referent was made adoptable.

## Tests Run

- Commands:

  - Pre-fix exact-binding proof: with the old data-ID multiset/bag projection
    temporarily restored as an unsafe mutation, and the swap negative's
    prior-state output projection populated, run:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./internal/plan -run 'TestValidateAssessmentPlanBindsDataReferenceKeysToPriorStateIDs/two-item_swap' -count=1

    Result: failed as required with error = nil, want an error containing
    provider-observed resource IDs. The old multiset implementation therefore
    wrongly authorized the swapped key-to-ID map. The unsafe function and call
    were removed before the final run.

  - Final exact-binding regression:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./internal/plan -run 'TestValidateAssessmentPlanBindsDataReferenceKeysToPriorStateIDs|TestValidateAssessmentPlanRejectsProviderDoubleRekeyCapture' -count=1 -v

    Result: exact two-item positive passes; two-item swap, renamed keys, and
    rekey_refusal all refuse.

  - Pre-fix Unicode ambiguity proof: temporarily delete the seenNames /
    strings.EqualFold check from transform/data_referent.go and run:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./internal/transform -run 'TestTransformDataReferentRejectsUnicodeCaseFoldAmbiguity' -count=1

    Result: both the duplicate 東京 and Å/å subtests failed with error = nil,
    want Unicode case-fold ambiguity refusal. The check was restored before
    final validation.

  - Final focused implementation suite:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./internal/plan ./internal/transform ./internal/assessment ./cmd/iw -run '^(TestValidateAssessmentPlan|TestSavedPlanEvidenceAttestationIsBoundAndValidated|TestPlanTerraformDirectRefreshOverridesInheritedPlanArgs|TestCreatePlanTerraformEmitsExactCommands|TestTransformDataReferent|TestPlanCommandUsesExactTerraformArgv|TestTerraformPreflightUsesResolvedCobraCommandAndEffectiveFlags|TestMakeGenEnvPassesStateAwareFlag)' -count=1 -v

    Result: PASS. This includes the fake-Terraform assertion that inherited
    TF_CLI_ARGS_plan=-refresh=false cannot override the received direct
    -refresh=true argv, attestation validation/recheck, exact binding,
    Unicode ambiguity, and the modules output guard.

  - Validation promotion checks:

        gofmt -l $(rg --files -g '*.go' | sort)
        git diff --check
        sh -n go/internal/plan/testdata/provider_double_capture/gen-captures.sh
        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go build ./...
        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go vet ./...

    Result: all passed with canonical absolute workspace cache paths.

  - Required exact corpus gate:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./... -count=1

    Result: nonzero only because the sandbox cannot bind loopback listeners:
    cmd/iw/TestFetchRecordedTransport and
    internal/httptransport/TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds
    both fail at httptest listener creation with
    listen tcp6 [::1]:0: bind: operation not permitted. The canonical
    absolute path run had no source, temp-path, or fixture assertion failures.

  - All-other-tests promotion characterization:

        GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./... -count=1 -skip '^(TestFetchRecordedTransport|TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds|TestRealTLSVerificationFailureIsClassifiedAsCertificate|TestCABundleAllowsCommentAndBlankResidueLines|TestHTTPProxySelectedFromRealProcessEnvironment|TestNoProxyExemptsHostFromRealProcessEnvironment)$'

    Result: PASS for every package. The skip list contains only tests that
    create httptest HTTP/TLS listeners unavailable in this sandbox.

- Relevant output summary:

  Focused plan, transform, assessment, and cmd/iw tests are green. The
  all-other-tests tail is green for cmd/iw, internal/adopt, assessment,
  authoring, envgen, httptransport non-listener tests, modulesgen, plan,
  terraformcmd, transform, and the remaining packages. The module test
  generated 152 modules / 1064 files under the root .gotmp and left
  go/cmd/iw/modules absent.

- Focused regression and pre-fix/unsafe-mutation proof:

  The exact-binding swap negative passed under the temporary old multiset
  implementation, proving the pre-fix overclaim, and failed after the exact
  map implementation was restored. The Unicode duplicate tests passed only
  with the EqualFold check present. The direct-refresh fake harness recorded
  the exact plan argv and a separate version invocation. Attestation tests
  cover absent sidecar compatibility, malformed JSON, invalid/unsupported
  version, refresh=false, digest mismatch, and sidecar mutation during
  evidence preparation/recheck.

- Promotion efficiency:

  Wall-clock elapsed time was not measured. Focused regressions were run before
  the broad gate. Two exact repository-wide go test ./... -count=1 attempts
  were made: the first used a noncanonical relative GOTMPDIR and exposed
  literal go/../ path comparisons; the corrected absolute-path attempt failed
  only on the two loopback listener tests above. One scoped all-other-tests
  pass followed. No duplicate full corpus sweep was run after a passing
  superset.

- Tests not run and why:

  The provider-double regeneration script was not run because the coordinator
  explicitly forbids Terraform in this sandbox and the environment denies
  provider IPC. The coordinator must run it with Terraform v1.15.4 to produce
  the three missing raw captures.

- Exact tests skipping pending coordinator captures:

  1. TestValidateAssessmentPlanAcceptsProviderDoubleCaptures/initial_create
     — capture not generated: the committed show.json is still the old
     one-item fixture while initial_create/main.tf now has two items.
  2. TestValidateAssessmentPlanProviderDoubleFreshnessCaptureMatrix/stale_refresh_false
     — capture not generated:
     testdata/provider_double_capture/stale_refresh_false/show.json.
  3. TestValidateAssessmentPlanProviderDoubleFreshnessCaptureMatrix/fresh_after_stale
     — capture not generated:
     testdata/provider_double_capture/fresh_after_stale/show.json.

## Attestation Format

The engine writes the sibling file tfplan.attestation beside a saved tfplan.
It is JSON with a trailing newline, created with mode 0600. The accepted
format version is 1 and the exact five fields are:

    {
      "format_version": 1,
      "terraform_version": "1.15.4",
      "argv": ["plan", "-input=false", "-refresh=true", "-out=tfplan"],
      "refresh": true,
      "plan_sha256": "<lowercase 64-hex SHA-256 of tfplan>"
    }

argv excludes the Terraform executable path but preserves the exact argument
vector passed to it, including var-file and output arguments. Validation
requires a plan-leading argv, direct -refresh=true, no -refresh=false,
refresh=true, a 1.15.x version, and a plan_sha256 matching the stable saved
plan digest. A present sidecar is stable-file-read and rechecked with the plan;
sidecar appearance, removal, mutation, malformed content, or mismatch refuses
the evidence transaction. A missing sidecar is tolerated by managed-kind
assessment only; a non-no-op data-kind authorization refuses it.

## Known Deferrals

- Deferred work:

  Fresh coordinator-generated initial_create, stale_refresh_false, and
  fresh_after_stale raw captures; sandbox-blocked loopback listener tests; and
  the fresh-context adversarial review.

- Reason it is safe to defer:

  The first two capture classes require the explicit coordinator Terraform
  regeneration workflow and no fabricated bytes are permitted. Listener tests
  are environment-only and all non-listener tests pass. Adversarial review is
  the mandated stop point for this high-risk evidence change.

- Follow-up owner or trigger:

  The coordinator generates captures with gen-captures.sh after confirming
  Terraform v1.15.4, then reruns the three skipped subtests. A fresh Codex
  reviewer must use docs/adversarial-review-run-prompt.md, record findings
  with docs/adversarial-review-template.md, and must not edit files. Any
  accepted finding must map finding -> root cause -> fix -> regression ->
  verification before acceptance.

## Review Focus

- Highest-risk files or paths:

  - go/internal/plan/attestation.go:
    exact five-field parser, 1.15.x qualification, digest binding, and
    sidecar format.
  - go/internal/plan/lifecycle.go and evidence.go:
    direct refresh flag, plan/version command sequencing, sidecar creation,
    stable read/recheck, and managed absence compatibility.
  - go/internal/plan/contract.go:
    data-versus-managed source selection, exact index-to-ID map construction,
    no-op path, and attestation requirement.
  - go/internal/plan/contract_data_referent_test.go:
    provider capture loading, negative swap/rekey/renamed-key shapes, and
    capture-skip behavior.
  - go/internal/transform/data_referent.go:
    raw-name EqualFold ambiguity check before slug or ID fallback.
  - go/internal/plan/testdata/provider_double_capture/gen-captures.sh:
    exact Terraform version assertion, directory dev override, and stale/fresh
    command matrix.
  - go/cmd/iw/cobra_test.go:
    test-workspace modules output and package-source guard.

- Specific assumptions to attack:

  - A sidecar with a valid digest but an incomplete or misleading argv cannot
    enter the data authorization path.
  - Terraform 1.15.x is the empirically qualified range, and a present plan
    terraform_version must agree with the sidecar.
  - Prior-state resource index strings are the authoritative output keys and
    no remaining path reads the prior-state engine-output map as evidence.
  - A rekeyed output with the same multiset of IDs is refused, including the
    committed rekey_refusal capture.
  - EqualFold is applied to raw names before both slug collision handling and
    ID fallback, including non-ASCII and empty-slug cases.
  - The managed absent-attestation behavior is not accidentally tightened by
    evidence preparation or direct contract validation.
  - Capture regeneration cannot point Terraform at the binary file rather
    than its containing directory and cannot silently run on an unqualified
    Terraform version.

- Source evidence the reviewer should verify:

  Read the raw report, lifecycle fake-Terraform logs, the provider-double
  implementation, every committed show.json, the new scenario HCL, and the
  production contract/evidence call sites. Confirm that no fabricated show
  bytes were added and that the old multiset projection is absent from the
  data contract.

- Generated artifacts the reviewer should compare:

  Compare the byte-identical rekey_refusal show.json with the prior
  output_only_change capture, inspect the still-pending initial_create
  one-item capture against its now two-item HCL, and verify that the script's
  three missing output paths are the only expected pending artifacts.

- Edge cases that could silently overclaim, remap, drop, or weaken evidence:

  Missing or malformed sidecars; wrong digest; sidecar plan argv with
  refresh=false; Terraform 1.14 or 1.16; plan/sidecar version disagreement;
  sidecar mutation between Prepare and Recheck; two-item ID swaps; renamed
  output keys; duplicate data resource indexes; planned-only data resources;
  resource_changes-only data evidence; duplicate ASCII or Unicode-folded
  names; distinct empty-slug names; data no-op output changes; and an
  existing managed plan with no attestation.
