# Import Oracle Adoption

## Why This Exists

Raw API fetches are good at discovering objects, but they are not always a
trustworthy source for Terraform/OpenTofu field coverage. The import oracle
path uses raw API data only for identity and import IDs, then asks the provider
what each imported object looks like through Terraform/OpenTofu state JSON.

The adoption flow is:

```text
raw API fetch
  -> derive stable key + import ID
  -> import into ephemeral fresh local Terraform/OpenTofu state
  -> terraform/tofu show -json state
  -> project provider-observed state through provider schema
  -> apply consumer-owned projection omissions
  -> write normal tfvars/imports/moves
  -> run normal plan
  -> classify clean / tolerated provider noise / blocked
```

## What It Does Not Do

- It does not use OpenAPI to decide field coverage.
- It does not use generated HCL as the source of truth.
- It does not store oracle state artifacts by default.
- It does not allow remote backend blocks in the oracle scratch root.
- It does not generate `lifecycle.ignore_changes`.
- It does not fix provider read/write bugs.

## Adoption Identity Metadata

Pack registries can provide explicit identity metadata for resources whose raw
API, provider import docs, and Terraform state use different names for the same
object identity:

```json
{
  "cloudflare_d1_database": {
    "generate": true,
    "product": "cloudflare",
    "adopt": {
      "key_field": "name",
      "identity_fields": {
        "raw_id": "uuid",
        "import_id": "uuid"
      }
    }
  }
}
```

`identity_fields` copies named raw identity paths into canonical identity fields
used by adoption. It preserves the source paths, fails loudly when a configured
path is missing, and does not infer aliases from field names. If an
`identity_fields.import_id` alias is present and `adopt.import_id` is omitted,
the import template defaults to `{import_id}`. An explicit `adopt.import_id`
always wins.

This metadata is only for stable key/import derivation. It does not change
Terraform schema projection, HCL rendering, advisory classification, or provider
state semantics.

## Workflow

```sh
make fetch TENANT=prod RESOURCE="zpa_application_segment"
make adopt IN=pulls/prod TENANT=prod RESOURCE="zpa_application_segment"
make gen-env TENANT=prod RESOURCE="zpa_application_segment"
make stage-imports TENANT=prod RESOURCE="zpa_application_segment"
make plan TENANT=prod RESOURCE="zpa_application_segment" SAVE=1
make assert-adoptable TENANT=prod RESOURCE="zpa_application_segment" POLICY=policy/prod/drift-policy.json
```

The existing transform path remains available:

```sh
make transform IN=pulls/prod TENANT=prod RESOURCE="zpa_application_segment"
make assert-clean TENANT=prod RESOURCE="zpa_application_segment"
```

## OpenTofu

The existing `TF` binary override is used for both Terraform and OpenTofu:

```sh
TF=tofu make adopt IN=pulls/prod TENANT=prod RESOURCE="zpa_application_segment"
TF=tofu make plan TENANT=prod RESOURCE="zpa_application_segment" SAVE=1
```

## Consumer Drift Policy

Projection omissions remove provider-observed values from generated tfvars when
they are not desired configuration:

```json
{
  "version": 1,
  "resource_types": {
    "aws_vpn_connection": {
      "projection_omit": [
        {
          "path": "vgw_telemetry[*].last_status_change",
          "reason": "Provider telemetry value; not desired configuration.",
          "approved_by": "network-platform",
          "ticket": "NET-1842"
        }
      ]
    }
  }
}
```

Plan tolerances classify final saved plans when provider noise remains:

```json
{
  "version": 1,
  "resource_types": {
    "aws_vpn_connection": {
      "plan_tolerate": [
        {
          "path": "vgw_telemetry[*].status",
          "actions": ["update"],
          "reason": "Known provider/API telemetry churn.",
          "approved_by": "network-platform",
          "ticket": "NET-1842"
        }
      ]
    }
  }
}
```

This policy is an Infrawright gate. It does not change Terraform/OpenTofu
planning behavior.

## Failure Modes

The oracle path fails closed for unsafe or ambiguous adoption:

- Raw JSON is not a list.
- Key or import ID metadata is missing.
- Derived keys or import IDs duplicate.
- Terraform/OpenTofu init/import/show fails.
- Imported state is missing.
- Required schema inputs are absent after projection.
- Sensitive input values would be written to generated tfvars.
- Final saved plans contain create, delete, replace, or untolerated update
  changes.

For troubleshooting scratch roots, set `INFRAWRIGHT_KEEP_ORACLE=1` before
running `make adopt`. Infrawright will print a warning with the kept directory.
That directory may contain unencrypted provider state, import IDs, credentials,
and provider diagnostics; remove it when debugging is complete.

Terraform/OpenTofu subprocess errors are redacted and truncated by default.
Full failing stdout/stderr is written only when the oracle workdir is explicitly
kept for debugging.

Each Terraform/OpenTofu subprocess has a timeout. Override the default with
`INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS=<seconds>` when a provider import/read is
legitimately slow.
