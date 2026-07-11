# Node Process API Migration

Infrawright is migrating its Zscaler runtime from Python to a typed Node 24
library behind one machine-only process host. Pipelines and supervised agents
are the audience. This is not a human command-line interface and it is not an
HTTP service.

The first slice ports the read-only root-topology operation. It establishes the
process protocol, deterministic JSON boundary, packaging, and differential
validation pattern that later adoption operations will follow.

## Runtime and Distribution

Build and test with the pinned package graph:

```sh
npm ci --ignore-scripts
npm run check
npm run build
```

The build produces `dist/infrawright.mjs`, a bundled executable module. A
downstream pipeline needs Node 24 but does not run `npm install`:

```sh
node dist/infrawright.mjs < request.json > response.json
```

The host reads exactly one request from stdin and writes exactly one response
plus a trailing newline to stdout. Expected errors are structured responses;
stderr is reserved for failures that prevent a protocol response.

## Version 1 Roots Request

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "delivery-123",
  "operation": "roots",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "tenant": "prod",
    "selectors": [
      "zpa/application_segment"
    ]
  }
}
```

`context.workspace` must be absolute. The other context paths may be absolute
or workspace-relative. The process never consults
`INFRAWRIGHT_DEPLOYMENT`, `INFRAWRIGHT_PACKS`, or its current directory.

A successful response wraps the existing `infrawright.root_topology` v1
document in `infrawright.process_response` v1. A grouped selection also carries
a structured `WHOLE_ROOT_SELECTION` diagnostic. Consumers join responses to
invocations with `request_id`; no timestamps, hostnames, durations, or other
nondeterministic fields are emitted.

Exit status is:

- `0`: successful operation;
- `2`: malformed request, deployment, catalog, or domain selection;
- `1`: I/O or internal operation failure.

The strict contracts are published in
`docs/schemas/process-request.schema.json` and
`docs/schemas/process-response.schema.json`.

## Transition Catalog

The Node operation consumes a versioned `infrawright.root_catalog` instead of
loading raw packs. This boundary is deliberate: Python currently validates
many pack and registry fields unrelated to root selection, so a partial Node
pack loader would accept invalid inputs that Python rejects.

For the Zscaler migration, the validated Python loaders produce the committed
catalog:

```sh
python3 -m engine.root_catalog \
  --providers zcc,zia,zpa,ztc \
  --out catalogs/zscaler-root-catalog.v1.json
```

CI regenerates it logically and fails if its bytes differ. Node performs no
Python call at runtime. A later migration slice will replace the producer only
after the full pack-validation contract has been ported.

## Compatibility Gate

The process envelope is intentionally new, so byte compatibility is measured
at the embedded operation result. Tests execute Python and Node over the same
Zscaler catalog and deployment fixtures, then compare:

- the complete root-topology stdout bytes;
- grouped-selection diagnostic bytes;
- parsed document equality; and
- the published JSON schema.

Coverage includes the default topology, exact and product selectors,
duplicates, explicit grouped roots, null and concrete tenants, and relative
overlay paths containing doubled separators and `..`. Each subsequent ported
operation must add its own Python-produced differential corpus before cutover.

Protocol/config JSON rejects duplicate keys, non-finite numbers, and integers
that JavaScript cannot represent exactly. A separate lossless parser already
preserves arbitrary provider/Terraform numeric tokens. The initial
Python-compatible renderer deliberately accepts only safe integers; float
format compatibility must be completed and adversarially tested before a
float-bearing output contract is migrated.

## Current Boundary

This slice supports only Zscaler root topology. Python remains authoritative
for all mutating adoption behavior, Terraform orchestration, saved-plan gates,
and raw pack catalog production. Downstream should dual-run this operation and
retain the Python result as the cutover oracle until its deployment corpus is
byte-clean.
