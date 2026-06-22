# Provider Readiness

Provider onboarding starts with the published contract, but it must not assume
that one provider maps to one OpenAPI document. The first pass is a surface
router: classify Terraform resources by likely API surface, then run field-level
schema reconciliation only after the correct API surface is identified.

## Workflow

1. Dump or collect the Terraform provider schema.
2. Collect the published API contracts for the product.
3. Run `openapi-map` for each plausible provider/schema and OpenAPI pair.
4. Read the coverage diagnostics before reading individual field gaps.
5. Run `reconcile` only for resources whose OpenAPI surface is plausibly
   matched.
6. Use live apply/import evidence only for the unresolved set: provider
   semantics, product state, paid gates, undocumented fields, or vendor
   contract bugs.

The important distinction is between "resource did not map because generic CRUD
name matching is weak" and "resource did not map because this OpenAPI document
is not the API surface backing that Terraform resource." The report exposes that
distinction through two separate signals:

- `coverage`: generic resource-to-CRUD candidate matching from Terraform
  resource names and OpenAPI path shapes.
- `registry_fetch_coverage`: fetch-backed registry resources whose configured
  `fetch.path` is present as an OpenAPI GET path.

For packs that already have curated fetch paths, `registry_fetch_coverage` is
the stronger coverage signal. Generic CRUD coverage is still useful as a
candidate generator for new packs and non-fetch-backed resources, but it should
not be treated as authoritative by itself.

When a provider has no pack registry yet but is written against generated
OpenAPI clients, derive a provisional registry from provider source:

```bash
make source-operation-map \
  SCHEMA=tmp/provider-schema.json \
  OPENAPI=tmp/openapi.json \
  SOURCE_ROOT=tmp/terraform-provider-example \
  PROVIDER_SOURCE=registry.terraform.io/example/example \
  RESOURCE_PREFIX=example \
  OUT=reports/readiness/example-read-registry.json \
  DIAGNOSTICS=reports/readiness/example-source-diagnostics.json

make openapi-map \
  SCHEMA=tmp/provider-schema.json \
  OPENAPI=tmp/openapi.json \
  PROVIDER_SOURCE=registry.terraform.io/example/example \
  RESOURCE_PREFIX=example \
  REGISTRY=reports/readiness/example-read-registry.json \
  OUT=reports/readiness/example-openapi-map.json
```

This does not replace a real pack registry, but it turns "provider source calls
OpenAPI operation X" into deterministic read-path evidence. The derived JSON
uses `read` and, when discoverable, `list` paths so a detail read endpoint is
not confused with a pack `fetch.path` that enumerates live resources.
The source evidence contract is documented at
[`docs/schemas/source-operation-evidence.schema.json`](schemas/source-operation-evidence.schema.json).
Each operation can carry a `hops` chain, such as provider call ->
OpenAPI operation, and later analyzers can add SDK-operation hops for providers
where the Terraform provider calls an SDK that constructs paths internally.

## Surface Warnings

`make openapi-map` emits `coverage.warnings` when the generic CRUD candidate
map looks incomplete for the provider schema:

- `api_prefix_matches_no_paths`: the selected `API_PREFIX` did not match any
  OpenAPI paths. This often means the product base path lives in `servers[]`, so
  use `API_PREFIX=/`.
- `low_openapi_resource_coverage`: fewer than 25 percent of resources mapped to
  the OpenAPI document. Treat this as a wrong-surface or partial-surface signal
  before doing field-by-field work.
- `provider_config_suggests_multiple_surfaces`: provider config exposes
  URL/token/cloud-style knobs while the OpenAPI coverage is incomplete.
- `uncovered_resource_families`: one or more Terraform resource families had no
  mapped CRUD endpoint.

These warnings should be resolved or consciously classified before live testing.
For existing packs, also inspect `registry_fetch_coverage.summary` and
`registry_fetch_coverage.warnings`. A registry-backed miss means a fetch path the
pack actually uses was not found in the published OpenAPI GET paths.
`registry_openapi_product_mismatch` means the OpenAPI document advertises a
different known product than the resource prefix; suffix matches are suppressed
so shared path names across products do not look like real coverage.
For source-derived read registries, inspect `registry_read_coverage` instead.
`registry_read_openapi_product_mismatch` applies the same wrong-spec guard to
provider-source evidence.

## Grafana Lesson

Grafana looked like it had a published OpenAPI, but the OpenAPI we tested was
only the core Grafana stack API (`/api`). The Terraform provider contained many
other product surfaces: Cloud/GCom, OnCall, Asserts, app-platform resources,
k6, Synthetic Monitoring, ML, Fleet, and Frontend O11y.

The useful takeaway was not "OpenAPI failed." The useful takeaway was that the
tool should have said "this OpenAPI only covers a slice of this provider" before
we tried to reconcile all resources against it. Low coverage plus provider
config hints are the early signal.

The stronger Grafana finding came from source-derived read registries. Using
Terraform provider `grafana/grafana` `v4.39.0`, generated-client specs covered
these surfaces once source matches were restricted to Go identifiers and close
operation ties were treated as ambiguous:

| Surface | Resources | Generic CRUD candidates | Source-derived read paths | Read |
|---|---:|---:|---:|---|
| Classic Grafana `/api` | `37` | `10/37` | `30/37`, plus `5` ambiguous | generic matcher undercounts action/parent-shaped paths; source pass must choose between name/UID and related subresource reads |
| GCom / Grafana Cloud | `12` | `0/12` | `9/12` | generated Cloud client maps cleanly once source operations are used; misses are rotating-token wrappers and Cloud Integration |
| SLO plugin API | `1` | `1/1` | `0/1` source-derived, `1/1` manual | packaged OpenAPI lacks `operationId`; path can still be inferred from the provider client call |
| Asserts | `9` | `0/9` | `8/9`, plus `1` ambiguous | generated client/spec maps cleanly except one model-rule/model-mapping tie |
| k6 Cloud | `6` | `0/6` | `5/6` | generated client/spec maps cleanly except project allowed-load-zone behavior |

That is `52` unambiguous source/OpenAPI read mappings before live testing, plus
`6` resources that are now explicitly `ambiguous_source_operation` instead of
silently picked. The SLO resource is also statically explainable, but needs a
non-operationId adapter because its packaged OpenAPI lacks operation IDs. The
remaining provider surface is not one problem:

- `3` rotating-token resources are derived wrappers around token APIs.
- `19` App Platform resources, including SCIM config, use Kubernetes-style
  `/apis/*` resources and need an App Platform discovery/definition adapter.
- `1` Cloud Integration resource is a hand-written plugin-proxy workflow that
  also creates folders/dashboards.
- `28` resources use other clients: Cloud Provider, Connections, Fleet
  ConnectRPC/protobuf, Frontend O11y, Machine Learning, OnCall, Synthetic
  Monitoring, and Assistant.

So Grafana did not invalidate the OpenAPI approach. It showed that the first
artifact needs to be a surface/client map, then an adapter per client family:
OpenAPI operation IDs, App Platform definitions/live discovery, hand-written
REST path extraction, ConnectRPC/protobuf, or live-only evidence.

## Zscaler Trial

The generated Zscaler OpenAPI artifacts made the product split clear:

- `zia.openapi.json`: ZIA
- `zpa.openapi.json`: ZPA
- `zcc.openapi.json`: ZCC
- `zcloudconnector.openapi.json`: ZTW / Cloud and Branch Connector
  (`servers[]` base path is `/ztw/api/v1`)

Zscaler paths keep the product base in `servers[]`, so run these with
`API_PREFIX=/`.

Example commands:

```bash
make openapi-map \
  SCHEMA=packs/zia/schemas/provider/zia.json \
  OPENAPI=/tmp/zscaler-automate-blob-proof/openapi/zia.openapi.json \
  RESOURCE_PREFIX=zia \
  API_PREFIX=/ \
  OUT=/tmp/infrawright-zscaler-openapi-map/zia.json

make openapi-map \
  SCHEMA=packs/zpa/schemas/provider/zpa.json \
  OPENAPI=/tmp/zscaler-automate-blob-proof/openapi/zpa.openapi.json \
  RESOURCE_PREFIX=zpa \
  API_PREFIX=/ \
  OUT=/tmp/infrawright-zscaler-openapi-map/zpa.json

make openapi-map \
  SCHEMA=packs/zcc/schemas/provider/zcc.json \
  OPENAPI=/tmp/zscaler-automate-blob-proof/openapi/zcc.openapi.json \
  RESOURCE_PREFIX=zcc \
  API_PREFIX=/ \
  OUT=/tmp/infrawright-zscaler-openapi-map/zcc.json

make openapi-map \
  SCHEMA=packs/ztc/schemas/provider/ztc.json \
  OPENAPI=/tmp/zscaler-automate-blob-proof/openapi/zcloudconnector.openapi.json \
  RESOURCE_PREFIX=ztc \
  API_PREFIX=/ \
  OUT=/tmp/infrawright-zscaler-openapi-map/ztc.json
```

Current local results:

| Product | OpenAPI | Generic CRUD candidates | Registry fetch paths | Read |
|---|---|---:|---:|---|
| `zia` | `zia.openapi.json` | `23/72` matched, `3` ambiguous | `53/53` | fetch-backed surface covered |
| `zpa` | `zpa.openapi.json` | `6/54` matched | `16/16` | parent-scoped/action paths undercount generically |
| `zcc` | `zcc.openapi.json` | `0/7` matched | `5/5` | action-shaped API undercounts generically |
| `ztc` | `zcloudconnector.openapi.json` | `15/16` matched, `1` special | `16/16` | ZTW covered; activation is action-shaped |

The generic column should not be read as final provider quality. It is surface
triage and candidate generation:

- ZIA has enough direct CRUD-shaped matches that generic matching looks useful,
  but the registry-backed result is the more accurate headline for the current
  managed fetch surface.
- ZPA is heavily parent-scoped and uses mixed endpoint names, so more provider
  source/SDK hints are needed for generic matching; the current fetch-backed
  paths are covered by the published OpenAPI.
- ZCC's OpenAPI is action-shaped (`listByCompany`, `edit`, `delete`) rather
  than collection CRUD, so the generic CRUD matcher correctly undercounts while
  registry-backed coverage confirms the current fetch surface exists.
- ZTW is represented by the Terraform provider `zscaler/ztc`, while the
  generated OpenAPI artifact is named `zcloudconnector.openapi.json`.
  `ztc_activation_status` is intentionally reported as a special action resource
  rather than CRUD.

## What To Do Next

When a provider shows low OpenAPI coverage:

1. Confirm `API_PREFIX` and server/path layout.
2. Inspect `openapi.profile.top_collection_segments`.
3. Inspect `coverage.family_coverage` for whole resource families with zero
   coverage.
4. Decide whether the missing surface needs another OpenAPI file, a generated Go
   client/SDK surface, a provider-source adapter, or a bespoke action mapper.
5. Only then run `reconcile` for resources whose API surface is known.

This keeps the human review set small and prevents us from treating "wrong API
document" as hundreds of field-level drift questions.
