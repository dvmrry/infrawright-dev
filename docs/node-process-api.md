# Node Process API Migration

Infrawright is migrating its Zscaler runtime from Python to a typed Node 24
library behind one machine-only process host. Pipelines and supervised agents
are the audience. This is not a human command-line interface and it is not an
HTTP service.

The first slices port the read-only root-topology, changed-path scoping,
materialized plan-root enumeration, exact-catalog Zscaler saved-plan
assessment, and the strict ZCC bootstrap compile, compare, and retry-forward
materialization operations. They establish the process protocol, deterministic
JSON boundary, packaging, and differential validation pattern that later
adoption operations will follow.

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

Schema-error details are bounded diagnostics, not an exhaustive list. Request
validation stops after a bounded failure set and all schema-to-error conversion
is capped, so a malformed request below the 1 MiB input limit cannot amplify
into an oversized response or lose its request identity and exit-`2` contract.

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

### Zscaler saved-plan assessment

Launch the host with the trusted Terraform binary configured out of band:

```sh
INFRAWRIGHT_TERRAFORM_EXECUTABLE=/opt/terraform/terraform \
  node dist/infrawright.mjs < request.json > response.json
```

The request cannot nominate an executable and the host never searches `PATH`:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "adoption-126",
  "operation": "assess_saved_plans",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "mode": "assert-adoptable",
    "tenant": "prod",
    "selectors": [
      "zpa/application_segment"
    ],
    "backend_config": "backend.hcl",
    "policy": "drift-policy.json"
  }
}
```

Terraform, the workspace, the restored plan pair, and any provider/plugin cache
are executable or assessment-authoritative inputs and must come from the trusted
planning lane, not PR-controlled request data.

`backend_config` and `policy` may be absolute or workspace-relative data paths.
`assert-clean` requires `policy: null`. The operation accepts only the exact
embedded all-Zscaler catalog. Its source identity covers the current zcc, zia,
zpa, and ztc manifests/registries, and CI proves those packs have no
provider-config, absent/default, or dynamic-schema guidance rules. Any catalog
change or newly declared guidance fails closed until the public profile is
reviewed again.

The response result is `infrawright.saved_plan_assessment` v1. Envelope
`status: "ok"` means a schema-valid assessment was produced; the gate outcome
is `result.summary.status`, including `blocked` or `error`. Stdout is the only
report transport. A pipeline must capture stdout on every exit, validate the
response, and atomically promote its own artifact if desired.

Request shape, catalog identity, and selector validity are checked before
policy loading and report production. If a request contains both an invalid
selection and an invalid policy, the selector/domain error is therefore the
defined process result (exit `2`), not Python CLI policy-error precedence.

### ZCC bootstrap artifact compilation

The first adoption-facing operation compiles one already-fetched ZCC pull into
an immutable artifact set. It performs no provider request and writes no file:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-bootstrap-127",
  "operation": "compile_pull_artifacts",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "mode": "bootstrap",
    "tenant": "prod",
    "resource_type": "zcc_trusted_network"
  }
}
```

The source path is derived, never supplied:
`pulls/<tenant>/<resource_type>.json`. The operation accepts exactly
`zcc_device_cleanup`, `zcc_failopen_policy`, `zcc_forwarding_profile`,
`zcc_trusted_network`, and `zcc_web_privacy`, the exact embedded ZCC transform
catalog, the exact all-Zscaler root catalog, integral JSON number tokens, and
JSON tfvars deployments. The raw pull is limited to 4 MiB and its path, bytes,
deployment, and root catalog are bound and rechecked before a result is
returned.

The result is `infrawright.zcc_pull_artifact_set` v1. It embeds, but does not
materialize, exact paths and UTF-8 bytes for the tfvars file, import blocks,
and the trusted-network lookup sidecar when applicable. Every descriptor binds
its bytes with a size and SHA-256 digest; the result also records the raw-pull
and full transform-catalog digests. `status: "review_required"` means the
transform encountered unacknowledged API paths and produces exit `3`; the
candidate bytes remain evidence for review and must not be promoted.

`mode: "bootstrap"` is deliberately narrower than Python refresh behavior.
Existing imports or move artifacts are refused because the Node port does not
yet derive identity-keyed `moved {}` blocks. HCL tfvars and a forwarding
profile grouped with its trusted-network referent while generated reference
binding is enabled are also refused. Those cases remain on Python until their
versioned artifact contracts and differentials land. A pipeline must validate
the response and require `result.status == "ready"`. It may retain the result
as evidence; canonical writes belong to the materialization operation below.

### ZCC materialized artifact comparison

The comparison operation recompiles the candidate from the same bound source
and controls, then compares its digests with artifacts already materialized at
the deployment-derived paths. The request cannot supply candidate bytes,
digests, or destinations:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-parity-128",
  "operation": "compare_pull_artifacts",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "mode": "bootstrap",
    "reference": "materialized",
    "tenant": "prod",
    "resource_type": "zcc_trusted_network"
  }
}
```

`reference: "materialized"` is intentionally factual: the filesystem cannot
prove which implementation wrote the files. During migration, the pipeline
must run the authoritative Python transform from the same raw pull immediately
before comparison. The result is
`infrawright.zcc_pull_artifact_parity` v1. It contains only paths, sizes, and
SHA-256 digests—never candidate or observed artifact contents—and reports each
tfvars, imports, and applicable lookup artifact as `match`, `mismatch`, or
`missing`. Non-lookup resources report lookup as `not_applicable`.

`status: "ready"` requires both a ready compiler candidate and exact byte
parity. A valid mismatch, missing file, or review-required candidate returns a
report with `status: "review_required"` and exit `3`. Source, deployment,
catalog, and every present or absent artifact are rebound before the report is
returned; concurrent appearance, disappearance, replacement, or byte mutation
is an I/O failure rather than a stale comparison result.

Comparison is the same narrow JSON/bootstrap profile as compilation. It
refuses move files, HCL alternates, generated-expression artifacts, an
inapplicable stale lookup, HCL deployments, and same-root generated reference
bindings. Materialized artifacts must be regular non-symlink files and their
aggregate bytes are bounded. The operation performs no write or deletion.

### ZCC retry-forward artifact materialization

The first write operation accepts the exact standalone, ready
`infrawright.zcc_pull_artifact_parity` v1 result from a successful comparison
as `input.assertion`. The remaining input is fixed:

```json
{
  "mode": "bootstrap",
  "publication": "create_or_verify_exact",
  "tenant": "prod",
  "resource_type": "zcc_trusted_network",
  "assertion": {
    "kind": "infrawright.zcc_pull_artifact_parity",
    "schema_version": 1
  }
}
```

The abbreviated assertion above must be replaced by the complete comparison
result; it must never be hand-built from individual paths or digests. The
operation recompiles from the current bound pull, deployment, and catalog,
constructs fresh clean parity evidence, and requires exact equality with the
assertion before creating directories or files. A non-ready assertion is not a
valid request. The assertion is unsigned and undated: protected pipeline
storage is what establishes that it came from the intended Python comparison
lane.

Write authority is never granted by request or deployment data alone. Launch
the host with `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` set to an absolute,
existing, canonical, non-symlink directory other than `/`. Every final and
unsupported artifact path must resolve beneath that trusted root. Deployments
using an external overlay set this variable to the approved physical overlay
root. Relative artifact descriptors remain relative to the compiler-bound
canonical workspace; the output root is a containment authority, not a path
rebase. For example, an `artifacts` deployment overlay is resolved once as
`<workspace>/artifacts`, not as `<output-root>/artifacts`.
Missing or malformed output-root configuration is a host I/O failure and exits
`1`; it is never treated as request data.

Publication is create-or-verify-exact and retry-forward. A target may be absent
or an exact regular non-symlink copy of the candidate. Existing exact files are
reused only when they form a valid publication prefix; mismatches, special
files, symlinks, moves, HCL alternates, generated bindings, and stale lookup
files fail closed before staging. Missing files are staged and synced in their
final parents, then made visible with a no-overwrite hard link in the order
imports, applicable lookup, and tfvars. Tfvars is last so a successful config
leaf is the final publication step. File and directory creation modes match the
Python writer (`0666` and `0777`, subject to umask), and aggregate candidate
content is limited to 32 MiB. Each authority-relative target is limited to 64
components and 4,096 UTF-8 bytes, with each component limited to 255 UTF-8
bytes, before filesystem traversal begins.

This is not an atomic artifact-set transaction. Each file becomes visible
atomically, but config and imports live in different directories. A crash or an
error after the first publication may leave an exact prefix; the operation
reports an indeterminate I/O failure and does not risk path-based rollback. A
serialized retry against unchanged inputs verifies that prefix and completes
the missing suffix. Pipelines must not run Terraform, Python writers, or a
second materializer concurrently, and must consume the set only after exit
`0`. Portable Node APIs do not provide descriptor-relative publication, so
parent identity rechecks are race detection in a trusted runner—not a security
boundary against a hostile same-UID process replacing ancestor paths.

A handled failure before the first link removes every staging alias whose
identity can be safely established, but may leave empty operation-created
directories. If an alias cannot be rebound for safe cleanup, the operation
returns `MATERIALIZATION_CLEANUP_FAILED` and may leave that alias rather than
risk deleting a replacement. Abrupt process or host termination can likewise
leave random `.infrawright-*.tmp` aliases containing complete staged bytes.
Those names are never consumed as final artifacts and a later run uses new
exclusive names; the trusted runner should remove abandoned aliases only after
it has established that no materializer is active.

Success returns `infrawright.zcc_pull_artifact_materialization` v1 with sorted
`created` and `reused` artifact names plus fresh digest-only verification. It
contains no artifact bytes and adds no output-authority or staging-path fields.
The embedded verification retains its deployment-derived logical artifact
paths, which may be absolute when the deployment itself uses an absolute
overlay.

`context.workspace` must be absolute. The other context paths may be absolute
or workspace-relative. The process never consults
`INFRAWRIGHT_DEPLOYMENT`, `INFRAWRIGHT_PACKS`, or its current directory.

A successful response wraps the operation's existing v1 result document in
`infrawright.process_response` v1. A grouped roots selection also carries a
structured `WHOLE_ROOT_SELECTION` diagnostic; `scope_paths` has no diagnostics.
Consumers join responses to invocations with `request_id`; no timestamps,
hostnames, durations, or other nondeterministic fields are emitted.

Exit status is:

- `0`: successful read operation, ready bootstrap artifacts, exact materialized
  parity, complete retry-forward publication, or a clean/tolerated assessment;
- `3`: schema-valid review-required bootstrap artifacts, a materialized parity
  difference, or a blocked assessment;
- `2`: malformed request, deployment, catalog, or domain selection;
- `1`: a schema-valid assessment error, indeterminate publication, or another
  I/O/internal host failure.

The strict contracts are published in
`docs/schemas/process-request.schema.json` and
`docs/schemas/process-response.schema.json`. The standalone comparison result
is `docs/schemas/zcc-pull-artifact-parity.schema.json`; the content-free write
receipt is `docs/schemas/zcc-pull-artifact-materialization.schema.json`.

## Transition Catalogs

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

The raw-pull migration uses a second, narrower authoring-time boundary for the
five fetch-backed ZCC resources:

```sh
python3 -m engine.transform_catalog \
  --product zcc \
  --out catalogs/zcc-transform-catalog.v1.json
```

That catalog binds the validated provider projection, reachable transform
overrides, strict import-ID segments, lookup/reference metadata, and complete
Python `html.unescape` compatibility tables. The pure Node transform kernel
accepts only the embedded catalog's exact semantics; it does not rediscover
schemas or overrides. The public compiler adds a bound filesystem adapter and
versioned artifact-set result, but still performs no materialization, provider
execution, HTTP, or credential handling. Raw items pass through the lossless
pull parser; native JavaScript numbers are rejected because they cannot
distinguish JSON `1` from `1.0`, and this checkpoint accepts integral numeric
tokens only.

Catalog regeneration structurally gates changes to the declarative provider
projection, reachable overrides, and serialized compatibility tables: any such
change produces reviewed catalog bytes. It does not prove universal parity
between the imperative Python and TypeScript helpers. That parity is bounded by
the committed differential corpus until downstream dual-running is byte-clean.

`engine.transform_catalog` serializes the `html.unescape` tables supplied by
the Python interpreter that runs the generator. Node consumes those committed
bytes instead of consulting its own HTML or Unicode tables. If regeneration
under a different Python standard library produces different tables, the
catalog diff is a reviewed contract change; it is never accepted silently.

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

These slices support Zscaler root topology, changed-path scoping, materialized
plan-root enumeration, exact-catalog saved-plan assessment, and immutable ZCC
bootstrap artifact compilation, materialized-byte comparison, and asserted
retry-forward publication as public process operations.

The ZCC compiler ports raw-item projection and exact tfvars/import/lookup byte
rendering for `zcc_device_cleanup`, `zcc_failopen_policy`,
`zcc_forwarding_profile`, `zcc_trusted_network`, and `zcc_web_privacy`. Python
remains the independent differential oracle. Node returns an immutable
candidate set, proves digest-only parity against materialized Python output,
and may publish only this exact five-resource JSON/bootstrap profile after a
complete ready parity assertion is supplied from the protected comparison
lane. The repository differential runs the actual Python writer, public Node
comparer, and public Node materializer for all five supported ZCC resources and
checks their resulting bytes exactly.

The bundled Zscaler assessment operation uses the internal saved-plan
assessment kernel, which provides:

- strict v1 drift-policy validation for every policy lane, plus matching and
  stale-entry tracking for `plan_tolerate`;
- lossless Terraform-number comparison with Python-compatible JSON equality;
- Python-compatible finding order, path order, import, replacement, update,
  unknown-value, and partial-tolerance behavior for valid plan documents; and
- a fail-closed entry point that accepts only supported Terraform JSON format
  `1.x`, complete and non-errored plans, structurally valid change records,
  known action sequences, valid import markers, no non-no-op output changes,
  no action invocations, and no failed checks before classification.

The valid-plan comparison intentionally hardens four Python edge cases:
drift-policy versions must be the JSON number `1` rather than `true`, policy
indexes use ASCII digits, create actions remain blocked even when an import
marker is present, and identity or sensitivity metadata changes produce
un-tolerable synthetic finding paths. Genuine Terraform 1.8+ imports are
no-op changes with a nested import marker and remain clean.

The unchecked compatibility kernel is private. There is deliberately no
process operation that accepts caller-supplied plan JSON: public assessment
must remain bound to a saved plan, source fingerprint, and policy. The
validator is intentionally stricter than the Python helper on malformed shapes
that Python can accidentally treat as an empty or clean plan.

The evidence boundary reproduces fingerprint v2 byte-for-byte, including
the generated-root HCL scanner, local-module tree ordering, root inputs,
var-file basenames, and backend/key payload. A saved plan is accepted only with
an exact `{version:2,sha256}` `tfplan.sources` file that matches current inputs.
The plan is copied into a mode-0700 private directory as a random
mode-0600 snapshot; the original, snapshot, fingerprint file, and recomputed
source fingerprint are bound and rechecked before and after assessment.
Cleanup scrubs snapshot bytes through a descriptor whose device/inode identity
was bound at capture time; it never path-unlinks a caller-influenceable file.
The operation-owned temporary directory then removes the empty artifact.

This binding detects staleness and in-process filesystem races; it is not an
authenticity proof against a principal that can replace plan artifacts. The v2
sidecar fingerprints plan inputs and does not attest the plan bytes or planner
identity. Pipelines must create `tfplan` and `tfplan.sources` in one trusted
planning step, store and restore them as an inseparable artifact through
authenticated CI storage, and assess them where untrusted changes cannot
substitute either file. A PR-controlled plan/sidecar pair is not trusted
evidence. Cryptographic planner attestation is a separate future contract, not
something an additional attacker-writable digest could provide.

All evidence traversals have explicit per-pass ceilings for file count,
directory count, directory entries, depth, individual bytes, and total bytes.
Reads fail when a file mutates or is replaced, and plan/sources diagnostics do
not include paths or content. The Node port intentionally hardens Python's
unbounded and best-effort filesystem behavior: undecodable UTF-8, unreadable
directories, excessive trees, special files, and mutation races fail closed.
Fingerprint traversal otherwise retains Python v2 symlink semantics so digest
bytes remain compatible. Filesystem entries whose raw names are not valid
UTF-8 are detected through byte-mode enumeration and fail closed instead of
being silently skipped; JavaScript cannot address those POSIX byte names
losslessly. The assessment transaction creates the trusted temporary directory
and owns cleanup; callers cannot supply a snapshot path or raw plan JSON.

The internal Terraform-show adapter accepts only an absolute, non-symlinked
executable and a private regular-file snapshot. It invokes a fixed
`terraform -chdir=<root> show -json <snapshot>` argv without a shell, replaces
the child environment with fixed locale/checkpoint values (so `TF_CLI_ARGS*`
and credentials cannot alter the call), enforces hard timeout/stdout/stderr
ceilings, discards stderr, and preflights stdout before lossless parsing. The
current Zscaler cutover boundary accepts at most 8 MiB of JSON, 100,000
structural tokens, 4 MiB of string content, and a 1 MiB scalar token; the same
deadline covers child execution, decode, preflight, and parse. Child output,
filesystem paths, and plan values never enter an error
diagnostic. Final evidence rechecks remain mandatory because the adapter alone
does not claim the root or snapshot stayed unchanged around execution.

The internal transaction resolves materialized roots, binds an optional drift
policy, classifies every selected plan, performs final plan/source/policy
rechecks, and constructs the saved-plan assessment v1 document synchronously
inside that final evidence window. Later-root failures retain already assessed
roots in an error report; invalid-policy and no-plan failures produce the same
zero-root error shapes and policy-hash precedence as Python. Reports are
validated against the published structural schema and its required
`x-infrawright-report-semantics` assertion. That semantic pass derives each root
status from findings, derives exact summary counts and status from roots, and
checks unique root membership, request/guidance/stale-policy joins, and policy
evidence coherence instead of trusting redundant report fields.

The public operation parses the deployment and root catalog from the same
bounded stable bytes it binds into the transaction. A missing deployment is a
bound absent state; creation or replacement during assessment fails closed.
The transaction rechecks both controls around Terraform work and re-materializes
the selected root/path tuples at entry, after policy loading even for the
zero-root case, and as the final awaited operation before synchronous report
construction. This catches deployment format/grouping changes, catalog changes,
and plans appearing or disappearing while an assessment is running.

The transaction snapshots library inputs before its first await, accepts at
most 1,000 roots, prevents retained plan snapshots from exceeding 2 GiB, and
has a ten-minute default/one-hour hard execution ceiling. Caller-supplied read
limits may only tighten the fixed source, plan, and policy ceilings. Individual
evidence traversals receive fresh budgets so repeated final checks can reread
the same bounded tree; transaction root, retained-byte, and deadline caps bound
the aggregate operation. Report-safe retained metadata is separately capped at
100,000 findings, 250,000 concrete paths, and 8 MiB of address/path/action text.

The report omits raw plan documents, before/after leaf values, Terraform
stdout/stderr, credentials, and filesystem paths from failures. It deliberately
retains Terraform resource addresses and concrete changed-path segments for
machine joins and Python compatibility. For-each keys and map-key path segments
can therefore contain tenant-sensitive metadata; assessment reports must be
handled as protected pipeline artifacts rather than public logs.

Generic guidance collection is not yet part of the Node transaction. The
report adapter validates already joined guidance against concrete and
normalized paths, canonicalizes order, and deduplicates it, but the Python
collectors still own provider-config, absent/default, and dynamic-schema
discovery. Those collectors, Python-compatible float rendering for guidance
values, and a versioned guidance catalog must land before assessment can accept
anything beyond the exact current Zscaler catalog.

Python remains authoritative for refresh, import-oracle adoption, move and
generated-binding production, HCL artifacts, Terraform orchestration, generic
guidance collection, and raw pack catalog production. Downstream should run
the public Node assessment beside Python and run `compare_pull_artifacts`
immediately after each authoritative Python ZCC transform. Retain Python as the
cutover oracle until the deployment and raw-pull corpus reports byte-clean
parity. The Node materializer is a gated consumer of that evidence, not a way
to bypass it: use it only for the documented exact bootstrap profile, from a
serialized protected lane, and only after an exit-`0` receipt.
