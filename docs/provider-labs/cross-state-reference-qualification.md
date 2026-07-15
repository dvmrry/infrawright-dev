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
| `zpa_server_group` | `zpa_application_segment.server_groups[0].id` |
| `zpa_app_connector_group` | `zpa_server_group.app_connector_groups[0].id` |
| `zpa_application_server` | `zpa_server_group.servers[0].id` |

ZIA predefined category tokens are intentionally absent from the custom
category lookup and must remain literal. A mixed rule should bind its managed
custom category and retain its predefined values byte-for-byte.

Exact canonical numeric indexes into ordered lists are supported binding
targets, for example `server_groups[0].id`. This does not authorize inference
of nested references from provider field names. Qualify a nested mapping only
when the candidate pack declares it and retained API/provider-schema evidence
proves the referent, referrer field, cardinality, and stable list position.

Do not use ZIA URL-filtering `cbi_profile[0].id` as the nested qualification
case while pinned to `zscaler/zia` 4.7.26. `action = "ISOLATE"` is classified
as version-scoped unsupported before identity derivation or the import Oracle,
and the provider import Read does not reconstruct `cbi_profile`. Root bindings
run after Adopt and cannot repair that provider limitation.

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

Before enabling the mode, run the same selected Transform or Adopt cohort with
both binding options absent. Confirm that it produces neither generated
expression files nor reference-derived lookup sidecars. This disabled control
is the legacy-artifact baseline; pre-existing explicit `lookup_sources`, such
as `zpa_segment_group`, remain present.

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
`infrawright_reference_ids` output. Confirm the enabled referent run wrote its
reference-derived lookup sidecar, then disable the mode and rerun that referent
in the disposable workspace to prove the sidecar and generated bindings are
removed. Do not print output values.

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
- no unexpected object or sensitivity changes; the referent's fully known,
  sensitive `infrawright_reference_ids` create/update/no-op is expected only when it
  exactly matches provider IDs reconstructed from planned module instances;
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

Do not use `zia_url_filtering_rules` as the first cross-state referrer when a
fresh Fetch contains any version-scoped unsupported `ISOLATE` rule. Provider
4.7.26 cannot reconstruct those rules' `cbi_profile`, so the current
all-or-nothing preflight correctly publishes no artifact for that resource.
Use the ZPA ordered-list cohort below to qualify indexed paths without changing
that adoption policy.

## Reported Live Scalar Qualification

A downstream disposable-workspace rerun at PR #225 commit `732a3be` completed
the scalar pair `zpa_segment_group -> zpa_application_segment` through the
sanctioned engine path across two local state files:

- referent Adopt and exact engine Apply: 15 imports, zero add/change/destroy;
- referrer plan: 28 imports, zero add/change/destroy;
- referrer `assert-adoptable`: passed;
- Terraform resolved `segment_group_id` from the referent's sensitive
  stable-key output at plan time; no managed ID was baked into generated HCL;
- no manual Terraform bypass and no tenant mutation occurred.

The prior run at `74d07ef` exposed the assessor's rejection of the expected
output create. The updated live run verifies that fix, while repository tests
cover initial, second-run, wrong/missing, and empty referent plans for both
assessment and exact Apply. The scalar cross-state qualification is closed for
this commit. Any later head requires a delta review and targeted rerun.

## Ordered-list ZPA Qualification

The first source-backed ZPA nested cohort is declared in `packs/zpa/pack.json`:

- `zpa_application_segment.server_groups.id -> zpa_server_group`;
- `zpa_server_group.app_connector_groups.id -> zpa_app_connector_group`;
- `zpa_server_group.servers.id -> zpa_application_server`.

The committed provider schema defines each outer block as an ordered list and
the `id` leaf as a required set of strings. The retained application-segment
and server-group raw/projected fixtures demonstrate the one-block shape that
becomes `server_groups[0].id`, `app_connector_groups[0].id`, and
`servers[0].id`. These declarations do not imply coverage of other ZPA nested
fields.

From a fresh workspace:

1. Record the exact pack entry and the API/provider-schema evidence establishing
   the referent type, target `id` leaf, ordered-list nesting, and `[0]`
   cardinality used by the fixture.
2. Fetch and Adopt the referent and referrer. Confirm the compiled sidecar uses
   a canonical target such as `server_groups[0].id`; an unindexed
   `server_groups.id` target must fail rather than silently render nothing.
3. Generate both singleton roots with `cross_state_references: true`. Confirm
   the producer exports only its minimal ID map and the referrer renders a
   remote-state expression at the indexed leaf. Do not print IDs or state.
4. Run referent-first stage, saved plan, and assessment. After separately
   authorized import-only Apply of the referent, run the referrer plan and
   assessment. Require zero create/update/replace/destroy actions apart from
   expected imports.
5. Repeat from a fresh workspace and require both plans to be no-op. A missing
   or out-of-range element, list reorder, ambiguous referent, set-backed field,
   or artifact difference fails qualification.

Also retain negative fixture coverage for wildcard, negative, quoted,
leading-zero, out-of-range, and list-without-index targets. Run the same schema
validation with both JSON and native-HCL tfvars. Record explicitly that this
ZPA qualification supplies no evidence for ZIA 4.7.26 `ISOLATE`/
`cbi_profile` support.

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
| Ordered-list ZPA target and pack evidence | |
| Indexed path failed closed for invalid/missing selectors | |
| ZIA 4.7.26 ISOLATE/cbi claim | must be `none` |
| Python invoked | must be `no` |
| Deployment Apply performed | `no`, unless separately authorized and identified |

Any missing state/output, unresolved managed ID, cycle, non-import action, or
artifact difference is a failed qualification. Do not work around it by
reintroducing literals or combining states without an explicit design decision.
