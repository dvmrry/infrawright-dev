# Builder handoff: Go Block E metadata CLI and Kubernetes qualification runbook

## Intent

- Add the three remaining metadata-only v2 operator commands to the Go CLI:
  `check-pack`, `check-pack-set`, and `deployment`.
- Preserve the Node command contracts exactly: argv parsing, ordered
  `PACK=`/`--pack` selection, per-command falsey environment fallback, help
  stream/status, the check-pack-set exit-3 requirements skip, and output bytes.
- Author—but do not execute—a controlled Kubernetes qualification for the
  complete Go adopt/saved-plan/exact-Apply path.
- Keep all existing metadata/deployment domain logic, dependencies, generated
  artifact bytes, and live-provider state unchanged.

## Base / Head

- Base: `cfe5c56912b579405e81795226774b88b330b03f`
- Head: uncommitted working tree on `feature/go-canonjson-foundation`; builder
  stopped at ready for fresh adversarial review as required.
- Diff command:
  `git diff -- go/cmd/iw/main.go && git diff --no-index /dev/null <each untracked file>`

## Files Changed

- `go/cmd/iw/main.go`: dispatch for the three commands and support for the two
  non-default help contracts.
- `go/cmd/iw/commands_metadata.go`: CLI composition only; existing metadata and
  deployment APIs own all domain behavior.
- `go/cmd/iw/commands_metadata_test.go`: focused parser, environment,
  short-circuit, help, exit-3, output, and load-order tests.
- `go/cmd/iw/block_e_differential_test.go`: required non-skipping differential
  against the exact frozen Node bundle SHA already enforced by
  `newBlockD5Runtime`.
- `go/cmd/iw/block_c4_differential_test.go`: three table entries proving the
  metadata-only commands never enter the Terraform execution platform gate.
- `docs/review-handoffs/go-live-qualification-runbook.md`: document and inline
  temporary fixture contents only; two explicit human execution stops.
- `docs/review-handoffs/go-block-e-live-qualification-adversarial-review.md`:
  coordinator transcription of six independent read-only review verdicts.
- This builder handoff.
- Files intentionally left untouched: `go.mod`, `go.sum`, Node source, frozen
  bundle, committed packs/schemas/overrides, all metadata/deployment domain
  packages, renderers, Makefile, and live/provider configuration.

## Source Inputs Consulted

- Provider schemas: local `terraform providers schema -json` extraction for
  `hashicorp/kubernetes` 2.38.0 with `-backend=false`; the runbook carries a
  behavior-preserving resource-minimal reduction for
  `kubernetes_config_map`. No kubeconfig or provider API was used.
- OpenAPI/API contracts: Kubernetes RBAC v1 Role/RoleBinding and core/v1
  ConfigMap shapes only, expressed in the document; no API was contacted.
- Provider source files: official HashiCorp Kubernetes provider 2.38.0 ConfigMap
  documentation (`namespace/name` import ID), versioned-resource guidance, and
  v2 authentication guidance (`KUBE_CONFIG_PATH`).
- Pack metadata: synthetic temporary `kubernetes` pack content included inline
  in the runbook; no committed pack was changed.
- Existing docs/design records: `docs/provider-labs/README.md`,
  `docs/integration-validation.md`, `docs/adversarial-review.md`, and the Block
  D handoffs.
- Other source evidence:
  - `node-src/cli/main.ts:241-320` (`checkPack`, `checkPackSet`)
  - `node-src/cli/main.ts:374-412` (`deployment`)
  - `node-src/cli/main.ts:161-185` (help behavior)
  - existing `internal/metadata`, `internal/deployment`, and `internal/cliargs`
    APIs
  - frozen `dist/infrawright-cli.mjs`, SHA-256
    `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`

## Generated Artifacts

- Reports: none generated or committed.
- Schemas: no repository schema added. The runbook includes one temporary
  minimal schema whose future execution location is outside the repository.
- Fixtures: no fixture tree committed. Differential fixtures are created only
  under `t.TempDir`; runbook fixtures are inline documentation.
- Snapshots: none.
- Demo/lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- `check-pack`, `check-pack-set`, and `deployment` dispatch to their Go ports
  instead of the `not yet ported` guard.
- `check-pack` and `deployment` help remain stderr/exit 2;
  `check-pack-set` help remains stdout/exit 0.
- `check-pack-set --requirements` remains exit 3 when requirements are
  unavailable, with the exact legacy stdout line used by `check-examples`.
- Empty `INFRAWRIGHT_PACKS`, `INFRAWRIGHT_PACK_PROFILE`, and
  `INFRAWRIGHT_DEPLOYMENT` values retain Node's falsey `||` fallback. Explicit
  command values win, and short-circuit package/env reads where Node does.
- No report/count/coverage, schema, artifact, provider, dependency, or live
  behavior changes.

## Invariants Claimed

- Evidence must not be silently dropped: no evidence or provider-readback path
  changes; commands only invoke already-ported read-only metadata/deployment
  functions.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance remains explicit: unchanged.
- Ambiguity stays classified: existing validators remain the only domain
  authority; the CLI does not catch or weaken their errors.
- Provider-readiness counts stay explainable: unchanged.
- Adoption safety invariants:
  - no test or authoring action reads kubeconfig, credentials, or a cluster;
  - no real Terraform plan or Apply is run;
  - runbook Apply has a separate post-plan human approval stop;
  - admin material is unexported and every qualification subprocess starts
    from an explicit `env -i` allowlist;
  - the future Terraform identity has only `get` on one named ConfigMap, and
    its full effective-permission matrix is rerun immediately before Apply;
  - the saved plan must be applyable, complete, exactly one typed/identified
    import, and empty across every other resource and non-resource effect;
  - the candidate, saved plan, fixture manifest, Terraform CLI config, lock,
    read-only kubeconfig, and actual installed provider executable are
    digest-bound across the human review stop;
  - exact Apply never receives `--allow-plan-changes` or `--allow-destroy`;
  - UID, resourceVersion, and full ConfigMap JSON must remain byte-identical;
  - state and both Oracle/generated roots are local only.

## Tests Run

- `gofmt -l .` — clean, no output.
- `go vet ./...` — clean, no output.
- `go test ./... -count=1` — all 22 tested packages green; `cmd/iw` 49.532s.
- `go test -race ./cmd/iw -run
  'Test(CheckPack|MetadataCommand|DeploymentCommand|BlockEMetadataCommands|RequiresTerraformExecution)'
  -count=1` — green, 19.601s.
- `go test ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation' -count=1`
  — green, 29.964s; standing artifact byte gates unchanged.
- `go test ./cmd/iw -run
  'TestBlockEMetadataCommandsDifferentialAgainstFrozenNodeOracle' -count=1`
  — green, 17.946s; hard-pinned, no skip path.
- Focused metadata command/unit+differential rerun after review corrections —
  green, 5.494s.
- All six JSON heredoc fixtures in the runbook parse; concatenated Bash fences
  pass `bash -n` and ShellCheck at warning severity.
- Adversarial jq fixtures prove both plan gates reject false/missing error
  status, top-level errors, wrong collection types, wrong identity, empty
  cardinality, drift, outputs, actions, and deferrals.
- `git diff --check` — clean.
- `go.mod` and `go.sum` SHA-256 exactly match base; no dependency delta.
- Tests not run: no cluster, kubeconfig, credentials, provider API, real plan,
  or real Apply. Those are explicitly outside this authorization.

## Adversarial Review Corrections

Independent read-only reviewers initially requested changes. The coordinator
accepted and mapped every finding as follows:

- missing CLI proof → assert the exact `validatePackResources` inputs, add an
  authoring-valid/resource-invalid frozen differential, exercise both mixed
  pack-selector orders, reset the deployment load witness, and cover
  check-pack-set falsey fallback/usage plus parse short-circuits;
- inherited/stale authority → keep admin variables unexported, run every
  qualification child under `env -i`, hash the read-only kubeconfig, and rerun
  the complete effective RBAC matrix immediately before Apply;
- approval/Apply TOCTOU → bind and recheck the candidate, plan, fixture
  manifest, Terraform CLI config, lock, read-only kubeconfig, and the exact
  installed provider executable under `.terraform/providers`;
- insufficient plan gates → require exact address/type/provider/import ID,
  strict collection types, explicit `errored: false`, `applyable: true` for the
  import plan, and empty drift/output/action/deferral/check/diagnostic/error
  surfaces; require one exact no-op managed resource on the second plan;
- cleanup gaps → pre-approve an ownership-labeled namespace manifest, arm the
  EXIT cleanup before creation, verify ownership before deletion, accept only
  explicit NotFound, index retry evidence, propagate credential-scrub failures,
  and retain evidence on any failure;
- incomplete filesystem/provenance proof → reject non-regular fixture entries,
  include the expanded RBAC manifest in the approved digest, capture provider
  discovery status and resolved executable metadata, and repeat all-`.tf`
  backend/cloud scans after staging and immediately before Apply.

## Known Deferrals

- Fresh adversarial review is complete: six independent lanes approve after
  the recorded fix/recheck loop.
- Coordinator commit/push after review; implementor did neither.
- User approval of resolved live names/commands, followed by a second explicit
  approval of the captured zero-mutation plan before exact Apply.
- Actual Kubernetes qualification and sanitized evidence report.
- Zscaler collector/auth/schema qualification remains entirely out of scope.

## Review Focus

- Attack Node sequencing, not just final values:
  - `check-pack` parses before default package-root resolution and uses the
    occurrence stream for last-wins selection;
  - `check-pack-set` resolves package root and reads env before parsing/help;
  - `deployment` parses before path/load, short-circuits env on an explicit
    path, loads the file before rejecting an unknown verb, and ignores extra
    positional arguments as Node does.
- Verify help stream/status and exit-3 output through the actual `main` error
  rendering, not only unit helpers.
- Verify the differential cannot skip and really binds the expected bundle
  digest.
- For the runbook, attack:
  - whether the reduced schema retains every behavior-reaching v2.38.0 flag;
  - whether the raw `kubectl get -o json` shape plus identity override can feed
    adoption without normalization;
  - whether the one-object `get` Role is sufficient for provider import/read
    yet structurally denies mutation;
  - whether either Terraform root can inherit a backend or alternate identity;
  - whether the jq gates truly reject every non-import/non-no-op action;
  - whether the two human stops are impossible to confuse with authorization;
  - whether evidence and cleanup commands can leak credentials or overclaim
    remote-object immutability.
