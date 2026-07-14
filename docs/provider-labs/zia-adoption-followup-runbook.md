# Zscaler Adoption Follow-Up Runbook

Use this runbook on the approved work machine to answer two bounded questions:

1. Did Transform and Adopt load and exercise the same additive pack overrides?
2. Are source-less generated resources entering roots automatically, through an
   explicit deployment group, or through a stale materialized root?

Complete only the phases the approved environment supports. Record an
unavailable phase as NOT RUN with its reason. Never substitute another
experiment, and report INCONCLUSIVE whenever an authority or output cannot be
bound.

## Execution Boundary

This runbook authorizes provider API reads and Terraform provider import/Read
only. It does not authorize a deployment plan, deployment Apply, or a remote
create, update, or delete.

The normal Adopt Oracle uses a mechanically verified import-only Terraform
plan and then runs Terraform Apply against backend-free scratch local state.
That scratch Apply materializes provider-observed state; it is not deployment
Apply and has no remote create, update, replace, or destroy action. Phase 2
must be marked NOT RUN unless that local scratch import-only Apply is allowed.

Counts in the final report are tenant-derived evidence even though they contain
no raw values. Return them only through the approved channel.

## Safety Rules

- Use the existing approved credential environment without printing it.
- Do not run env, set, shell tracing, or Terraform debug logging.
- Do not print raw pulls, tfvars, generated HCL, plans, state, object names,
  IDs, URLs, import IDs, item keys, or tenant-bearing paths.
- Capture command stdout and stderr in a mode-0700 job directory. Do not paste
  those logs into the report.
- Report only hashes, resource type names, counts, exit statuses, normalized
  classifications, and tenant-independent paths.
- Relocate Transform and Adopt outputs into distinct disposable lanes before
  executing either command.
- Keep retained Oracle directories under the private job directory and delete
  the entire directory after recording sanitized evidence.

## Inputs

Set these to the exact authorities used by the clean-room harness:

    export IW_CLI=/absolute/path/to/dist/infrawright-cli.mjs
    export IW_PACKS=/absolute/path/to/packs
    export IW_PROFILE=/absolute/path/to/packsets/full.json
    export IW_CATALOG=/absolute/path/to/packsets/full.json
    export IW_DEPLOYMENT=/absolute/path/to/deployment.json
    export IW_PULL=/absolute/path/to/the/frozen/pull/root
    export IW_TENANT=the-existing-test-tenant

For Phase 2, explicitly acknowledge the local scratch import-only Apply:

    export IW_ALLOW_ORACLE_SCRATCH_APPLY=1

For optional materialized-root comparison in Phase 3, set the exact workspace
from which gen-env was run. Do not report it:

    export IW_GENERATED_WORKSPACE=/absolute/path/to/the/generation/workspace

## Phase 0: Create Private, Disjoint Output Lanes

Run every phase in the same bash session. This block intentionally creates
path-relocated copies of the deployment file. Only overlay and module_dir
change; all grouping, format, and pack semantics remain byte-equivalent after
those two keys are removed.

    set -u
    umask 077
    IW_JOB=$(mktemp -d /tmp/iw-adoption-followup.XXXXXX)
    trap 'rm -rf "$IW_JOB"' EXIT HUP INT TERM
    mkdir -m 700 "$IW_JOB/logs" "$IW_JOB/lanes"

    prepare_lane() {
      lane="$IW_JOB/lanes/$1"
      mkdir -m 700 "$lane" "$lane/tmp"
      jq '.overlay = "." | .module_dir = "modules"' "$IW_DEPLOYMENT" \
        > "$lane/deployment.json"
      jq -S 'del(.overlay, .module_dir)' "$IW_DEPLOYMENT" \
        > "$lane/original.semantic.json"
      jq -S 'del(.overlay, .module_dir)' "$lane/deployment.json" \
        > "$lane/lane.semantic.json"
      cmp "$lane/original.semantic.json" "$lane/lane.semantic.json"
      printf '%s\n' "$lane"
    }

    run_private() {
      label="$1"
      lane="$2"
      shift 2
      status=0
      (cd "$lane" && "$@") \
        > "$IW_JOB/logs/$label.stdout" \
        2> "$IW_JOB/logs/$label.stderr" || status=$?
      printf '%s\n' "$status" > "$IW_JOB/logs/$label.status"
      printf '%s exit=%s\n' "$label" "$status"
    }

Create four lanes:

    NS_T=$(prepare_lane network-transform)
    NS_A=$(prepare_lane network-adopt)
    BC_T=$(prepare_lane browser-transform)
    BC_A=$(prepare_lane browser-adopt)

The derived deployment fixes overlay to the lane current directory. Prove the
resolved config, imports, and envs directories are physically contained before
running Transform or Adopt:

    for lane in "$NS_T" "$NS_A" "$BC_T" "$BC_A"; do
      for verb in config-dir imports-dir envs-dir; do
        value=$(cd "$lane" && node "$IW_CLI" deployment \
          --deployment "$lane/deployment.json" "$verb" "$IW_TENANT")
        node -e '
    const fs = require("node:fs");
    const path = require("node:path");
    const root = fs.realpathSync(process.argv[1]);
    const target = path.resolve(root, process.argv[2]);
    const relative = path.relative(root, target);
    if (relative === "" || relative === ".." ||
        relative.startsWith(".." + path.sep) || path.isAbsolute(relative)) {
      throw new Error("deployment output escapes its private lane");
    }
    fs.mkdirSync(target, {recursive: true, mode: 0o700});
    const physical = fs.realpathSync(target);
    const physicalRelative = path.relative(root, physical);
    if (physicalRelative === ".." ||
        physicalRelative.startsWith(".." + path.sep) ||
        path.isAbsolute(physicalRelative)) {
      throw new Error("deployment output resolves through a symlink escape");
    }
    ' "$lane" "$value"
      done
    done

If any comparison or containment check fails, mark Phase 2 NOT RUN. Since every
successful target is physically contained in one of four distinct lane roots,
the Transform and Adopt output trees cannot overlap.

Query and record the deployment tfvars format:

    IW_TFVARS_FORMAT=$(node "$IW_CLI" deployment \
      --deployment "$IW_DEPLOYMENT" tfvars-format)

The field-count procedure below supports JSON tfvars only. If the result is
hcl, mark tfvars-count and field-pipeline classifications NOT RUN; do not parse
HCL with jq.

## Phase 1: Bind Runtime, Pack, And Registry Authorities

Record the Node and Terraform versions and CLI hash. The CLI SHA-256, not the
current shell repository, is the runtime authority:

    node --version
    terraform version
    shasum -a 256 "$IW_CLI"

Record the pack repository commit and whether the pack tree is dirty, without
printing its path:

    git -C "$IW_PACKS" rev-parse HEAD
    test -z "$(git -C "$IW_PACKS" status --porcelain -- .)" \
      && echo "pack tree: clean" || echo "pack tree: dirty"

Record hashes for both registries, the profile, catalog, deployment, and the
two lane deployment copies:

    shasum -a 256 \
      "$IW_PACKS/zia/registry.json" \
      "$IW_PACKS/zpa/registry.json" \
      "$IW_PROFILE" "$IW_CATALOG" "$IW_DEPLOYMENT" \
      "$NS_A/deployment.json" "$BC_A/deployment.json"

Use a helper so a missing override is reported without leaking its absolute
path:

    hash_override() {
      label="$1"
      file="$2"
      if test -f "$file"; then
        digest=$(shasum -a 256 "$file" | awk '{print $1}')
        printf '%s PRESENT %s\n' "$label" "$digest"
      else
        printf '%s MISSING\n' "$label"
      fi
    }
    hash_override network-service \
      "$IW_PACKS/zia/overrides/zia_firewall_filtering_network_service.json"
    hash_override browser-control \
      "$IW_PACKS/zia/overrides/zia_browser_control_policy.json"

If present, print only the non-sensitive policy fragment:

    jq -c '{drop_if_default,defaults,key_field}' \
      "$IW_PACKS/zia/overrides/zia_firewall_filtering_network_service.json"
    jq -c '{drop_if_default,defaults,key_field}' \
      "$IW_PACKS/zia/overrides/zia_browser_control_policy.json"

A missing file or missing rule is a pack-authority failure, not an Oracle
rewriter failure.

## Phase 2: Exercise Transform And Adopt

Do not proceed unless IW_ALLOW_ORACLE_SCRATCH_APPLY is exactly 1:

    test "$IW_ALLOW_ORACLE_SCRATCH_APPLY" = 1

All command diagnostics remain in private files. Report only each emitted
label and exit status.

    run_private network-transform "$NS_T" \
      env TMPDIR="$NS_T/tmp" node "$IW_CLI" transform \
        --root "$IW_PACKS" --profile "$IW_PROFILE" \
        --catalog "$IW_CATALOG" --deployment "$NS_T/deployment.json" \
        --in "$IW_PULL" --tenant "$IW_TENANT" \
        --resource zia_firewall_filtering_network_service

    run_private network-adopt "$NS_A" \
      env TMPDIR="$NS_A/tmp" INFRAWRIGHT_KEEP_ORACLE=1 \
        node "$IW_CLI" adopt \
        --root "$IW_PACKS" --profile "$IW_PROFILE" \
        --catalog "$IW_CATALOG" --deployment "$NS_A/deployment.json" \
        --in "$IW_PULL" --tenant "$IW_TENANT" \
        --resource zia_firewall_filtering_network_service

    run_private browser-transform "$BC_T" \
      env TMPDIR="$BC_T/tmp" node "$IW_CLI" transform \
        --root "$IW_PACKS" --profile "$IW_PROFILE" \
        --catalog "$IW_CATALOG" --deployment "$BC_T/deployment.json" \
        --in "$IW_PULL" --tenant "$IW_TENANT" \
        --resource zia_browser_control_policy

    run_private browser-adopt "$BC_A" \
      env TMPDIR="$BC_A/tmp" INFRAWRIGHT_KEEP_ORACLE=1 \
        node "$IW_CLI" adopt \
        --root "$IW_PACKS" --profile "$IW_PROFILE" \
        --catalog "$IW_CATALOG" --deployment "$BC_A/deployment.json" \
        --in "$IW_PULL" --tenant "$IW_TENANT" \
        --resource zia_browser_control_policy

Do not paste the captured stdout or stderr. Inspect them only on the approved
machine and report a normalized error category when a command fails.

### Count Fields Without Printing Values

For JSON pull and tfvars files, use this helper. It emits only a label and an
integer, while parser diagnostics remain private:

    count_json() {
      label="$1"
      file="$2"
      filter="$3"
      value=$(jq "$filter" "$file" \
        2>> "$IW_JOB/logs/field-counts.stderr") || value=ERROR
      printf '%s %s\n' "$label" "$value"
    }

    TAG_FILTER='[.. | objects | select(has("tag") and .tag == "")] | length'
    PLUGIN_FILTER='[.. | objects | select(has("plugin_check_frequency") and .plugin_check_frequency == "")] | length'
    END_FILTER='[.. | objects | select(has("end") and .end == 0)] | length'

    count_json network-raw-tag \
      "$IW_PULL/zia_firewall_filtering_network_service.json" "$TAG_FILTER"
    count_json network-transform-tag \
      "$NS_T/config/$IW_TENANT/zia_firewall_filtering_network_service.auto.tfvars.json" \
      "$TAG_FILTER"
    count_json network-adopt-tag \
      "$NS_A/config/$IW_TENANT/zia_firewall_filtering_network_service.auto.tfvars.json" \
      "$TAG_FILTER"

    count_json network-raw-end \
      "$IW_PULL/zia_firewall_filtering_network_service.json" "$END_FILTER"
    count_json network-transform-end \
      "$NS_T/config/$IW_TENANT/zia_firewall_filtering_network_service.auto.tfvars.json" \
      "$END_FILTER"
    count_json network-adopt-end \
      "$NS_A/config/$IW_TENANT/zia_firewall_filtering_network_service.auto.tfvars.json" \
      "$END_FILTER"

    count_json browser-raw-plugin \
      "$IW_PULL/zia_browser_control_policy.json" "$PLUGIN_FILTER"
    count_json browser-transform-plugin \
      "$BC_T/config/$IW_TENANT/zia_browser_control_policy.auto.tfvars.json" \
      "$PLUGIN_FILTER"
    count_json browser-adopt-plugin \
      "$BC_A/config/$IW_TENANT/zia_browser_control_policy.auto.tfvars.json" \
      "$PLUGIN_FILTER"

Do not use this helper when IW_TFVARS_FORMAT is hcl.

For each Adopt lane, privately enumerate every retained
infrawright-import-oracle-* directory below its lane tmp directory. This helper
sums a target across generated.tf.before-policy and generated.tf without
printing filenames or matching lines:

    count_generated() {
      label="$1"
      lane="$2"
      pattern="$3"
      before=0
      after=0
      before_files=0
      oracle_dirs=0
      while IFS= read -r -d '' directory; do
        oracle_dirs=$((oracle_dirs + 1))
        if test -f "$directory/generated.tf.before-policy"; then
          before_files=$((before_files + 1))
          count=$(rg -c --no-filename "$pattern" \
            "$directory/generated.tf.before-policy" || true)
          count=${count:-0}
          before=$((before + count))
        fi
        if test -f "$directory/generated.tf"; then
          count=$(rg -c --no-filename "$pattern" \
            "$directory/generated.tf" || true)
          count=${count:-0}
          after=$((after + count))
        fi
      done < <(find "$lane/tmp" -type d \
        -name 'infrawright-import-oracle-*' -print0)
      printf '%s oracle_dirs=%s before_files=%s before=%s after=%s\n' \
        "$label" "$oracle_dirs" "$before_files" "$before" "$after"
    }

    count_generated network-tag "$NS_A" \
      '^[[:space:]]*tag[[:space:]]*=[[:space:]]*""[[:space:]]*$'
    count_generated network-end "$NS_A" \
      '^[[:space:]]*end[[:space:]]*=[[:space:]]*0[[:space:]]*$'
    count_generated browser-plugin "$BC_A" \
      '^[[:space:]]*plugin_check_frequency[[:space:]]*=[[:space:]]*""[[:space:]]*$'

The policy is proven exercised for one target only when all of these hold:

- the Adopt command exited zero;
- generated.tf.before-policy exists;
- the target count before policy is greater than zero;
- the target count after policy is zero;
- the target count in adopted tfvars is zero.

Use these classifications:

| Observation | Classification |
|---|---|
| Override or target rule absent from bound pack | pack-authority failure |
| Adopt failed | INCONCLUSIVE for policy success; report normalized failure |
| Before file absent or target before count is zero | NOT EXERCISED |
| Target before is greater than zero and target remains after | generated-config dispatch failure |
| Target removed from HCL but remains in adopted tfvars | provider-state projection failure |
| Successful Adopt, target before greater than zero, after zero, tfvars zero | behavior correct for this target |

An unrelated edit can create generated.tf.before-policy, so a zero target count
in that file is still NOT EXERCISED. Absence from Transform alone is not proof
that its override loaded: the raw API may omit a value later materialized by
provider Read.

## Phase 3: Classify Root Membership

This phase needs no tenant and performs no Terraform command. All raw topology
files remain in IW_JOB.

For each product, bind the active resource set, derive source-less generated
entries from the exact registry, and emit only resource type and slug intent:

    for product in zia zpa; do
      node "$IW_CLI" resources --root "$IW_PACKS" \
        --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
        --resource "$product" > "$IW_JOB/$product.active"
      jq --rawfile active "$IW_JOB/$product.active" '
        ($active | split("\n") | map(select(length > 0))) as $active_types
        | [to_entries[]
           | select(.key as $key | $active_types | index($key))
           | select(.value.generate == true
                    and (.value | has("fetch") | not)
                    and (.value | has("derive") | not))
           | {type: .key,
              slug_group: (if (.value | has("slug_group"))
                           then .value.slug_group
                           else "UNSET_DEFAULT_TRUE" end)}]
      ' "$IW_PACKS/$product/registry.json" \
        > "$IW_JOB/$product.source-less.json"

      node "$IW_CLI" roots --root "$IW_PACKS" \
        --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
        --deployment "$IW_DEPLOYMENT" --resource "$product" \
        > "$IW_JOB/$product.topology.json" \
        2> "$IW_JOB/logs/$product-roots.stderr"

      jq -s --arg provider "$product" \
        --slurpfile source_less "$IW_JOB/$product.source-less.json" '
        .[0] as $deployment | .[1] as $topology
        | ($deployment.roots[$provider] // {}) as $config
        | ($source_less[0] | map(.type)) as $source_types
        | {provider: $provider,
           strategy: ($config.strategy // "explicit"),
           roots:
             [$topology.roots[]
              | . as $root
              | [$root.members[]
                 | select(. as $member | $source_types | index($member))]
                as $source_members
              | select(($source_members | length) > 0)
              | {label: $root.label,
                 origin:
                   (if (($config.groups // {}) | has($root.label))
                    then "explicit-group"
                    elif (($config.strategy // "explicit") == "slug"
                          and ($root.members | length) > 1)
                    then "automatic-slug"
                    else "standalone" end),
                 members: $root.members,
                 source_less_members: $source_members}]}
      ' "$IW_DEPLOYMENT" "$IW_JOB/$product.topology.json" \
        > "$IW_JOB/$product.classified.json"
    done

Return only these sanitized files:

    jq '{count:length,entries:.}' "$IW_JOB/zia.source-less.json"
    jq '{count:length,entries:.}' "$IW_JOB/zpa.source-less.json"
    jq . "$IW_JOB/zia.classified.json"
    jq . "$IW_JOB/zpa.classified.json"

### Optional Materialized-Root Comparison

Only this comparison can classify an existing generated root as current or
stale. If IW_GENERATED_WORKSPACE is unavailable, report materialized-root
comparison NOT RUN and keep stale-root causality INCONCLUSIVE.

For each root label under investigation, select its first member from the
tenant-free topology, then run roots from the exact gen-env working directory
with tenant output captured privately. Resolve env_dir privately and inspect
only module and variable declarations from main.tf.

Run this block once per product/root label. IW_PRODUCT and IW_ROOT_LABEL contain
only non-sensitive names such as zia and zia_url:

    export IW_PRODUCT=zia
    export IW_ROOT_LABEL=zia_url
    IW_TOPOLOGY="$IW_JOB/$IW_PRODUCT.topology.json"
    IW_ROOT_SELECTOR=$(jq -er --arg label "$IW_ROOT_LABEL" \
      '.roots[] | select(.label == $label) | .members[0]' "$IW_TOPOLOGY")
    (
      cd "$IW_GENERATED_WORKSPACE"
      node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
        --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
        --deployment "$IW_DEPLOYMENT" --resource "$IW_ROOT_SELECTOR"
    ) > "$IW_JOB/materialized.private.json" \
      2> "$IW_JOB/logs/materialized-roots.stderr"
    IW_ENV_REL=$(jq -er --arg label "$IW_ROOT_LABEL" \
      '.roots[] | select(.label == $label) | .env_dir' \
      "$IW_JOB/materialized.private.json")
    IW_MATERIALIZED_ROOT=$(node -e \
      'process.stdout.write(require("node:path").resolve(process.argv[1], process.argv[2]))' \
      "$IW_GENERATED_WORKSPACE" "$IW_ENV_REL")
    test -f "$IW_MATERIALIZED_ROOT/main.tf"

    jq -r --arg label "$IW_ROOT_LABEL" \
      '.roots[] | select(.label == $label) | .members[]' "$IW_TOPOLOGY" \
      | LC_ALL=C sort -u > "$IW_JOB/expected.modules"
    rg --no-filename -o '^module "[a-z0-9_]+"' \
      "$IW_MATERIALIZED_ROOT/main.tf" \
      | sed -E 's/^module "([^"]+)"/\1/' | LC_ALL=C sort -u \
      > "$IW_JOB/actual.modules"

    jq -r --arg label "$IW_ROOT_LABEL" '
      .roots[] | select(.label == $label) | .members[]
      | if . == $label then "items" else . + "_items" end
    ' "$IW_TOPOLOGY" | LC_ALL=C sort -u > "$IW_JOB/expected.variables"
    rg --no-filename -o '^variable "[a-z0-9_]+"' \
      "$IW_MATERIALIZED_ROOT/main.tf" \
      | sed -E 's/^variable "([^"]+)"/\1/' | LC_ALL=C sort -u \
      > "$IW_JOB/actual.variables"

    for kind in modules variables; do
      if diff -u "$IW_JOB/expected.$kind" "$IW_JOB/actual.$kind" \
          > "$IW_JOB/$kind.diff"; then
        printf '%s MATCH\n' "$kind"
      else
        printf '%s MISMATCH\n' "$kind"
        cat "$IW_JOB/$kind.diff"
      fi
    done

Expected modules are the topology members. Expected root variables are items
for a same-name singleton member and RESOURCE_items for grouped members.
Compare sorted expected and actual lists. Return only MATCH/MISMATCH and the
resource or variable names in a difference; never return the root path or any
other main.tf line.

Interpretation:

- An explicit deployment group is authoritative even when member metadata says
  slug_group:false.
- A multi-member root under slug strategy and absent from explicit groups is
  automatic.
- Source-less means generate:true with neither Fetch nor Derive in the exact
  hashed registry; it does not itself prove the grouping origin.
- A materialized module/variable mismatch proves the generated root is stale
  or came from another authority.
- Without the materialized comparison, stale-root causality is INCONCLUSIVE.

## Report Template

Return one sanitized report:

    Zscaler adoption follow-up

    Authority
    - pack repository commit and clean/dirty:
    - Node version:
    - Terraform version:
    - CLI SHA-256:
    - ZIA/ZPA registry SHA-256:
    - profile/catalog/original deployment SHA-256:
    - relocated lane deployment SHA-256:
    - tfvars format:
    - network-service override: PRESENT/MISSING, SHA-256
    - browser-control override: PRESENT/MISSING, SHA-256

    Field pipeline
    Resource | Raw count | Transform count | Generated before | Generated after | Adopt tfvars | Exit/result
    network service tag | | | | | |
    network service end=0 | | | | | |
    browser plugin frequency | | | | | |

    Root topology
    Root | Origin | Members | Source-less members | Materialized MATCH/MISMATCH/NOT RUN

    Classification
    - pack authority:
    - generated-config policy:
    - provider-state projection:
    - stale output:
    - ZIA topology:
    - ZPA topology:

    Unavailable steps
    - step:
    - reason:

    Safety confirmation
    - no deployment plan or deployment Apply
    - Oracle scratch import-only Apply: RUN/NOT RUN
    - no remote create/update/delete
    - no credentials or raw tenant values printed
    - tenant-derived counts returned only through approved channel
    - private job directory and retained Oracle data deleted

Do not generalize beyond observed resource types. Return INCONCLUSIVE when the
exact pack, registry, deployment, lane, generated-policy target, or
materialized root cannot be proved.
