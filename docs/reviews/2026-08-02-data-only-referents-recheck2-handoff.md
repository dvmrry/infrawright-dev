# Builder Review Handoff: data-only referents third-recheck fixes

This handoff supersedes the earlier second-recheck inventory. It covers the
uncommitted working tree based on the reviewed committed head
`472316ef653426b3405d0dda84cb0b1a92c24b7b`. The builder stops at ready for
adversarial review and does not self-approve this high-risk change.

## Intent

- What problem does this change solve?

  It closes the third-recheck findings without preserving the disproven
  stale-data interpretation. The plan attestation remains provenance
  defense-in-depth, records either refresh flag, and binds the sidecar to the
  exact plan bytes and qualified Terraform version. Data output authorization
  still uses the exact prior-state instance-index-to-ID map. Generated data
  modules now fail loudly when a provider returns a name whose case differs
  from the requested name. Capture regeneration stages a complete,
  environment-normalized set before promotion.

- What user-visible or maintainer-visible behavior should change?

  `iw plan` continues to pass direct `-refresh=true` for deterministic engine
  lifecycle behavior, but that flag is not described as a stale-data guard.
  An attestation with `refresh=false` is parsed, recorded, and accepted when
  its remaining schema, version, argv, and digest checks pass. The two
  refresh-flag fixture scenarios are named `refresh_false` and `refresh_true`
  and prove that Terraform 1.15.4 produces provider-observed equivalent
  evidence for this configuration, modulo timestamp.

- What behavior must stay unchanged?

  Digest mismatch, malformed attestation, unsupported format, and
  unqualified-version refusals remain. Managed plans without an attestation
  retain their compatibility behavior; data no-op or absent-output-change
  paths make no new reference authorization claim. The attestation does not
  become an authenticated receipt, and no provider pack, source-operation
  mapping, readiness count, or unrelated assessment rule changes.

## Base / Head

- Base: `472316ef653426b3405d0dda84cb0b1a92c24b7b`
  (`Enforce plan freshness, key-bound authorization, and name-fold uniqueness`)
- Head: uncommitted working tree on `claude/data-only-referents`, based on the
  base above
- Diff command: `git diff 472316ef653426b3405d0dda84cb0b1a92c24b7b --` plus direct
  inspection of untracked `refresh_false/` and `refresh_true/` fixture paths
- Git state: no stage, commit, push, branch, or other git write was performed

## Files Changed

- Files:

  - `go/internal/plan/attestation.go`
  - `go/internal/plan/evidence_test.go`
  - `go/internal/plan/contract_data_referent_test.go`
  - `go/internal/plan/lifecycle_test.go`
  - `go/internal/modulesgen/data_module.go`
  - `go/internal/modulesgen/data_module_test.go`
  - `go/internal/plan/testdata/provider_double_capture/README.md`
  - `go/internal/plan/testdata/provider_double_capture/gen-captures.sh`
  - all seven provider-double scenario data modules, with the refresh pair
    renamed from `stale_refresh_false/` and `fresh_after_stale/` to
    `refresh_false/` and `refresh_true/`
  - `docs/superpowers/specs/2026-08-01-data-only-referents-design.md`
  - this handoff

- Files intentionally left untouched:

  - provider packs, OpenAPI/API material, provider source-operation mapping,
    provider-readiness/count artifacts, and production provider binaries;
  - all existing `show.json` bytes. The two renamed refresh captures retain
    their raw bytes; no capture output was fabricated or regenerated;
  - `.gocache`, `.gotmp`, and `.provider-double-bin`, which remain local
    ignored artifacts only.

## Source Inputs Consulted

- Provider schemas:

  The test-only provider-double schema exposes required string `name` and
  computed string `id`; `ReadDataSource` returns both fields. The generated
  postcondition therefore has a provider-observed name to check.

- OpenAPI/API contracts:

  None changed or required for these engine and fixture-contract fixes. No
  source evidence was added that proves same-case duplicate names are unique
  in the production ZIA tenant.

- Provider source files:

  The provider-double implementation and its deterministic v1/v2 ID behavior.
  The pinned ZIA provider/SDK first-match behavior remains a documented
  qualification caveat in the data-only referents spec.

- Pack metadata:

  Existing data-referent metadata and the production reference-output contract
  materialization in assessment and exact-plan Apply. No pack vocabulary or
  provider pin changed.

- Existing docs or design records:

  The complete third-recheck report supplied by the coordinator,
  `AGENTS.md`, the adversarial-review workflow/templates, and the data-only
  referents design spec.

- Other source evidence:

  The committed provider-double `show.json` captures, lifecycle fake-
  Terraform harness, existing stable-file/digest conventions, and the
  Terraform data-source behavior qualified by the coordinator for 1.15.4.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures:

  The seven provider-double scenario configurations now mirror the generated
  data-module postcondition. The refresh pair is renamed and redocumented as
  a refresh-flag-independence proof. All seven committed `show.json` files
  remain the previously reviewed raw Terraform outputs and need coordinator
  regeneration under the updated scenario/configuration set.

- Snapshots: None regenerated.
- Demo or lab outputs: None. Terraform/provider capture regeneration was not
  run because this worker is prohibited from running Terraform.
- Artifact drift intentionally expected:

  Generated module `main.tf` goldens gain the lifecycle postcondition. Capture
  timestamps and any configuration representation affected by the postcondition
  are expected to change only when the coordinator reruns the full matrix
  under Terraform 1.15.4.

## Expected Delta

- Expected behavior change:

  1. The attestation trust boundary is documented as unsigned defense-in-depth;
     `refresh=false` is no longer a refusal reason.
  2. The refresh pair is accepted on both sides and compared after removing
     only the top-level timestamp.
  3. Every generated data-source instance asserts exact, case-sensitive name
     equality after the provider read.
  4. Capture generation rejects inherited CLI arguments, normalizes locale and
     timezone, stages every result, and promotes only a complete set.

- Expected report/count/coverage changes: None.
- Expected generated-output changes: modulesgen data-module `main.tf` goldens
  gain `lifecycle.postcondition`; no provider readiness or coverage count
  changes.
- Expected no-op areas: provider packs, source mapping, count accounting,
  managed-resource module rendering, and no-op authorization semantics.

## Invariants Claimed

- Evidence must not be silently dropped:

  Non-no-op data output still requires a present, well-formed attestation whose
  plan SHA-256 matches the saved plan, whose Terraform version is in the
  qualified `1.15.x` range, and whose argv contains an explicit refresh flag.
  The data output map must equal the exact map reconstructed from
  `prior_state.values.root_module` data instances.

- Generic matcher evidence must not outrank source-backed evidence:

  Data IDs come only from refreshed prior-state data instances. Planned data
  resources, `planned_values`, resource changes, and prior-state engine-output
  projections are not data-kind evidence.

- Source precedence/provenance must remain explicit:

  Managed kinds read `planned_values.root_module`; data kinds read
  `prior_state.values.root_module`. The attestation is bound to the plan and
  sidecar bytes, but its writer is not authenticated beyond the same trust
  class as `tfplan.sources`.

- Ambiguity must stay classified instead of being coerced to success:

  Raw transform names still use `strings.EqualFold` before slug or ID fallback.
  The generated postcondition rejects a case-different provider first-match.
  A same-case duplicate introduced after publication remains a documented
  residual caveat because exact-name equality cannot distinguish identical
  names and the provider can still return its first match.

- Provider-readiness counts must stay explainable:

  No readiness report, coverage count, generated module count, or provider pack
  contract vocabulary changed.

- Adoption safety invariants:

  These changes affect saved-plan evidence authorization and generated data
  module validation only. No adoption side-effect lane was widened.

## Tests Run

- Commands:

  - `gofmt -w` on all changed Go files; `gofmt -l` and `git diff --check`.
  - `sh -n go/internal/plan/testdata/provider_double_capture/gen-captures.sh`.
  - Focused `go test` for modulesgen goldens, attestation acceptance/refusals,
    refresh-flag proof, exact key binding, rekey refusal, and the lifecycle
    explicit-argv regression.
  - The script's hostile-environment preflight with `TF_CLI_ARGS=-refresh=false`
    and no Terraform execution; it exits 1 before provider build/capture.
  - `GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go build ./...`.
  - `GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go vet ./...`.
  - `GOCACHE=<workspace>/.gocache GOTMPDIR=<workspace>/.gotmp go test ./... -count=1`.
  - The non-listener characterization:
    `go test ./... -count=1 -skip
    '^(TestFetchRecordedTransport|TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds|TestRealTLSVerificationFailureIsClassifiedAsCertificate|TestCABundleAllowsCommentAndBlankResidueLines|TestHTTPProxySelectedFromRealProcessEnvironment|TestNoProxyExemptsHostFromRealProcessEnvironment)$'`.

- Relevant output summary:

  Focused tests passed. `go build ./...`, `go vet ./...`, and the NUL-safe
  `gofmt -l` check passed. The full test gate failed only at the two sandbox
  loopback listener tests, `cmd/iw/TestFetchRecordedTransport` and
  `internal/httptransport/TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds`,
  both reporting `listen tcp6 [::1]:0: bind: operation not permitted`. The
  non-listener characterization passed for every package, including
  `cmd/iw`, `internal/modulesgen`, `internal/plan`, and `internal/transform`.
- Focused regression and pre-fix/unsafe-mutation proof:

  The refresh-false focused regression passes with a `refresh=false` attestation
  and compares the two captures after timestamp removal. The modulesgen golden
  contains the postcondition bytes. Removing only the renderer's postcondition
  made `TestDataModuleRendersItemsOutputForTwoItems` fail with the generated
  `main.tf` missing the expected `lifecycle.postcondition`; the renderer was
  restored and the focused suite passed again. The hostile environment
  preflight returned exit 1 with the expected refusal before Terraform work.

- Promotion efficiency: not measured. One required full-corpus attempt was
  made; after its environment-only listener failures, one justified
  non-listener characterization passed. No duplicate full-corpus sweep was run
  after a passing superset.
- Tests not run and why:

  Terraform/provider capture regeneration is not run by this worker. The
  coordinator must regenerate all seven captures with Terraform 1.15.4. Any
  capture-dependent test with a missing fixture skips loudly and names the
  coordinator action. Listener-dependent tests fail in this sandbox because
  the sandbox cannot bind loopback listeners.

## Attestation Format

The engine writes the sibling file `tfplan.attestation` beside a saved
`tfplan`. It is JSON with a trailing newline, mode 0600, and exactly five
fields:

    {
      "format_version": 1,
      "terraform_version": "1.15.4",
      "argv": ["plan", "-input=false", "-refresh=true", "-out=tfplan"],
      "refresh": true,
      "plan_sha256": "<lowercase 64-hex SHA-256 of tfplan>"
    }

`refresh` may be either Boolean value and is recorded rather than treated as a
staleness refusal. The engine's normal saved-plan lifecycle continues to emit
direct `-refresh=true`. Validation retains plan-leading argv, an explicit
refresh flag, the qualified `1.15.x` version, lowercase SHA-256 syntax, exact
plan digest binding, and malformed/field-count refusals.

Trust boundary text, verbatim:

> It does NOT authenticate against a writer who can forge both plan and sidecar — the same trust class as tfplan.sources.

The attestation authenticates engine-created plans against accidental drift and
version qualification only in this defense-in-depth sense; it is not an
authenticated receipt from an issuer outside the plan-directory owner.

## Known Deferrals

- Deferred work:

  Coordinator regeneration of `initial_create`, `no_op`, `refresh_id_change`,
  `rekey_refusal`, `empty_for_each`, `refresh_false`, and `refresh_true`;
  sandbox-blocked loopback listener tests; and the fresh-context adversarial
  review.

- Reason it is safe to defer:

  Terraform/provider IPC is explicitly outside this worker's authority and
  cannot be replaced with fabricated `show.json` bytes. The committed capture
  tests remain loud when coordinator artifacts are missing. Adversarial review
  is the required stop point for this generated-evidence/provenance change.

- Follow-up owner or trigger:

  The coordinator runs `gen-captures.sh` in a clean environment with Terraform
  1.15.4, checks the complete staged promotion, then reruns the capture-
  dependent subtests. A fresh Codex reviewer uses
  `docs/adversarial-review-run-prompt.md`, records findings with
  `docs/adversarial-review-template.md`, and does not edit files.

## Review Focus

- Highest-risk files or paths:

  - `go/internal/plan/attestation.go`: accepted refresh flags, qualified
    version/digest checks, and the explicitly unsigned trust boundary.
  - `go/internal/plan/contract_data_referent_test.go` and provider-double
    captures: refresh-flag independence, exact evidence comparison, and loud
    capture skips.
  - `go/internal/modulesgen/data_module.go` and its goldens: per-instance
    exact-name postcondition and generic `name_field` substitution.
  - `go/internal/plan/testdata/provider_double_capture/gen-captures.sh`:
    fail-fast environment checks, UTC/C locale, temp staging, complete-set
    validation, rollback, and direct refresh flags.
  - `docs/superpowers/specs/2026-08-01-data-only-referents-design.md`: trust
    boundary, exact key map rule, provider first-match caveat, and same-case
    residual caveat.

- Specific assumptions to attack:

  - `refresh=false` capture evidence is genuinely provider-observed under the
    qualified Terraform 1.15.4 behavior, rather than a synthetic Boolean test.
  - Removing only `timestamp` is the correct byte-identity normalization for
    the two refresh captures.
  - `self.<name_field> == each.value.<name_field>` is evaluated per data-source
    instance and catches case-different first matches.
  - A same-case duplicate remains possible after publication and is not
    accidentally claimed closed by the postcondition.
  - Capture promotion cannot leave a mixed or partial committed set after a
    scenario or move failure.
  - Adding the postcondition changes the modulesgen golden and removing it
    fails the golden regression.

- Source evidence the reviewer should verify:

  Read the complete third-recheck report, the provider-double schema/read
  response, every committed capture, the generated module renderer/goldens,
  the script, and the production attestation/contract call sites. Verify that
  no authenticated issuer is being implied by unsigned sidecar fields.

- Generated artifacts the reviewer should compare:

  Compare the seven current `show.json` files with the coordinator's clean
  Terraform 1.15.4 regeneration after the postcondition/configuration change;
  compare the refresh pair after removing only `timestamp`; and compare the
  modulesgen golden postcondition lines byte-for-byte.

- Edge cases that could silently overclaim, remap, drop, or weaken evidence:

  Missing or malformed sidecars; wrong digest; unsupported format/version;
  refresh=false argv and field; sidecar mutation during evidence recheck;
  two-item ID swaps; renamed output keys; duplicate data indexes; planned-only
  data resources; resource_changes-only evidence; case-different provider
  first matches; same-case post-publication duplicates; distinct empty-slug
  names; hostile `TF_CLI_ARGS*`/`TF_VAR_*`/workspace inputs; partial capture
  output; and promotion failure after an intermediate move.
