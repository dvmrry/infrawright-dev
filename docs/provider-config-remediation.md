# Provider Config Requirement Guidance

Provider-config diagnostics classify saved-plan drift caused by provider-level
defaults rather than resource projection. This document defines how packs can
record that evidence and guide the operator toward the needed provider setting.
It is not a provider-config rendering design.

The engine boundary is intentionally conservative:

```text
saved plan drift -> explicit pack requirement -> guidance
```

The engine should explain the provider setting that appears necessary for clean
adoption. It should not own, mutate, or render provider configuration.

## Motivating Case

The Google Cloud lab found provider-created attribution-label drift under:

```text
terraform_labels.goog-terraform-provisioned
```

Setting `add_terraform_attribution_label = false` in consumer-owned provider
configuration removed that drift. That is not a resource projection issue, not
an absent/default normalization rule, and not drift-policy tolerance. It is a
provider-level adoption precondition.

## Goals

- Preserve provider-config drift as a distinct class from resource tfvars
  projection.
- Let packs document provider-level settings that explain adoption drift.
- Validate provider-config guidance metadata so unsafe or ambiguous claims fail
  loudly.
- Keep consumer-owned provider configuration authoritative.
- Keep future `assert-adoptable` behavior guidance-only: it may explain why a
  plan is blocked, but it must not downgrade a blocked plan to tolerated or
  clean.

## Non-Goals

- Do not infer provider settings from changed plan paths.
- Do not render provider blocks from pack metadata.
- Do not emit provider HCL.
- Do not mutate oracle scratch provider config.
- Do not mutate generated env-root provider config.
- Do not mutate user-authored provider configuration.
- Do not use drift policy to suppress provider-config drift.
- Do not normalize resource values to compensate for provider defaults.
- Do not treat provider-config matching as drift tolerance or plan cleanliness.
- Do not render credentials, tokens, tenant IDs, endpoints, account IDs,
  regions, aliases, or other environment-specific provider arguments from pack
  metadata.

## Current Diagnostic Metadata

Packs can already declare provider-config requirements:

```json
{
  "provider_config": {
    "requirements": [
      {
        "id": "google_disable_attribution_label",
        "provider": "google",
        "setting": "add_terraform_attribution_label",
        "value": false,
        "reason": "Google provider adds terraform attribution labels by default.",
        "resource_types": [
          "google_bigquery_dataset",
          "google_pubsub_subscription",
          "google_pubsub_topic"
        ],
        "plan_paths": [
          "terraform_labels.goog-terraform-provisioned"
        ]
      }
    ]
  }
}
```

`engine.provider_config` reads saved plan JSON and reports whether changed paths
match declared requirements. It does not render provider configuration.

Requirements with no `remediation` block remain valid diagnostic-only metadata.
They do not need `remediation.evidence`; absent remediation is equivalent to
`diagnostic_only`.

## Guidance Metadata

The metadata key is still named `remediation` for the current schema, but its
semantics are guidance-only. It records evidence and operator intent; it does
not authorize the engine to apply provider configuration.

```json
{
  "provider_config": {
    "requirements": [
      {
        "id": "google_disable_attribution_label",
        "provider": "google",
        "setting": "add_terraform_attribution_label",
        "value": false,
        "reason": "Google provider adds terraform attribution labels by default.",
        "plan_paths": [
          "terraform_labels.goog-terraform-provisioned"
        ],
        "remediation": {
          "kind": "provider_argument",
          "mode": "renderable_default",
          "evidence": "docs/provider-labs/gcp-pr38.md",
          "safety": {
            "non_sensitive": true,
            "not_tenant_specific": true,
            "not_destructive": true
          }
        }
      }
    ]
  }
}
```

The existing `setting`, `value`, `reason`, and `plan_paths` fields identify the
provider argument that explains the drift and the saved-plan path that proves
the connection. The `remediation` object validates how strong the guidance
claim is.

When `remediation` is present, `kind` is required. V1 allows only
`provider_argument`.

## Guidance Modes

`mode` is intentionally small:

- `diagnostic_only`: explain drift, but never render, enforce, or mutate
  provider config. This is the current effective behavior and the safe default
  when `remediation` is absent.
- `required_external`: the requirement is known, but the value must come from
  consumer-owned provider configuration. It never renders. A future
  `assert-adoptable` path may use it to emit guidance when matching drift is
  detected. It may carry `value` as advisory context only.
- `renderable_default`: a stronger evidence claim that the value is
  non-secret, not tenant-specific, not destructive, and primitive enough to be
  reviewed. Despite the name, it is still guidance-only in the current engine.
  It must behave exactly like `diagnostic_only` unless a future design changes
  the provider-config ownership model.

No other modes should be added until a provider lab proves the need.

## Provider Config Ownership

Consumer-owned provider configuration is authoritative. Provider config has a
wider blast radius than resource config, so the engine must not merge pack
defaults into provider blocks or user-authored files.

Oracle scratch overrides under `packs/<pack>/oracle/<provider>.tf` are a lab
and import-oracle mechanism only. A clean oracle result proves that the scratch
root converged with the provider settings it used. It does not prove that a
committed env root will converge unless the operator applies equivalent
consumer-owned provider configuration.

## Validator Contract

The validator parses and validates guidance metadata only. It must not render
provider blocks.

The validator rejects:

- Unknown remediation modes.
- Unknown remediation kinds.
- Unknown remediation keys.
- Malformed remediation objects.
- Missing `remediation.evidence` for `renderable_default`.
- Missing `safety.non_sensitive`, `safety.not_tenant_specific`, or
  `safety.not_destructive` for `renderable_default`.
- Non-boolean safety values, or any safety value other than exactly `true`, for
  `renderable_default`.
- Any `renderable_default` value that is not a JSON boolean or finite number.
- Duplicate provider and setting requirements with conflicting values.
- Duplicate provider and setting requirements with conflicting modes.
- Duplicate provider and setting entries even when otherwise identical, unless
  a future design defines a merge rule.
- `renderable_default` requirements with `resource_types` or
  `resource_prefixes`.
- Missing `plan_paths` or `reason`.

Unknown remediation keys are rejected, not silently ignored. This keeps the
metadata contract small enough to review.

Validator-only behavior is explicitly forbidden from:

- Rendering provider blocks.
- Emitting HCL.
- Mutating oracle scratch config.
- Mutating generated env-root provider config.
- Comparing oracle scratch config with generated env-root provider config.
- Changing drift-policy tolerance.
- Downgrading `assert-adoptable` results.
- Inferring provider settings from plan paths.

`required_external` validation is intentionally lighter than
`renderable_default` validation. It requires `kind`, `mode`, `reason`, and
`plan_paths`, but it never renders and does not need bool/number value
restrictions. If `value` is present, it is advisory only.

## Conflict Handling

Ambiguous provider-config guidance is a metadata error:

- Two requirements for the same provider and setting specify different values.
- Two requirements for the same provider and setting specify conflicting modes.
- Duplicate requirements for the same provider and setting appear, even if they
  are otherwise identical.
- A `renderable_default` requirement is missing evidence or safety attestations.
- A `renderable_default` value is not a JSON boolean or finite number.
- A requirement has no `plan_paths` or `reason`.

Conflicts should be reported as provider-config metadata errors, not adoption
drift.

## Evidence Criteria

A requirement should not be marked `renderable_default` unless a provider lab
proves:

- The default provider configuration creates saved-plan drift.
- The proposed provider setting removes that drift.
- The setting does not alter the remote object outside Terraform's attribution,
  bookkeeping, or provider-owned metadata behavior.
- The value is not secret and not environment-specific.
- At least one resource that previously drifted becomes import-only/adoptable
  after the setting is applied.
- The lab report records cleanup and avoids committed state, plans, raw dumps,
  credentials, logs, and temp roots.

The GCP attribution-label finding satisfies the shape of this evidence for
diagnostic and validator work. It does not authorize provider config rendering.

## Assert-Adoptable Guidance

`assert-adoptable` may annotate blocked plan changes with the provider-config
requirement id, setting, value when present, and reason when a matching
requirement explains the drift.

That annotation must keep the plan blocked. Matching a provider-config
requirement is guidance, not tolerance. It must not downgrade a blocked plan to
tolerated or clean.

## Expected Workflow

1. A lab finds provider-level drift in saved plan JSON.
2. `engine.provider_config` reports the drift as unmatched.
3. The pack adds a `provider_config.requirements` entry with `plan_paths`,
   `setting`, `value` when appropriate, and `reason`.
4. The diagnostic reports the drift as a provider-config requirement.
5. The validator accepts or rejects optional `remediation` guidance metadata.
6. `assert-adoptable` can explain matching blocked drift, while keeping the
   plan blocked.
7. The operator applies any needed provider configuration through the
   consumer-owned provider config path.

## Open Questions

- What consumer-owned provider config path should lab runbooks recommend?
- Should `assert-adoptable` eventually fail with provider-config-specific
  guidance text when matching drift is detected?
- Should provider-config guidance metadata live only in pack manifests, or
  should tenant overlays be able to disable or replace pack guidance?
- Should the metadata key eventually be renamed from `remediation` to
  `guidance`, and if so, what compatibility path should be used?
