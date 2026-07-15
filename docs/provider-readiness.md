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

## Surface Map Artifact

`openapi-map` also emits a stable `surface_map` section. This is the shipped
contract that pack and provider-readiness work should consume when deciding
whether a Terraform resource has known API read/list evidence.

The JSON shape is documented at
[`docs/schemas/surface-map.schema.json`](schemas/surface-map.schema.json). Each
record includes:

- `resource_type`: Terraform resource type.
- `provider`: Terraform provider source address when supplied.
- `api_surface`: matched product/client/path surface when known.
- `match_status`: one of `matched`, `ambiguous`, `missing`, `action_shaped`,
  `adapter_required`, or `unsupported_for_now`.
- `read_path` and `read_operation`: the selected OpenAPI GET path/operation
  when the evidence has one.
- `source`: `generic_crud`, `registry_fetch`, or `source_read_registry`.
- `confidence`, `ambiguity_reason`, `adapter_required`, and `evidence`.

The three sources are intentionally separate records. A resource can have a
weak or missing generic CRUD candidate while also having a matched curated
`registry_fetch` path or source-derived `source_read_registry` read path. In
that case, read the registry/source record as the stronger evidence and keep the
generic record as candidate-generation context.

`action_shaped` and `adapter_required` mean the mapper found a non-vanilla CRUD
shape and did not pretend it is ordinary collection/detail CRUD. Examples
include Zscaler Client Connector action paths such as `listByCompany`, ZTW
activation actions, parent-scoped policy paths, GraphQL-backed resources, or
client families that need a dedicated adapter.

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
`--out` is a full resource-keyed evidence registry: mapped resources include
selected operation evidence, while ambiguous and unmapped resources stay present
with `status` and `reason` so downstream coverage cannot silently drop them.
The source evidence contract is documented at
[`docs/schemas/source-operation-evidence.schema.json`](schemas/source-operation-evidence.schema.json).
Each operation can carry a `hops` chain, such as provider call ->
OpenAPI operation, and later analyzers can add SDK-operation hops for providers
where the Terraform provider calls an SDK that constructs paths internally.
The source pass also keeps non-OpenAPI evidence explicit. Direct
`client.NewRequest("GET", ...)` calls can map to OpenAPI paths through raw REST
path evidence, relationship resources can use list endpoints as
`relationship_list_read` evidence when that is the provider's read check, and
GraphQL-backed resources are reported as `graphql_source` instead of being
buried as ordinary unmapped REST misses.

Before replacing the legacy source scanner with AST-backed evidence, run the
A/B harness:

```bash
make source-evidence-eval \
  SCHEMA=tmp/provider-schema.json \
  OPENAPI=tmp/openapi.json \
  SOURCE_ROOT=tmp/terraform-provider-example \
  PROVIDER_SOURCE=registry.terraform.io/example/example \
  RESOURCE_PREFIX=example \
  OUT_DIR=reports/readiness/example-source-eval
```

The harness writes an artifact bundle:

- `source-facts.json`: AST facts generated from provider source, unless
  `SOURCE_FACTS=<facts.json>` was supplied.
- `control-report.json`: legacy text-scanner source mapping.
- `ast-report.json`: AST-backed source mapping.
- `source-facts-compare.json`: raw old-vs-AST delta.
- `source-evidence-eval.json` and `source-evidence-eval.md`: classified
  evaluation report.

Use `FAIL_ON_REGRESSION=1` in automation. The evaluator treats these as hard
regressions: `mapped -> unmapped`, mapped read-path changes, and source files
dropping to zero. New mappings, ambiguity changes, and read/list split changes
are review-required. Same mapping with a narrower source-file set is usually
acceptable and often means the AST path avoided a loose text false positive.

## SDK Path Evidence

For providers whose SDK builds request paths internally (DigitalOcean `godo`,
AWS/GCP-style SDKs, and many generated clients), name-based fuzzy scoring is
weak because the provider call (`client.Domains.Get`) shares no identifier with
the OpenAPI operation. The missing middle layer is the SDK source itself:

```text
provider call        client.Domains.Get(...)
SDK source           const domainsBasePath = "v2/domains"
                     path := fmt.Sprintf("%s/%s", domainsBasePath, name)
OpenAPI              GET /v2/domains/{domain_name}
```

`source-operation-map` accepts an optional `--sdk-root` pointing at a vendored
Go SDK source tree. When provided, the Node authoring library recovers
`(client_symbol, method, path_template)` triples from the simple, common
shapes:

- `const <name>BasePath = "v2/foo"`
- `path := <baseVar>` and `path := fmt.Sprintf("%s/%s", <baseVar>, <arg>)`
- `s.client.NewRequest(ctx, http.MethodGet, path, nil)` for method detection

Resolved GET paths win over fuzzy name scoring and emit an `sdk_path` hop
between `provider_call` and `openapi_operation`:

```json
{
  "kind": "sdk_path",
  "method": "GET",
  "path_template": "v2/domains/{domain}",
  "sdk_file": "domains.go"
}
```

Failures are reported per call, never silently dropped:

- `source.sdk_path_unresolved`: the SDK call was found in provider source but
  its path could not be resolved to a single OpenAPI operation. `reason` is one
  of `sdk_symbol_not_found`, `path_template_not_found`, `method_not_detected`,
  `openapi_path_not_found`, or `openapi_path_ambiguous`.
- `source.sdk_action_paths`: non-GET (write/action) paths extracted from SDK
  source, surfaced so action-shaped resources are visible without being
  confused with read paths.

When `--sdk-root` is omitted, behavior is unchanged: only fuzzy name scoring
runs. No downloaded provider repos or SDK source trees are committed; the
extractor runs against a local checkout supplied at runtime.

```bash
make source-operation-map \
  SCHEMA=tmp/provider-schema.json \
  OPENAPI=tmp/openapi.json \
  SOURCE_ROOT=tmp/terraform-provider-digitalocean \
  SDK_ROOT=tmp/terraform-provider-digitalocean/vendor/github.com/digitalocean/godo \
  PROVIDER_SOURCE=registry.terraform.io/digitalocean/digitalocean \
  RESOURCE_PREFIX=digitalocean \
  OUT=reports/readiness/digitalocean-read-registry.json \
  DIAGNOSTICS=reports/readiness/digitalocean-source-diagnostics.json
```

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

The generated Zscaler OpenAPI artifacts used during the Zscaler trial made the
product split clear. Those artifacts are not committed in this repository; when
re-running the trial, place or generate equivalent files under an ignored local
work directory such as `local/zscaler-openapi/`.

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
  OPENAPI=local/zscaler-openapi/zia.openapi.json \
  RESOURCE_PREFIX=zia \
  API_PREFIX=/ \
  OUT=reports/readiness/zscaler/zia-openapi-map.json

make openapi-map \
  SCHEMA=packs/zpa/schemas/provider/zpa.json \
  OPENAPI=local/zscaler-openapi/zpa.openapi.json \
  RESOURCE_PREFIX=zpa \
  API_PREFIX=/ \
  OUT=reports/readiness/zscaler/zpa-openapi-map.json

make openapi-map \
  SCHEMA=packs/zcc/schemas/provider/zcc.json \
  OPENAPI=local/zscaler-openapi/zcc.openapi.json \
  RESOURCE_PREFIX=zcc \
  API_PREFIX=/ \
  OUT=reports/readiness/zscaler/zcc-openapi-map.json

make openapi-map \
  SCHEMA=local/zscaler-ztw-provider-schema.json \
  OPENAPI=local/zscaler-openapi/zcloudconnector.openapi.json \
  RESOURCE_PREFIX=ztw \
  API_PREFIX=/ \
  OUT=reports/readiness/zscaler/ztw-openapi-map.json
```

Current local results:

| Product | OpenAPI | Generic CRUD candidates | Registry fetch paths | Read |
|---|---|---:|---:|---|
| `zia` | `zia.openapi.json` | `23/72` matched, `3` ambiguous | `53/53` | fetch-backed surface covered |
| `zpa` | `zpa.openapi.json` | `6/54` matched | `16/16` | parent-scoped/action paths undercount generically |
| `zcc` | `zcc.openapi.json` | `0/7` matched | `5/5` | action-shaped API undercounts generically |
| ZTW / Cloud and Branch Connector | `zcloudconnector.openapi.json` | `15/16` matched, `1` special | `16/16` | Covered in the trial using the ZTW Terraform provider schema; activation is action-shaped |

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
- ZTW / Cloud and Branch Connector used the Terraform provider schema available
  during the trial, while the generated OpenAPI artifact was named
  `zcloudconnector.openapi.json`. The activation status resource was
  intentionally reported as a special action resource rather than CRUD.

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

## Provider Probe Recipes

Provider readiness needs version-locked evidence, but the repository should not
store rendered schemas, provider source trees, SDK source trees, and OpenAPI
artifacts for every provider version. Instead, store small provider recipes
under `docs/recipes/providers/` and render the evidence bundle on the consumer
or CI side for the requested provider version with `make provider-probe`.

The committed recipe should describe how to resolve inputs:

- Terraform provider address and version/ref formula.
- Provider source location and ref template.
- Terraform schema strategy, usually `terraform providers schema -json`.
- SDK strategy, usually the provider source's pinned `go.mod` module versions.
- Published API contract location and ref/path strategy.
- Source evidence adapters and contract resolvers to try.
- Coverage thresholds and whether ambiguity is allowed.

The rendered local bundle should contain the resolved, hash-locked facts for one
provider/version tuple:

- Terraform provider version and source commit.
- Terraform schema hash.
- SDK module versions and sums from that provider version.
- OpenAPI/spec resolved ref and hash.
- Source evidence registry and OpenAPI map report hashes.

Nix flakes may be useful as an optional development harness for reproducible
tooling, but they should not be required as the product interface. The durable
contract is the recipe plus rendered probe artifacts; flakes, dev shells, or CI
jobs can be different ways to produce and verify that contract.
