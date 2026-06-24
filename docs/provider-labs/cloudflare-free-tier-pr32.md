# Cloudflare Free-Tier Lab

Date: 2026-06-24  
Provider: `cloudflare/cloudflare` `v5.21.1`  
Terraform: `v1.15.4`  
Target: disposable Cloudflare account with active free-plan zone `iw-cf-lab.xyz`

This lab expanded the Cloudflare coverage beyond the first account/zone passes by
testing as much of the current free-tier surface as was safe to create, import,
plan, and clean up. No raw dumps, state files, plans, tokens, or temporary
Terraform roots are committed.

## Summary

Result: `16` additional resources reached the import-oracle/advisory path.

| Outcome | Count |
|---|---:|
| Oracle import/read succeeded | 16 |
| Adopted config generated | 16 |
| Import-only/adoptable plan | 15 |
| Blocked by plan drift | 1 |
| Sensitive blocked | 0 |
| Required missing | 0 |

Static advisory totals for the 16 resources:

| Advisory bucket | Count |
|---|---:|
| Items | 16 |
| Projected paths | 102 |
| Raw-only paths | 19 |
| Provider-only paths | 20 |
| Omitted by policy | 0 |
| Required missing | 0 |
| Sensitive blocked | 0 |

Cleanup was verified after the run: no lab-prefixed access rules, user-agent
rules, filters, D1 databases, Pages projects, Workers scripts, or Workers
objects remained.

## Credential Safety

During setup, a non-lab Cloudflare token was detected in the shell environment.
No live writes were performed with that token. The lab switched back to the
disposable Cloudflare lab token before creating resources.

## Certified Resources

These resources imported, projected, staged imports, and planned as import-only:

| Resource | Notes |
|---|---|
| `cloudflare_access_rule` | Seeded disabled-impact lab IP challenge rule. |
| `cloudflare_argo_tiered_caching` | Read-only singleton import. |
| `cloudflare_d1_database` | Required registry identity alias: raw/API `uuid` is the import database ID. |
| `cloudflare_email_routing_settings` | Read-only singleton import, zone remained unconfigured. |
| `cloudflare_managed_transforms` | Read-only singleton import. |
| `cloudflare_pages_project` | Clean plan; provider emitted nested deprecated `usage_model` warnings from output projection. |
| `cloudflare_tiered_cache` | Read-only singleton import. |
| `cloudflare_total_tls` | Read-only singleton import. |
| `cloudflare_universal_ssl_setting` | Read-only singleton import. |
| `cloudflare_url_normalization_settings` | Read-only singleton import. |
| `cloudflare_user_agent_blocking_rule` | Seeded paused lab user-agent rule. |
| `cloudflare_worker` | Seeded lab Worker metadata object. |
| `cloudflare_workers_script` | Clean with a provisional schema prune for unused dynamic `assets.config.run_worker_first`. |
| `cloudflare_workers_script_subdomain` | Seeded lab Workers subdomain binding, then destroyed. |
| `cloudflare_zone_dnssec` | Read-only singleton import. |

`cloudflare_zone_hold` imported and projected, but `assert-adoptable` blocked it:

```text
BLOCKED: cf-free/cloudflare_zone_hold
  module.cloudflare_zone_hold.cloudflare_zone_hold.this["<zone_id>"] update blocked
    - hold
    - hold_after
    - include_subdomains
```

This is another absent/default-value case. The provider imports the disabled
zone hold, but the projected config is not neutral: Terraform wants to update
`hold`, `hold_after`, and `include_subdomains` after import.

## Free-Tier And Provider Boundaries

The following candidates did not reach certification because Cloudflare or the
provider rejected them before Infrawright projection could be evaluated:

| Resource | Result |
|---|---|
| `cloudflare_argo_smart_routing` | Import failed with Cloudflare `401` on the Argo smart-routing endpoint. |
| `cloudflare_regional_tiered_cache` | Import failed with Cloudflare `403`. |
| `cloudflare_logpull_retention` | Import failed with Cloudflare `401`. |
| `cloudflare_zone_cache_reserve` | Import failed with Cloudflare `403`. |
| `cloudflare_zone_cache_variants` | Import failed with Cloudflare `403`. |
| `cloudflare_healthcheck` | Create failed: health checks disabled for the free zone. |
| `cloudflare_firewall_rule` | Create failed: legacy firewall rules API is in maintenance mode. |
| `cloudflare_filter` | Deprecated API/provider issue: apply returned an invalid result object with unknown `id`; cleanup found no remaining lab filter. |
| `cloudflare_workers_cron_trigger` | Skipped before apply because provider warns this resource cannot be destroyed from Terraform. |

`cloudflare_zone_dns_settings` and `cloudflare_leaked_credential_check` were not
included in the live pass because the provider docs say they do not currently
support `terraform import`.

## Lessons

Cloudflare is still a strong target for the import-oracle architecture. With
correct token scopes, many singleton zone settings can be adopted with key-only
raw input and no writes.

The recurring engine gaps are now clearer:

- Dynamic schema attributes are a real Cloudflare blocker. `cloudflare_dns_record`
  previously needed a temporary `data.flags` prune, and `cloudflare_workers_script`
  needed a temporary `assets.config.run_worker_first` prune.
- Identity aliases need pack metadata. D1’s raw API uses `uuid`, provider docs call
  it `database_id`, and Terraform state exposes it as `id`.
- Absent/default semantics are provider-specific. `cloudflare_zone_hold` is the
  Cloudflare version of the NetBox-style absent optional value problem.
- Generated outputs can still read deprecated nested fields. `cloudflare_pages_project`
  planned cleanly, but output projection triggered provider deprecation warnings
  for nested `deployment_configs.*.usage_model`.
- Deprecated or maintenance-mode provider resources should be classified as
  provider boundaries, not engine defects.

## Follow-Ups

- Use the dynamic schema diagnostics command to classify temporary per-resource
  schema prunes before choosing a keep/drop/remediate strategy.
- Track provider/API identity aliases in pack metadata, starting with D1
  `uuid`/`database_id`/`id`.
- Use the absent/default diagnostics command to classify `cloudflare_zone_hold`
  plan drift before choosing normalization, omission, or tolerance behavior.
- Consider nested deprecated-field output projection so clean plans do not emit
  noisy deprecation warnings.
