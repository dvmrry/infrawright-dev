# Node Process API Migration

Infrawright is migrating its Zscaler runtime from Python to a typed Node 24
library behind one machine-only process host. Pipelines and supervised agents
are the audience. This is not a human command-line interface and it is not an
HTTP service.

The first slices port the read-only root-topology, changed-path scoping,
materialized plan-root enumeration, exact-catalog Zscaler saved-plan
assessment, the strict ZCC bootstrap compile, compare, and retry-forward
materialization operations, a read-only ZCC refresh compiler, and an
assertion-bound imports-last ZCC refresh publisher. They establish
an explicit ZCC post-apply acknowledgement/retirement operation and a public,
read-only provider-observed ZCC adoption compiler and digest-only adoption
parity operation. Together these establish the
process protocol, deterministic JSON boundary, packaging, and differential
validation pattern for later migration slices.

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

## Publisher Ownership

Concurrent jobs are supported through physically disjoint, job-owned
workspaces and output roots. Every persistent Node mutation treats the complete
canonical `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` as one publisher unit; different
tenants or resources beneath that root are not independent writer lanes. The
configured root must equal the common authority derived from the resolved
config/import/lookup targets. A containing ancestor is rejected before artifact
writes with non-retryable I/O code `OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY`.
Bootstrap materialization, refresh materialization, and refresh
acknowledgement acquire `.infrawright.publisher.lock` at the output-root
boundary with exclusive no-follow creation. Contention fails immediately with
retryable I/O code `OUTPUT_ROOT_BUSY`; the host never waits or auto-breaks an
existing guard.

The host binds open handles and device/inode identities for the authority and
guard. Cleanup refuses a guard-path replacement or root rollover with
`PUBLISHER_GUARD_CLEANUP_FAILED` rather than unlinking the observed foreign
path. The final path recheck and pathname `unlink` are not an indivisible kernel
operation, so this remains trusted-runner race detection rather than a hostile
same-UID security boundary.

The guard is transient process coordination, not a generated artifact or
refresh-state marker. The exact pending-move marker remains authoritative
between refresh publication and post-apply acknowledgement. Pipeline isolation
spans the longer Python/Node/Terraform workflow, while the Node guard catches
accidental overlapping process-host mutations inside that boundary. See
[ADR 0001](adr/0001-publisher-ownership.md) for the ADO job-path convention,
stale cleanup responsibility, and supported parallelism.

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

Request and response schemas validate separate JSON documents, so they cannot
by themselves prove their cross-document mode join. A client must retain the
validated request and require `compile_pull_artifacts` mode/result agreement:
`bootstrap` maps only to `infrawright.zcc_pull_artifact_set` with result mode
`bootstrap`, while `refresh` maps only to
`infrawright.zcc_pull_refresh_artifact_set` with result mode `refresh`. Reject a
standalone-valid response whose result kind or mode does not match the request.
The same retained-request rule applies to `compile_adoption_artifacts`, which
maps only `bootstrap` to `infrawright.zcc_adoption_artifact_set` and must match
the requested tenant and resource type. `compare_adoption_artifacts` maps only
`bootstrap` plus `reference: "materialized"` to
`infrawright.zcc_adoption_artifact_parity`; mode, reference, tenant, and
resource must all agree with the retained request.

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
catalog, the exact all-Zscaler root catalog, finite lossless JSON number
tokens, and JSON tfvars deployments. The raw pull is limited to 4 MiB and its
path, bytes, deployment, and root catalog are bound and rechecked before a
result is returned.

The result is `infrawright.zcc_pull_artifact_set` v1. It embeds, but does not
materialize, exact paths and UTF-8 bytes for the tfvars file, import blocks,
and the trusted-network lookup sidecar when applicable. Every descriptor binds
its bytes with a size and SHA-256 digest; the result also records the raw-pull
and full transform-catalog digests. `status: "review_required"` means the
transform encountered unacknowledged API paths and produces exit `3`; the
candidate bytes remain evidence for review and must not be promoted.

`mode: "bootstrap"` is deliberately narrower than refresh behavior. Existing
imports, move artifacts, or in-flight pending-move markers are refused. HCL
tfvars and a forwarding profile grouped with its trusted-network referent while
generated reference binding is enabled are also refused. A pipeline must
validate the response and require `result.status == "ready"`. It may retain the
result as evidence; canonical writes belong to the materialization operation
below.

### ZCC provider-observed adoption compilation

`compile_adoption_artifacts` uses the same derived pull, deployment, root
catalog, JSON layout, no-generated-binding rule, and bootstrap absence gates as
the raw compiler, then runs the pinned provider import/read oracle. It is a
separate operation because provider observation and raw transformation are
different evidence classes:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-adoption-128",
  "operation": "compile_adoption_artifacts",
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

The request cannot select a source path, executable, credentials, provider
state, catalog, dependency lock, timeout, temporary root, or output root. The
host supplies one closed authority out of band:

- `INFRAWRIGHT_TERRAFORM_EXECUTABLE` selects the trusted canonical Terraform
  1.15.4 executable;
- `INFRAWRIGHT_ZCC_ADOPTION_TEMP_ROOT` selects an existing canonical private
  job-owned scratch parent; and
- only the fixed Zscaler credential plus proxy/certificate environment
  allowlist is copied into the child. Inherited Terraform CLI arguments and
  plugin-cache configuration are excluded.

For a nonempty pull, the operation runs the exact import-only plan and state
transaction described in Current Boundary. The pull, deployment, root catalog,
and absent imports/moves/pending marker are checked before the oracle and again
after successful oracle cleanup. An empty identity set returns complete empty
artifacts without touching Terraform or the temporary authority. No caller
artifact is written, replaced, or deleted in either case.

The result is `infrawright.zcc_adoption_artifact_set` v1 with exact
tfvars/import/lookup paths, bytes, sizes, and digests. Success means only that
this candidate was compiled from provider-observed state under the pinned
transaction. It does **not** establish Python byte parity, saved-plan
cleanliness, destroy safety, apply readiness, live-tenant qualification, or
cutover readiness. Pipelines must retain Python as the independent oracle until
those separate evidence gates pass; publication remains a separate operation.

### ZCC provider-observed adoption parity

`compare_adoption_artifacts` runs the same hardened provider-observation
transaction against the same bound pull, but compares the resulting candidate
with stable materialized bootstrap artifacts instead of returning either side's
bytes:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-adoption-parity-129",
  "operation": "compare_adoption_artifacts",
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

The externally materialized reference is expected to come from the retained
Python adoption lane. Production Node never launches Python and does not infer
which writer created the files. Before Terraform runs, it binds the canonical
tfvars and imports paths plus the trusted-network lookup path, including each
file's presence, identity, size, and SHA-256. A missing applicable reference is
a comparison result, not an I/O error. A lookup for any other resource is an
unsupported stale artifact.

This comparison policy is intentionally separate from the compile operation's
bootstrap-absence policy: existing imports are reference evidence here and
remain forbidden for `compile_adoption_artifacts`. HCL tfvars, generated
bindings, moves, pending transitions, non-applicable lookup files, unsupported
resources, and grouped same-root generated bindings still fail closed. Pull,
deployment, root catalog, reference files, and required absent states are all
rechecked after successful oracle cleanup. The operation writes, replaces, and
deletes no caller artifact.

The result is `infrawright.zcc_adoption_artifact_parity` v1. For each applicable
role it reports only candidate/reference logical paths, byte sizes, SHA-256s,
and `equal` or `different`; non-applicable lookup is explicit. It contains no
artifact contents, provider state, import IDs, credentials, child diagnostics,
or scratch paths. Exact equality returns `status: "ready"` and exit `0`.
Missing or unequal reference bytes return a complete
`status: "review_required"` report and exit `3`. I/O, domain, timeout, and
provider-contract failures remain structured errors, not parity mismatches.

Ready proves byte equality with the bound external reference only. It does not
prove plan cleanliness, apply safety, live-tenant qualification, reference
provenance, or cutover readiness, and it never materializes the candidate.

### ZCC read-only refresh compilation

Refresh mode compiles the current pull and binds an existing canonical imports
file as rename evidence. It returns a deterministic desired artifact set but
does not write, replace, or delete any file:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-refresh-128",
  "operation": "compile_pull_artifacts",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "mode": "refresh",
    "tenant": "prod",
    "resource_type": "zcc_trusted_network"
  }
}
```

The supported resources, raw-pull path, catalogs, JSON tfvars profile, source
limits, and generated-binding exclusions are the same as bootstrap. Unlike
bootstrap, refresh requires the deployment-derived
`<resource_type>_imports.tf` to be a present, regular, non-symlink file whose
complete bytes are the exact canonical Infrawright-generated import grammar.
An empty canonical imports file is valid. The existing tfvars and applicable
lookup files may be present or absent; each state is bound. Non-applicable
lookup files must be absent. Each present baseline artifact is limited to 32
MiB and each baseline read or recheck pass has a 96 MiB aggregate ceiling.

Refresh also requires the deployment-derived move file, reserved
`<resource_type>_moves.pending.json` marker, HCL tfvars alternate, and generated
expression bindings to be absent. These are transition preconditions, not
cleanup requests. An existing move file means a prior rename transition has
not been explicitly completed; the compiler refuses it rather than deleting or
silently superseding it. The deployment, root catalog, pull, all present and
absent baseline artifacts, physical file identities, and unsupported adjacent
states are rebound after compilation. Baseline appearance, disappearance,
identity replacement, or byte mutation is an I/O failure. The raw pull is
rebound by canonical path, digest, and size; an identical-byte replacement at
that path is equivalent source evidence. Deployment-derived external overlays
are supported, but the raw pull remains contained inside `context.workspace`.

The result is `infrawright.zcc_pull_refresh_artifact_set` v1. `baseline`
contains content-free path/state/digest/size evidence for prior tfvars,
imports, and lookup plus the required absent states. `desired` contains exact
tfvars, imports, applicable lookup, and safe move bytes, or an explicit absent
state for each inapplicable target. `moves.safe` records unambiguous
same-import-ID key changes; `moves.suppressed` records `ambiguous`,
`duplicate_from`, `key_swap`, and `destination_occupied` candidates without
re-emitting provider import IDs. Additions and removals are not inferred to be
renames. Bootstrap, comparison, parity, and materialization continue to require
unique desired import IDs. Refresh alone retains Python run-two output when one
prior address fans out to duplicate desired IDs: it emits both ordered
`duplicate_from` suppressions, emits no move file, and requires review. For
trusted networks, the lookup sidecar also preserves Python's deterministic
key-sorted, last-key-wins collapse for the duplicate ID; that collapsed lookup
is review evidence, not a promotion-safe uniqueness claim.

`status: "ready"` means only that the raw transform found no unacknowledged API
surface and rename derivation found no suppressed candidate. Safe moves can be
ready. This status does **not** establish pull completeness, provider-read or
adoption-oracle parity, plan cleanliness, destroy safety, apply readiness, or
that a previously staged move has been applied. Downstream must still
materialize through a future assertion-bound transition operation, plan, and run
the saved-plan gate. A suppression or unexpected drop produces a complete
review artifact with exit `3`; it must not be promoted automatically.

`baseline.fingerprint_sha256` hashes compact canonical JSON encoded as UTF-8.
The normative encoding is equivalent to Python
`json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))`:
object keys use Unicode code-point order, non-ASCII Unicode is written
unescaped, JSON control characters use their required escapes, and no optional
whitespace or trailing newline is present. Inputs must be acyclic inert JSON,
use well-formed Unicode, safe integers without negative zero, and remain within
the 128-container depth bound. The domain is
`infrawright.zcc_refresh_baseline_fingerprint` version `1`. Its `states` object
contains the seven baseline states except the fingerprint itself.
`transition_sha256` uses the same canonical encoding and the domain
`infrawright.zcc_refresh_transition` version `1`; it covers product, resource,
tenant, source, catalog, root, the complete baseline (including its
fingerprint), unexpected drops, move decisions, and content-free desired
artifact descriptors. These hashes bind baseline states, decisions, and
content-free descriptors and make transitions reproducible; complete semantic
validation separately binds each desired artifact's bytes to its size and
digest. The hashes are not signatures and do not authenticate who produced the
baseline or candidate; retain them in protected pipeline storage.

The compiler itself remains read-only and does not port import-oracle adoption,
provider reads, generated bindings, HCL tfvars, apply, or move acknowledgement.
The assertion-bound publisher described below is the only supported promotion
path. It deliberately retains a safe move file and its pending-transition
marker until a later apply-and-acknowledge contract can prove that Terraform
consumed the move. Do not manually delete either artifact to force progress.
The Python Make wrapper can skip a missing pull; the Node operation treats its
derived pull as required and returns an error.

### ZCC two-phase refresh parity

Refresh byte parity uses two physically isolated, still-materialized twins so
Node never has to infer what Python started from after Python overwrites the
artifacts. The candidate twin is read-only for the complete workflow. The
reference twin is touched only by the authoritative Python transform between
the two Node calls.

Before Python runs, request a compact seed:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-refresh-seed-129",
  "operation": "seed_pull_refresh_parity",
  "context": {
    "workspace": "/twins/candidate",
    "deployment": "deployment.json",
    "root_catalog": "catalog.json"
  },
  "input": {
    "mode": "refresh",
    "reference": "materialized_twin",
    "tenant": "prod",
    "resource_type": "zcc_trusted_network",
    "reference_context": {
      "workspace": "/twins/reference",
      "deployment": "deployment.json",
      "root_catalog": "catalog.json"
    }
  }
}
```

Both workspaces and their artifact authorities must be existing canonical,
non-root directories. Candidate physical regions may not equal, contain, be
contained by, or share directory identities with reference regions. An
authority may equal its own workspace, live beneath it, or be a physically
disjoint external overlay; an authority may not contain its own workspace.
All seven role targets must resolve beneath their own authority. Existing
cross-twin artifact hard links are refused. Deployment and root-catalog
controls must resolve inside their respective workspace. Symlink aliases for
workspaces, authorities, controls, raw pulls, or artifacts are not accepted by
this stricter twin protocol.

Both twins must be complete run-one baselines before seeding: tfvars and
imports are required for every supported ZCC resource, and the trusted-network
lookup is additionally required for `zcc_trusted_network`. The host preflights
both controls, the raw pull, every artifact target parent, and each target
parent's authority-relative ancestor chain before reading any baseline or raw
pull contents. Parent and ancestor device/inode identities are included in the
opaque binding digest and rechecked, so replacing a directory with exact-copy
artifact bytes still invalidates the transaction.

The result is `infrawright.zcc_pull_refresh_parity_seed` v1. It is limited to
256 KiB and contains only states, sizes, digests, counts, sorted difference
labels, and opaque request/binding digests. It never contains artifact bytes,
physical paths, provider import IDs, move keys, or move lists. Side-specific
path-bearing baseline and transition hashes are retained only as candidate
trace evidence; they are never compared across twins. Cross-twin comparison
uses a domain-separated, role-tagged, path-neutral digest over the full refresh
decisions plus content-free baseline and desired descriptors. Root-catalog
digests and normalized deployment semantics must also agree; each twin's raw
deployment bytes remain bound separately so distinct absolute overlay paths
do not create a false difference. `status: "ready"` requires two
ready compilers, zero suppressed moves, and no path-neutral or control
differences. A complete ready seed exits `0`; a valid review seed exits `3` and
must not be sent to the final comparison.

Only after a ready seed is retained in protected pipeline storage should the
pipeline run `python3 -m engine.transform` once against the reference twin.
Any nonzero Python exit invalidates that twin: discard or restore it from the
original baseline, create a **new** seed, and rerun. Never rerun Python in place
after a failed or partial attempt. Python overwrites imports last, and a second
run against already-advanced imports can erase rename moves. Python
suppressed-move and dropped-field exit behavior is not numerically equivalent
to Node readiness: compare bytes separately and require both a ready seed and
Python exit `0` before continuing.

After Python succeeds, embed the complete seed in refresh comparison:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "zcc-refresh-compare-130",
  "operation": "compare_pull_artifacts",
  "context": {
    "workspace": "/twins/candidate",
    "deployment": "deployment.json",
    "root_catalog": "catalog.json"
  },
  "input": {
    "mode": "refresh",
    "reference": "materialized_twin",
    "tenant": "prod",
    "resource_type": "zcc_trusted_network",
    "reference_context": {
      "workspace": "/twins/reference",
      "deployment": "deployment.json",
      "root_catalog": "catalog.json"
    },
    "seed": {
      "kind": "infrawright.zcc_pull_refresh_parity_seed",
      "schema_version": 1,
      "status": "ready"
    }
  }
}
```

The abbreviated seed must be replaced by the complete prior result. The host
validates and snapshots the inert, acyclic, depth-bounded seed before any I/O.
It joins the outer tenant, resource type, candidate context, and reference
context to the seed's two request hashes before touching either filesystem,
then preflights both sides, recompiles, and exactly rejoins the untouched
candidate transition. It binds the reference roles `tfvars`, `lookup`,
`moves`, `pending_moves`,
`alternate_hcl`, and `generated_bindings` before binding `imports` last as the
Python commit boundary. After a complete reference snapshot, its final CAS
rechecks controls, raw source, target parents/ancestors, six artifact roles,
and imports last. The untouched candidate transaction is the last awaited CAS;
the report is then constructed synchronously. Cross-twin hard-link isolation
is checked throughout. Special files, symlinks, oversized files, binding
changes, and races are I/O failures rather than parity differences.

The result is `infrawright.zcc_pull_refresh_parity` v1. It embeds the complete
ready seed and reports every role as `match`, `mismatch`, `missing`, or
`unexpected`, with derived counts, status, and an assertion digest. Expected
absence is a first-class match; a present artifact in an absent role is
`unexpected`. Exact seven-role parity exits `0`; a valid difference exits `3`;
malformed, nonready, stale, replayed, or isolation-invalid seed joins exit `2`;
I/O, race, and budget failures exit `1`.

`reference: "materialized_twin"` is deliberately not `python`: the filesystem
does not prove writer provenance. The process performs no write or deletion,
and the two filesystems do not provide an atomic joint snapshot. The trusted
pipeline must serialize Python and both Node calls. A successfully produced
final assertion may be compared again read-only while all inputs remain
unchanged; retrying Python requires a restored twin and a new seed.

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
the missing suffix. Pipelines must not run Terraform or Python writers in the
same output root concurrently and must consume the set only after exit `0`.
The process host rejects a second persistent Node mutation with
`OUTPUT_ROOT_BUSY`; disjoint output roots remain parallel-capable. Portable
Node APIs do not provide descriptor-relative publication, so parent identity
rechecks are race detection in a trusted runner—not a security boundary against
a hostile same-UID process replacing ancestor paths.

A handled failure before the first link removes every staging alias whose
identity can be safely established, but may leave empty operation-created
directories. If an alias cannot be rebound for safe cleanup, the operation
returns `MATERIALIZATION_CLEANUP_FAILED` and may leave that alias rather than
risk deleting a replacement. Abrupt process or host termination can likewise
leave random `.infrawright-*.tmp` aliases containing complete staged bytes.
Those names are never consumed as final artifacts and a later run uses new
exclusive names; the trusted runner should remove abandoned aliases only after
it has established that no materializer is active.

The root-level `.infrawright.publisher.lock` is deliberately outside generated
config/import paths and is never considered artifact residue. An existing lock
produces `OUTPUT_ROOT_BUSY` whether its owner is active or stale. Remove a stale
lock only after proving no publisher is active, or discard the complete
job-owned workspace; guard acquisition never removes a pre-existing lock. For
overlay `.`, the exact authority is the workspace. Relative and absolute
overlays use their canonical overlay directory itself, not a containing output
root.

Success returns `infrawright.zcc_pull_artifact_materialization` v1 with sorted
`created` and `reused` artifact names plus fresh digest-only verification. It
contains no artifact bytes and adds no output-authority or staging-path fields.
The embedded verification retains its deployment-derived logical artifact
paths, which may be absolute when the deployment itself uses an absolute
overlay.

### ZCC assertion-bound refresh materialization

Refresh publication uses the same process operation with a distinct, fixed
input contract:

```json
{
  "mode": "refresh",
  "publication": "replace_or_verify_exact_imports_last",
  "tenant": "prod",
  "resource_type": "zcc_trusted_network",
  "assertion": {
    "kind": "infrawright.zcc_pull_refresh_parity",
    "schema_version": 1
  }
}
```

The abbreviated assertion must be replaced by the complete, ready result from
the two-phase refresh parity workflow. The request tenant, resource, and
context hash must join the assertion before operation I/O. The operation then
recompiles the current candidate from bound inputs and requires its raw source,
catalog, root, baseline fingerprint, desired descriptors, move decision, and
transition fingerprint to join the protected assertion. It does not accept a
caller-authored artifact list or derive authority from the request.

`INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` is the same trusted host-only write
authority used by bootstrap materialization. Existing artifact targets must be
exactly the asserted baseline or desired state. The HCL alternate and generated
expression bindings must remain absent. A foreign payload, foreign pending
marker, mutated baseline-equals-desired role, non-prefix payload vector, or
reserved artifact is an ambiguous workspace and fails closed without being
repaired or overwritten.

The payload order is `lookup`, `moves`, `tfvars`, then `imports`, after removing
roles whose baseline and desired bytes are identical. Imports is the final
payload operation and final verification read. It is not by itself a commit
signal because an imports file may be identical across a valid refresh. Before
the first changed payload becomes visible, the publisher durably creates the
exact content-free `<resource>_moves.pending.json` fence with an exclusive
no-overwrite link and syncs its parent. Each changed payload is staged and
synced before visibility, then its parent is synced. Present-to-present payload
replacement uses a same-parent rename under the documented serialized trusted
runner model; absent-to-present publication uses an exclusive no-overwrite
link.

The only valid in-flight vector is a desired prefix followed by a baseline
suffix in that effective order. A crash after the marker fence or any payload
publication may therefore leave durable partial progress; an unchanged,
serialized retry verifies the exact marker and prefix before advancing only the
remaining suffix. A markerless partial prefix is ambiguous. A markerless
all-desired state is accepted only for a no-move transition; when moves are
desired it cannot prove whether the move transition was published and fails
closed. Exact hard-linked files are not rejected merely for link count two,
because a crash can occur after canonical link creation but before the random
staging alias is removed.

A no-move transition removes only the exact marker after all payload and input
rechecks, syncs the marker parent, and returns `status: "complete"`. A safe-move
transition retains both the move file and exact marker and returns
`status: "awaiting_apply"` with `next_action: "apply_moves_then_ack"`. That is a
successful publication receipt, not apply evidence and not permission to clear
either file. Move retirement remains intentionally deferred until a separate
contract binds successful Terraform apply evidence.

### Trusted ZCC post-apply acknowledgement

Safe-move publication is completed by a separate machine operation after the
trusted pipeline has successfully applied the saved Terraform plan and removed
the staged import/move files from the environment root:

```json
{
  "kind": "infrawright.process_request",
  "schema_version": 1,
  "request_id": "refresh-ack-127",
  "operation": "acknowledge_pull_refresh",
  "context": {
    "workspace": "/workspace/deployment",
    "deployment": "deployment.json",
    "root_catalog": "catalogs/zscaler-root-catalog.v1.json"
  },
  "input": {
    "mode": "refresh",
    "policy": "retire_exact_after_external_acknowledgement",
    "tenant": "prod",
    "resource_type": "zcc_forwarding_profile",
    "assertion": {
      "kind": "infrawright.zcc_pull_refresh_parity",
      "schema_version": 1
    },
    "publication": {
      "kind": "infrawright.zcc_pull_refresh_materialization",
      "schema_version": 1
    },
    "acknowledgement": {
      "kind": "trusted_pipeline_assertion",
      "statement": "terraform_apply_succeeded"
    }
  }
}
```

The abbreviated assertion and publication objects must be replaced by the
complete ready/equal parity assertion and its complete `awaiting_apply`
publication receipt. The host must independently set both
`INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` and
`INFRAWRIGHT_ALLOW_EXTERNAL_APPLY_ACK=1`. The second variable is an explicit
capability granted only to the trusted post-apply pipeline step; it is not
request-controlled.

This contract intentionally does **not** pretend to prove Terraform execution.
The acknowledgement is a trusted caller statement, and the result records
`apply_observed_by_engine: false`. An unsigned JSON receipt would not be
stronger evidence, while executing Terraform here would also require the full
credential, plan-policy, branch-policy, post-state, and crash-recovery surface
of a future Node apply executor. That executor can later invoke the same
retirement kernel after observing its own successful apply.

Before deletion, the operation validates and joins both embedded contracts,
recomputes the request/context binding, recompiles the current raw ZCC
candidate, rechecks deployment/catalog/source/parent bindings, and verifies the
exact published tfvars, imports, optional lookup, move, marker, and reserved
artifact states. It accepts only three retirement states:

- exact move plus exact marker (`awaiting_apply`);
- move absent plus exact marker after an interrupted retirement
  (`retirement_prefix`);
- both absent on an idempotent retry (`already_retired`).

Any foreign state or marker-absent/move-present state fails without deletion.
Immediately before each path-based unlink, the operation rechecks the bound
regular file's content, metadata, and device/inode identity and rechecks its
parent. Portable Node does not provide an unlink-by-open-descriptor primitive,
so the remaining check-to-unlink interval is covered by the required
serialized, single-writer runner boundary rather than claimed as an atomic
inode guarantee. That is a caller-enforced concurrency precondition, not
protection against a hostile or accidentally concurrent path replacement. The
operation retires the move first, syncs its parent, rechecks all inputs and
payloads while the exact marker remains, then verifies and retires the marker
and syncs again. A failure after either deletion returns retryable
`REFRESH_ACKNOWLEDGEMENT_INDETERMINATE`. An ordinary crash prefix finishes with
the unchanged request; a detected foreign replacement requires operator
reconciliation before retry. Final verification reads imports last.

The content-free `infrawright.zcc_pull_refresh_acknowledgement` v1 result binds
the assertion, original publication receipt digest, baseline, transition, and
seven final artifact states. It emits no paths, bytes, move keys, import IDs,
state addresses, credentials, or apply output. It retires only the canonical
publisher-owned move and marker; the pipeline remains responsible for staging,
Terraform apply, and `unstage-imports` before acknowledgement.

During this transition, Python `transform` and `adopt` refuse to operate while
the canonical pending marker exists. This prevents the legacy writer from
deleting an unapplied move on a subsequent identical pull. Staging and
unstaging remain available so the required apply sequence is not blocked.

The preceding publication result is
`infrawright.zcc_pull_refresh_materialization` v1. It contains only the ordered
roles advanced by that publication invocation, the initial/final transition
states, the next action, assertion and transition hashes, and seven
content-free final artifact states. It never emits paths, bytes, import IDs,
move keys, staging names, or the output authority. Consumers must validate the
complete response, retain `awaiting_apply` receipts with the protected
assertion, and treat publication and acknowledgement as single-writer
operations: Terraform, Python, cleanup, and another publisher or
acknowledgement must not mutate the same artifact paths concurrently.

`context.workspace` must be absolute. The other context paths may be absolute
or workspace-relative. The process never consults
`INFRAWRIGHT_DEPLOYMENT`, `INFRAWRIGHT_PACKS`, or its current directory.

A successful response wraps the operation's existing v1 result document in
`infrawright.process_response` v1. A grouped roots selection also carries a
structured `WHOLE_ROOT_SELECTION` diagnostic; `scope_paths` has no diagnostics.
Consumers join responses to invocations with `request_id`; no timestamps,
hostnames, durations, or other nondeterministic fields are emitted.

Exit status is:

- `0`: successful read operation, ready bootstrap or refresh artifacts, a ready
  refresh seed, exact materialized/bootstrap or twin-refresh parity, complete
  retry-forward bootstrap publication, complete refresh publication, an
  awaiting-apply refresh receipt, a retired trusted acknowledgement, or a
  clean/tolerated assessment;
- `3`: schema-valid review-required bootstrap or refresh artifacts, a
  review-required refresh seed, a materialized parity difference, or a blocked
  assessment;
- `2`: malformed request, deployment, catalog, or domain selection;
- `1`: a schema-valid assessment error, indeterminate publication, or another
  I/O/internal host failure.

The strict contracts are published in
`docs/schemas/process-request.schema.json` and
`docs/schemas/process-response.schema.json`. The standalone comparison result
is `docs/schemas/zcc-pull-artifact-parity.schema.json`; refresh compilation is
`docs/schemas/zcc-pull-refresh-artifact-set.schema.json`; the two-phase refresh
contracts are `docs/schemas/zcc-pull-refresh-parity-seed.schema.json` and
`docs/schemas/zcc-pull-refresh-parity.schema.json`; the content-free write
receipt is
`docs/schemas/zcc-pull-artifact-materialization.schema.json`; the refresh
pending fence and write receipt are
`docs/schemas/zcc-pull-refresh-pending-transition.schema.json` and
`docs/schemas/zcc-pull-refresh-materialization.schema.json`; the post-apply
retirement receipt is
`docs/schemas/zcc-pull-refresh-acknowledgement.schema.json`.

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
distinguish JSON `1` from `1.0`. Arbitrary-size integer tokens remain exact;
finite float lexemes use Python's binary64 value and JSON spelling. The
product-neutral kernel contract also understands provider `set(string)` and
`map(string)` encodings, while the public operation remains gated to the exact
embedded five-resource ZCC catalog.

Transform snake-case and transform/adoption slug bytes use the pinned
[Python lowercase compatibility contract](python-lower-unicode-contract.md).
The reviewed Unicode 16 and 17 tables shipped by Node 24 patch releases are
adjusted to the Python 3.12/3.13 Unicode 15.0/15.1 behavior with compact
generated deltas and exhaustive live-Python differentials; every other runtime
Unicode version fails closed.

A private migration differential now binds exactly three ZIA transforms to a
separate compact catalog:

```sh
python3 -m engine.transform_catalog \
  --product zia \
  --resource zia_admin_roles \
  --resource zia_traffic_forwarding_static_ip \
  --resource zia_url_categories \
  --out catalogs/zia-transform-cohort.v1.json
```

The catalog digest covers the committed ZIA pack, registry, provider schema,
and the two present override files. It captures finite-float projection,
schema-set ordering, map-value coercion, and the URL-category `sort_lists`
override. A committed corpus compares the complete Node transform result and
rendered tfvars bytes with live `engine.transform` output. This remains a pure
source-to-value seam: it accepts already-pulled JSON and adds no process
operation, collector, publisher, Terraform execution, adoption/oracle path,
or HTTP client. The product-neutral `sort_lists` kernel branch is necessarily
present in the release bundle, but the private ZIA catalog, schema, validator,
wrapper, and product-specific markers are not.

The private runtime gate is semantic, not a caller-attested byte check: it
schema-validates a candidate, requires exact equality with the embedded
contract, and returns the canonical embedded snapshot. Catalog regeneration,
the committed-file freshness check, and its test-only known SHA-256 retain the
exact serialized-byte gate at authoring time.

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

A private ZPA differential cohort now exercises that same pure kernel for
already-pulled JSON from `zpa_pra_console_controller` and
`zpa_pra_portal_controller`. Its compact catalog is regenerated with:

```sh
python3 tools/zpa_transform_cohort_catalog.py \
  --out catalogs/zpa-transform-cohort-catalog.v1.json
```

The catalog binds the ZPA 4.4.6 schema and provider-evidence bytes, asserts
that neither selected resource has acquired an override, and reuses the exact
committed Python HTML-compatibility table. It is not a process operation: it
performs no collection, HTTP, publication, Terraform, adoption, or generated
configuration. Every generated-configuration qualification remains
`terraform_runtime_evidence_required`.

Catalog authoring uses this checkout as one source authority. The ZPA wrapper
reads and hashes fixed repository inputs and invokes the generic cohort
compiler in a fresh Python process with `INFRAWRIGHT_PACKS` forced to this
checkout's `packs/`; ambient pack roots and already-primed registry/schema
caches cannot supply semantics attributed to the committed source digest.

The first cohort intentionally does not include the initially preferred
application resources. `zpa_app_connector_group` and
`zpa_application_server` require `drop_if_default`; the connector group also
has range policy. `zpa_application_segment` additionally needs object-list
attributes, merged blocks, and an enum-to-boolean value map. Those resources
must wait for independently reviewed kernel semantics rather than gaining
resource-specific shortcuts in this slice.

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
plan-root enumeration, exact-catalog saved-plan assessment, immutable ZCC
bootstrap and refresh artifact compilation, two-phase refresh byte parity,
materialized bootstrap comparison, asserted retry-forward bootstrap
publication, asserted imports-last refresh publication, and provider-observed
ZCC bootstrap adoption compilation as public process operations.

The bundled, single-use ZCC adoption-oracle transaction covers the exact
five-resource catalog. It renders only the pinned provider root and import
blocks, runs bounded `init`, import-only `plan`, saved plan `apply`, and
lossless `show` stages in an operation-owned mode-0700 directory, then compiles
the existing immutable bootstrap artifacts from exact provider-state
observations. This lane supports HashiCorp Terraform 1.15.4 exactly; it is not
an OpenTofu or generic-Terraform compatibility claim. The plan and state gates
accept only Terraform JSON format `1.2` and `1.0`, respectively, and also
require `terraform_version` `1.15.4`. Every nonempty
transaction gets one host-owned 300-second monotonic deadline across `init`,
generated-config plan, both shows, apply, state validation, and artifact
compilation. Each subprocess receives only the budget remaining immediately
before it starts. Protection and output-binding phases record deadline state
before and after their work. After artifact compilation, a zero-argument
host-only checkpoint rechecks the exact final protected set and remaining
monotonic budget before accepting the result. Callers cannot select or tighten
the timeout. Cleanup runs after success or failure under a separate fixed
30-second budget, so an exhausted transaction cannot consume its cleanup
window.

The concrete adapter accepts only a trusted canonical Terraform executable, a
private scratch authority, and a closed Zscaler/proxy/certificate environment
allowlist; it never merges `process.env`. Shared plugin-cache support is
intentionally absent. Every nonempty transaction writes the committed
multi-platform ZCC `0.1.0-beta.1` dependency lock and invokes `terraform init`
with `-lockfile=readonly`. That lock was generated by Terraform 1.15.4 for
Linux/macOS on amd64/arm64 from the Terraform Registry and its partner-signed
upstream checksums. Root configuration, lock bytes, generated configuration,
saved plan, and local state remain bound private inputs for the entire
transaction.

If deadline exhaustion and a protected-file mutation are both observed during
preflight, produced-file binding, post-stage verification, or final result
acceptance, `ZCC_ADOPTION_ORACLE_TIMEOUT` remains the primary failure. A
single generic `protection` detail records the integrity failure without
disclosing a path, value, credential, import identifier, state value, or child
diagnostic. Before deadline exhaustion, the same integrity failure retains its
ordinary fail-closed precedence.

The public `compile_adoption_artifacts` adapter now supplies those authorities,
derives and binds the caller-workspace inputs, and exposes only the projected
artifact candidate. The versioned secret-safe v1 parity report separately
records shared-observation
and stable Python-before/Node/Python-after comparisons but deliberately leaves
both qualification fields fail-closed. A host-bound successor must derive its
runner authority before it can qualify projection or executor, and only a later
downstream gate may claim cutover. No live tenant or provider parity is claimed
by the committed tests.

The ZCC compiler ports raw-item projection and exact tfvars/import/lookup byte
rendering for `zcc_device_cleanup`, `zcc_failopen_policy`,
`zcc_forwarding_profile`, `zcc_trusted_network`, and `zcc_web_privacy`. Python
remains the independent differential oracle. Node returns an immutable
candidate set, proves digest-only parity against materialized Python output,
and may publish only this exact five-resource JSON bootstrap or refresh profile
after a complete ready parity assertion is supplied from the protected
comparison lane. The repository differential runs the actual Python writer, public Node
comparer, and public Node materializer for all five supported ZCC resources and
checks their resulting bytes exactly. A separate run-two differential lets
Python write the baseline and refreshed outputs, then proves the read-only Node
refresh result for all five resources and adversarial rename cases, including
HTML/Unicode/CSV normalization, grouped variable names, escaped HCL strings,
key swaps, occupied destinations, ambiguous identities, and mixed safe and
suppressed moves.

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
the child environment with either its fixed locale/checkpoint defaults or the
oracle adapter's complete closed environment (never inherited `TF_CLI_ARGS*`),
enforces bounded timeout/stdout/stderr ceilings, discards stderr, and preflights
stdout before lossless parsing. The
current Zscaler cutover boundary accepts at most 8 MiB of JSON, 100,000
structural tokens, 4 MiB of string content, and a 1 MiB scalar token; the same
deadline covers child execution, decode, preflight, and parse. Child output,
filesystem paths, and plan values never enter an error
diagnostic. Final evidence rechecks remain mandatory because the adapter alone
does not claim the root or snapshot stayed unchanged around execution.
Terraform and its provider are trusted executables. POSIX runs use a detached
process group that is killed and reaped on every outcome; a descendant that
deliberately creates a new session can escape that in-process group and must be
contained by the pipeline job/container before this executor is exposed as a
generic public facility.

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

Python remains authoritative for refresh materialization and move lifecycle,
generated-binding production, HCL artifacts, generic guidance collection, raw
pack catalog production, and adoption cutover qualification. The bundled Node
operation now owns a bounded Terraform transaction for its exact five-resource
ZCC scope and returns a provider-observed candidate, but it remains unqualified
by live parity and cannot replace the independent Python/downstream gates.
The Node refresh operation is an exact read-only raw-transform candidate
compiler, not an apply or adoption decision. Downstream should dual-run it
against Python run-two outputs and run `compare_pull_artifacts` immediately
after each authoritative Python ZCC bootstrap transform. Retain Python as the
cutover oracle until the deployment and raw-pull corpus reports byte-clean
parity. The Node materializer is a gated consumer of bootstrap evidence, not a
way to bypass it: use it only for the documented exact bootstrap profile, from
a serialized protected lane, and only after an exit-`0` receipt.
