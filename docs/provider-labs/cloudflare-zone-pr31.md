# Cloudflare Zone Lab PR31

## Scope

This lab exercised the PR15 import-oracle adoption path, the PR20 static
certification/advisory report, the PR21 oracle safety hardening, and the PR28
static sensitive advisory derivation against a disposable Cloudflare zone in
the dummy account.

This is a provider lab report, not a committed Cloudflare pack. The temporary
pack, raw API details, oracle state, projected tfvars, Terraform roots, state,
plans, and logs were kept under `/tmp/infrawright-cloudflare-zone-lab` and are
not part of this repository.

The lab performed live writes only for lab-prefixed zone resources and then
cleaned them up.

## Environment

| Component | Value |
|---|---|
| Cloudflare account | disposable dummy account |
| Zone | `iw-cf-lab.xyz` |
| Zone status | `pending` at seed time, `active` at final cleanup verification |
| Terraform provider | `registry.terraform.io/cloudflare/cloudflare` `v5.21.1` |
| Terraform | `v1.15.4` |
| Lab run prefix | `iw-cf-zone-20260623a` |
| Live cleanup | completed and verified |

The lab used a temporary `INFRAWRIGHT_PACKS` root and the provider schema from
`terraform providers schema -json`. Provider credentials were supplied by
environment variables. No remote backend was used.

## Seed Results

| Resource | Seed result | Notes |
|---|---:|---|
| `cloudflare_dns_record` | pass after retry | Initial attempt with DNS record tags failed because the free-zone tag quota was `0`. Untagged record worked. |
| `cloudflare_ruleset` | pass | Disabled custom firewall ruleset. |
| `cloudflare_workers_route` | pass | Route without script. |
| `cloudflare_zone_setting` | read fixture only | `browser_cache_ttl` setting was readable, but module generation is blocked by `dynamic` schema type. |
| `cloudflare_page_rule` | blocked | Cloudflare returned `1011`: Page Rules endpoint does not support account-owned tokens. |

Post-run cleanup deleted the DNS record, ruleset, and Workers route. A final
direct probe confirmed no lab-prefixed DNS records, page rules, rulesets, or
Workers routes remained.

## Engine Blockers

The unmodified Cloudflare provider schema exposed a real engine limitation:
`engine.tfschema.hcl_type()` does not support Terraform provider schema
primitive type encoding `dynamic`.

That blocked module generation for:

| Resource | Dynamic path | Impact |
|---|---|---|
| `cloudflare_dns_record` | `data.flags` | The A record did not need `data`, but the generated module still failed on the optional dynamic member. |
| `cloudflare_zone_setting` | `value` | Zone setting values are inherently dynamic, so this resource could not be exercised through generated modules. |

To continue collecting Cloudflare evidence, the lab used a temporary narrowed
schema for the clean-plan pass: it removed the unused DNS `data` attribute and
excluded `cloudflare_zone_setting`. That workaround was local to `/tmp` and is
not a production claim.

## Matrix

| Resource | Raw fixtures | Oracle adopt | Saved plan | Final gate |
|---|---:|---:|---:|---:|
| `cloudflare_dns_record` | 1 | pass | import-only | pass |
| `cloudflare_ruleset` | 1 | pass | import-only | pass |
| `cloudflare_workers_route` | 1 | pass | import-only | pass |

Final gate:

```text
all 3 saved plan(s) clean (no-op/imports only)
```

Each saved Terraform plan contained one imported resource with
`actions: ["no-op"]` and import metadata present in the plan JSON.

## Advisory Summary

The static certification report compared raw Cloudflare detail JSON,
oracle-imported provider state, projected tfvars, and policy omissions.

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `cloudflare_dns_record` | 0 | 5 | 10 | 0 | 0 | 0 |
| `cloudflare_ruleset` | 3 | 1 | 93 | 0 | 0 | 0 |
| `cloudflare_workers_route` | 1 | 0 | 2 | 0 | 0 | 0 |
| **Total** | **4** | **6** | **105** | **0** | **0** | **0** |

Representative advisory paths:

| Resource | Raw-only examples | Provider-only examples |
|---|---|---|
| `cloudflare_dns_record` | none | `data`, `meta`, `priority`, `private_routing`, `tags_modified_on` |
| `cloudflare_ruleset` | `rules[].last_updated`, `rules[].version`, `source` | `account_id` |
| `cloudflare_workers_route` | `request_limit_fail_open` | none |

## Findings

The key-only import-oracle model worked cleanly for the zone resources that
could be generated with the temporary narrowed schema. DNS record, ruleset, and
Workers route all imported from empty scratch stubs, projected into tfvars, and
planned as import-only/no-op.

Cloudflare zone resources introduced a different failure class than the earlier
NetBox, Grafana, and Cloudflare account-level labs: provider schema `dynamic`
types. This is not a provider API mismatch; it is an engine type-rendering
gap. Supporting dynamic types, or failing louder with a provider-readiness
classification, is required before broad Cloudflare zone support can be called
real.

The Page Rules failure is token-class-specific. The account-owned API token was
valid for modern zone APIs such as DNS, rulesets, Workers routes, and settings,
but the legacy Page Rules endpoint rejected it. Future Cloudflare labs should
either skip Page Rules for account-owned tokens or use an appropriate user API
token specifically for that legacy endpoint.

The DNS tag failure is plan/entitlement-specific. Free zones can have a DNS
record tag quota of `0`, so seeders should not assume tags are available even
when the provider schema exposes them.

Cloudflare rulesets are a high-surface resource. A single disabled rule
projected 93 leaf paths because the provider expands many optional nested rule
members. The resulting plan was still clean, but this is a good candidate for
future schema-aware or absent-value classification work.

## Lab Friction

The zone became visible through the API while still `pending`, and the seeded
zone resources could be created/imported before the final nameserver
activation completed. By final cleanup verification, the zone reported
`active`.

The temporary schema workaround was necessary only for the lab. It should not
be merged as a Cloudflare pack behavior.

## Follow-Ups

- Add engine support or explicit readiness diagnostics for Terraform provider
  schema primitive type `dynamic`.
- Re-test `cloudflare_dns_record` against the unmodified provider schema after
  dynamic type handling exists.
- Re-test `cloudflare_zone_setting` after dynamic values are supported or
  explicitly classified as unsupported.
- Decide whether Cloudflare Page Rules should be skipped for account-owned API
  tokens or tested with a separate user-token path.
- Keep DNS record tags out of default Cloudflare seeders unless the zone quota
  proves they are available.
