# Cloudflare Provider Lab PR29

## Scope

This lab exercised the PR15 import-oracle adoption path, the PR20 static
certification/advisory report, the PR21 oracle safety hardening, and the PR28
static sensitive advisory derivation against a real Cloudflare account.

This is a provider lab report, not a committed Cloudflare pack. The temporary
pack, schema dump, raw API details, oracle state, projected tfvars, Terraform
roots, state, plans, and logs were kept under
`/tmp/infrawright-cloudflare-readonly-lab` and are not part of this repository.

The account is real, so the lab was intentionally read-only. A previously
prepared seeder that would create and delete account-level Cloudflare resources
was not run.

## Environment

| Component | Value |
|---|---|
| Cloudflare account | real account, read-only lab usage |
| Terraform provider | `registry.terraform.io/cloudflare/cloudflare` `v5.21.1` |
| Terraform | `v1.15.4` |
| Lab run prefix | `cf-readonly` |
| Live cleanup | no live writes performed |

The lab used a temporary `INFRAWRIGHT_PACKS` root with four Cloudflare resource
registry entries and the provider schema from `terraform providers schema
-json`. Provider credentials were supplied by environment variables. No remote
backend was used.

The token was an account-owned API token. The user-token verifier endpoint did
not accept it, but safe product reads against account and zone list endpoints
worked. This is a useful onboarding trap: provider health checks for Cloudflare
should distinguish user API tokens from account-owned API tokens instead of
treating `/user/tokens/verify` as the only authority.

The user identified `99x.gg` as the preferred disposable domain for edits, but
that zone was not visible to the token during the lab. The visible zones were
`91x.gg` and `mrry.io`, so the run stayed read-only.

## Token Scope

Safe account-scoped reads worked:

- account listing
- zone listing
- account detail
- account rules lists
- account rules list items
- queues list
- KV namespace list

Several zone-scoped reads returned authorization errors:

- DNS records
- zone rulesets
- zone settings
- page rules

That means this lab is not comprehensive Cloudflare provider coverage. It is a
real account-token, read-only sample across four accessible resource types.

## Matrix

| Resource | Raw fixtures | Oracle adopt | Saved plan | Final gate |
|---|---:|---:|---:|---:|
| `cloudflare_account` | 1 | pass | import-only | pass |
| `cloudflare_zone` | 2 | pass | import-only | pass |
| `cloudflare_list` | 1 | pass | import-only | pass |
| `cloudflare_list_item` | 2 | pass | import-only | pass |

Final gate:

```text
all 4 saved plan(s) clean (no-op/imports only)
```

Every sampled resource imported from an empty scratch resource stub, refreshed
through the provider, projected into tfvars, produced a saved import-only plan,
and passed the adoptability gate.

## Advisory Summary

The static certification report compared raw Cloudflare detail JSON,
oracle-imported provider state, projected tfvars, and policy omissions.

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `cloudflare_account` | 6 | 2 | 3 | 0 | 0 | 0 |
| `cloudflare_zone` | 6 | 12 | 8 | 0 | 0 | 0 |
| `cloudflare_list` | 0 | 0 | 15 | 0 | 0 | 0 |
| `cloudflare_list_item` | 0 | 10 | 18 | 0 | 0 | 0 |
| **Total** | **12** | **24** | **44** | **0** | **0** | **0** |

Representative advisory shapes:

| Resource | Raw-only examples | Provider-only examples |
|---|---|---|
| `cloudflare_account` | `legacy_flags.enterprise_zone_quota.*`, `settings.api_access_enabled`, `settings.oauth_app_access_enabled` | `managed_by`, `unit` |
| `cloudflare_zone` | `account.name`, `owner.email`, `vanity_name_servers_ips` | `cname_suffix`, `verification_key`, `meta.*`, `owner.name` |
| `cloudflare_list` | none in this sample | none in this sample |
| `cloudflare_list_item` | none in this sample | absent union variants such as `asn`, `hostname`, `ip`, plus `operation_id` |

## Findings

The key-only import-oracle model worked cleanly for this accessible Cloudflare
sample. The lab did not hit NetBox-style absent optional placeholders or
Grafana-style sensitive-but-required nested blocks.

The sampled Cloudflare resources mostly showed metadata and union-shape
differences:

- Account raw API details included legacy quota and access-toggle metadata that
  the provider did not expose for configuration.
- Zone raw API details included relationship/display fields such as account and
  owner names, while provider state added computed metadata such as
  verification keys and zone metadata flags.
- List resources aligned exactly across raw, provider state, and projection for
  the sampled redirect list.
- List item resources projected the selected redirect shape cleanly, while the
  provider also surfaced empty alternative union fields for other item kinds.

This is an encouraging result for the import-oracle path, but it should not be
read as broad Cloudflare certification. The provider has a large surface, and
the token only allowed a narrow read-only slice.

## Lab Friction

Cloudflare account-owned API tokens have a different validation shape than user
API tokens. The initial token check against the user-token verifier produced an
invalid-token response, while safe account and zone list calls with the same
token succeeded. Future Cloudflare lab tooling should validate the exact token
class and intended surface instead of relying on one verifier endpoint.

The user had a preferred disposable edit target, `99x.gg`, but it was not
visible to the token. Because the visible zones were not explicitly disposable,
the lab did not create, update, or delete Cloudflare resources.

## Follow-Ups

- Add a Cloudflare provider-readiness note for account-owned API tokens and
  token-class-aware health checks.
- Run a broader Cloudflare lab once a disposable zone is visible and the token
  has DNS, ruleset, setting, and page-rule read scopes.
- Consider classifying provider-only union alternatives such as
  `cloudflare_list_item.asn`, `hostname`, and `ip` as harmless absent
  alternatives when a different union arm is selected.
- Do not generalize this result across the full Cloudflare provider surface.
  This lab sampled four accessible resource types, not the complete provider.
