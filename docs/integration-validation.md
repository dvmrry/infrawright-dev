# Integration Validation Runbook

Integration validation proves the full import-oracle adoption workflow against a
real or controlled provider environment. It is different from the shipped demo:
`make demo-contract` proves the credential-free local artifact and module
contract, while integration validation proves that provider credentials,
provider state import, saved plan classification, and apply safety work together
for a selected tenant/resource scope.

This runbook is intentionally conservative. A validation failure is evidence to
classify, not an automatic engine feature request.

The commands below run through the generic Node 24 `infrawright` CLI. Python
must be unavailable when qualifying the operational runtime; retained Python
tests, differentials, probes, and authoring tools are outside this workflow.
Repository fake-Terraform tests establish readiness to qualify, not live
qualification. See [Operational Node Runtime](operational-runtime.md) for the
exact bundle/checksum contract and the separately authorized read-only and
import-only Apply checklists.

## Preconditions

- Use a controlled non-production tenant, or a production tenant that is
  explicitly approved for the selected resource scope.
- Configure provider credentials outside the repository. Do not commit
  credentials, tokens, private keys, provider state, saved plans, raw logs, or
  tenant/account identifiers.
- Ensure Terraform/OpenTofu is available and matches the version being
  validated.
- Use Node 24 and run `make verify-runtime` against the accepted
  `dist/infrawright-cli.mjs` and checksum before qualification. Do not run
  `npm ci`, install runtime npm dependencies, or rebuild from source in the
  qualification job. A restricted corporate registry needs the lockfile's
  pinned build packages only on a separate source-build path; inspect that
  path with `make source-build-preflight`.
- Make Python unavailable so a retained migration path cannot satisfy an
  operational step accidentally.
- Choose the backend/state policy before running. Local scratch state and
  remote backends have different retention and audit requirements.
- Start from a clean working tree or an isolated worktree.
- Select the active deployment and overlay with `DEPLOYMENT=<file>` and
  `OVERLAY=<path>` when the default demo deployment is not the target. A Make
  `DEPLOYMENT` value is authoritative; otherwise a nonempty imported
  `INFRAWRIGHT_DEPLOYMENT` is used, then `deployment.json`.
- Treat generated plans, state, logs, raw API details, and provider diagnostics
  as sensitive local artifacts.

## Workflow

Run the primary adoption sequence for one provider or resource scope:

```sh
make fetch TENANT=<tenant> RESOURCE=<resource-or-provider>
make adopt IN=pulls/<tenant> TENANT=<tenant> RESOURCE=<resource-or-provider>
make gen-modules RESOURCE=<resource-or-provider>  # grouped selection expands to the complete root
make gen-env TENANT=<tenant> RESOURCE=<resource-or-provider>
make stage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
make plan TENANT=<tenant> RESOURCE=<resource-or-provider> SAVE=1
make assert-adoptable TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make apply TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make unstage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
```

Run this persistent-writer sequence in one job-owned physical workspace and
serialize it for the complete materialization output root. Concurrent jobs,
including runs of the same branch, require disjoint workspaces and output
roots. Configure that root as the exact canonical deployment overlay; a
containing ancestor is not a second valid authority. The ADO path convention,
publisher-guard behavior, and stale cleanup rules are defined in
[ADR 0001](adr/0001-publisher-ownership.md).

Selective `gen-modules`, `validate-modules`, `gen-env`, staging, planning, and
Apply resolve through the same deployment topology. Selecting either member
of a grouped root therefore generates and validates every module referenced by
that root. Omitting `RESOURCE` remains the simplest qualification path because
it generates every active module.

Generated `<resource_type>_moves.tf` files are durable unresolved migration
evidence. Repeating Transform or Adopt preserves them byte-for-byte. If a new
rename would conflict with an existing move artifact, artifact generation
fails before changing config, imports, lookups, bindings, or moves. Remove a
move file explicitly only after confirming that its state migration has been
applied (and after any required staged copy is no longer needed).

### Frozen ZCC migration note

The following process-host lane is retained only for consumers of the frozen
ZCC migration architecture; it is not the primary generic operational path.
For the exact five-resource Node ZCC provider-observed bootstrap lane, use a
protected Python-reference workspace to obtain a complete exit-`0`
`compare_adoption_artifacts` assertion, then submit that assertion unchanged to
`materialize_adoption_artifacts` in the target job-owned workspace. Configure
the existing adoption-oracle host authority and set
`INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` to the exact canonical target overlay.
The guard is acquired before target binding; once binding derives the artifact
coordinates, exact-root authority is proved before the materializer re-runs
the provider oracle. Its receipt is byte/publication evidence, not a live plan
result. Only after its exit `0` may the serialized workflow continue with
`gen-env`, `stage-imports`, `plan SAVE=1`, and `assert-adoptable`. Do not treat
fake-provider or repository differentials as live-tenant qualification, and do
not apply from the materialization receipt alone.

### Saved-plan and Apply evidence

Use the same `POLICY=<file>` for `assert-adoptable` and `apply`. Apply
reclassifies saved plans before execution and should only proceed for clean,
import-only, or explicitly policy-tolerated saved plans.

When using a remote backend, also pass the same `BACKEND_CONFIG=<file>` to
`plan`, `assert-adoptable` (or `assert-clean`), and `apply`. The saved-plan
fingerprint treats a missing or changed backend config as stale.

For a frozen Node ZCC refresh that returned `awaiting_apply`, call the versioned
`acknowledge_pull_refresh` process operation only after the apply and unstage
steps succeed. The acknowledgement requires the complete parity assertion,
the complete publication receipt, `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT`, and
the host-only `INFRAWRIGHT_ALLOW_EXTERNAL_APPLY_ACK=1` capability. It is an
explicit trusted-pipeline statement, not independent Terraform apply proof.

Useful inspection and cleanup commands:

```sh
make unstage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
make clean-plans TENANT=<tenant> RESOURCE=<resource-or-provider>
make assert-clean TENANT=<tenant> RESOURCE=<resource-or-provider>
make demo-contract
```

`make assert-clean` is the compatibility/no-policy gate for no-op or import-only
plans. Prefer `make assert-adoptable` for adoption validation because it
understands drift policy and guidance annotations.

For Zscaler batch-oracle validation, include a resource that exercises
generated-config projection timing. `zia_url_filtering_rules` is the current
case. Provider-generated config can contain optional-zero sentinels such as
`size_quota = 0`, and ISOLATE rules can require `cbi_profile` even though
provider readback omits it. A passing validation must show that configured
projection omissions and pack-declared `projection_fill` entries are effective
before the provider validates generated config, not only after post-import
`show -json` projection. Until that is proven, URL-filtering adoption and any
dependent binding proof remain blocked even when the engine's fail-closed
safety checks pass.

## Failure Classification

Classify each failure before deciding where work belongs.

| Category | Meaning | Evidence to collect | Fix location, if any | Blocks validation? |
|---|---|---|---|---|
| Core bug | Provider-agnostic engine behavior is wrong, unsafe, or inconsistent with the documented command contract. | Command, sanitized error/output, minimal fixture, generated artifact paths, saved plan summary. | `node-src/` with focused tests. | Yes, until fixed or scoped out. |
| Pack metadata bug | Provider behavior can be represented by existing pack metadata, but the pack value is missing or wrong. | Resource type, pack name, relevant registry/override/adoption metadata, sanitized plan or projection evidence. | `packs/<provider>/` metadata and pack tests. | Yes for affected resources. |
| Registry/fetch metadata bug | Collection/adoption inventory metadata points at the wrong list/detail path, key, import ID, or identity alias. | Fetch command, raw item shape, key/import ID derivation, registry entry, sanitized fetch output. | Pack registry/adoption metadata. | Yes for affected resources. |
| Collector bug | The collector cannot reliably fetch current provider evidence independent of projection/adoption. | Collector command, sanitized HTTP/status/pagination/auth diagnostics, input config, no secret values. | Generic Node collector code or pack registry metadata. | Yes if fetch evidence is required. |
| Provider behavior unsupported by current generic contract | Provider semantics are real but need a new explicit metadata/guidance/design lane before automation is safe. | Saved plan classification, provider schema path, raw/provider state contrast, existing diagnostic output. | Design doc first, then validator/metadata/behavior if approved. | Usually yes for affected resources. |
| Provider bug / upstream evidence candidate | Terraform provider or API behavior appears inconsistent, lossy, or contrary to published contracts. | Repro command, provider version, sanitized provider diagnostics, minimal Terraform config/state evidence. | Upstream issue or provider-specific workaround metadata if safe. | Yes unless isolated and approved. |
| Operator input required | The resource needs a human-supplied value, provider setting, expression binding, approval, or tenant-specific decision. | Blocked path, required input, policy/guidance annotation, source of the requirement. | Consumer config, drift policy, expression binding, or operator runbook. | Blocks until supplied or explicitly scoped out. |
| Unsafe to automate | Automation would hide sensitive values, suppress provider-blind API surface, or risk destructive changes. | Sensitive/advisory output, plan action summary, redaction notes, why no safe generic rule exists. | Documentation or manual workflow; do not add automatic behavior. | Yes unless excluded from validation scope. |
| Private environment issue | Failure is caused by tenant permissions, billing/API gates, network/TLS, quotas, or account state. | Sanitized provider/API error, enabled services/permissions summary, environment notes. | Tenant setup or operator environment. | Blocks that environment, not necessarily the engine. |
| Documentation gap | Behavior is correct but the command, precondition, or interpretation was unclear. | Confusing doc section, command used, observed output. | `docs/` update. | No, once understood. |
| Test gap | Behavior is understood but not covered by unit, fixture, demo, or lab tests. | Missing scenario, smallest fixture shape, target module. | Tests only, unless behavior is also wrong. | No by itself. |

## Evidence Capture

Record enough information to reproduce or classify the result without
committing sensitive artifacts:

- command run and exit code,
- tenant and resource scope, sanitized if needed,
- provider, Terraform/OpenTofu, and pack versions,
- active `DEPLOYMENT`, `OVERLAY`, and backend/state policy,
- sanitized error output or plan classification summary,
- generated artifact paths, not full artifact contents,
- drift policy file path and summary of relevant entries,
- whether saved plans, state, logs, raw details, and provider diagnostics stayed
  local/private,
- redaction notes for account IDs, tenant IDs, ARNs, URLs, tokens, secrets, and
  local temp paths.

## Exit Criteria

An integration-validation run is successful when:

- saved plans are clean/import-only, or all non-import drift is explicitly
  policy-tolerated,
- there are zero destroys and zero creates unless each is intentional and
  approved for the validation scope,
- no sensitive values are rendered into committed artifacts,
- no blocked plan paths remain unclassified,
- the run is repeatable from clean generated artifacts,
- every failure has an owner, classification, and next action.

Use [the integration validation report template](templates/integration-validation-report.md)
to record results.
