# Post-Parity Performance Benchmark

This runbook qualifies opt-in performance candidates without changing the
provider/Oracle authority. Raw API data still supplies inventory and identity;
the Terraform provider still imports and reads every selected object; provider
schema and projection policy still shape configuration; and Terraform plan
still proves convergence.

Repository fixtures prove determinism and request structure. They do not prove
a live speed improvement. Select a new default only after the approved work
machine produces the live evidence below.

## Performance report

Set `INFRAWRIGHT_PERFORMANCE_REPORT` to a file path for any operational CLI
command. Reporting is silent when the variable is absent or empty. Reports use
static product/resource/endpoint-family labels and exclude credentials, tokens,
cookies, response bodies, import IDs, concrete URLs, plan/state contents, and
tenant identifiers.

The report separates:

- command and phase wall time;
- logical pages from physical HTTP attempts;
- retry count, HTTP 429 count, and accumulated retry delay;
- p50/p95 wire-attempt duration by static endpoint family and status;
- Oracle Terraform commands and the optional corrected-plan phase;
- resource instance counts and success/skip/failure status.

Fetch accepts `--concurrency 1|2|4|8` (and other positive values through 64).
The default remains `1` until live evidence establishes a safe replacement.
The scheduler has one global bound and rotates products fairly; pages and
expansion paths within one resource remain sequential.

## Work-machine Fetch A/B

Run from the exact candidate checkout with the accepted Node 24 bundle and
approved read-only credentials already supplied by the job environment. Do not
place credentials in this script, the evidence directory, shell history, or
the repository.

Set one identical, bounded selector for every run:

```sh
export IW_CLI="$PWD/dist/infrawright-cli.mjs"
export IW_TENANT='<approved-tenant-label>'
export IW_RESOURCE='<resource-or-provider-selector>'
export IW_EVIDENCE="$(mktemp -d)"
```

Run each viable concurrency three times. Each run has an isolated pull tree,
report, and exact-byte manifest:

```sh
for concurrency in 1 2 4 8; do
  for repetition in 1 2 3; do
    run="$IW_EVIDENCE/fetch-c${concurrency}-r${repetition}"
    mkdir -p "$run"
    INFRAWRIGHT_PERFORMANCE_REPORT="$run/fetch.performance.json" \
      node "$IW_CLI" fetch \
        --tenant "$IW_TENANT" \
        --resource "$IW_RESOURCE" \
        --concurrency "$concurrency" \
        --out "$run/pulls"
    node scripts/performance-artifact-manifest.mjs \
      --root "pulls=$run/pulls" \
      --out "$run/artifacts.sha256.json"
  done
done
```

Compare any set of runs without credentials:

```sh
node scripts/compare-performance-reports.mjs \
  --variant "c1-r1=$IW_EVIDENCE/fetch-c1-r1" \
  --variant "c2-r1=$IW_EVIDENCE/fetch-c2-r1" \
  --variant "c4-r1=$IW_EVIDENCE/fetch-c4-r1" \
  --variant "c8-r1=$IW_EVIDENCE/fetch-c8-r1"
```

Stop increasing concurrency if output hashes differ, HTTP request count rises,
429s increase materially, failures appear, or latency worsens. Do not select a
default from a single run.

## Complete read-only qualification variants

Use one fresh, job-owned checkout/workspace per variant. Keep the deployment,
pack/profile, resource selectors, Terraform version, provider version, and
backend inputs identical. The ordinary baseline is:

- Fetch concurrency `1`;
- provider cache disabled;
- `INFRAWRIGHT_ORACLE_STATE_SOURCE=applied-state` (or unset, which retains that
  default);
- per-resource Oracle transactions.

For every operational command, set a distinct report file in the variant
directory, for example:

```sh
run='<variant-evidence-directory>'
INFRAWRIGHT_PERFORMANCE_REPORT="$run/fetch.performance.json" \
  node "$IW_CLI" fetch --tenant "$IW_TENANT" --resource "$IW_RESOURCE" \
    --concurrency 1 --out "$run/pulls"
INFRAWRIGHT_PERFORMANCE_REPORT="$run/adopt.performance.json" \
  node "$IW_CLI" adopt --in "$run/pulls" --tenant "$IW_TENANT" \
    --resource "$IW_RESOURCE"
INFRAWRIGHT_PERFORMANCE_REPORT="$run/modules.performance.json" \
  node "$IW_CLI" modules generate --resource "$IW_RESOURCE"
INFRAWRIGHT_PERFORMANCE_REPORT="$run/gen-env.performance.json" \
  node "$IW_CLI" gen-env --tenant "$IW_TENANT" --resource "$IW_RESOURCE"
INFRAWRIGHT_PERFORMANCE_REPORT="$run/stage.performance.json" \
  node "$IW_CLI" stage-imports --tenant "$IW_TENANT" \
    --resource "$IW_RESOURCE" --state-aware
INFRAWRIGHT_PERFORMANCE_REPORT="$run/plan.performance.json" \
  node "$IW_CLI" plan --tenant "$IW_TENANT" --resource "$IW_RESOURCE" --save
INFRAWRIGHT_PERFORMANCE_REPORT="$run/assessment.performance.json" \
  node "$IW_CLI" assert-adoptable --tenant "$IW_TENANT" \
    --resource "$IW_RESOURCE"
```

This qualification stops before deployment Apply. The Oracle's mechanically
verified import-only scratch Apply writes only ephemeral local state and remains
part of Adopt.

After each variant, hash the exact pull, configuration, imports, module, and
environment roots. Query deployment-owned paths rather than guessing them:

```sh
config_dir="$(node "$IW_CLI" deployment config-dir "$IW_TENANT")"
imports_dir="$(node "$IW_CLI" deployment imports-dir "$IW_TENANT")"
envs_dir="$(node "$IW_CLI" deployment envs-dir "$IW_TENANT")"
module_dir="$(node "$IW_CLI" deployment module-dir)"
node scripts/performance-artifact-manifest.mjs \
  --root "pulls=$run/pulls" \
  --root "config=$config_dir" \
  --root "imports=$imports_dir" \
  --root "modules=$module_dir" \
  --root "envs=$envs_dir" \
  --out "$run/artifacts.sha256.json"
```

If an optional root does not exist for the selected workflow, omit that root
consistently from every variant. The manifest deliberately records relative
file names, sizes, and SHA-256 values, never file contents or absolute paths.

Provider-cache and accepted-plan flags will be added to this matrix only by the
candidate slice that implements them. Do not invent or infer flags before that
slice lands.

## Acceptance

An optimization remains default-off unless the returned evidence shows:

- identical pull and generated-artifact manifests;
- identical provider-observed projected state and sensitive masks;
- the same clean/import-only assessment;
- zero create, update, replace, and destroy actions;
- lower structural request or Terraform-command count;
- materially lower repeated-run wall time;
- no additional rate-limit instability.

Keep raw pulls, state, plans, provider diagnostics, and performance reports in
the approved private evidence location. Commit only sanitized aggregate tables.
