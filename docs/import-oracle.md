# Import Oracle Adoption

## Why This Exists

Raw API fetches are good at discovering objects, but they are not always a
trustworthy source for Terraform/OpenTofu field coverage. The import oracle
path uses raw API data only for identity and import IDs, plus explicit
`projection_fill` exceptions where a pack or operator policy identifies a
provider-read omission that must be restored from the same raw pull. Otherwise
the provider state reported through Terraform/OpenTofu JSON remains the
configuration truth.

The adoption flow is:

```text
raw API fetch
  -> derive stable key + import ID
  -> render scratch Terraform/provider header + import blocks
  -> plan with generated provider config into ephemeral local state
  -> apply the import-only scratch plan
  -> terraform/tofu show -json state
  -> project provider-observed state through provider schema
     (projection_omit applies inline)
  -> apply consumer-owned post-projection policy
     (projection_sync -> projection_fill -> projection_omit_if)
  -> write normal tfvars/imports/moves
  -> run normal plan
  -> classify clean / tolerated provider noise / blocked
```

## What It Does Not Do

- It does not use OpenAPI to decide field coverage.
- It does not use generated HCL as the source of truth.
- It does not use raw API values for configuration unless a pack or operator
  policy explicitly declares a `projection_fill` path.
- It does not store oracle state artifacts by default.
- It does not allow remote backend blocks in the oracle scratch root.
- It does not apply non-import changes from the scratch root; it checks the
  saved plan JSON for exact import-only resource changes and stops before apply
  if Terraform or OpenTofu reports drift, add, change, destroy, or unexpected
  addresses.
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

Identity-less singleton resources can use a literal `constant_key` instead of a
`key_field`:

```json
{
  "zia_end_user_notification": {
    "generate": true,
    "product": "zia",
    "adopt": {
      "constant_key": "enduser_notification",
      "import_id": "enduser_notification"
    }
  }
}
```

This is only valid for singleton reads. `make adopt` fails before oracle import
if `constant_key` is configured and the raw read produces more than one item
after skip predicates. The key is used verbatim as the generated item key; it is
not derived from, or filled into, the provider-state payload.

## Workflow

The import-oracle path uses the stable adoption command sequence documented in
[Adoption Command Surface](adoption-command-surface.md). The normal workflow is:

```sh
make fetch TENANT=prod RESOURCE="zpa_application_segment"
make adopt IN=pulls/prod TENANT=prod RESOURCE="zpa_application_segment"
make gen-env TENANT=prod RESOURCE="zpa_application_segment"
make stage-imports TENANT=prod RESOURCE="zpa_application_segment"
make plan TENANT=prod RESOURCE="zpa_application_segment" SAVE=1
make assert-adoptable TENANT=prod RESOURCE="zpa_application_segment" POLICY=policy/prod/drift-policy.json
make apply TENANT=prod RESOURCE="zpa_application_segment" POLICY=policy/prod/drift-policy.json
```

Use the same `POLICY` for `apply` that was used for `assert-adoptable` when a
saved plan contains intentionally tolerated drift. `ALLOW_PLAN_CHANGES=1`
remains a broad legacy override for blocked saved plans and is not the normal
path for policy-backed adoption.

The existing transform path remains available:

```sh
make transform IN=pulls/prod TENANT=prod RESOURCE="zpa_application_segment"
make assert-clean TENANT=prod RESOURCE="zpa_application_segment"
```

`transform` projects raw API bodies directly and remains useful for demos and
pack development. It is not the import-oracle adoption path; use `adopt` when
Terraform/OpenTofu provider state should be the configuration truth.

## Credential-Free Demo Contract

The shipped demo can prove the deterministic local artifact contract without
provider credentials:

```sh
make demo
make demo-contract
```

`make demo` materializes the demo overlay from committed fixtures.
`make demo-contract` then verifies committed demo config/import artifacts do not
drift, rejects stale demo moved-block files, and checks that the generated demo
module tree matches the module generator.

This is not a live-provider import or plan proof. It does not call provider
APIs, run Terraform/OpenTofu import, or prove provider read semantics. The real
import-oracle plan contract begins with `make fetch` / `make adopt` against a
real provider tenant and continues through `make plan SAVE=1` and
`make assert-adoptable`.

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

Projection sync fills an absent, null, or empty-list/object target from a
source path after normal schema projection. It never overwrites a populated
target, even when the target differs from the source; that difference remains
visible to the adoption oracle. Sync targets are restricted to writable input
attributes and the source/target schema types must match.

The ZIA DNS rule policy below handles zia provider 4.7.26 evidence from
`firewallDNSCategoriesMirrorCustomizeDiff`, which compares
`res_categories` and `dest_ip_categories` as sets. Live probes rejected
`dest_ip_categories` with `res_categories` omitted, rejected
`res_categories = ["ANY"]`, and accepted both sets equal with
0 add / 0 change / 0 destroy:

```json
{
  "version": 1,
  "resource_types": {
    "zia_firewall_dns_rule": {
      "projection_sync": [
        {
          "target_path": "res_categories",
          "source_path": "dest_ip_categories",
          "reason": "ZIA provider 4.7.26 CustomizeDiff firewallDNSCategoriesMirrorCustomizeDiff requires res_categories to equal dest_ip_categories as sets; live probes rejected dest set + res omitted and res=[\"ANY\"], and accepted both equal with 0 add / 0 change / 0 destroy.",
          "approved_by": "zscaler-adoption"
        }
      ]
    }
  }
}
```

Conditional projection omit removes only matching leaves. Values use strict JSON
equality: booleans are not numbers, so `false` does not match `0`. It does not
cascade-remove emptied parent objects or blocks. A `projection_omit_if` entry is
rejected if its terminal schema path is required.

The ZIA network service policy below handles live readback probes for
`h_323`, `zscaler_proxy_my_services`, and `webex`, where single-port blocks
come back as `{ "start": N, "end": 0 }` and projecting `end = 0` diffs. The
policy omits only the provider's `end: 0` sentinel:

```json
{
  "version": 1,
  "resource_types": {
    "zia_firewall_filtering_network_service": {
      "projection_omit_if": [
        {
          "path": "dest_tcp_ports[*].end",
          "values": [0],
          "reason": "ZIA provider 4.7.26 reads h_323, zscaler_proxy_my_services, and webex single-port blocks back as {start: N, end: 0}; omitting only the end: 0 sentinel avoids a projected diff.",
          "approved_by": "zscaler-adoption"
        },
        {
          "path": "dest_udp_ports[*].end",
          "values": [0],
          "reason": "ZIA provider 4.7.26 reads h_323, zscaler_proxy_my_services, and webex single-port blocks back as {start: N, end: 0}; omitting only the end: 0 sentinel avoids a projected diff.",
          "approved_by": "zscaler-adoption"
        },
        {
          "path": "src_tcp_ports[*].end",
          "values": [0],
          "reason": "ZIA provider 4.7.26 reads h_323, zscaler_proxy_my_services, and webex single-port blocks back as {start: N, end: 0}; omitting only the end: 0 sentinel avoids a projected diff.",
          "approved_by": "zscaler-adoption"
        },
        {
          "path": "src_udp_ports[*].end",
          "values": [0],
          "reason": "ZIA provider 4.7.26 reads h_323, zscaler_proxy_my_services, and webex single-port blocks back as {start: N, end: 0}; omitting only the end: 0 sentinel avoids a projected diff.",
          "approved_by": "zscaler-adoption"
        }
      ]
    }
  }
}
```

Projection fill restores a whole top-level writable attribute or block from the
raw API pull when provider readback omits it but provider write validation
requires it. V1 deliberately accepts only a single top-level target name and a
single top-level raw source name. It never overwrites provider readback: if the
projected provider state already contains the target, even as an empty list or
object, the fill entry remains stale. It also refuses sensitive targets.

The ZIA URL filtering rule policy below handles ISOLATE rules where provider
readback omits `cbi_profile`, but provider write validation requires
`cbi_profile` when `action = "ISOLATE"`. The raw `urlFilteringRules` API pull
carries `cbiProfile` with `id`, `name`, `profileSeq`, and `url`, so the value is
recoverable without synthesis:

```json
{
  "version": 1,
  "resource_types": {
    "zia_url_filtering_rules": {
      "projection_fill": [
        {
          "path": "cbi_profile",
          "source": "cbiProfile",
          "reason": "ZIA provider 4.7.26 read omits cbi_profile for ISOLATE URL filtering rules, while write validation requires cbi_profile when action is ISOLATE; the raw urlFilteringRules API pull carries cbiProfile with id/name/profileSeq/url.",
          "approved_by": "zscaler-adoption"
        }
      ]
    }
  }
}
```

Projection policy application order is fixed: `projection_omit` applies inline
during schema projection and may suppress sensitive, absent, or optional fields,
preserving its established behavior. After projection, `projection_sync` fills
from provider-observed state, `projection_fill` fills from explicit raw-pull
sources, and then `projection_omit_if` strips matching leaves. The order is not
configurable. Conditional omit can strip a value that sync or fill just wrote;
the shipped use cases touch disjoint paths.

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
raw API pull values, generated configuration, and provider diagnostics; remove
it when debugging is complete.

Terraform/OpenTofu subprocess errors are redacted and truncated by default.
Full failing stdout/stderr is written only when the oracle workdir is explicitly
kept for debugging.

Each Terraform/OpenTofu subprocess has a timeout. Override the default with
`INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS=<seconds>` when a provider import/read is
legitimately slow.
