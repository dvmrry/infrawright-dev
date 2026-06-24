# NetBox Provider Lab PR22

## Scope

This lab exercised the PR15 import-oracle adoption path, the PR20 static
advisory report, and the PR21 oracle safety hardening against a disposable
NetBox target.

This is a provider lab report, not a committed NetBox pack. The temporary pack,
schema dump, raw API details, oracle state, projected tfvars, Terraform roots,
state, plans, and logs were kept under `/tmp/infrawright-netbox-lab` and are not
part of this repository.

## Environment

| Component | Value |
|---|---|
| NetBox | `4.6.3` / `4.6.3-Docker-5.0.1` |
| Terraform provider | `registry.terraform.io/e-breuninger/netbox` `v5.6.2` |
| Terraform | `v1.15.4` |
| Lab run prefix | `iw-pr22-20260623a` |
| Live cleanup | completed |

The lab used a temporary `INFRAWRIGHT_PACKS` root with ten NetBox resource
registry entries and the provider schema from `terraform providers schema
-json`. Provider credentials were supplied by environment variables. No remote
backend was used.

## Matrix

| Resource | Seeded | Oracle adopt | Initial plan | Policy omissions | Final plan |
|---|---:|---:|---:|---|---:|
| `netbox_tag` | yes | pass | import-only | none | pass |
| `netbox_tenant` | yes | pass | import-only | none | pass |
| `netbox_site` | yes | pass | import-only | none | pass |
| `netbox_manufacturer` | yes | pass | import-only | none | pass |
| `netbox_device_role` | yes | pass | import-only | none | pass |
| `netbox_device_type` | yes | pass | import-only | none | pass |
| `netbox_device` | yes | pass | failed validation | `rack_face` | pass |
| `netbox_prefix` | yes | pass | failed conflicts | `location_id`, `region_id`, `site_group_id`, `site_id` | pass |
| `netbox_ip_address` | yes | pass | failed conflicts/validation | `device_interface_id`, `interface_id`, `virtual_machine_interface_id`, `object_type`, `role` | pass |
| `netbox_vlan` | yes | pass | import-only | none | pass |

Final gate:

```text
all 10 saved plan(s) clean
```

## Advisory Summary

The static advisory report compared raw NetBox detail JSON, oracle-imported
provider state, projected tfvars, and policy omissions.

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `netbox_tag` | 8 | 2 | 4 | 0 | 0 | 0 |
| `netbox_tenant` | 18 | 3 | 3 | 0 | 0 | 0 |
| `netbox_site` | 18 | 1 | 14 | 0 | 0 | 0 |
| `netbox_manufacturer` | 12 | 0 | 2 | 0 | 0 | 0 |
| `netbox_device_role` | 13 | 0 | 5 | 0 | 0 | 0 |
| `netbox_device_type` | 32 | 1 | 7 | 0 | 0 | 0 |
| `netbox_device` | 70 | 3 | 21 | 1 | 0 | 0 |
| `netbox_prefix` | 25 | 1 | 9 | 4 | 0 | 0 |
| `netbox_ip_address` | 23 | 1 | 6 | 5 | 0 | 0 |
| `netbox_vlan` | 22 | 3 | 5 | 0 | 0 | 0 |
| **Total** | **241** | **15** | **76** | **10** | **0** | **0** |

Raw-only paths were expected to be high for NetBox because the API returns rich
relationship objects and UI metadata while the provider state often keeps the
Terraform-writeable scalar ID. Representative raw-only categories:

| Category | Count | Examples |
|---|---:|---|
| Relationship/display/metadata | 119 | `device_type.manufacturer.id`, `tenant.slug`, `display_url` |
| Derived counters/tree metadata | 51 | `device_count`, `prefix_count`, `_depth` |
| API enum/display objects | 16 | `status.label`, `status.value`, `family.label` |
| Other raw-only paths | 55 | `airflow`, `cluster`, `latitude`, `longitude` |

## Findings

The key-only import-oracle model worked for the sampled NetBox surface. All ten
objects imported from empty scratch resource stubs and projected provider state
without `required_missing` or `sensitive_blocked` failures.

The first real provider-specific adoption issue was absent-value placeholders.
For some optional fields, the NetBox provider reports `""` or `0` for an absent
API value. Projecting those values into tfvars can make Terraform fail before a
plan can be produced:

- `netbox_device.rack_face = ""` failed enum validation.
- `netbox_prefix` projected multiple zero-valued scope IDs that conflict with
  each other.
- `netbox_ip_address` projected zero-valued mutually exclusive interface target
  IDs plus empty validated strings for `object_type` and `role`.

The PR15 drift policy handled these as provider-observed projection omissions.
After policy-backed re-adoption, all ten saved plans were import-only and
`assert-adoptable` passed.

The PR20 advisory semantics worked as intended: policy omissions classified only
provider-observed unprojected paths, and raw-only provider-blind API paths
remained visible.

## Lab Friction

The first cleanup attempt used a broad NetBox list request with `limit=0` and
received a `502` from the small test deployment. The seeder was changed to use
narrow filtered lookups.

Temporary env roots under `/tmp` initially generated module source paths that
crossed macOS' `/tmp` to `/private/tmp` symlink and Terraform could not read the
module directory. Normalizing the lab overlay to the real path fixed the issue.
This is lab-harness friction rather than a NetBox provider failure, but it is a
useful reminder for future provider labs that use absolute overlays outside the
repository.

## Follow-Ups

- Keep testing more providers before adding generic zero/empty omission logic.
  NetBox proves the pattern exists, but Grafana/Cloudflare should shape whether
  this becomes policy guidance, diagnostics, or engine behavior.
- Use the absent/default diagnostics command to classify placeholder-shaped
  projected values before turning any zero/empty omission into pack behavior.
- Consider a provider-lab helper that creates realpath-normalized temporary
  overlays and avoids broad API list requests by default.
- Consider reporting raw-only path categories in the advisory output so
  relationship/display metadata is easier to distinguish from security-relevant
  provider-blind fields.
