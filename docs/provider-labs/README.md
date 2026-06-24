# Provider Labs

Provider labs are disposable, evidence-gathering runs for the import-oracle
adoption path. They are not provider packs, and they must not commit raw API
dumps, Terraform state, plans, logs, credentials, or temporary roots.

Use labs to answer three questions:

- Can provider resources import from key-only raw input?
- Does projected provider state produce import-only/adoptable plans?
- What does raw API detail expose that Terraform/provider state cannot see?

## Current Evidence Set

| Provider | Report | Primary signal |
|---|---|---|
| NetBox | [netbox-pr22.md](netbox-pr22.md) | Absent/default placeholder drift |
| Grafana | [grafana-pr24.md](grafana-pr24.md) | Sensitive-but-required nested blocks |
| Cloudflare | [cloudflare-free-tier-pr32.md](cloudflare-free-tier-pr32.md) | Dynamic schema attrs, identity aliases, singleton/default drift |

These labs are enough to treat the import-oracle approach as viable. Future
labs should focus on repeatability, provider-specific failure classes, and the
smallest engine or pack follow-ups needed to productize adoption.

## Standard Workflow

1. Create a fresh branch from `main` for the report.
2. Create all temporary lab artifacts under `/tmp/infrawright-<provider>-lab`.
3. Use a temporary `INFRAWRIGHT_PACKS` root with only the resources under test.
4. Extract the provider schema from the exact provider version under test.
5. Seed only disposable resources with a unique lab prefix.
6. Capture raw detail JSON, oracle state, projected tfvars, saved plans, and
   advisory reports under the temporary root.
7. Run `make adopt`, `make stage-imports`, `make plan SAVE=1`, and
   `make assert-adoptable` against the temporary overlay.
8. Run static advisory reports with `python -m engine.adopt_certify`.
9. Classify temporary dynamic/schema prunes with
   `python -m engine.dynamic_schema`.
10. Destroy live resources and run a separate cleanup verification pass.
11. Run validation and artifact checks.
12. Commit only the report.

## Credential Safety Checklist

Before live writes:

- Confirm the shell token points to the intended disposable account, org, stack,
  tenant, or namespace.
- If a non-lab credential is detected, record that in the report and do not
  perform writes until the lab credential is active.
- Prefer scoped, short-lived tokens.
- Redact tokens, account IDs, tenant IDs, and import IDs from report prose unless
  an ID is intentionally part of public fixture context.
- Never commit `.env`, provider config with credentials, token dumps, state, plan
  JSON, provider logs, or raw API dumps.

Recommended report wording for a caught credential risk:

```text
During setup, a non-lab <provider> token was detected in the shell environment.
No live writes were performed with that token. The lab switched back to the
disposable lab token before creating resources.
```

## Forbidden Artifacts

Provider lab PRs must not commit:

- raw API details
- oracle state
- generated tfvars from live data
- Terraform state or state backups
- saved plans or plan JSON
- Terraform working directories
- provider plugin caches
- provider logs
- credentials or provider config containing credentials
- copied vendor schemas unless the PR is explicitly a pack/schema PR

Run these before committing:

```bash
find . -name '*.tfstate*' -o -name 'tfplan*' -o -name '.terraform'
git status --short
git diff --check
```

The PR body should explicitly state that only the report is committed.

## Temporary Root Guidance

Use one realpath-normalized lab root:

```bash
export LAB_ROOT="$(python3 - <<'PY'
import os
print(os.path.realpath("/tmp/infrawright-<provider>-lab"))
PY
)"
```

Set shared Terraform paths inside that root:

```bash
export TF_DATA_DIR="$LAB_ROOT/.tfdata"
export TF_PLUGIN_CACHE_DIR="$LAB_ROOT/plugin-cache"
export INFRAWRIGHT_PACKS="$LAB_ROOT/packs"
```

For multiple resource roots, prefer a shared plugin cache and per-root
`TF_DATA_DIR` values under `$LAB_ROOT/tfdata/<resource>` if concurrent Terraform
runs are needed.

## Standard Matrix Fields

Every report should include a matrix with these columns:

| Field | Meaning |
|---|---|
| Resource | Terraform resource type. |
| Seeded | Whether a disposable live object was created. Use `read-only` for singleton imports. |
| Raw detail | Whether raw detail JSON was captured and from where. |
| Oracle import | `pass`, `fail`, or `not run`. |
| Adopt/project | `pass`, `fail sensitive`, `fail required`, `fail identity`, or `not run`. |
| Initial plan | `import-only`, `blocked`, `failed validation`, `failed provider`, or `not run`. |
| Policy omissions | Paths intentionally omitted by drift policy. |
| Final plan | `pass`, `blocked`, `skipped`, or `not run`. |
| Cleanup | `verified`, `not created`, `manual`, or `blocked`. |

## Standard Advisory Fields

Every report should include the static advisory summary:

| Field | Meaning |
|---|---|
| Raw-only | Raw API leaf paths absent from provider state and projected config. |
| Provider-only | Provider-observed paths absent from raw API and projected config. |
| Projected | Paths written to generated tfvars. |
| Omitted by policy | Provider-observed paths intentionally omitted from projection. |
| Required missing | Caller-supplied required projection failures, when available. |
| Sensitive blocked | Sensitive provider-observed paths absent from projected config. |

`required_missing` is not computed by the static advisory CLI. It must come from
projection or plan diagnostics when available.

## Cleanup Verification Format

Reports should state both the cleanup action and the verification query shape:

```text
Cleanup: Terraform destroy completed for tracked objects. A separate API
verification queried <resource-list endpoints> for the lab prefix and found no
remaining objects.
```

If Terraform cannot clean up a resource, record the exact class:

- provider cannot destroy
- provider state lacks ID
- API refuses deletion
- resource is sticky or externally validated
- manual dashboard cleanup required

## Validation

For provider lab report PRs, prefer:

```bash
make check
git diff --check
```

If `make check` is intentionally skipped because the PR is documentation-only,
say so in the PR body and report why. The report itself should still record live
cleanup verification and any provider-specific validation commands that matter
to the lab.

## Follow-Up Categories

Use these buckets so provider findings stay comparable:

- `engine:dynamic-schema` for Terraform `dynamic` attributes or normalized
  dynamic values. Use [dynamic-schema-diagnostics.md](../dynamic-schema-diagnostics.md)
  to classify lab paths before choosing a remediation.
- `engine:absent-defaults` for provider-specific null/empty/zero/default drift.
- `engine:sensitive-required` for sensitive state that is structurally required
  by Terraform config.
- `engine:advisory-containers` for block/container omission reporting.
- `engine:deprecated-output` for warnings caused by generated outputs reading
  deprecated provider fields.
- `pack:identity-alias` for raw/API/provider/state naming differences used in
  keys or import IDs.
- `provider-boundary:paid-or-disabled` for plan, product, or feature gates.
- `provider-boundary:deprecated-api` for provider resources backed by deprecated
  or maintenance-mode APIs.
- `lab-harness` for local temp-root, plugin-cache, cleanup, or seeding friction.
