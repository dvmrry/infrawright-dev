# Integration Validation Runbook

Integration validation proves the full import-oracle adoption workflow against a
real or controlled provider environment. It is different from the shipped demo:
`make demo-contract` proves the credential-free local artifact and module
contract, while integration validation proves that provider credentials,
provider state import, saved plan classification, and apply safety work together
for a selected tenant/resource scope.

This runbook is intentionally conservative. A validation failure is evidence to
classify, not an automatic engine feature request.

The commands below run through the Go `iw` CLI.
Repository fake-Terraform tests establish readiness to qualify, not live
qualification. See [Operational Go Runtime](operational-runtime.md) for the
runtime contract and separately authorized read-only and import-only Apply
checklists.

Fixture timing is not live performance evidence. Record HTTP-attempt counts,
exact artifact manifests, and the concurrency setting with any performance
qualification.

## Preconditions

- Use a controlled non-production tenant, or a production tenant that is
  explicitly approved for the selected resource scope.
- Configure provider credentials outside the repository. Do not commit
  credentials, tokens, private keys, provider state, saved plans, raw logs, or
  tenant/account identifiers.
- Ensure Terraform/OpenTofu is available and matches the version being
  validated.
- Build the accepted Go revision with `make dist/iw`, then run `make check`
  before qualification. Record the revision and candidate SHA-256 with the
  sanitized evidence.
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
make gen-modules RESOURCE=<resource-or-provider>
make gen-env TENANT=<tenant> RESOURCE=<resource-or-provider>
make stage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
make plan TENANT=<tenant> RESOURCE=<resource-or-provider> SAVE=1
make assert-adoptable TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make apply TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make unstage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
```

Run this persistent-writer sequence in one job-owned physical workspace and
serialize writes to the active deployment overlay. Concurrent jobs, including
runs of the same branch, require disjoint checkouts, overlays, generated
artifacts, environment roots, saved plans, and Terraform working directories.
The supported generic runtime does not provide a cross-process publisher lock:
the pipeline owns workspace isolation and serialization for persistent writers.

Selective `gen-modules`, `validate-modules`, `gen-env`, staging, planning, and
Apply resolve through the same singleton-state topology. A provider selector
expands to its individual resource roots. Omitting `RESOURCE` remains the
simplest qualification path because it generates every active module.

Generated `<resource_type>_moves.tf` files are durable unresolved migration
evidence. Repeating Transform or Adopt preserves them byte-for-byte. If a new
rename would conflict with an existing move artifact, artifact generation
fails before changing config, imports, lookups, bindings, or moves. Remove a
move file explicitly only after confirming that its state migration has been
applied (and after any required staged copy is no longer needed).

### ZCC provider qualification

Before qualifying ZCC adoption, use the
[ZCC beta provider audit and downstream matrix](provider-labs/zcc-beta-provider-audit.md).
It pins the provider/SDK authority, separates pack-policy candidates from
provider-only limitations, and records the live gates for the v1/v2 trusted
network boundary and the two source-less v2 resources.

Qualify ZCC through the same generic Fetch, Adopt, module/root, staging, saved
plan, assessment, and exact-plan Apply commands shown above. The retired ZCC
process-host candidate/receipt/materialization lane is not a supported or
required compatibility path. Do not treat fake-provider or repository
differentials as live-tenant qualification.

### Saved-plan and Apply evidence

Use the same `POLICY=<file>` for `assert-adoptable` and `apply`. Apply
reclassifies saved plans before execution and should only proceed for clean,
import-only, or explicitly policy-tolerated saved plans.

For an opted-in cross-state referent, the engine-owned
`infrawright_reference_ids` create/update/no-op is also clean only when the loaded
topology names that referent and the sensitive, fully known output exactly
matches provider-observed IDs reconstructed from Terraform's planned child
modules. Arbitrary output changes remain outside the assessment contract.

Cross-state validation is limited to the conditional pre-production cohort and
dependency-state restrictions in
[Cross-state Reference Qualification](provider-labs/cross-state-reference-qualification.md).
A clean plan does not qualify undeclared pairs or prove that a saved referrer
plan is bound to an unchanged referent-state version.

When using a remote backend, also pass the same `BACKEND_CONFIG=<file>` to
`plan`, `assert-adoptable` (or `assert-clean`), and `apply`. The saved-plan
fingerprint treats a missing or changed backend config as stale.

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
case for optional-zero sentinels such as `size_quota = 0`. A passing validation
must show that configured projection omissions are effective before the
provider validates generated config, not only after post-import `show -json`
projection. ISOLATE rules are not part of this validation: for pinned provider
4.7.26, `cbi_profile` is required on write but a fresh import Read cannot
reliably reconstruct it when omitted by the API, so version-scoped
`unsupported_if` metadata rejects `action = "ISOLATE"` before Oracle. The
former ZIA `cbi_profile` `projection_fill` was intentionally removed. Generic
`projection_fill` timing remains covered by engine tests, but the current ZIA
pack declares no such fill.

## Failure Classification

Classify each failure before deciding where work belongs.

| Category | Meaning | Evidence to collect | Fix location, if any | Blocks validation? |
|---|---|---|---|---|
| Core bug | Provider-agnostic engine behavior is wrong, unsafe, or inconsistent with the documented command contract. | Command, sanitized error/output, minimal fixture, generated artifact paths, saved plan summary. | `go/` with focused tests. | Yes, until fixed or scoped out. |
| Pack metadata bug | Provider behavior can be represented by existing pack metadata, but the pack value is missing or wrong. | Resource type, pack name, relevant registry/override/adoption metadata, sanitized plan or projection evidence. | `packs/<provider>/` metadata and pack tests. | Yes for affected resources. |
| Registry/fetch metadata bug | Collection/adoption inventory metadata points at the wrong list/detail path, key, import ID, or identity alias. | Fetch command, raw item shape, key/import ID derivation, registry entry, sanitized fetch output. | Pack registry/adoption metadata. | Yes for affected resources. |
| Collector bug | The collector cannot reliably fetch current provider evidence independent of projection/adoption. | Collector command, sanitized HTTP/status/pagination/auth diagnostics, input config, no secret values. | Generic Go collector code or pack registry metadata. | Yes if fetch evidence is required. |
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
- any cross-state reference output change is mechanically verified against the
  bound root topology and planned provider IDs,
- there are zero destroys and zero creates unless each is intentional and
  approved for the validation scope,
- no sensitive values are rendered into committed artifacts,
- no blocked plan paths remain unclassified,
- the run is repeatable from clean generated artifacts,
- every failure has an owner, classification, and next action.

Use [the integration validation report template](templates/integration-validation-report.md)
to record results.
