# <Provider> Provider Lab

Date: `<YYYY-MM-DD>`  
Provider: `<source>` `<version>`  
Terraform/OpenTofu: `<version>`  
Target: `<disposable target summary>`

This lab exercised the import-oracle adoption path and static advisory report
against a disposable `<provider>` target. This is a provider lab report, not a
committed provider pack. Temporary packs, schemas, raw API details, oracle state,
projected tfvars, Terraform roots, state, plans, and logs were kept under
`/tmp/infrawright-<provider>-lab` and are not part of this repository.

## Summary

| Outcome | Count |
|---|---:|
| Resources attempted | `<n>` |
| Oracle import/read succeeded | `<n>` |
| Adopted config generated | `<n>` |
| Import-only/adoptable plan | `<n>` |
| Blocked by plan drift | `<n>` |
| Required missing | `<n>` |
| Sensitive blocked | `<n>` |

Static advisory totals:

| Advisory bucket | Count |
|---|---:|
| Items | `<n>` |
| Projected paths | `<n>` |
| Raw-only paths | `<n>` |
| Provider-only paths | `<n>` |
| Omitted by policy | `<n>` |
| Required missing | `<n>` |
| Sensitive blocked | `<n>` |

Cleanup: `<Terraform/API/manual cleanup result and verification summary>`.

## Credential Safety

`<State how credentials were scoped and whether a non-lab credential was
detected. Do not include token values, account IDs, tenant IDs, or other secrets
unless the ID is intentionally public fixture context.>`

## Environment

| Component | Value |
|---|---|
| Product | `<version/build>` |
| Terraform provider | `<source>` `<version>` |
| Terraform/OpenTofu | `<version>` |
| Lab run prefix | `<prefix>` |
| Temporary root | `/tmp/infrawright-<provider>-lab` |
| Live cleanup | `<completed/partial/not-created>` |

Provider credentials were supplied by environment variables. No remote backend
was used. Terraform plugin/data directories were scoped under the temporary lab
root.

## Matrix

| Resource | Seeded | Raw detail | Oracle import | Adopt/project | Initial plan | Policy omissions | Final plan | Cleanup |
|---|---|---|---|---|---|---|---|---|
| `<resource_type>` | `<yes/read-only/no>` | `<api/state/not captured>` | `<pass/fail>` | `<pass/fail reason>` | `<import-only/blocked/fail>` | `<none/paths>` | `<pass/blocked/skipped>` | `<verified/manual/not created>` |

Final gate:

```text
<assert-adoptable result or reason it was not run>
```

## Advisory Summary

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `<resource_type>` | `<n>` | `<n>` | `<n>` | `<n>` | `<n>` | `<n>` |
| **Total** | `<n>` | `<n>` | `<n>` | `<n>` | `<n>` | `<n>` |

Representative raw-only/provider-only shapes:

| Resource | Raw/API shape | Provider/projection shape |
|---|---|---|
| `<resource_type>` | `<raw path examples>` | `<provider path examples>` |

## Findings

`<Summarize what worked, what failed, and whether each failure is an engine gap,
pack metadata gap, provider boundary, product entitlement boundary, or lab
harness issue.>`

Use standard follow-up categories where possible:

- `engine:dynamic-schema`
- `engine:absent-defaults`
- `engine:sensitive-required`
- `engine:advisory-containers`
- `engine:deprecated-output`
- `pack:identity-alias`
- `pack:provider-config`
- `provider-boundary:paid-or-disabled`
- `provider-boundary:deprecated-api`
- `lab-harness`

## Lab Friction

`<Record setup, seeding, cleanup, token-scope, temp-root, plugin-cache, or
provider-download issues that affected the run but are not provider adoption
findings.>`

## Cleanup Verification

```text
<API queries or Terraform state/list checks used to verify no lab-prefixed
objects remained>
```

## Validation

```text
<make check / focused tests / git diff --check / provider-specific validation>
```

## Follow-Ups

- `<Issue/category and concise next action>`
