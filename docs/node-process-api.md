# Node Process API Migration

Infrawright is migrating its Zscaler runtime from Python to a typed Node 24
library behind one machine-only process host. Pipelines and supervised agents
are the audience. This is not a human command-line interface and it is not an
HTTP service.

The first slices port the read-only root-topology, changed-path scoping, and
materialized plan-root enumeration operations. They establish the process
protocol, deterministic JSON boundary, packaging, and differential validation
pattern that later adoption operations will follow.

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

The internal saved-plan classifier requires Terraform 1.8 or newer because it
fails closed unless the plan's `complete` contract field is present and true.
The migration compatibility baseline is currently Terraform 1.15.4.

The host reads exactly one request from stdin and writes exactly one response
plus a trailing newline to stdout. Expected errors are structured responses;
stderr is reserved for failures that prevent a protocol response.

Error responses never carry partial success diagnostics. If discovery would
emit a whole-root note before a later invalid tenant is found, the process
response contains only the structured error. Consumers must not depend on
Python CLI stderr emitted before a failed operation.

## Version 1 Requests

### Roots

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

### Changed-path scoping

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "delivery-124",
  "operation": "scope_paths",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "paths": [
      "config/prod/zpa_application_segment.auto.tfvars.json"
    ]
  }
}
```

The result is the existing `infrawright.changed_path_scope` v1 contract. It
maps recognized deployment, config, import, environment-root, and module paths
to affected resources and complete logical roots. Unknown paths remain in
`unmatched_paths`; downstream owns the fail, full-scope, or ignore policy.
Paths need not exist, so deleted files and VCS-supplied paths scope correctly.

### Materialized plan roots

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "delivery-125",
  "operation": "plan_roots",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "tenant": null,
    "selectors": [
      "zpa/application_segment"
    ]
  }
}
```

The result is the existing `infrawright.plan_roots` v1 contract. It enumerates
only materialized, recognized environment roots and names each root's
`tfplan`/`tfplan.sources` pair. `artifact_state` is presence-only: `complete`
means both paths are regular files (including file symlinks), not that their
contents are fresh or valid. A consumer must archive and restore the pair
together, then rerun the engine assessment before using it. Unknown directories
and stale labels are ignored; selection of one grouped member returns the whole
materialized root and a structured diagnostic.

`context.workspace` must be absolute. The other context paths may be absolute
or workspace-relative. The process never consults
`INFRAWRIGHT_DEPLOYMENT`, `INFRAWRIGHT_PACKS`, or its current directory.

A successful response wraps the operation's existing v1 result document in
`infrawright.process_response` v1. A grouped roots selection also carries a
structured `WHOLE_ROOT_SELECTION` diagnostic; `scope_paths` has no diagnostics.
Consumers join responses to invocations with `request_id`; no timestamps,
hostnames, durations, or other nondeterministic fields are emitted.

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
duplicates, explicit grouped roots, null and concrete tenants, every scoped
artifact suffix, deleted leaves, and absolute, relative, and symlink-alias
spellings for external overlays and deployment files. Plan-root coverage adds
all three artifact states, explicit and discovered tenants, grouped-root notes,
lexical overlay paths, directory/file impostors, and symlinked roots/artifacts.
Each subsequent ported operation must add its own Python-produced Zscaler
differential corpus before cutover.

Protocol/config JSON rejects duplicate keys, non-finite numbers, and integers
that JavaScript cannot represent exactly. A separate lossless parser already
preserves arbitrary provider/Terraform numeric tokens. The initial
Python-compatible renderer deliberately accepts only safe integers; float
format compatibility must be completed and adversarially tested before a
float-bearing output contract is migrated.

## Current Boundary

These slices support only Zscaler root topology, changed-path scoping, and
materialized plan-root enumeration as public process operations.

The Node library also contains the internal saved-plan classification kernel:

- strict v1 drift-policy validation for every policy lane, plus matching and
  stale-entry tracking for `plan_tolerate`;
- lossless Terraform-number comparison with Python-compatible JSON equality;
- Python-compatible finding order, path order, import, replacement, update,
  unknown-value, and partial-tolerance behavior for valid plan documents; and
- a fail-closed entry point that accepts only supported Terraform JSON format
  `1.x`, complete and non-errored plans, structurally valid change records,
  known action sequences, valid import markers, no non-no-op output changes,
  no action invocations, and no failed checks before classification.

The unchecked compatibility kernel is private. There is deliberately no
process operation that accepts caller-supplied plan JSON: until evidence
capture lands, such input would not be bound to a saved plan, source
fingerprint, or policy. The validator is intentionally stricter than the
Python helper on malformed shapes that Python can accidentally treat as an
empty or clean plan.

Python remains authoritative for all mutating adoption behavior, Terraform
orchestration, saved-plan evidence capture and reports, gate exit semantics,
and raw pack catalog production. Downstream should dual-run the public Node
operations and retain the Python result as the cutover oracle until its
deployment corpus is byte-clean.
