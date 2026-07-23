# Controlled Kubernetes qualification for the Go adopt/apply chain

Status: **document only; not authorized for execution**.

This runbook is the proposed first real-provider proof of the complete Go
`adopt` → saved-plan → exact-Apply chain. Writing and reviewing this document
does not authorize any command below. Execution requires two explicit human
decisions:

1. approval of the resolved cluster context, namespace, ConfigMap, service
   account, candidate commit, binaries, and commands; and
2. after the saved plan exists, approval of its captured zero-mutation,
   strictly import-only classification before exact Apply.

The authoring task performed no cluster access, kubeconfig read, credential
read, provider API operation, plan, or Apply. It locally initialized the pinned
provider with backends disabled solely to verify the reduced schema below.

## 1. Scope and safety claim

The target is one uniquely named ConfigMap in one dedicated throw-away
namespace. An administrator creates the namespace, ConfigMap, service account,
Role, and RoleBinding during approved setup. Every Infrawright, Terraform, and
evidence command then uses only a short-lived service-account kubeconfig whose
Role permits `get` on that one ConfigMap and no create, update, patch, or delete
verb. This RBAC boundary is the hard stop against unexpected remote mutation.

Terraform state is local and disposable. Neither the Oracle scratch root nor
the generated qualification root may declare a remote backend or Terraform
Cloud configuration.

A passing run proves provider-agnostic machinery with real authentication and
a real provider: provider init, import, plan, show, the Oracle's import-only
completeness checks, generated configuration, saved-plan fingerprint/freshness
enforcement, assessment, and exact saved-plan Apply without changing the
remote object. It does **not** qualify Zscaler collection, authentication,
schemas, SDK behavior, API readback, or pack policy.

Provider pin: `hashicorp/kubernetes` `2.38.0`. The pin is deliberate:
`kubernetes_config_map` is supported by this release, its documented import ID
is `namespace/name`, and the provider accepts `KUBE_CONFIG_PATH`. Do not float
to provider 3.x during this qualification; that major deprecates the unversioned
resource requested by this test. Source authorities:
[provider 2.38.0](https://registry.terraform.io/providers/hashicorp/kubernetes/2.38.0),
[ConfigMap resource and import](https://github.com/hashicorp/terraform-provider-kubernetes/blob/v2.38.0/docs/resources/config_map.md),
and [provider v2 authentication](https://registry.terraform.io/providers/hashicorp/kubernetes/2.38.0/docs/guides/v2-upgrade-guide).

## 2. Preconditions and approval record

Use Bash 5 or newer. The executor must fill in the five values below, perform
only local repository/tool/kubeconfig resolution, paste the resulting approval
record into the request, and stop. `kubectl config view` below reads the named
kubeconfig locally; it does not contact the cluster. Do not put a kubeconfig,
token, state, plan, raw object, or transcript in the repository.

```bash
set -euo pipefail
umask 077

REPO='/absolute/path/to/infrawright-dev'
EXPECTED_COMMIT='<fresh-review-approved full commit SHA>'
TF_BIN='/absolute/path/to/the real terraform binary'
ADMIN_KUBECONFIG='/absolute/path/to/the approved admin kubeconfig'
ADMIN_CONTEXT='<approved disposable-cluster context>'
KUBECTL_BIN="$(command -v kubectl)"
GO_BIN="$(command -v go)"
REALPATH_BIN="$(command -v realpath)"

test "$(git -C "$REPO" rev-parse HEAD)" = "$EXPECTED_COMMIT"
test -z "$(git -C "$REPO" status --short)"
test -f "$REPO/package.json"
test -x "$TF_BIN"
test ! -L "$TF_BIN"
test -x "$KUBECTL_BIN"
test -x "$GO_BIN"
test -x "$REALPATH_BIN"

ORIGINAL_TMPDIR="${TMPDIR:-/tmp}"
LAB_ROOT="$(mktemp -d "$ORIGINAL_TMPDIR/infrawright-k8s-qual.XXXXXX")"
chmod 0700 "$LAB_ROOT"
test -O "$LAB_ROOT"
TMPDIR="$LAB_ROOT/tmp"
install -d -m 0700 "$TMPDIR"
test -O "$TMPDIR"
export TMPDIR

private_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}
test "$(private_mode "$LAB_ROOT")" = 700
test "$(private_mode "$TMPDIR")" = 700

RUN_ID="$(date -u '+%Y%m%d%H%M%S')-$(openssl rand -hex 4)"
NAMESPACE="infrawright-qual-$RUN_ID"
CONFIGMAP="infrawright-qual-$RUN_ID"
SERVICE_ACCOUNT="infrawright-qual-reader-$RUN_ID"
ROLE="infrawright-qual-get-$RUN_ID"
ROLE_BINDING="infrawright-qual-get-$RUN_ID"
TENANT='k8s_qualification'
RESOURCE='kubernetes_config_map'
ROOT_LABEL='qualification'

install -d -m 0700 "$LAB_ROOT/bin" "$LAB_ROOT/evidence" "$LAB_ROOT/home" \
  "$LAB_ROOT/plugin-cache"
printf 'infrawright-k8s-qualification\nrun_id=%s\n' "$RUN_ID" \
  > "$LAB_ROOT/.infrawright-k8s-qualification"
printf '{}\n' > "$LAB_ROOT/package.json"

sha256_value() {
  shasum -a 256 "$1" | awk '{print $1}'
}

# --flatten makes file-backed CA references self-contained before jsonpath.
CLUSTER_SERVER="$(
  KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
    config view --raw --flatten --minify \
    -o jsonpath='{.clusters[0].cluster.server}'
)"
CLUSTER_CA_DATA="$(
  KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
    config view --raw --flatten --minify \
    -o jsonpath='{.clusters[0].cluster.certificate-authority-data}'
)"
test -n "$CLUSTER_SERVER"
test -n "$CLUSTER_CA_DATA"
ADMIN_EXEC_USERS="$(
  KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
    config view --raw --flatten --minify -o json |
    jq '[.users[]?.user | select(has("exec"))] | length'
)"
test "$ADMIN_EXEC_USERS" = 0
CLUSTER_CA_DATA_SHA256="$(printf '%s' "$CLUSTER_CA_DATA" | shasum -a 256 | awk '{print $1}')"

# Build before approval so the approved record names the exact executable bytes.
test "$(git -C "$REPO" rev-parse HEAD)" = "$EXPECTED_COMMIT"
test -z "$(git -C "$REPO" status --short)"
(cd "$REPO/go" && "$GO_BIN" build -trimpath -o "$LAB_ROOT/bin/iw" ./cmd/iw)
IW="$LAB_ROOT/bin/iw"
IW_SHA256="$(sha256_value "$IW")"
TF_SHA256="$(sha256_value "$TF_BIN")"
KUBECTL_SHA256="$(sha256_value "$KUBECTL_BIN")"

"$TF_BIN" version -json > "$LAB_ROOT/evidence/terraform-version.json"
"$KUBECTL_BIN" version --client -o json > "$LAB_ROOT/evidence/kubectl-client-version.json"
printf '%s  %s\n' "$IW_SHA256" "$IW" > "$LAB_ROOT/evidence/iw.sha256"
printf '%s  %s\n' "$TF_SHA256" "$TF_BIN" > "$LAB_ROOT/evidence/terraform.sha256"
printf '%s  %s\n' "$KUBECTL_SHA256" "$KUBECTL_BIN" > "$LAB_ROOT/evidence/kubectl.sha256"
git -C "$REPO" rev-parse HEAD > "$LAB_ROOT/evidence/candidate-commit.txt"

cat > "$LAB_ROOT/terraform.rc" <<HCL
disable_checkpoint = true
plugin_cache_dir  = "$LAB_ROOT/plugin-cache"

provider_installation {
  direct {
    include = ["registry.terraform.io/hashicorp/kubernetes"]
  }
}
HCL
chmod 0600 "$LAB_ROOT/terraform.rc"

TF_CLI_CONFIG_SHA256="$(sha256_value "$LAB_ROOT/terraform.rc")"
cat > "$LAB_ROOT/evidence/approval-record.txt" <<RECORD
commit=$EXPECTED_COMMIT
context=$ADMIN_CONTEXT
cluster_server=$CLUSTER_SERVER
cluster_ca_data_sha256=$CLUSTER_CA_DATA_SHA256
namespace=$NAMESPACE
configmap=$CONFIGMAP
service_account=$SERVICE_ACCOUNT
role=$ROLE
role_binding=$ROLE_BINDING
iw=$IW
iw_sha256=$IW_SHA256
terraform=$TF_BIN
terraform_sha256=$TF_SHA256
kubectl=$KUBECTL_BIN
kubectl_sha256=$KUBECTL_SHA256
terraform_cli_config=$LAB_ROOT/terraform.rc
terraform_cli_config_sha256=$TF_CLI_CONFIG_SHA256
provider_installation=direct registry.terraform.io/hashicorp/kubernetes 2.38.0
admin_auth=self-contained kubeconfig; no exec credential plugin
lab_root=$LAB_ROOT
RECORD
```

Do not request approval yet: first create and hash the Section 3 fixtures. The
minimal `package.json` is required because the CLI locates its package root by
walking upward from its executable; every pack/profile/deployment path below
remains explicit.

## 3. Temporary pack and deployment overlay

All files in this section are under `LAB_ROOT`, outside the repository. The
schema is a resource-minimal reduction of
`terraform providers schema -json` for `hashicorp/kubernetes` 2.38.0: it keeps
every type, required/optional/computed flag, nesting mode, and item bound used
by `kubernetes_config_map`. It is intentionally not a copied full-provider
schema.

Create the directories:

```bash
install -d -m 0700 \
  "$LAB_ROOT/packs/kubernetes/schemas/provider" \
  "$LAB_ROOT/packs/kubernetes/overrides" \
  "$LAB_ROOT/packs/kubernetes/oracle" \
  "$LAB_ROOT/packsets" \
  "$LAB_ROOT/pulls/$TENANT" \
  "$LAB_ROOT/overlay"
```

`$LAB_ROOT/packs/kubernetes/pack.json`:

```bash
cat > "$LAB_ROOT/packs/kubernetes/pack.json" <<'JSON'
{
  "drift_policy": {
    "resource_types": {},
    "version": 1
  },
  "pin": "2.38.0",
  "provider_prefixes": {
    "kubernetes_": "kubernetes"
  },
  "provider_sources": {
    "kubernetes": "hashicorp/kubernetes"
  },
  "vendor": "hashicorp"
}
JSON
```

`$LAB_ROOT/packs/kubernetes/registry.json`:

```bash
cat > "$LAB_ROOT/packs/kubernetes/registry.json" <<'JSON'
{
  "kubernetes_config_map": {
    "generate": true,
    "product": "kubernetes"
  }
}
JSON
```

`$LAB_ROOT/packs/kubernetes/overrides/kubernetes_config_map.json` maps the
unmodified Kubernetes object's nested identity into the Terraform import ID.
The composite key prevents namespace collisions.

```bash
cat > "$LAB_ROOT/packs/kubernetes/overrides/kubernetes_config_map.json" <<'JSON'
{
  "identity_fields": {
    "name": "metadata.name",
    "namespace": "metadata.namespace"
  },
  "import_id": "{namespace}/{name}",
  "key_field": [
    "namespace",
    "name"
  ]
}
JSON
```

`$LAB_ROOT/packs/kubernetes/oracle/kubernetes.tf`:

```bash
cat > "$LAB_ROOT/packs/kubernetes/oracle/kubernetes.tf" <<'HCL'
provider "kubernetes" {
  # Authentication is exclusively through the approved KUBE_CONFIG_PATH.
}
HCL
```

`$LAB_ROOT/packs/kubernetes/schemas/provider/kubernetes.json`:

```bash
cat > "$LAB_ROOT/packs/kubernetes/schemas/provider/kubernetes.json" <<'JSON'
{
  "data_source_schemas": {},
  "provider": {
    "block": {
      "attributes": {
        "config_path": {
          "description_kind": "plain",
          "optional": true,
          "type": "string"
        }
      },
      "block_types": {},
      "description_kind": "plain"
    },
    "version": 0
  },
  "resource_schemas": {
    "kubernetes_config_map": {
      "block": {
        "attributes": {
          "binary_data": {
            "description_kind": "plain",
            "optional": true,
            "type": ["map", "string"]
          },
          "data": {
            "description_kind": "plain",
            "optional": true,
            "type": ["map", "string"]
          },
          "id": {
            "computed": true,
            "description_kind": "plain",
            "optional": true,
            "type": "string"
          },
          "immutable": {
            "description_kind": "plain",
            "optional": true,
            "type": "bool"
          }
        },
        "block_types": {
          "metadata": {
            "block": {
              "attributes": {
                "annotations": {
                  "description_kind": "plain",
                  "optional": true,
                  "type": ["map", "string"]
                },
                "generate_name": {
                  "description_kind": "plain",
                  "optional": true,
                  "type": "string"
                },
                "generation": {
                  "computed": true,
                  "description_kind": "plain",
                  "type": "number"
                },
                "labels": {
                  "description_kind": "plain",
                  "optional": true,
                  "type": ["map", "string"]
                },
                "name": {
                  "computed": true,
                  "description_kind": "plain",
                  "optional": true,
                  "type": "string"
                },
                "namespace": {
                  "description_kind": "plain",
                  "optional": true,
                  "type": "string"
                },
                "resource_version": {
                  "computed": true,
                  "description_kind": "plain",
                  "type": "string"
                },
                "uid": {
                  "computed": true,
                  "description_kind": "plain",
                  "type": "string"
                }
              },
              "block_types": {},
              "description_kind": "plain"
            },
            "max_items": 1,
            "min_items": 1,
            "nesting_mode": "list"
          }
        },
        "description_kind": "plain"
      },
      "version": 0
    }
  }
}
JSON
```

Both `$LAB_ROOT/packsets/profile.json` and
`$LAB_ROOT/packsets/catalog.json` contain:

```bash
cat > "$LAB_ROOT/packsets/profile.json" <<'JSON'
{
  "kind": "infrawright.pack-set",
  "packs": [
    "kubernetes"
  ],
  "shared": [],
  "version": 1
}
JSON
cp "$LAB_ROOT/packsets/profile.json" "$LAB_ROOT/packsets/catalog.json"
```

`$LAB_ROOT/deployment.json`:

```bash
cat > "$LAB_ROOT/deployment.json" <<'JSON'
{
  "module_dir": "overlay/modules",
  "overlay": "overlay",
  "roots": {
    "kubernetes": {
      "groups": {
        "qualification": [
          "kubernetes_config_map"
        ]
      }
    }
  },
  "tfvars_format": "json"
}
JSON
```

Create the exact namespace and RBAC manifests before approval. The namespace's
run-id label is atomic with creation and is the ownership marker the EXIT
cleanup must verify before any deletion.

```bash
cat > "$LAB_ROOT/namespace.yaml" <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: $NAMESPACE
  labels:
    app.kubernetes.io/managed-by: infrawright-qualification
    infrawright.dev/qualification-run-id: $RUN_ID
YAML

cat > "$LAB_ROOT/rbac.yaml" <<YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: $ROLE
  namespace: $NAMESPACE
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["$CONFIGMAP"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: $ROLE_BINDING
  namespace: $NAMESPACE
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: $ROLE
subjects:
  - kind: ServiceAccount
    name: $SERVICE_ACCOUNT
    namespace: $NAMESPACE
YAML
```

Hash the exact temporary pack and deployment bytes, append that digest to the
approval record, and stop. Paths under `LAB_ROOT` cannot contain newlines; that
precondition keeps the portable sorted manifest unambiguous.

```bash
case "$LAB_ROOT" in *$'\n'*) exit 1 ;; esac
write_fixture_manifest() {
  local destination="$1" irregular source
  : > "$destination" || return 1
  irregular="$(find \
    "$LAB_ROOT/packs" "$LAB_ROOT/packsets" \
    ! -type f ! -type d -print -quit)"
  test -z "$irregular" || return 1
  for source in "$LAB_ROOT/deployment.json" "$LAB_ROOT/namespace.yaml" "$LAB_ROOT/rbac.yaml"; do
    test -f "$source" && test ! -L "$source" || return 1
  done
  while IFS= read -r source; do
    shasum -a 256 "$source" >> "$destination" || return 1
  done < <({
    find "$LAB_ROOT/packs" "$LAB_ROOT/packsets" -type f -print
    printf '%s\n' \
      "$LAB_ROOT/deployment.json" "$LAB_ROOT/namespace.yaml" "$LAB_ROOT/rbac.yaml"
  } | LC_ALL=C sort)
}
write_fixture_manifest "$LAB_ROOT/evidence/fixture-files.sha256"
FIXTURE_MANIFEST_SHA256="$(sha256_value "$LAB_ROOT/evidence/fixture-files.sha256")"
printf 'fixture_manifest_sha256=%s\n' "$FIXTURE_MANIFEST_SHA256" \
  >> "$LAB_ROOT/evidence/approval-record.txt"
cat "$LAB_ROOT/evidence/approval-record.txt"
```

Approval must quote that exact record and approve the setup, failure cleanup,
and qualification commands in this document. A generic “go ahead” without the
resolved cluster identity, object names, executable/configuration digests, and
fixture-manifest digest is insufficient. **Stop here for the first approval.**

## 4. Approved live setup and RBAC hard stop

Run this section only after the first explicit approval. The admin kubeconfig
and context remain unexported shell variables. Administrative commands receive
them only through `run_admin_capture`; every qualification command later runs
under `env -i`, so neither the admin path nor any ambient credential can reach
Infrawright, Terraform, or the provider.

```bash
run_capture() {
  local label="$1"
  shift
  local status
  if "$@" > "$LAB_ROOT/evidence/$label.stdout" 2> "$LAB_ROOT/evidence/$label.stderr"; then
    status=0
  else
    status=$?
  fi
  printf '%s\n' "$status" > "$LAB_ROOT/evidence/$label.exit"
  cat "$LAB_ROOT/evidence/$label.stdout"
  cat "$LAB_ROOT/evidence/$label.stderr" >&2
  return "$status"
}

run_secret_capture() {
  local label="$1" destination="$2" redaction="$3" status
  shift 3
  if "$@" > "$destination" 2> "$LAB_ROOT/evidence/$label.stderr"; then
    status=0
  else
    status=$?
  fi
  printf '%s\n' "$redaction" > "$LAB_ROOT/evidence/$label.stdout"
  printf '%s\n' "$status" > "$LAB_ROOT/evidence/$label.exit"
  cat "$LAB_ROOT/evidence/$label.stderr" >&2
  return "$status"
}

run_binary_capture() {
  local label="$1" status
  shift
  if "$@" > "$LAB_ROOT/evidence/$label.stdout" 2> "$LAB_ROOT/evidence/$label.stderr"; then
    status=0
  else
    status=$?
  fi
  printf '%s\n' "$status" > "$LAB_ROOT/evidence/$label.exit"
  cat "$LAB_ROOT/evidence/$label.stderr" >&2
  return "$status"
}

ADMIN_ENV=(
  env -i
  HOME="$LAB_ROOT/home"
  PATH='/usr/bin:/bin'
  TMPDIR="$TMPDIR"
  KUBECONFIG="$ADMIN_KUBECONFIG"
)
run_admin_capture() {
  local label="$1"
  shift
  run_capture "$label" "${ADMIN_ENV[@]}" "$KUBECTL_BIN" \
    --context "$ADMIN_CONTEXT" "$@"
}

verify_admin_cluster_binding() {
  local current_ca current_ca_sha current_server
  current_server="$(
    "${ADMIN_ENV[@]}" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
      config view --raw --flatten --minify \
      -o jsonpath='{.clusters[0].cluster.server}'
  )" || return 1
  current_ca="$(
    "${ADMIN_ENV[@]}" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
      config view --raw --flatten --minify \
      -o jsonpath='{.clusters[0].cluster.certificate-authority-data}'
  )" || return 1
  current_ca_sha="$(printf '%s' "$current_ca" | shasum -a 256 | awk '{print $1}')"
  test "$current_server" = "$CLUSTER_SERVER" || return 1
  test "$current_ca_sha" = "$CLUSTER_CA_DATA_SHA256" || return 1
  printf 'admin cluster server and CA digest reverified\n'
}

verify_pre_live_integrity() {
  local current_server current_ca current_ca_sha current_exec_users current_fixture_manifest
  test "$(git -C "$REPO" rev-parse HEAD)" = "$EXPECTED_COMMIT" || return 1
  test -z "$(git -C "$REPO" status --short)" || return 1
  test "$(sha256_value "$IW")" = "$IW_SHA256" || return 1
  test "$(sha256_value "$TF_BIN")" = "$TF_SHA256" || return 1
  test "$(sha256_value "$KUBECTL_BIN")" = "$KUBECTL_SHA256" || return 1
  test "$(sha256_value "$LAB_ROOT/terraform.rc")" = "$TF_CLI_CONFIG_SHA256" || return 1
  current_fixture_manifest="$LAB_ROOT/evidence/fixture-files.current.sha256"
  write_fixture_manifest "$current_fixture_manifest" || return 1
  cmp -s "$LAB_ROOT/evidence/fixture-files.sha256" "$current_fixture_manifest" || return 1
  test "$(sha256_value "$current_fixture_manifest")" = "$FIXTURE_MANIFEST_SHA256" || return 1
  rm -f -- "$current_fixture_manifest" || return 1
  current_server="$(
    KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
      config view --raw --flatten --minify \
      -o jsonpath='{.clusters[0].cluster.server}'
  )" || return 1
  current_ca="$(
    KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
      config view --raw --flatten --minify \
      -o jsonpath='{.clusters[0].cluster.certificate-authority-data}'
  )" || return 1
  current_exec_users="$(
    KUBECONFIG="$ADMIN_KUBECONFIG" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
      config view --raw --flatten --minify -o json |
      jq '[.users[]?.user | select(has("exec"))] | length'
  )" || return 1
  current_ca_sha="$(printf '%s' "$current_ca" | shasum -a 256 | awk '{print $1}')"
  test "$current_server" = "$CLUSTER_SERVER" || return 1
  test "$current_ca_sha" = "$CLUSTER_CA_DATA_SHA256" || return 1
  test "$current_exec_users" = 0 || return 1
  printf 'commit, executable digests, server, and CA digest reverified\n'
}
run_capture pre-live-integrity verify_pre_live_integrity

REMOTE_SETUP_ARMED=0
CLEANUP_ATTEMPT=0
QUALIFICATION_PASSED=0

cleanup_remote() {
  local cleanup_failed=0 ownership_status=0 verify_status=0 prefix
  CLEANUP_ATTEMPT=$((CLEANUP_ATTEMPT + 1))
  prefix="cleanup-attempt-$CLEANUP_ATTEMPT"
  if [ "$REMOTE_SETUP_ARMED" -eq 1 ]; then
    if ! run_capture "$prefix-admin-cluster-binding" verify_admin_cluster_binding; then
      printf '%s\n' 'CLEANUP REFUSED: admin cluster binding did not match approval' >&2
      cleanup_failed=1
    else
      run_admin_capture "$prefix-ownership" get namespace "$NAMESPACE" -o json || \
        ownership_status=$?
    fi
    if [ "$cleanup_failed" -eq 0 ] && [ "$ownership_status" -eq 0 ]; then
      if run_capture "$prefix-ownership-gate" jq -e --arg run_id "$RUN_ID" \
        '.metadata.labels["infrawright.dev/qualification-run-id"] == $run_id and
         .metadata.labels["app.kubernetes.io/managed-by"] == "infrawright-qualification"' \
        "$LAB_ROOT/evidence/$prefix-ownership.stdout"; then
        run_admin_capture "$prefix-service-account" --namespace "$NAMESPACE" \
          delete serviceaccount "$SERVICE_ACCOUNT" --ignore-not-found \
          --wait=true --timeout=5m || cleanup_failed=1
        run_admin_capture "$prefix-namespace" delete namespace "$NAMESPACE" \
          --ignore-not-found --wait=true --timeout=5m || cleanup_failed=1
      else
        printf '%s\n' 'CLEANUP REFUSED: namespace ownership marker did not match' >&2
        cleanup_failed=1
      fi
    elif [ "$cleanup_failed" -eq 0 ] && { [ "$ownership_status" -ne 1 ] ||
      ! grep -F '(NotFound)' "$LAB_ROOT/evidence/$prefix-ownership.stderr" >/dev/null ||
      ! grep -F "namespaces \"$NAMESPACE\" not found" \
        "$LAB_ROOT/evidence/$prefix-ownership.stderr" >/dev/null; }; then
      printf '%s\n' 'CLEANUP FAILED: ownership could not be checked' >&2
      cleanup_failed=1
    fi
    if [ "$cleanup_failed" -eq 0 ]; then
      run_admin_capture "$prefix-namespace-verify" get namespace "$NAMESPACE" -o name || \
        verify_status=$?
      if [ "$verify_status" -eq 0 ]; then
        printf '%s\n' 'CLEANUP FAILED: qualification namespace still exists' >&2
        cleanup_failed=1
      elif [ "$verify_status" -ne 1 ] ||
        ! grep -F '(NotFound)' "$LAB_ROOT/evidence/$prefix-namespace-verify.stderr" >/dev/null ||
        ! grep -F "namespaces \"$NAMESPACE\" not found" \
          "$LAB_ROOT/evidence/$prefix-namespace-verify.stderr" >/dev/null; then
        printf '%s\n' 'CLEANUP FAILED: namespace absence was not an explicit NotFound' >&2
        cleanup_failed=1
      fi
    fi
  fi
  run_capture "$prefix-local-credentials" rm -f -- \
    "$LAB_ROOT/service-account.token" "$LAB_ROOT/read-only.kubeconfig" || \
    cleanup_failed=1
  return "$cleanup_failed"
}

finish_qualification() {
  local original_status=$? cleanup_status=0 final_status
  trap - EXIT INT TERM
  cleanup_remote || cleanup_status=$?
  final_status="$original_status"
  if [ "$cleanup_status" -ne 0 ]; then
    final_status=1
  fi
  if [ "$QUALIFICATION_PASSED" -ne 1 ]; then
    final_status=1
  fi
  if [ "$original_status" -ne 0 ] || [ "$cleanup_status" -ne 0 ] || \
    [ "$QUALIFICATION_PASSED" -ne 1 ]; then
    printf 'Qualification or cleanup did not pass; evidence and local work remain at %s\n' \
      "$LAB_ROOT" >&2
  fi
  exit "$final_status"
}
trap finish_qualification EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

REMOTE_SETUP_ARMED=1
run_admin_capture setup-namespace create -f "$LAB_ROOT/namespace.yaml"
run_admin_capture setup-configmap --namespace "$NAMESPACE" \
  create configmap "$CONFIGMAP" \
  --from-literal="qualification.json={\"purpose\":\"infrawright-go-adopt-apply-qualification\",\"run_id\":\"$RUN_ID\"}"
run_admin_capture setup-service-account --namespace "$NAMESPACE" \
  create serviceaccount "$SERVICE_ACCOUNT"
run_admin_capture setup-rbac apply -f "$LAB_ROOT/rbac.yaml"
```

Build a one-hour service-account kubeconfig without copying admin credentials.
The token is never placed on a command line and the intermediate token file is
mode 0600 under the private job root.

```bash
run_secret_capture create-reader-token "$LAB_ROOT/service-account.token" \
  '<redacted service-account token>' \
  "${ADMIN_ENV[@]}" "$KUBECTL_BIN" --context "$ADMIN_CONTEXT" \
  --namespace "$NAMESPACE" create token "$SERVICE_ACCOUNT" --duration=1h
run_capture create-reader-token-mode chmod 0600 "$LAB_ROOT/service-account.token"

READ_ONLY_KUBECONFIG="$LAB_ROOT/read-only.kubeconfig"
run_secret_capture create-reader-kubeconfig "$READ_ONLY_KUBECONFIG" \
  '<redacted read-only kubeconfig>' jq -n \
  --arg server "$CLUSTER_SERVER" \
  --arg ca "$CLUSTER_CA_DATA" \
  --arg namespace "$NAMESPACE" \
  --rawfile token "$LAB_ROOT/service-account.token" \
  '{
    apiVersion: "v1",
    kind: "Config",
    clusters: [{name: "qualification", cluster: {
      server: $server,
      "certificate-authority-data": $ca
    }}],
    users: [{name: "qualification-reader", user: {
      token: ($token | rtrimstr("\n"))
    }}],
    contexts: [{name: "qualification", context: {
      cluster: "qualification",
      user: "qualification-reader",
      namespace: $namespace
    }}],
    "current-context": "qualification"
  }'
run_capture create-reader-kubeconfig-mode chmod 0600 "$READ_ONLY_KUBECONFIG"
READ_ONLY_KUBECONFIG_SHA256="$(sha256_value "$READ_ONLY_KUBECONFIG")"
printf '%s  %s\n' "$READ_ONLY_KUBECONFIG_SHA256" '<redacted read-only kubeconfig>' \
  > "$LAB_ROOT/evidence/read-only-kubeconfig.sha256"
run_capture create-reader-token-scrub rm -f -- "$LAB_ROOT/service-account.token"
unset CLUSTER_CA_DATA
```

Bind every qualification subprocess to a literal environment allowlist. This
is stronger than unsetting known variables: the child starts with `env -i` and
receives no admin kubeconfig, proxy, provider reattachment, development
override, ambient Terraform argument, backend, or alternate Kubernetes
selector. In particular, `KUBE_TLS_SERVER_NAME`, `KUBE_PROXY_URL`,
`TF_REATTACH_PROVIDERS`, `TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE`,
`TERRAFORM_CONFIG`, every `TF_CLI_ARGS*` (including `TF_CLI_ARGS_show`), and
standard proxy variables are absent. The job-local `TF_CLI_CONFIG_FILE` allows
only direct installation of the reviewed provider address.

```bash
QUAL_ENV=(
  env -i
  HOME="$LAB_ROOT/home"
  PATH='/usr/bin:/bin'
  LANG=C
  LC_ALL=C
  TMPDIR="$TMPDIR"
  KUBECONFIG="$READ_ONLY_KUBECONFIG"
  KUBE_CONFIG_PATH="$READ_ONLY_KUBECONFIG"
  TF="$TF_BIN"
  TF_IN_AUTOMATION=1
  CHECKPOINT_DISABLE=1
  TF_CLI_CONFIG_FILE="$LAB_ROOT/terraform.rc"
  TF_PLUGIN_CACHE_DIR="$LAB_ROOT/plugin-cache"
  INFRAWRIGHT_ORACLE_STATE_SOURCE=applied-state
  INFRAWRIGHT_PACKS="$LAB_ROOT/packs"
  INFRAWRIGHT_PACK_PROFILE="$LAB_ROOT/packsets/profile.json"
  INFRAWRIGHT_DEPLOYMENT="$LAB_ROOT/deployment.json"
)
run_qual_capture() {
  local label="$1"
  shift
  run_capture "$label" "${QUAL_ENV[@]}" "$@"
}

printf '%s\n' \
  "TMPDIR=$TMPDIR" \
  "HOME=$LAB_ROOT/home" \
  "PATH=/usr/bin:/bin" \
  "KUBECONFIG=$READ_ONLY_KUBECONFIG" \
  "KUBE_CONFIG_PATH=$READ_ONLY_KUBECONFIG" \
  "INFRAWRIGHT_KEEP_ORACLE=<unset>" \
  "KEEP_ORACLE=<unset>" \
  "INFRAWRIGHT_ORACLE_STATE_SOURCE=applied-state" \
  "INFRAWRIGHT_PACKS=$LAB_ROOT/packs" \
  "INFRAWRIGHT_PACK_PROFILE=$LAB_ROOT/packsets/profile.json" \
  "INFRAWRIGHT_DEPLOYMENT=$LAB_ROOT/deployment.json" \
  "TF=$TF_BIN" \
  "TF_CLI_CONFIG_FILE=$LAB_ROOT/terraform.rc" \
  "TF_PLUGIN_CACHE_DIR=$LAB_ROOT/plugin-cache" \
  "ambient Kubernetes/Terraform/proxy selectors=<absent via env -i>" \
  > "$LAB_ROOT/evidence/environment-allowlist.txt"
```

Prove effective permissions, not merely the intended Role manifest. The
credential may get exactly the named ConfigMap; it may not read the collection,
list/watch, get a different name, mutate the named object, create, or
delete-collect. `kubectl auth can-i` asks authorization questions and does not
try the operation.

```bash
expect_auth() {
  local label="$1" expected="$2" verb="$3" resource="$4"
  run_qual_capture "$label-query" "$KUBECTL_BIN" --namespace "$NAMESPACE" \
    auth can-i "$verb" "$resource"
  run_capture "$label-gate" /bin/sh -c \
    'test "$(tr -d "\n" < "$1")" = "$2"' sh \
    "$LAB_ROOT/evidence/$label-query.stdout" "$expected"
}

prove_read_only_rbac() {
  local prefix="$1" verb
  expect_auth "$prefix-rbac-named-get" yes get "configmap/$CONFIGMAP"
  expect_auth "$prefix-rbac-collection-get" no get configmaps
  expect_auth "$prefix-rbac-list" no list configmaps
  expect_auth "$prefix-rbac-watch" no watch configmaps
  expect_auth "$prefix-rbac-other-name" no get "configmap/$CONFIGMAP-other"
  for verb in update patch delete; do
    expect_auth "$prefix-rbac-named-$verb" no "$verb" "configmap/$CONFIGMAP"
  done
  for verb in create deletecollection; do
    expect_auth "$prefix-rbac-collection-$verb" no "$verb" configmaps
  done
}

prove_read_only_rbac initial

run_qual_capture configmap-before "$KUBECTL_BIN" --namespace "$NAMESPACE" \
  get configmap "$CONFIGMAP" -o json
cp "$LAB_ROOT/evidence/configmap-before.stdout" \
  "$LAB_ROOT/evidence/configmap-before.json"
```

Any denial of the required `get`, or any `yes` for a mutation verb, aborts the
run before Terraform.

## 5. Pull input and local-only preparation

The pull is the full, unnormalized `kubectl get -o json` object wrapped in the
array the adoption command expects. No SDK or conversion normalizes evidence.

```bash
run_capture pull-array jq -s '.' "$LAB_ROOT/evidence/configmap-before.json"
cp "$LAB_ROOT/evidence/pull-array.stdout" \
  "$LAB_ROOT/pulls/$TENANT/kubernetes_config_map.json"
run_capture pull-gate jq -e 'length == 1 and .[0].kind == "ConfigMap"' \
  "$LAB_ROOT/pulls/$TENANT/kubernetes_config_map.json"

run_capture configmap-before-identity jq -r \
  '[.metadata.uid, .metadata.resourceVersion, .metadata.namespace, .metadata.name] | @tsv' \
  "$LAB_ROOT/evidence/configmap-before.json"
cp "$LAB_ROOT/evidence/configmap-before-identity.stdout" \
  "$LAB_ROOT/evidence/configmap-before.identity.tsv"

cd "$LAB_ROOT"
COMMON=(
  --resource "$RESOURCE"
  --root "$LAB_ROOT/packs"
  --profile "$LAB_ROOT/packsets/profile.json"
  --catalog "$LAB_ROOT/packsets/catalog.json"
  --deployment "$LAB_ROOT/deployment.json"
)

run_qual_capture check-pack "$IW" check-pack --pack kubernetes --root "$LAB_ROOT/packs"
run_qual_capture check-pack-set "$IW" check-pack-set \
  --root "$LAB_ROOT/packs" \
  --profile "$LAB_ROOT/packsets/profile.json" \
  --catalog "$LAB_ROOT/packsets/catalog.json"
run_qual_capture modules-generate "$IW" modules generate "${COMMON[@]}" --terraform "$TF_BIN"
run_qual_capture gen-env "$IW" gen-env --tenant "$TENANT" "${COMMON[@]}" --terraform "$TF_BIN"

ENV_DIR="$LAB_ROOT/overlay/envs/$TENANT/$ROOT_LABEL"
run_capture environment-directory-gate test -d "$ENV_DIR"
verify_no_backend() {
  local configuration_list invalid_metadata irregular status terraform_file
  local -a terraform_files
  configuration_list="$(mktemp "$LAB_ROOT/evidence/backend-scan.XXXXXX")" || return 1
  invalid_metadata="$(find "$LAB_ROOT/overlay" "$LAB_ROOT/packs/kubernetes/oracle" \
    -name .terraform ! -type d -print -quit)" || return 1
  if [ -n "$invalid_metadata" ]; then
    printf 'ABORT: .terraform is not a real directory: %s\n' "$invalid_metadata" >&2
    rm -f -- "$configuration_list"
    return 1
  fi
  irregular="$(find "$LAB_ROOT/overlay" "$LAB_ROOT/packs/kubernetes/oracle" \
    -type d -name .terraform -prune -o ! -type f ! -type d -print -quit)" || return 1
  if [ -n "$irregular" ]; then
    printf 'ABORT: non-regular generated configuration entry: %s\n' "$irregular" >&2
    rm -f -- "$configuration_list"
    return 1
  fi
  if ! find "$LAB_ROOT/overlay" "$LAB_ROOT/packs/kubernetes/oracle" \
    -type d -name .terraform -prune -o -type f -name '*.tf' -print0 \
    > "$configuration_list"; then
    rm -f -- "$configuration_list"
    return 1
  fi
  mapfile -d '' terraform_files < "$configuration_list"
  rm -f -- "$configuration_list" || return 1
  test "${#terraform_files[@]}" -gt 0 || return 1
  for terraform_file in "${terraform_files[@]}"; do
    if grep -nE '(^|[^[:alnum:]_])(backend|cloud)([^[:alnum:]_]|$)' \
      "$terraform_file"; then
      printf 'ABORT: backend/cloud token in %s\n' "$terraform_file" >&2
      return 1
    else
      status=$?
    fi
    test "$status" -eq 1 || return 1
  done
}
run_capture initial-backend-cloud-gate verify_no_backend
```

No `--backend` or `--backend-config` option appears anywhere in this runbook.
The generated environment and Oracle scratch roots must both remain local. The
gate is deliberately conservative and ignore-independent: outside Terraform's
own `.terraform` metadata it rejects every non-regular entry and every complete
`backend` or `cloud` token in every regular `.tf` file, including tokens in
comments or strings. False positives abort; they are never waived in place.

## 6. Qualification sequence and pre-Apply stop

Run the requested sequence in this exact order through saved-plan assessment:

```bash
run_qual_capture adopt "$IW" adopt \
  --in "$LAB_ROOT/pulls/$TENANT" \
  --tenant "$TENANT" \
  "${COMMON[@]}" \
  --terraform "$TF_BIN"

run_qual_capture stage-imports "$IW" stage-imports \
  --tenant "$TENANT" \
  "${COMMON[@]}"

run_capture post-stage-backend-cloud-gate verify_no_backend

run_qual_capture plan-first "$IW" plan \
  --tenant "$TENANT" \
  "${COMMON[@]}" \
  --save \
  --terraform "$TF_BIN"

FIRST_PLAN="$ENV_DIR/tfplan"
run_capture first-plan-file-gate test -f "$FIRST_PLAN"
run_qual_capture plan-first-show "$TF_BIN" -chdir="$ENV_DIR" show -json "$FIRST_PLAN"
cp "$LAB_ROOT/evidence/plan-first-show.stdout" \
  "$LAB_ROOT/evidence/plan-first.json"
run_capture plan-first-digest shasum -a 256 "$FIRST_PLAN"
FIRST_PLAN_SHA256="$(awk '{print $1}' "$LAB_ROOT/evidence/plan-first-digest.stdout")"
cp "$LAB_ROOT/evidence/plan-first-digest.stdout" \
  "$LAB_ROOT/evidence/plan-first.sha256"

run_capture plan-first-classification jq --arg expected_import_id "$NAMESPACE/$CONFIGMAP" '{
  applyable: .applyable,
  complete: .complete,
  errored: .errored,
  total_resource_changes: ((.resource_changes // []) | length),
  imports: [.resource_changes[]? | select(.change.importing != null) | {
    address: .address,
    import_id: .change.importing.id,
    mode: .mode,
    provider_name: .provider_name,
    type: .type
  }],
  creates: ([.resource_changes[]? | select(.change.actions == ["create"])] | length),
  updates: ([.resource_changes[]? | select(.change.actions == ["update"])] | length),
  replacements: ([.resource_changes[]? | select(
    (.change.actions | index("create")) != null and
    (.change.actions | index("delete")) != null
  )] | length),
  destroys: ([.resource_changes[]? | select(.change.actions == ["delete"])] | length),
  resource_drift: ((.resource_drift // []) | length),
  output_changes: ((.output_changes // {}) | length),
  action_invocations: ((.action_invocations // []) | length),
  deferred_action_invocations: ((.deferred_action_invocations // []) | length),
  deferred_changes: ((.deferred_changes // []) | length),
  checks: ((.checks // []) | length),
  diagnostics: ((.diagnostics // []) | length),
  errors: ((.errors // []) | length),
  expected_import_id: $expected_import_id
}' "$LAB_ROOT/evidence/plan-first.json"

cp "$LAB_ROOT/evidence/plan-first-classification.stdout" \
  "$LAB_ROOT/evidence/plan-first.classification.json"

run_capture plan-first-structural-gate jq -e \
  --arg expected_import_id "$NAMESPACE/$CONFIGMAP" \
  --arg expected_provider "registry.terraform.io/hashicorp/kubernetes" \
  --arg expected_type "$RESOURCE" '
  type == "object" and
  .applyable == true and
  .complete == true and
  .errored == false and
  (.resource_changes | type) == "array" and
  ((has("resource_drift") | not) or (.resource_drift | type) == "array") and
  ((has("output_changes") | not) or (.output_changes | type) == "object") and
  ((has("action_invocations") | not) or (.action_invocations | type) == "array") and
  ((has("deferred_action_invocations") | not) or (.deferred_action_invocations | type) == "array") and
  ((has("deferred_changes") | not) or (.deferred_changes | type) == "array") and
  ((has("checks") | not) or (.checks | type) == "array") and
  ((has("diagnostics") | not) or (.diagnostics | type) == "array") and
  ((has("errors") | not) or (.errors | type) == "array") and
  (.resource_changes | length) == 1 and
  all(.resource_changes[];
    .mode == "managed" and
    .type == $expected_type and
    .provider_name == $expected_provider and
    .change.importing.id == $expected_import_id and
    .change.actions == ["no-op"]
  ) and
  ((.resource_drift // []) | length) == 0 and
  ((.output_changes // {}) | length) == 0 and
  ((.action_invocations // []) | length) == 0 and
  ((.deferred_action_invocations // []) | length) == 0 and
  ((.deferred_changes // []) | length) == 0 and
  ((.checks // []) | length) == 0 and
  ((.diagnostics // []) | length) == 0 and
  ((.errors // []) | length) == 0
' "$LAB_ROOT/evidence/plan-first.json"

run_qual_capture assert-adoptable "$IW" assert-adoptable \
  --tenant "$TENANT" \
  "${COMMON[@]}" \
  --report "$LAB_ROOT/evidence/assert-adoptable.json" \
  --terraform "$TF_BIN"

run_capture assert-adoptable-gate jq -e '
  .mode == "assert-adoptable" and
  .summary.status == "clean" and
  .summary.checked == 1 and
  .summary.clean == 1 and
  .summary.tolerated == 0 and
  .summary.blocked == 0 and
  ((has("error") | not) or .error == null)
' "$LAB_ROOT/evidence/assert-adoptable.json"

run_qual_capture terraform-providers "$TF_BIN" -chdir="$ENV_DIR" providers
run_capture provider-lock-copy cp "$ENV_DIR/.terraform.lock.hcl" \
  "$LAB_ROOT/evidence/terraform.lock.hcl"
run_capture provider-lock-gate /bin/sh -c '
  grep -F '\''provider "registry.terraform.io/hashicorp/kubernetes"'\'' "$1" >/dev/null &&
  grep -F '\''version     = "2.38.0"'\'' "$1" >/dev/null
' sh "$LAB_ROOT/evidence/terraform.lock.hcl"
run_capture provider-lock-digest shasum -a 256 "$LAB_ROOT/evidence/terraform.lock.hcl"
TF_LOCK_SHA256="$(awk '{print $1}' "$LAB_ROOT/evidence/provider-lock-digest.stdout")"
cp "$LAB_ROOT/evidence/provider-lock-digest.stdout" \
  "$LAB_ROOT/evidence/terraform-lock.sha256"

capture_effective_provider() {
  local prefix="$1" provider_root
  local -a provider_links
  provider_root="$ENV_DIR/.terraform/providers/registry.terraform.io/hashicorp/kubernetes/2.38.0"
  run_binary_capture "$prefix-provider-discovery" find -L "$provider_root" \
    -type f -name 'terraform-provider-kubernetes*' -print0
  mapfile -d '' provider_links < "$LAB_ROOT/evidence/$prefix-provider-discovery.stdout"
  run_capture "$prefix-provider-count" test "${#provider_links[@]}" -eq 1
  EFFECTIVE_PROVIDER_LINK="${provider_links[0]}"
  run_capture "$prefix-provider-realpath" "$REALPATH_BIN" "$EFFECTIVE_PROVIDER_LINK"
  EFFECTIVE_PROVIDER_BINARY="$(tr -d '\n' < "$LAB_ROOT/evidence/$prefix-provider-realpath.stdout")"
  run_capture "$prefix-provider-file-gate" test -f "$EFFECTIVE_PROVIDER_BINARY"
  run_capture "$prefix-provider-paths" printf 'installed=%s\nresolved=%s\n' \
    "$EFFECTIVE_PROVIDER_LINK" "$EFFECTIVE_PROVIDER_BINARY"
  run_capture "$prefix-provider-digest" shasum -a 256 "$EFFECTIVE_PROVIDER_BINARY"
  EFFECTIVE_PROVIDER_SHA256="$(awk '{print $1}' "$LAB_ROOT/evidence/$prefix-provider-digest.stdout")"
  run_capture "$prefix-provider-file" file "$EFFECTIVE_PROVIDER_BINARY"
  run_capture "$prefix-provider-build" "$GO_BIN" version -m "$EFFECTIVE_PROVIDER_BINARY"
}
capture_effective_provider reviewed
REVIEWED_PROVIDER_SHA256="$EFFECTIVE_PROVIDER_SHA256"
cp "$LAB_ROOT/evidence/reviewed-provider-digest.stdout" \
  "$LAB_ROOT/evidence/provider-binary.sha256"
```

Stop here. Present these artifacts to the user/reviewer:

- `plan-first.stdout`, `plan-first.stderr`, `plan-first.exit`, and the raw
  `plan-first.json`;
- `plan-first.classification.json`, `plan-first.sha256`, the exact generated
  import address, and the saved plan's absolute path;
- `assert-adoptable.stdout`, `assert-adoptable.stderr`,
  `assert-adoptable.exit`, and `assert-adoptable.json`;
- `terraform.lock.hcl`, `terraform-lock.sha256`, `terraform-providers.stdout`,
  the logical and resolved installed-provider paths, digest, `file` output, and
  Go build metadata;
- `read-only-kubeconfig.sha256` (never the kubeconfig itself);
- `iw.sha256`, the before identity tuple, and the proposed exact Apply command
  below.

The reviewer must confirm `applyable: true`, `complete: true`, exactly one
managed import for `registry.terraform.io/hashicorp/kubernetes`, the approved
resource type and `namespace/name` import ID, zero other effects, no assessment
tolerance, and no assessment block. The user's approval must return all six
literal values below; they bind the human-reviewed address and bytes to the
subsequent Apply. Exact Apply remains unauthorized until then.

```bash
APPROVED_IMPORT_ADDRESS='<exact address from plan-first.classification.json>'
APPROVED_PLAN_SHA256='<digest from plan-first.sha256>'
APPROVED_IW_SHA256='<digest from iw.sha256>'
APPROVED_PROVIDER_SHA256='<digest from provider-binary.sha256>'
APPROVED_TF_LOCK_SHA256='<digest from terraform-lock.sha256>'
APPROVED_KUBECONFIG_SHA256='<digest from read-only-kubeconfig.sha256>'
```

## 7. Exact saved-plan Apply and second no-op plan

Only after the second approval, execute the already saved plan. Do not add
`--allow-plan-changes` or `--allow-destroy`. `--allow-non-main` is explicit
because this is an isolated qualification workspace, not a production branch.
The pre-Apply block re-shows the current saved plan, rechecks every approved
digest, and binds its exact address to the value the human reviewed.

```bash
validate_approval_values() {
  local approved_digest
  for approved_digest in \
    "$APPROVED_PLAN_SHA256" "$APPROVED_IW_SHA256" \
    "$APPROVED_PROVIDER_SHA256" "$APPROVED_TF_LOCK_SHA256" \
    "$APPROVED_KUBECONFIG_SHA256"; do
    if [[ ! "$approved_digest" =~ ^[0-9a-f]{64}$ ]]; then
      printf '%s\n' 'ABORT: a literal 64-character approval digest was not supplied' >&2
      return 1
    fi
  done
  case "$APPROVED_IMPORT_ADDRESS" in
    ''|*'<'*)
      printf '%s\n' 'ABORT: literal approved import address was not supplied' >&2
      return 1
      ;;
  esac
}
run_capture approval-values-gate validate_approval_values

verify_pre_apply_binding() {
  local current_fixture_manifest
  test "$(git -C "$REPO" rev-parse HEAD)" = "$EXPECTED_COMMIT" || return 1
  test -z "$(git -C "$REPO" status --short)" || return 1
  test "$(sha256_value "$IW")" = "$APPROVED_IW_SHA256" || return 1
  test "$IW_SHA256" = "$APPROVED_IW_SHA256" || return 1
  test "$(sha256_value "$FIRST_PLAN")" = "$APPROVED_PLAN_SHA256" || return 1
  test "$FIRST_PLAN_SHA256" = "$APPROVED_PLAN_SHA256" || return 1
  test "$(sha256_value "$TF_BIN")" = "$TF_SHA256" || return 1
  test "$(sha256_value "$KUBECTL_BIN")" = "$KUBECTL_SHA256" || return 1
  test "$(sha256_value "$LAB_ROOT/terraform.rc")" = "$TF_CLI_CONFIG_SHA256" || return 1
  test "$(sha256_value "$READ_ONLY_KUBECONFIG")" = "$READ_ONLY_KUBECONFIG_SHA256" || return 1
  test "$READ_ONLY_KUBECONFIG_SHA256" = "$APPROVED_KUBECONFIG_SHA256" || return 1
  test "$(private_mode "$READ_ONLY_KUBECONFIG")" = 600 || return 1
  test "$(sha256_value "$ENV_DIR/.terraform.lock.hcl")" = "$APPROVED_TF_LOCK_SHA256" || return 1
  test "$(sha256_value "$LAB_ROOT/evidence/terraform.lock.hcl")" = "$APPROVED_TF_LOCK_SHA256" || return 1
  test "$TF_LOCK_SHA256" = "$APPROVED_TF_LOCK_SHA256" || return 1
  test "$REVIEWED_PROVIDER_SHA256" = "$APPROVED_PROVIDER_SHA256" || return 1
  current_fixture_manifest="$LAB_ROOT/evidence/pre-apply-fixture-files.sha256"
  write_fixture_manifest "$current_fixture_manifest" || return 1
  cmp -s "$LAB_ROOT/evidence/fixture-files.sha256" "$current_fixture_manifest" || return 1
  test "$(sha256_value "$current_fixture_manifest")" = "$FIXTURE_MANIFEST_SHA256" || return 1
}
run_capture pre-apply-binding verify_pre_apply_binding

capture_effective_provider pre-apply
run_capture pre-apply-provider-digest-gate test \
  "$EFFECTIVE_PROVIDER_SHA256" = "$APPROVED_PROVIDER_SHA256"
run_capture pre-apply-backend-cloud-gate verify_no_backend
run_qual_capture pre-apply-plan-show "$TF_BIN" -chdir="$ENV_DIR" show -json "$FIRST_PLAN"

run_capture pre-apply-plan-gate jq -e \
  --arg expected_address "$APPROVED_IMPORT_ADDRESS" \
  --arg expected_import_id "$NAMESPACE/$CONFIGMAP" \
  --arg expected_provider "registry.terraform.io/hashicorp/kubernetes" \
  --arg expected_type "$RESOURCE" '
  type == "object" and
  .applyable == true and
  type == "object" and
  .complete == true and
  .errored == false and
  (.resource_changes | type) == "array" and
  ((has("resource_drift") | not) or (.resource_drift | type) == "array") and
  ((has("output_changes") | not) or (.output_changes | type) == "object") and
  ((has("action_invocations") | not) or (.action_invocations | type) == "array") and
  ((has("deferred_action_invocations") | not) or (.deferred_action_invocations | type) == "array") and
  ((has("deferred_changes") | not) or (.deferred_changes | type) == "array") and
  ((has("checks") | not) or (.checks | type) == "array") and
  ((has("diagnostics") | not) or (.diagnostics | type) == "array") and
  ((has("errors") | not) or (.errors | type) == "array") and
  (.resource_changes | length) == 1 and
  all(.resource_changes[];
    .address == $expected_address and
    .mode == "managed" and
    .type == $expected_type and
    .provider_name == $expected_provider and
    .change.importing.id == $expected_import_id and
    .change.actions == ["no-op"]
  ) and
  ((.resource_drift // []) | length) == 0 and
  ((.output_changes // {}) | length) == 0 and
  ((.action_invocations // []) | length) == 0 and
  ((.deferred_action_invocations // []) | length) == 0 and
  ((.deferred_changes // []) | length) == 0 and
  ((.checks // []) | length) == 0 and
  ((.diagnostics // []) | length) == 0 and
  ((.errors // []) | length) == 0
' "$LAB_ROOT/evidence/pre-apply-plan-show.stdout"

prove_read_only_rbac pre-apply

run_qual_capture apply-exact "$IW" apply \
  --tenant "$TENANT" \
  "${COMMON[@]}" \
  --allow-non-main \
  --terraform "$TF_BIN"

run_qual_capture configmap-after "$KUBECTL_BIN" --namespace "$NAMESPACE" \
  get configmap "$CONFIGMAP" -o json
cp "$LAB_ROOT/evidence/configmap-after.stdout" \
  "$LAB_ROOT/evidence/configmap-after.json"
run_capture configmap-after-identity jq -r \
  '[.metadata.uid, .metadata.resourceVersion, .metadata.namespace, .metadata.name] | @tsv' \
  "$LAB_ROOT/evidence/configmap-after.json"
cp "$LAB_ROOT/evidence/configmap-after-identity.stdout" \
  "$LAB_ROOT/evidence/configmap-after.identity.tsv"

run_capture configmap-identity-compare cmp -s \
  "$LAB_ROOT/evidence/configmap-before.identity.tsv" \
  "$LAB_ROOT/evidence/configmap-after.identity.tsv"
run_capture configmap-bytes-compare cmp -s \
  "$LAB_ROOT/evidence/configmap-before.json" \
  "$LAB_ROOT/evidence/configmap-after.json"

run_qual_capture plan-second "$IW" plan \
  --tenant "$TENANT" \
  "${COMMON[@]}" \
  --save \
  --terraform "$TF_BIN"

SECOND_PLAN="$ENV_DIR/tfplan"
run_capture second-plan-file-gate test -f "$SECOND_PLAN"
run_qual_capture plan-second-show "$TF_BIN" -chdir="$ENV_DIR" show -json "$SECOND_PLAN"
cp "$LAB_ROOT/evidence/plan-second-show.stdout" \
  "$LAB_ROOT/evidence/plan-second.json"
run_capture plan-second-digest shasum -a 256 "$SECOND_PLAN"
cp "$LAB_ROOT/evidence/plan-second-digest.stdout" \
  "$LAB_ROOT/evidence/plan-second.sha256"

run_capture plan-second-classification jq --arg expected_address "$APPROVED_IMPORT_ADDRESS" '{
  applyable: .applyable,
  complete: .complete,
  errored: .errored,
  resources: [.resource_changes[]? | {
    actions: .change.actions,
    address: .address,
    importing: .change.importing,
    mode: .mode,
    provider_name: .provider_name,
    type: .type
  }],
  resource_drift: ((.resource_drift // []) | length),
  output_changes: ((.output_changes // {}) | length),
  action_invocations: ((.action_invocations // []) | length),
  deferred_action_invocations: ((.deferred_action_invocations // []) | length),
  deferred_changes: ((.deferred_changes // []) | length),
  checks: ((.checks // []) | length),
  diagnostics: ((.diagnostics // []) | length),
  errors: ((.errors // []) | length),
  expected_address: $expected_address
}' "$LAB_ROOT/evidence/plan-second.json"
cp "$LAB_ROOT/evidence/plan-second-classification.stdout" \
  "$LAB_ROOT/evidence/plan-second.classification.json"

run_capture plan-second-gate jq -e \
  --arg expected_address "$APPROVED_IMPORT_ADDRESS" \
  --arg expected_provider "registry.terraform.io/hashicorp/kubernetes" \
  --arg expected_type "$RESOURCE" '
  .complete == true and
  .errored == false and
  (.resource_changes | type) == "array" and
  ((has("resource_drift") | not) or (.resource_drift | type) == "array") and
  ((has("output_changes") | not) or (.output_changes | type) == "object") and
  ((has("action_invocations") | not) or (.action_invocations | type) == "array") and
  ((has("deferred_action_invocations") | not) or (.deferred_action_invocations | type) == "array") and
  ((has("deferred_changes") | not) or (.deferred_changes | type) == "array") and
  ((has("checks") | not) or (.checks | type) == "array") and
  ((has("diagnostics") | not) or (.diagnostics | type) == "array") and
  ((has("errors") | not) or (.errors | type) == "array") and
  (.resource_changes | length) == 1 and
  all(.resource_changes[];
    .address == $expected_address and
    .mode == "managed" and
    .type == $expected_type and
    .provider_name == $expected_provider and
    .change.importing == null and
    .change.actions == ["no-op"]
  ) and
  ((.resource_drift // []) | length) == 0 and
  ((.output_changes // {}) | length) == 0 and
  ((.action_invocations // []) | length) == 0 and
  ((.deferred_action_invocations // []) | length) == 0 and
  ((.deferred_changes // []) | length) == 0 and
  ((.checks // []) | length) == 0 and
  ((.diagnostics // []) | length) == 0 and
  ((.errors // []) | length) == 0
' "$LAB_ROOT/evidence/plan-second.json"

QUALIFICATION_PASSED=1
```

The `cmp` checks deliberately include UID, resourceVersion, and every byte of
the full `kubectl get -o json` document. Any difference is a failed
qualification, even if Terraform exits zero.

## 8. PASS and abort criteria

PASS requires all of the following:

1. The candidate commit and binaries match the approved record.
2. The private job root and `TMPDIR` are owned by the executor and mode 0700;
   the qualification runs under the recorded `env -i` allowlist and both
   `KEEP_ORACLE` selectors remain absent.
3. The service-account credential can get only the named ConfigMap; broad and
   other-name reads plus every named/collection mutation probe are denied.
4. `adopt`, `stage-imports`, first saved `plan`, and `assert-adoptable` exit 0.
5. The first plan is applyable, complete, and strictly one managed import at
   the human-approved address, provider, type, and `namespace/name` ID, with no
   drift, outputs, actions, deferrals, checks, diagnostics, creates, updates,
   replacements, or destroys.
6. The assessment report is clean, not tolerated or blocked.
7. Exact saved-plan Apply exits 0 without either broad change/destroy allow
   flag.
8. The post-Apply UID, resourceVersion, and full ConfigMap JSON are byte-for-byte
   identical to the pre-Apply evidence.
9. The second saved plan exits 0, contains exactly the same managed address as
   a no-op, and has no import, drift, output, action, deferral, check, or
   diagnostic effects.
10. The provider lock entry and actual installed provider path/digest/build
    metadata match the approved direct-install record.
11. Both plan transcripts, every safety-gate and cleanup exit, both plan
    classifications, executable digests, tool versions, and before/after
    evidence are captured under the private evidence directory.

Abort immediately, before any further qualification or Apply command, on the
conditions below. The installed EXIT handler is the sole exception: it always
attempts credential revocation and namespace deletion, records every cleanup
exit, and preserves evidence when qualification or cleanup fails.

- any non-import resource action in the first plan;
- an incomplete, errored, deferred, non-applyable, tolerated, or blocked
  assessment;
- any RBAC denial on the required read;
- any broad/other-name read authorization or any named/collection ConfigMap
  create/update/patch/delete authorization;
- any command exit not explicitly expected to be zero;
- any backend/cloud declaration or backend argument;
- any UID, resourceVersion, identity, or full-content difference;
- any second-plan address drift, import, action other than no-op, or non-resource
  effect;
- any provider lock, installed-binary provenance, plan digest, candidate digest,
  commit, cluster-server, or cluster-CA mismatch;
- any request to use admin credentials for Infrawright or Terraform;
- token expiry, context uncertainty, unexpected namespace contents, or any
  evidence that the resolved names differ from the approved names.

A failed command is evidence to retain for review; do not “fix forward” and
rerun Apply in place.

## 9. Teardown

Teardown is also live mutation and was included in the first explicit approval.
The EXIT handler already guarantees that a failed `unstage-imports` or any
earlier error cannot prevent remote cleanup. On the normal path, unstage local
imports, invoke that same remote cleanup explicitly, then scrub credentials,
state, provider cache, pack, and overlay while retaining the evidence directory
for review. Never commit the raw evidence directory.

```bash
run_qual_capture unstage-imports "$IW" unstage-imports \
  --tenant "$TENANT" \
  "${COMMON[@]}"

if cleanup_remote; then
  REMOTE_SETUP_ARMED=0
else
  printf '%s\n' 'ABORT CLEANUP: remote cleanup was not proved' >&2
  exit 1
fi

verify_lab_root_for_scrub() {
  case "$LAB_ROOT" in
    "$ORIGINAL_TMPDIR"/infrawright-k8s-qual.*) ;;
    *) return 1 ;;
  esac
  test -O "$LAB_ROOT" || return 1
  test "$(private_mode "$LAB_ROOT")" = 700 || return 1
  grep -Fx 'infrawright-k8s-qualification' \
    "$LAB_ROOT/.infrawright-k8s-qualification" >/dev/null || return 1
  grep -Fx "run_id=$RUN_ID" "$LAB_ROOT/.infrawright-k8s-qualification" >/dev/null || return 1
}
run_capture local-scrub-guard verify_lab_root_for_scrub
run_capture local-sensitive-scrub rm -rf -- \
  "$LAB_ROOT/bin" \
  "$LAB_ROOT/home" \
  "$LAB_ROOT/overlay" \
  "$LAB_ROOT/packs" \
  "$LAB_ROOT/packsets" \
  "$LAB_ROOT/plugin-cache" \
  "$LAB_ROOT/pulls" \
  "$LAB_ROOT/tmp" \
  "$LAB_ROOT/deployment.json" \
  "$LAB_ROOT/namespace.yaml" \
  "$LAB_ROOT/package.json" \
  "$LAB_ROOT/read-only.kubeconfig" \
  "$LAB_ROOT/rbac.yaml" \
  "$LAB_ROOT/service-account.token" \
  "$LAB_ROOT/terraform.rc"
```

Namespace deletion removes the ConfigMap, Role, and RoleBinding. The explicit
service-account deletion proves the short-lived identity is removed before the
namespace finishes terminating. The local scrub removes state, saved plans,
generated overlay, provider cache, read-only kubeconfig, raw pull, and candidate
binary; only the qualification marker and evidence remain. After the evidence
has been reviewed and sanitized conclusions copied out, rerun
`verify_lab_root_for_scrub` and remove that exact `LAB_ROOT`. If namespace or
local cleanup cannot be verified, report cleanup as blocked rather than
claiming PASS.
