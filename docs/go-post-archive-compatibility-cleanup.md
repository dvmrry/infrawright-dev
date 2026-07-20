# Go post-archive compatibility cleanup map

Status: PARKED EXPLORATION — this is an inventory, not authorization to change
runtime behavior. Revisit after the Node operator runtime reaches the archive
phase in [go-cutover-roadmap.md](go-cutover-roadmap.md), except where an item is
explicitly folded into
[singleton-state-topology-v2.md](singleton-state-topology-v2.md).

The byte-exact Go port intentionally carried Python, JavaScript, TypeScript,
Node, and AJV behavior into Go. Some of that is temporary migration
scaffolding. Some is merely an implementation choice that can be replaced
after its product contract is versioned. Some currently protects Terraform
addresses, committed artifacts, evidence, automation, or operator decisions.

The final category is not a permanent keep decision. It means only that Node
archive by itself is not a sufficient removal gate: a side-by-side candidate
and an explicit state/artifact migration must prove that the replacement is
safe.

## 1. Classification rule

| Class | Meaning | Change gate |
|---|---|---|
| Archive cleanup | Exists solely to reproduce or dispatch to the Node operator runtime | Node operator archive plus a versioned Go CLI contract |
| Contract simplification | Behavior remains useful, but its Python/Node implementation shape is not the product requirement | Replacement implementation, focused differentials, and any required schema/version bump |
| State/artifact migration candidate | Behavior reaches Terraform addresses, committed configuration, evidence, reports, or authorization decisions | Explicit format/state migration with full-corpus and live no-op proof |
| Safety mechanism | Prevents stale, incomplete, unbounded, or path-rebound operation | Retain the invariant even if the implementation is rewritten |

## 2. Archive cleanup candidates

### Native Go CLI contract

The completed Cobra command tree now owns native Go help, command inventory,
and completion, and no production "not yet ported" dispatch guard remains.
`go/cmd/iw/main.go` still discovers the repository by walking to
`package.json` and uses a top-level `recover` to reproduce JavaScript's
catch-all entry point. `go/cmd/iw/commands_topology.go` retains legacy
usage-exit translation and accepts unused `--terraform` flags for Node CLI
compatibility.

After the rollback window:

- replace `package.json` discovery with the release package-root contract;
- remove accepted-but-unused compatibility flags;
- decide whether the top-level catch-all remains part of the supported Go CLI
  failure boundary or can become ordinary error propagation;
- publish the intended Go stdout, stderr, and exit-code contract rather than
  inheriting every historical Node spelling.

This is operator-visible automation behavior, so it is a versioned CLI cleanup,
not a silent rewrite.

### Diagnostic-only JavaScript spelling

Several packages locally reproduce `JSON.stringify` or template-literal
spelling for diagnostics, including `cmd/iw`, `modulesgen`, `transformrun`, and
`adopt`. Trace each caller, then replace diagnostic-only uses with one product
error renderer. Any caller that contributes artifact or REPORT bytes is
excluded until its enclosing contract is versioned.

### Dead frozen-compatibility switch

`transform.StrictFrozenCompatibility` is threaded through projection,
coercion, identity, and overrides, but the current tree has no assignment of
`true`; production constructors explicitly set it to `false`. Reconfirm with
the full differential corpus and authoring tests, then delete the flag and its
unreachable branches.

### Executable oracle scaffolding

At archive, remove the default test/build dependency on an executable Node
operator bundle. Preserve frozen fixtures, digests, provenance, and the minimum
oracle artifact required by any retained differential lane. Do not erase the
historical evidence merely to remove Node from `PATH`.

## 3. Contract-simplification candidates

### Assessment structural schema validation

`go/internal/assessment/semantics.go` is a 1,107-line hand-port of the
assessment schema and AJV-like first-64 error-detail behavior. Replace the
structural portion with `github.com/santhosh-tekuri/jsonschema/v6`, already
approved by the dependency policy, while retaining the custom fail-closed
cross-field semantics.

The candidate needs a versioned policy for validation-error detail ordering and
text. REPORT meaning, completeness, freshness, and authorization behavior must
not weaken.

### TypeScript throw/catch topology

Metadata, transform, deployment, roots, plan, envgen, tfrender, and CLI argument
paths use `panic`/`recover` to mirror TypeScript exceptions. Convert these to
ordinary Go errors incrementally as their packages are otherwise touched,
preserving stable process-failure codes and useful error categories.

Recovery that protects transaction cleanup, hooks, descriptor-bound removal,
or another safety boundary is not part of this cleanup.

### Fingerprint HCL parsing

`go/internal/plan/fingerprint_hcl.go` manually recognizes HCL using Python
regular-expression whitespace and `str.strip` behavior. Explore an `hclsyntax`
AST implementation under a new fingerprint schema version. Existing saved-plan
fingerprints cannot silently change interpretation.

### Terraform runner runtime model

`go/internal/terraformcmd` retains the JavaScript safe-integer timeout ceiling,
Node timer chunking, JavaScript whitespace/UTF-16 accounting for Terraform JSON,
and parts of Node stream-completion ordering. Replace these with explicit
Infrawright limits and Go cancellation/stream contracts while retaining:

- bounded output and environment sizes;
- process-group termination;
- redaction;
- deterministic failure precedence;
- fail-closed parsing and completeness checks.

### Product limits named after Node

`go/internal/artifacts` freezes Node's `buffer.constants.MAX_STRING_LENGTH` and
the JavaScript safe-integer domain. Replace them with documented Infrawright
limits chosen for the files and evidence the product actually accepts. Removing
the Node-derived values must not mean removing the bounds.

### Python path model

`go/internal/pypath` carries Python POSIX join, normalization, realpath, and
exactly-two-leading-slash behavior. Do not mechanically replace it with
`filepath`. First split:

- portable artifact/schema paths, with an explicit POSIX contract; and
- real filesystem paths, using `filepath`, `os.Root`, descriptors, and the
  existing identity checks.

Remove CPython-only corner cases only after supported input grammar,
fingerprints, and path-safety tests prove they are unreachable or intentionally
rejected.

### Deployment optionality and Python truthiness

`go/internal/deployment` distinguishes absent, null, empty, and false to mirror
TypeScript `undefined`, `||`, `??`, and Python `bool()` behavior. The
singleton-state topology change removes `strategy`, `groups`, and
`bind_references`; a later deployment-schema version can type the remaining
fields and defaults directly rather than preserve language-specific coercion.

## 4. State/artifact migration candidates

These are deliberately on the table for later testing. They are not routine
post-archive deletions.

| Candidate | Current reason it is load-bearing | Required proof before replacement |
|---|---|---|
| `canonjson` Python-compatible JSON, number spelling, escaping, and equality | Feeds committed JSON, HCL values, evidence/report bytes, hashes, and comparisons | Candidate renderer beside the existing one; full 151-type byte and semantic diff; fingerprint/report decision diff; Terraform validate/plan; explicit artifact schema version |
| `pyunicode` lowercasing | Feeds slug/tfvars map keys and therefore Terraform addresses | Full key/address inventory before and after; collision analysis; re-adopt/state-migration rehearsal; no-op plan on live canary |
| `pyunicode` HTML unescape | Changes provider values before committed tfvars rendering | Provider fixture corpus plus live value comparison and no-op plans |
| Transform Python/JavaScript numeric coercion and equality | Can change identities, set ordering, state keys, drift-policy decisions, and REPORT classification | Lossless wide-number/signed-zero corpus; address and artifact diff; assessment-decision differential; live canary |
| Hand-rolled `tfrender`/import/move renderers | Produce committed configuration whose bytes are presently gated | Define whether future compatibility is semantic or byte-level; side-by-side HCL; Terraform parse/validate/plan; automation consumer audit; artifact version bump |
| Python/JavaScript whitespace rules in assessment and Terraform JSON limits | Can affect guidance validation, parsing, and operator decisions | Versioned policy/schema vocabulary plus adversarial decision-boundary corpus |

`canonjson.ComparePythonStrings` is ordinary Unicode code-point ordering. It can
be renamed to a product-neutral name independently if every caller and test
retains the same behavior.

## 5. Qualification protocol for the migration candidates

Before deleting a load-bearing compatibility implementation:

1. Enumerate every byte-reaching and decision-reaching caller.
2. Run old and candidate implementations side-by-side, without changing the
   default.
3. Generate the complete pack/profile surface and record byte, address, and
   semantic differences.
4. Classify every difference as intended, unsafe, or unexplained; unexplained
   differences fail the experiment.
5. Run Terraform parse, validate, import-only plan, assessment, exact Apply in
   an approved disposable target, and a second no-op plan where applicable.
6. Prove the state-address migration or re-adopt procedure before changing any
   address-producing behavior.
7. Version affected artifact, fingerprint, report, or deployment contracts and
   retain a rollback path.

The three safety spines remain independent of language compatibility and are
not removal candidates: complete-plan gating, freshness/TOCTOU enforcement,
and raw evidence preservation. Descriptor-bound filesystem operations and
bounded/serial reads likewise remain unless replaced by mechanisms proving the
same or stronger invariants.

## 6. Suggested order when this work is reopened

1. Finish singleton-state topology v2.
2. Complete the Go cutover and Node operator rollback window.
3. Remove CLI/oracle scaffolding and the dead frozen-compatibility switch.
4. Replace assessment structural validation.
5. Normalize errors, Terraform runtime limits, fingerprint parsing, and path
   contracts incrementally.
6. Select artifact/state migration candidates only from measured maintenance or
   operational benefit; do not create an artifact v2 solely to make the Go
   implementation look more idiomatic.
