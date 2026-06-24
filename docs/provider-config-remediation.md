# Provider Config Remediation Design

This is a design note, not implemented behavior. Provider-config diagnostics
already classify saved-plan drift against explicit pack metadata. Remediation
would be the next step: letting a pack say which provider-level setting must be
present for adoption to converge.

The motivating case is the Google Cloud lab. With the default Google provider
configuration, several imported resources planned an update under:

```text
terraform_labels.goog-terraform-provisioned
```

Setting `add_terraform_attribution_label = false` in provider configuration
removed that drift. That is not a resource projection issue, not an
absent/default normalization rule, and not a drift-policy tolerance. It is a
provider-level adoption precondition.

## Goals

- Preserve provider-config drift as a distinct class from resource tfvars
  projection.
- Let packs document and eventually apply provider-level settings needed for
  clean adoption.
- Keep all provider-config behavior explicit, auditable, and backed by lab
  evidence.
- Fail loudly on conflicts, missing requirements, or unsafe metadata.
- Keep consumer-owned credentials, endpoints, regions, tenants, and aliases out
  of generated defaults unless a future design explicitly handles them.

## Non-Goals

- Do not infer provider settings from changed plan paths.
- Do not use drift policy to suppress provider-config drift.
- Do not normalize resource values to compensate for provider defaults.
- Do not render credentials, tokens, tenant IDs, endpoints, account IDs, or
  other environment-specific provider arguments from pack metadata.
- Do not mutate user-authored provider configuration.
- Do not treat provider-config matching as drift tolerance or plan cleanliness.
- Do not implement provider-config remediation in this design PR.

## Current Diagnostic Metadata

Packs can already declare diagnostic requirements:

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

This remains diagnostic-only. `engine.provider_config` reads saved plan JSON and
reports whether plan changes are explained by declared requirements. It does not
render provider configuration.

Requirements with no `remediation` block remain valid diagnostic-only metadata.
They do not need `remediation.evidence`; absent remediation is equivalent to
`diagnostic_only`.

## Proposed Remediation Metadata

Remediation should be an explicit extension of the diagnostic requirement, not a
new inference path:

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

The existing `setting` and `value` fields identify the provider argument to
write. `plan_paths` remains the evidence link between provider drift and the
setting. `remediation` declares that the requirement is eligible for future
rendering or validation.

When `remediation` is present, `kind` is required. V1 allows only
`provider_argument`; unknown `kind` values must be rejected by the future
validator.

### Remediation Modes

`mode` should be intentionally small at first:

- `diagnostic_only`: explain drift, but never render or enforce. This is the
  current effective behavior and the safe default when `remediation` is absent.
- `renderable_default`: future generated roots may render the provider argument
  when all rendering preconditions are satisfied. Until then it must behave
  exactly like `diagnostic_only`.
- `required_external`: the requirement is known, but the value must come from a
  consumer-owned file or environment-specific provider config. It never
  renders. A future `assert-adoptable` path may use it to emit remediation
  guidance when matching drift is detected. It may carry `value` as advisory
  context only, but that value is never rendered and is not subject to the V1
  boolean/number rendering restriction.

No other modes should be added until a provider lab proves the need.

## Rendering Boundary

Rendering is forbidden until all of these preconditions exist:

- A consumer-owned provider-config path exists.
- Generated roots can detect whether that consumer config is active for the
  provider/root being generated.
- Oracle scratch config and generated env-root config derive equivalent
  provider settings for the same requirement.

Until those preconditions are implemented, `renderable_default` is metadata for
validation and review only. It must not change generated provider blocks.

Generated env roots currently render a minimal provider block:

```hcl
provider "google" {
  # credentials via provider environment variables
}
```

Future rendering, if accepted, should only add pack-approved provider arguments
to generated provider blocks:

```hcl
provider "google" {
  # credentials via provider environment variables
  add_terraform_attribution_label = false
}
```

The first implementation should not invent a general HCL merge engine. It
should handle primitive provider arguments from `renderable_default`
requirements only. Complex nested provider blocks, aliases, credentials,
endpoints, and per-tenant values should remain out of scope.

V1 rendering, if accepted later, is restricted to the default unaliased provider
block. Provider aliases are out of scope.

### Oracle And Env-Root Equivalence

There are two provider-config mechanisms today or proposed:

- Existing oracle scratch overrides under `packs/<pack>/oracle/<provider>.tf`.
- Future generated env-root provider arguments.

These must not diverge for the same provider-config requirement. A lab/oracle
clean result proves only that the oracle scratch root converged with the
provider settings it used. It does not imply the committed generated env root
will converge unless equivalent provider settings are also applied there.

Any future renderer or renderer preflight must compare the requirement's
intended provider setting across oracle scratch configuration and generated
env-root configuration before claiming the requirement is remediated.
Validator-only V1 must not inspect or compare oracle scratch HCL and generated
env-root provider config.

### V1 Value Encoding

V1 `renderable_default` accepts only JSON booleans and numbers.

- JSON boolean values render as HCL `true` or `false`.
- JSON numbers render as canonical numeric literals.
- Strings, `null`, arrays, and objects are rejected for V1 rendering.

String-valued defaults are deliberately deferred. They can contain regions,
endpoints, project IDs, account IDs, tenant values, aliases, or text that looks
like interpolation. Those need a separate design before they can be rendered.

The safety metadata is necessary but not sufficient. For `renderable_default`,
`safety.non_sensitive`, `safety.not_tenant_specific`, and
`safety.not_destructive` must all be present, boolean, and exactly `true`.
Missing values, `false`, and non-boolean values must be rejected. These
attestations do not override the V1 type restrictions.

### Rendering Granularity

Provider configuration is provider-block-wide. `resource_types` and
`resource_prefixes` remain diagnostic scoping fields only. A requirement that
applies to only some resource roots is not eligible for V1
`renderable_default`, because rendering it into the provider block would affect
every resource using that provider configuration.

A `renderable_default` requirement must not include `resource_types` or
`resource_prefixes`. If a requirement only applies to some resource roots, it
must remain `diagnostic_only` or `required_external` in V1.

## Precedence

Provider config has a wider blast radius than resource config, so precedence
must be conservative:

1. Consumer-owned provider configuration is authoritative.
2. Pack `renderable_default` values may be generated only when no consumer
   provider config path is active for that provider/root.
3. If consumer config exists, generated roots should not attempt to merge pack
   defaults into it.
4. If a required setting is absent or conflicts, diagnostics should report the
   missing or conflicting requirement instead of silently accepting drift.

The exact consumer override path is intentionally not chosen here. A future PR
should design it alongside implementation. Until then, provider-config
remediation should stay a design/diagnostic concept.

## Validator-Only First Stage

The first behavior PR after this design should parse and validate remediation
metadata only. It must not render provider blocks.

The validator should reject:

- Unknown remediation modes.
- Unknown remediation kinds.
- Unknown remediation keys.
- Malformed remediation objects.
- Missing `remediation.evidence` for `renderable_default`.
- Missing `safety.non_sensitive`, `safety.not_tenant_specific`, or
  `safety.not_destructive` for `renderable_default`.
- Non-boolean safety values, or any safety value other than exactly `true`, for
  `renderable_default`.
- Any renderable value that is not a JSON boolean or number.
- Duplicate provider and setting requirements with conflicting values.
- Duplicate provider and setting requirements with conflicting remediation
  modes.
- Duplicate provider and setting entries even when otherwise identical, unless
  a future design defines a merge rule.
- `renderable_default` requirements with `resource_types` or
  `resource_prefixes`.
- Missing `plan_paths` or `reason`.

Unknown remediation keys should be rejected, not silently ignored. This keeps
the metadata contract small enough to review.

Validator-only V1 is explicitly forbidden from:

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
`plan_paths`, but it never renders and does not need V1 bool/number value
restrictions. If `value` is present, it is advisory only.

## Conflict Handling

The engine should fail before rendering when pack metadata produces ambiguous
provider configuration:

- Two requirements for the same provider and setting specify different values.
- Two requirements for the same provider and setting specify conflicting
  remediation modes.
- Duplicate requirements for the same provider and setting appear, even if they
  are otherwise identical.
- A requirement marked `renderable_default` is missing safety evidence.
- A `renderable_default` value is not a JSON boolean or number.
- A requirement has no `plan_paths` or `reason`.
- A `renderable_default` requirement has no `remediation.evidence`.

Conflicts should be reported as provider-config metadata errors, not as
adoption drift.

## Promotion Criteria

A provider-config requirement should not become `renderable_default` until a
provider lab proves:

- The default provider configuration creates saved-plan drift.
- The proposed provider setting removes that drift.
- The setting does not alter the remote object outside Terraform's attribution,
  bookkeeping, or provider-owned metadata behavior.
- The value is not secret and not environment-specific.
- At least one resource that previously drifted becomes import-only/adoptable
  after the setting is applied.
- The lab report records cleanup and avoids committed state, plans, raw dumps,
  credentials, logs, and temp roots.

The GCP attribution-label finding satisfies the shape of this evidence, but the
finding is valid for diagnostic and validator work only. It is not valid for
rendering until the rendering preconditions above are implemented.

## Future Assert-Adoptable Behavior

Future `assert-adoptable` behavior may annotate blocked plan changes with the
provider-config requirement id, setting, and reason when a matching requirement
explains the drift.

That annotation must keep the plan blocked. Matching a provider-config
requirement is guidance, not tolerance. It must not downgrade a blocked plan to
tolerated or clean.

## Expected Workflow

1. A lab finds provider-level drift in saved plan JSON.
2. `engine.provider_config` reports the drift as unmatched.
3. The pack adds a `provider_config.requirements` entry with `plan_paths`,
   `setting`, `value`, and `reason`.
4. The diagnostic reports the drift as a provider-config requirement.
5. A validator-only PR accepts or rejects remediation metadata without rendering
   provider blocks.
6. A later design-reviewed implementation may allow selected requirements to
   become `renderable_default` after rendering preconditions are implemented.
7. The original lab is rerun to prove the generated or required provider config
   produces import-only/adoptable plans.

## Open Questions

- What consumer-owned provider config path should generated env roots respect?
- Should `assert-adoptable` eventually fail with provider-config-specific
  remediation text when matching drift is detected?
- Should generated provider config be per resource root, per tenant, or shared
  through a common generated file?
- How should a future design represent provider aliases without expanding state
  blast radius or hiding user intent?
- Should provider-config metadata live only in pack manifests, or should tenant
  overlays be able to disable a pack default?

These should be answered before writing remediation code.
