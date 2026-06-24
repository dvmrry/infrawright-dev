# Cloudflare Dummy Account Lab PR30

## Scope

This lab exercised the PR15 import-oracle adoption path, the PR20 static
certification/advisory report, the PR21 oracle safety hardening, and the PR28
static sensitive advisory derivation against a disposable Cloudflare account.

This is a provider lab report, not a committed Cloudflare pack. The temporary
pack, schema dump, raw API details, oracle state, projected tfvars, Terraform
roots, state, plans, and logs were kept under
`/tmp/infrawright-cloudflare-dummy-lab` and are not part of this repository.

Unlike the first Cloudflare read-only lab, this run performed live writes in a
dummy account and then cleaned them up.

## Environment

| Component | Value |
|---|---|
| Cloudflare account | disposable dummy account |
| Terraform provider | `registry.terraform.io/cloudflare/cloudflare` `v5.21.1` |
| Terraform | `v1.15.4` |
| Lab run prefix | `iw-cf-dummy-20260623a` |
| Live cleanup | completed and verified |

The lab used a temporary `INFRAWRIGHT_PACKS` root with six Cloudflare resource
registry entries and the provider schema from `terraform providers schema
-json`. Provider credentials were supplied by environment variables. No remote
backend was used.

The account had no zone/domain during this run. The lab therefore focused on
account-level resources that can be created without a zone. R2 was skipped
because the account returned that R2 must be enabled in the Cloudflare
dashboard before bucket APIs are usable.

## Seeded Resources

| Resource | Seeded object |
|---|---|
| `cloudflare_list` | IP list |
| `cloudflare_list_item` | IP list item |
| `cloudflare_workers_kv_namespace` | Workers KV namespace |
| `cloudflare_workers_kv` | Workers KV key/value |
| `cloudflare_queue` | Queue |
| `cloudflare_turnstile_widget` | Managed Turnstile widget |

Pre-clean found no matching lab resources. Seed created all six resource types
without API errors. Post-run cleanup deleted all lab resources and a direct
follow-up probe confirmed no lab-prefixed lists, KV namespaces, queues, or
Turnstile widgets remained.

## Matrix

| Resource | Raw fixtures | Oracle adopt | Saved plan | Final gate |
|---|---:|---:|---:|---:|
| `cloudflare_list` | 1 | pass | import-only | pass |
| `cloudflare_list_item` | 1 | pass | import-only | pass |
| `cloudflare_workers_kv_namespace` | 1 | pass | import-only | pass |
| `cloudflare_workers_kv` | 1 | pass | import-only | pass |
| `cloudflare_queue` | 1 | pass | import-only | pass |
| `cloudflare_turnstile_widget` | 1 | pass | import-only | pass |

Final gate:

```text
all 6 saved plan(s) clean (no-op/imports only)
```

Each saved Terraform plan contained one imported resource with
`actions: ["no-op"]` and import metadata present in the plan JSON.

## Advisory Summary

The static certification report compared raw Cloudflare detail JSON,
oracle-imported provider state, projected tfvars, and policy omissions.

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `cloudflare_list` | 3 | 0 | 9 | 0 | 0 | 0 |
| `cloudflare_list_item` | 0 | 4 | 4 | 0 | 0 | 0 |
| `cloudflare_workers_kv_namespace` | 0 | 0 | 2 | 0 | 0 | 0 |
| `cloudflare_workers_kv` | 1 | 0 | 5 | 0 | 0 | 0 |
| `cloudflare_queue` | 0 | 1 | 5 | 0 | 0 | 0 |
| `cloudflare_turnstile_widget` | 0 | 1 | 9 | 0 | 0 | 1 |
| **Total** | **4** | **6** | **34** | **0** | **0** | **1** |

Representative advisory paths:

| Resource | Raw-only examples | Provider-only examples | Sensitive blocked |
|---|---|---|---|
| `cloudflare_list` | `items[].created_on`, `items[].id`, `items[].modified_on` | none | none |
| `cloudflare_list_item` | none | `asn`, `hostname`, `operation_id`, `redirect` | none |
| `cloudflare_workers_kv` | `metadata.source` | none | none |
| `cloudflare_queue` | none | `id` | none |
| `cloudflare_turnstile_widget` | none | `id` | `secret` |

## Findings

The key-only import-oracle model worked cleanly for all six account-level
Cloudflare resources tested. The lab did not hit NetBox-style absent optional
placeholders or Grafana-style sensitive-but-required nested blocks.

This run added one important data point over the read-only Cloudflare lab:
Cloudflare Turnstile returns a provider-observed sensitive `secret`. PR28's
static advisory derivation correctly reported that path as
`sensitive_blocked`, while adoption still planned cleanly because `secret` is
read-only provider state rather than a structurally required config block. That
is the healthy version of the Grafana contact-point failure class: sensitive
state is visible in advisory output without becoming an adoption blocker.

Cloudflare list-item resources again showed provider-only union alternatives.
An IP list item projected `ip` and `comment`, while provider state also exposed
other possible item arms such as `asn`, `hostname`, and `redirect`. These look
like harmless inactive union branches for this resource shape.

Workers KV exposed a minor static diff shape: raw metadata was an object with
`metadata.source`, while provider state/projection represented metadata as the
top-level `metadata` string input. The plan was clean, so this is an advisory
shape mismatch rather than an adoption failure.

Queue adoption projected provider defaults for `settings.delivery_delay`,
`settings.delivery_paused`, and `settings.message_retention_period`, and the
saved import-only plan stayed clean.

## Lab Friction

The account-token verifier endpoint worked for this dummy account:
`/accounts/<account_id>/tokens/verify` reported an active token. That reinforces
the read-only lab's token-class lesson: Cloudflare account-owned tokens should
be verified with account-token-aware checks.

The temporary harness hit a shell issue when a zsh scalar containing multiple
resource names was passed as a single selector. Re-running with a bash array
fixed it. Future provider-lab helpers should avoid shell-dependent word
splitting and pass resource selectors as explicit argv elements.

## Follow-Ups

- Run a zone-scoped Cloudflare lab after adding a disposable domain to the
  dummy account. That should cover DNS records, zone settings, rulesets, page
  rules, and Workers routes without risking a real account.
- Consider classifying provider-only union alternatives such as
  `cloudflare_list_item.asn`, `hostname`, and `redirect` as harmless inactive
  union branches when another union arm is selected.
- Consider improving advisory comparison for string-encoded JSON fields such as
  Workers KV `metadata`, where raw detail paths and provider config paths are
  semantically related but structurally different.
- Add a Cloudflare provider-readiness note for account-owned API token health
  checks.
