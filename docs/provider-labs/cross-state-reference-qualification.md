# Cross-state Reference Qualification

This runbook qualifies the opt-in singleton-state reference mode before a
production adoption. It does not authorize a deployment Apply. Keep tenant
data, state, plans, credentials, backend files, and command logs out of the
repository.

## Scope

The currently declared, directly testable reference pairs are:

| Referent (run first) | Referrer (run second) |
|---|---|
| `zia_url_categories` | `zia_url_filtering_rules` |
| `zpa_segment_group` | `zpa_application_segment` |

ZIA predefined category tokens are intentionally absent from the custom
category lookup and must remain literal. A mixed rule should bind its managed
custom category and retain its predefined values byte-for-byte.

Nested reference paths remain outside the v1 generated-binding contract. Do
not infer broader coverage from this qualification.

## Deployment And Backend

Start from a fresh, disposable checkout and set the relevant provider to
cross-state mode. Omit `strategy` and automatic groups for the resources being
qualified:

```json
{
  "roots": {
    "zia": { "cross_state_references": true },
    "zpa": { "cross_state_references": true }
  }
}
```

Existing grouped state must not be split by this runbook. A topology change
requires an explicit state-migration decision.

For `azurerm`, create a private JSON `BACKEND_CONFIG` containing non-secret
address fields only. Do not include `key` or credentials. Authentication stays
in the approved environment. The engine derives each state key as
`<tenant>/<root-label>.tfstate`.

The `terraform_remote_state` reader requires access to the complete referent
state snapshot even though generated expressions consume only the minimal ID
output. Confirm that this backend trust boundary is approved before using the
mode.

Record, without secrets or tenant data:

- repository commit and CLI SHA-256;
- Node and Terraform versions;
- deployment-file SHA-256;
- pack/profile SHA-256;
- selected pair and root labels;
- backend kind (not backend values).

## Materialize Both Sides

Set the variables for one pair:

```sh
export TENANT='<approved-tenant>'
export REFERENT='zpa_segment_group'
export REFERRER='zpa_application_segment'
export BACKEND_CONFIG='<private-absolute-path>/backend.json'
```

Fetch and Adopt must run in reference order so the referent lookup exists when
the referrer binding is compiled:

```sh
make fetch TENANT="$TENANT" RESOURCE="$REFERENT $REFERRER"
make adopt IN="pulls/$TENANT" TENANT="$TENANT" RESOURCE="$REFERENT $REFERRER"
make gen-modules RESOURCE="$REFERENT $REFERRER"
make gen-env TENANT="$TENANT" BACKEND=azurerm RESOURCE="$REFERENT $REFERRER"
```

Selecting only `$REFERRER` for `gen-env` is also supported: root generation
expands through the referent dependency closure. The explicit two-resource form
above keeps the qualification transcript obvious. Plan and Apply selection do
not widen automatically and must remain referent-first.

Confirm the topology reports two singleton roots. Confirm the referrer has a
generated expression sidecar and its root contains a
`terraform_remote_state` block. Confirm the referent root contains the minimal
`infrawright_reference_ids` output. Do not print output values.

## Referent-first State Publication

The referent state must exist before the referrer can plan. For each root, use
the exact saved-plan workflow. Stop after assessment unless import-only Apply
has separate approval.

Once a referrer plan is saved, do not mutate, replace, or re-adopt its referent
before that exact plan is applied. If referent state changes, discard and
regenerate every affected referrer plan before assessment or Apply.

```sh
make stage-imports TENANT="$TENANT" RESOURCE="$REFERENT" \
  STATE_AWARE=1 BACKEND_CONFIG="$BACKEND_CONFIG"
make plan TENANT="$TENANT" RESOURCE="$REFERENT" SAVE=1 \
  BACKEND_CONFIG="$BACKEND_CONFIG"
make assert-adoptable TENANT="$TENANT" RESOURCE="$REFERENT" \
  BACKEND_CONFIG="$BACKEND_CONFIG" REPORT='<private-path>/referent-report.json'
```

Acceptance before Apply:

- zero create/update/replace/destroy actions;
- only expected imports or no-op;
- no unexpected object or sensitivity changes;
- saved-plan fingerprint present and current.

After explicit review and authorization, apply exactly that saved plan:

```sh
make apply TENANT="$TENANT" RESOURCE="$REFERENT" \
  BACKEND_CONFIG="$BACKEND_CONFIG"
```

Then repeat stage/plan/assessment for the referrer:

```sh
make stage-imports TENANT="$TENANT" RESOURCE="$REFERRER" \
  STATE_AWARE=1 BACKEND_CONFIG="$BACKEND_CONFIG"
make plan TENANT="$TENANT" RESOURCE="$REFERRER" SAVE=1 \
  BACKEND_CONFIG="$BACKEND_CONFIG"
make assert-adoptable TENANT="$TENANT" RESOURCE="$REFERRER" \
  BACKEND_CONFIG="$BACKEND_CONFIG" REPORT='<private-path>/referrer-report.json'
```

The referrer plan must resolve the referent output without copying literal
managed IDs into generated root HCL. After separate authorization, apply the
exact assessed referrer plan.

## Fresh-workspace Convergence

Discard the workspace, start from the same approved source commit, and repeat
Fetch through assessment against the same remote state. State-aware staging
must remove already-managed imports. Both saved plans must classify as no-op
with zero create/update/replace/destroy.

For ZIA, additionally record counts (not values) for:

- custom category references that became remote-state expressions;
- predefined/system category values retained as literals;
- unresolved non-system values, which block qualification until explained.

## Report Back

Return only sanitized evidence:

| Check | Result |
|---|---|
| Exact source/CLI authority | |
| Singleton topology | |
| Referent output present | |
| Referrer data source present | |
| Managed references bound | |
| Expected system literals retained | |
| Referent assessment | |
| Referrer assessment | |
| Fresh-workspace referent plan | |
| Fresh-workspace referrer plan | |
| Python invoked | must be `no` |
| Deployment Apply performed | `no`, unless separately authorized and identified |

Any missing state/output, unresolved managed ID, cycle, non-import action, or
artifact difference is a failed qualification. Do not work around it by
reintroducing literals or combining states without an explicit design decision.
