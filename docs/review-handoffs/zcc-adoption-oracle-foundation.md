# Builder Review Handoff: Private ZCC Adoption Oracle Foundation

## Intent

- Add the private Node Terraform transaction that was intentionally deferred
  from the ZCC adoption projection and artifact slice.
- Accept only the exact five-resource adoption catalog, exact import-only plan
  and root-state shapes, then reuse the already reviewed projection and
  bootstrap artifact compiler.
- Add a secret-safe, versioned parity report that can distinguish simulation,
  shared-live-observation, and independent-executor evidence without claiming
  downstream cutover.
- Keep credentials, Terraform diagnostics, tenant values, import IDs, state,
  and artifact bytes out of reports and failures.
- Keep the new executor private and unbundled until a later protected process
  request supplies trusted paths and environment authority.

## Base / Head

- Base: `3a6388f59690347086f7c0d203b47e024ef8da66`
- Head: the checked-out commit on `feature/node-zcc-oracle-parity`; resolve it
  with `git rev-parse HEAD` at the review checkpoint.
- Diff: `git diff 3a6388f59690347086f7c0d203b47e024ef8da66...HEAD`

## Files Changed

- Bounded Terraform process and lossless-show adapters:
  `node-src/io/terraform-command.ts`, `node-src/io/terraform-show.ts`,
  `node-src/io/zcc-adoption-oracle-adapters.ts`,
  `node-tests/terraform-command.test.ts`,
  `node-tests/terraform-show.test.ts`, and
  `node-tests/zcc-adoption-oracle-adapters.test.ts`.
- Private transaction core and tests:
  `node-src/domain/zcc-adoption-oracle.ts`,
  `node-tests/zcc-adoption-oracle.test.ts`,
  `node-tests/zcc-adoption-oracle-integration.test.ts`, and
  `node-tests/fixtures/terraform-import-structure-v1.15.4.json`.
- Secret-safe parity contract:
  `node-src/domain/zcc-adoption-parity.ts`,
  `docs/schemas/zcc-adoption-oracle-parity.schema.json`, and
  `node-tests/zcc-adoption-parity.test.ts`.
- Normative parity framing and trust contract:
  `docs/zcc-adoption-oracle-parity-contract.md`.
- Boundary documentation: `docs/node-process-api.md` and this handoff.
- Files intentionally left untouched: public process request/response schemas,
  dispatch, release entry points, Python adoption behavior, pack metadata,
  provider schema/catalog bytes, and caller-owned artifact publication.

## Source Inputs Consulted

- Provider schema: `packs/zcc/schemas/provider/zcc.json` through the existing
  versioned adoption catalog.
- Provider source/version: `zscaler/zcc` `0.1.0-beta.1`, already pinned by the
  catalog and pack. Its five resource implementations were checked directly:
  `internal/framework/resources/device_cleanup.go`, `failopen_policy.go`,
  `forwarding_profile.go`, `trusted_network.go`, and `web_privacy.go`. Each
  importer/read mapper retains the resolved API identity as the computed string
  `id`; numeric forwarding/trusted-network IDs are rendered in base-10.
- Pack metadata: the committed `catalogs/zcc-adoption-catalog.v1.json` and its
  hashed ZCC source set.
- Existing Python transaction: `engine/import_oracle.py`,
  `engine/state_project.py`, `engine/adopt.py`, and the Terraform root rendered
  by the legacy oracle.
- Terraform JSON contracts: Terraform v1.8 through v1.15 source definitions for
  plan `complete`, `errored`, `applyable`, import change, and state-value
  fields.
- Terraform v1.15.4 executable: an offline built-in `terraform_data` import was
  used only to retain exact plan/state structural shapes.
- OpenAPI/API contracts: N/A; this slice consumes Terraform provider Read
  state and performs no REST mapping.

## Generated Artifacts

- Report schema: `infrawright.zcc_adoption_oracle_parity` version 1.
- Fixture: one sanitized, credential-free Terraform v1.15.4 built-in-resource
  import plan/state wrapper with reproduction commands and structural-only
  provenance.
- Reports: none committed.
- Snapshots: no ZCC provider plan, state, generated HCL, or tenant output is
  committed.
- Artifact drift intentionally expected: none for existing public operations,
  catalogs, Python output, or release-bundle bytes.

## Expected Delta

- A generic Terraform runner snapshots argv, limits, and the complete child
  environment, uses no shell, bounds stdout/stderr/time, and reaps the isolated
  POSIX process group on every outcome.
- Terraform show retains its existing failure codes and lossless JSON limits
  while using the shared runner and accepting an explicitly complete
  environment for private transactions.
- The ZCC adapter creates one private transaction, writes two exclusive
  mode-0600 inputs, runs only exact stage argv, and binds directory/file
  identity, metadata, and SHA-256 around every command and show. Plan-produced
  config/plan and apply-produced state are bound immediately after their
  producing command. Cleanup is mandatory and verified.
- The transaction rejects anything except the catalog-derived import set, an
  exact import-only complete/applyable/non-errored plan, and the exact root
  managed state for the selected resource/provider/address set.
- The parity builder commits every tenant-derived comparison role with a fresh
  caller-held, domain-separated HMAC-SHA256 key. Only public catalog/build
  bindings and the digest of the already redacted report use plain SHA-256.
- Version 1 records simulation, shared-observation, and independent-executor
  comparisons but qualifies none of them because evidence class/build bindings
  remain caller assertions. A host-bound successor schema must derive those
  authorities before it can qualify projection or executor; cutover remains a
  later downstream-gate decision.
- No public process behavior changes and no live provider result is claimed.

## Invariants Claimed

- Evidence must not be silently dropped: all catalog-derived imports must
  appear exactly once in both the plan and final root state; extra, missing,
  duplicate, deferred, drift, output, action-invocation, failed-check, deposed,
  or tainted evidence is rejected.
- Generic versus source-backed evidence: the Terraform fixture proves only
  Terraform JSON structure. It is explicitly not ZCC provider or tenant
  evidence.
- Source precedence/provenance: the already versioned ZCC adoption catalog is
  the sole resource/provider/identity authority; the executor does not infer
  provider behavior or read mutable pack metadata.
- Ambiguity: resource, provider, address, import ID, observation, lookup
  applicability, and report/build stability must join exactly or the comparison
  fails.
- Provider-returned identity: every one of the five pinned resources exposes a
  computed string `id`; final state must return exactly the requested catalog-
  derived import ID before its values can become an observation.
- Secret safety: child stdout is captured only for bounded show JSON; stderr is
  counted and discarded. Errors contain no paths, credentials, import IDs,
  state values, artifact bytes, or child diagnostics. The parity report
  contains commitments only for every tenant-derived role.
- Process safety: no shell or inherited environment; exact trusted executable,
  cwd, argv, protected paths, and single-use adapter lifecycle; mandatory
  cleanup preserves the primary error with a generic cleanup detail.
- Adoption safety: generated configuration is never trusted as an input until
  bound after plan; the saved plan is applied only after the import-only gate;
  state is projected only after the exact root-state gate.
- Qualification safety: unequal survivor/observation counts fail, aggregate
  zero coverage remains visible only as one bit, and every v1 qualification
  field is fail-closed `not_qualified` until a host-bound successor lands.

## Tests Run

- `npm run check`: 578 total, 577 passed, 1 platform skip, 0 failed.
- `make test`: 1,365 passed, 0 failed.
- Focused Terraform runner/show/oracle/adapter/parity suites, including repeated
  process-group tests and output replacement races.
- `npm run build`, `python3 -m engine.audit_vendor_boundary`, and
  `git diff --check`: passed at the review head.
- Live ZCC provider import: not run; this branch contains no tenant authority
  or credentials and must not claim live parity.

## Builder Preflight Remediation

- Finding: the private Node lane inherited 120-second defaults while Python's
  oracle allows 300 seconds per Terraform subprocess. Root cause: the generic
  runner defaults were reused as the provider-lane compatibility ceiling. Fix:
  keep generic/public show at 120 seconds but use private 300-second command and
  show defaults that callers may only tighten. Tests accept 300,000 ms and
  reject 300,001 ms. A whole-transaction deadline remains a public-host gate.
- Finding: the parity digest/HMAC bytes were implementation-defined and the
  plain report hash could be mistaken for evidence authentication. Fix: add a
  normative v1 trust/encoding/framing/build-binding contract, fixed HMAC/report
  vectors, signed-zero coverage, and explicit trusted-process/authenticated-CI
  transport requirements. The validator and digest are integrity checks only.
- Finding: per-resource `input_presence` disclosed tenant presence and mapped
  cardinality mismatch to ordinary emptiness. Fix: reject every survivor versus
  observation count mismatch and expose only the aggregate live coverage bit
  retained for a future host-bound successor. Simulation reports use
  `not_applicable`.
- Finding: caller-asserted evidence class and build hashes could mint a
  syntactically `qualified` live report before the protected host existed. Fix:
  make v1 comparison-only and require both qualification fields to remain
  `not_qualified`; a host-bound successor schema must derive all authority.
  Schema regressions tamper both fields, and semantic regressions recompute the
  unkeyed report digest after each forgery so rejection cannot pass merely on
  stale-digest detection.
- Finding: a zero-exit trusted command could bind an output that existed before
  its producing stage. Fix: require generated config/plan/state absence before
  plan/apply, with precreated-output regressions.
- Finding: core and concrete adapter tests did not exercise their actual join.
  Fix: add a fake-Terraform integration that drives init, plan, show, apply,
  state show, artifact compilation, stripped environment, and verified cleanup
  through the real combined boundary.
- Finding: final observations copied the requested import ID into their own
  metadata without independently checking provider-returned state identity.
  Fix: require `values.id` to equal the catalog-derived import ID for every one
  of the five pinned resources, with missing/wrong-ID regressions. Forwarding
  profile and trusted-network cases use provider-representable base-10 identity
  strings above JavaScript's safe-integer range rather than opaque placeholders.
- Finding: Terraform plan/state tests used only synthetic envelopes. Fix:
  retain an exact offline Terraform 1.15.4 built-in import envelope, explicitly
  scoped to Terraform-core structure and never cited as ZCC/provider/live
  evidence.

## Known Deferrals

- Public protected process request/response operation and release-bundle
  reachability.
- Immutable Python runner archive construction and host-derived build bindings.
  Until the host binds exact runner artifact bytes, this private builder's
  caller-supplied evidence class/build hashes cannot support a public live
  qualification claim.
- Live shared-observation and independent-executor runs for all five resources.
- A later downstream saved-plan/adoption-gate aggregator that alone may make a
  cutover decision.
- Batching multiple resource types into one Terraform init/provider session;
  the first private contract stays one resource per transaction for simpler
  evidence isolation.
- A host-owned whole-transaction deadline. The private compatibility default
  currently permits up to five 300-second subprocess windows per nonempty
  resource; public exposure must pass one remaining budget through every stage.
- Python removal, generated reference/group bindings, HCL tfvars, generic pack
  support, and non-ZCC adoption.
- Same-UID principals can always interfere with owner-private scratch paths;
  the intended pipeline trust boundary is an isolated job user/container.
- The trusted-executable boundary does not contain a descendant that
  deliberately creates a new POSIX session while retaining command pipes.
  Public/generic exposure requires a job/container boundary and a host-owned
  whole-transaction deadline with a separate cleanup budget.
- A shared plugin cache is a separately trusted host authority, not protected
  transaction evidence. It must be prewarmed/protected from untrusted writes,
  live Terraform initialization using it must be serialized, and the public
  host must bind an immutable dependency lock or provider-archive hashes before
  any run can become live evidence. Source address and version alone do not
  identify provider bytes.
- Plan and state gates currently accept future `1.x` Terraform JSON format
  revisions. Before public or live exposure, pin the exact reviewed plan/state
  format versions or reject every unknown evidence-bearing container so a
  future format cannot silently weaken the closed gates.
- Cleanup becomes non-retryable once the adapter is spent. The public process
  host must own a dedicated per-request parent and sweep it at job teardown.

## Review Focus

- Whether Terraform plan/state gates accept real v1.8+ import shapes but reject
  any extra semantic work, missing evidence, or `null`/wrong-type weakening.
- Whether generated config, plan, and state are bound immediately enough to
  prevent show from establishing trust in an attacker-replaced output.
- Whether directory/file checks, cleanup, process-group reaping, environment
  stripping, timeout/error precedence, bounded output handling, and scrubbing
  of runner-owned buffers hold on every path. GC-managed show copies are not
  claimed to be synchronously zeroed and must never enter reports or errors.
- Whether the one-resource transaction can apply a saved plan whose generated
  config and exact import set were both verified, without reading caller paths.
- Whether HMAC domain separation, canonical value encoding, report semantics,
  resource ordering, role counts, input presence, and build stability prevent
  false equality or overqualification.
- Whether the schema, builder, semantic validator, and tests agree exactly and
  whether any report field leaks tenant-derived values, lengths, labels, or
  path identities.
- Whether the structural fixture is faithful to raw Terraform output and
  remains clearly separated from ZCC/provider/live evidence.
