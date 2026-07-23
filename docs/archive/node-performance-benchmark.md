# Post-Parity Performance Benchmark

> Archived 2026-07-22 with the Node runtime. Commands and helper scripts in
> this document are historical and must be ported before reuse.

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
set -eu
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

The comparison refuses failed reports, duplicate or missing command reports,
malformed counters, tampered manifests, and variants that cover different
artifact-root sets. Its first table retains the aggregate timing/request/
parity contract; a second table surfaces HTTP 429s, retries, and accumulated
retry delay so a faster but rate-limit-unstable candidate cannot look ordinary.

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

The manifest is created immediately after root generation and before
state-aware staging or Terraform init. If an optional root does not exist for
the selected workflow, omit it consistently from every variant. The manifest
records relative file names, sizes, and SHA-256 values, never file contents or
absolute paths. Saved plans, fingerprints, provider installations,
`.terraform`, state, and assessment output remain separate private runtime
evidence and are not part of deterministic artifact parity.

### Applied-state versus accepted-plan Oracle A/B

`applied-state` remains the baseline and default. `accepted-plan` is an
experimental candidate that still runs and validates the provider-backed exact
import-only plan, but skips the scratch Apply and state show when the plan
contains complete, internally identical provider observations. It fails closed
instead of silently falling back when any value or sensitivity mask is unknown,
missing, or inconsistent.

Run both modes against the same bounded cohort in separate fresh workspaces.
Keep Fetch concurrency at `1` for this comparison so the Oracle change is the
only variable:

```sh
export IW_HEAD='<exact-performance-candidate-commit>'
export IW_RUN_ROOT="$(mktemp -d)"
export IW_TENANT='<approved-tenant-label>'
export IW_RESOURCE='<identical-bounded-selector>'
export IW_DEPLOYMENT_REL='<repo-relative-identical-deployment-file>'
export IW_RUNTIME='<approved-runtime-tree-built-from-IW_HEAD>'
export IW_RUNTIME_SOURCE_COMMIT='<commit-from-trusted-build-attestation>'
export IW_RUNTIME_SHA256='<bundle-sha256-from-trusted-build-attestation>'
set -eu

test "$IW_RUNTIME_SOURCE_COMMIT" = "$IW_HEAD"
IW_CLI="$IW_RUNTIME/dist/infrawright-cli.mjs"
read -r runtime_sha256 runtime_name < "$IW_RUNTIME/dist/infrawright-cli.mjs.sha256"
test "$runtime_sha256" = "$IW_RUNTIME_SHA256"
test "$runtime_name" = "infrawright-cli.mjs"

for state_source in applied-state accepted-plan; do
  run="$IW_RUN_ROOT/$state_source"
  repo="$run/repo"
  mkdir -p "$run"
  git worktree add --detach "$repo" "$IW_HEAD"
done

# Use the verifier committed at IW_HEAD, not a script supplied by the runtime.
node "$IW_RUN_ROOT/applied-state/repo/scripts/verify-runtime-release.mjs" \
  "$IW_RUNTIME"

for state_source in applied-state accepted-plan; do
  run="$IW_RUN_ROOT/$state_source"
  repo="$run/repo"
  (
    cd "$repo"
    export INFRAWRIGHT_DEPLOYMENT="$repo/$IW_DEPLOYMENT_REL"
    export INFRAWRIGHT_ORACLE_STATE_SOURCE="$state_source"
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/fetch.performance.json"
    node "$IW_CLI" fetch \
      --tenant "$IW_TENANT" --resource "$IW_RESOURCE" \
      --concurrency 1 --out "$run/pulls"
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/adopt.performance.json"
    node "$IW_CLI" adopt \
      --in "$run/pulls" --tenant "$IW_TENANT" --resource "$IW_RESOURCE"
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/modules.performance.json"
    node "$IW_CLI" modules generate --resource "$IW_RESOURCE"
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/gen-env.performance.json"
    node "$IW_CLI" gen-env \
      --tenant "$IW_TENANT" --resource "$IW_RESOURCE"

    # Hash only deterministic generated inputs, before Terraform init/staging.
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

    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/stage.performance.json"
    node "$IW_CLI" stage-imports \
      --tenant "$IW_TENANT" --resource "$IW_RESOURCE" --state-aware
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/plan.performance.json"
    node "$IW_CLI" plan \
      --tenant "$IW_TENANT" --resource "$IW_RESOURCE" --save
    export INFRAWRIGHT_PERFORMANCE_REPORT="$run/assessment.performance.json"
    node "$IW_CLI" assert-adoptable \
      --tenant "$IW_TENANT" --resource "$IW_RESOURCE"
  )
done

node "$IW_RUN_ROOT/applied-state/repo/scripts/compare-performance-reports.mjs" \
  --oracle-ab \
  --variant "applied-state=$IW_RUN_ROOT/applied-state" \
  --variant "accepted-plan=$IW_RUN_ROOT/accepted-plan"
```

`--oracle-ab` requires exactly those two labels, binds each label to the state
source recorded inside its Adopt report, displays the observed source, and
requires every selected resource family to have one successful state-source
span plus matching scratch-Apply and state-show evidence. Accepted-plan phases
must be zero-command skips; applied-state phases must be one-command successes.
Missing, duplicated, truncated, or mislabeled provenance is a comparison
failure.

The deployment file must resolve its overlay and module directories inside that
variant's worktree (normally by using relative paths). Abort if both deployment
files resolve to the same canonical output directory; variants must never share
persistent writers.

`IW_RUNTIME_SOURCE_COMMIT` and `IW_RUNTIME_SHA256` must come from the same
trusted build attestation that maps the bundle digest to `IW_HEAD`; do not
populate them by merely copying the desired benchmark commit or the checksum
shipped beside the bundle. The release verifier proves the bundle matches its
packaged checksum, and the explicit digest comparison proves that checksum
matches the independent attestation. The work machine consumes that immutable
runtime tree without npm, `node_modules`, TypeScript, or Python. Build and
attest the bundle on the trusted build path when the candidate head changes.

The manifest is deliberately written before state-aware staging or Terraform
planning. It covers generated inputs only. Keep saved-plan, fingerprint,
provider installation, `.terraform`, state, and assessment evidence in the
private run directory and compare their classifications separately; do not add
those nondeterministic or sensitive runtime files to the artifact manifest.

The Adopt report includes a static `oracle_state_source` label and records the
skipped scratch-Apply/state-show spans with zero Terraform commands. With no
corrected plan, the expected structural command count falls from five to three;
with a corrected plan, from six to four. These are command-count expectations,
not live speed claims.

Before considering a default change, inspect the private provider evidence and
confirm the accepted plan's values and sensitivity masks equal the applied
scratch state for every selected address. The committed manifest comparison
then must show identical pull, config, import/move/binding, module, and root
bytes, and the deployment assessment must remain clean/import-only with zero
create, update, replace, or destroy actions.

### Provider-cache handoff and snapshot feasibility

The repository pins ZIA provider `v4.7.26` source at commit
`6e6509f001ca71adcedfd4884250d09227395bf0` and Zscaler SDK `v3.8.40` at
`4371c9bab44d852526721b4b5999e2471dda5198`. No matching installed provider
binary or archive was available on this machine, so those source pins cannot be
claimed as the binary used by the live baseline. Do not build or select a cache
prototype until the work machine records the lock entry, installed executable
path, SHA-256, `go version -m` metadata, and any development override or mirror.

Run the provenance capture from the exact preserved Terraform root used by the
baseline. These commands print package/version metadata and paths, not provider
credentials:

```sh
terraform version
terraform providers
sed -n '/provider "registry.terraform.io\/zscaler\/zia"/,/^}/p' \
  .terraform.lock.hcl
find .terraform/providers -type f -name 'terraform-provider-zia*' -perm -111 -print
provider_bin='<the-executable-path-reported-above>'
shasum -a 256 "$provider_bin"
go version -m "$provider_bin"
for config in "$HOME/.terraformrc" "$HOME/.tofurc"; do
  if test -f "$config" && \
    grep -Eq 'provider_installation|dev_overrides|filesystem_mirror' "$config"; then
    printf 'provider installation override/mirror configured in %s\n' "$config"
  fi
done
```

Do not print the complete CLI config. If `dev_overrides` or a filesystem mirror
is reported, resolve the selected binary from that configuration privately and
record only its path, digest, and build metadata in sanitized evidence.

The source-backed option order is:

| Rank | Option | Expected effect | Decision before live trace |
|---:|---|---|---|
| 1 | Provider list-backed Read cache | Replace N detail reads with list pages plus real misses inside one provider process | Preferred first prototype for the measured largest family |
| 2 | Private job-local read-through cache | Reuse identical GET responses across Terraform processes | Consider only after per-process cache evidence |
| 3 | Provider-native immutable snapshot transport | Replay exact provider response bytes through normal Read mapping | Feasible only with complete provider-consumable captures |
| 4 | Existing transport injection | Enables metrics/cache but reduces no requests by itself | Enabler, not a candidate |
| 5 | Loopback replay | Could reuse reads, but no clean arbitrary base-URL seam is proven | Reject; do not build interception/proxy machinery |

Current Fetch files are normalized item arrays, not exact provider HTTP
responses, and request shapes differ. They must not be treated as a provider
snapshot. URL Categories is also a poor first cache target because its provider
Read already uses a bulk `GetAll` and detail fallback. Source inspection makes
Location Management a credible candidate (`GetLocationOrSublocationByID` may
fall through to parent/sub-location scans), but only phase-level live request
accounting can establish that it is the actual hotspot or that list objects are
complete enough.

### Oracle batching feasibility

Provider-wide batching could reduce scratch `init`, provider starts, and the
five-or-six-command transaction from once per resource type to once per batch.
It does not remove one-detail-GET-per-object behavior without a provider cache
or exact response replay. The current Oracle, generated-config policy, state
extraction, failure attribution, and artifact publication are all intentionally
single-resource-type boundaries. Generalizing them would be a material change,
not a bounded performance patch.

Do not prototype batching yet. Reconsider a provider-wide batch only after live
telemetry shows init/provider startup or repeated list-cache initialization is a
material share after request amplification and accepted-plan testing. Default
deployment roots are usually one resource type, so deployment-root batching is
not expected to provide a useful generic boundary.

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
