# Builder Review Handoff: ZIA URL Categories Artifacts

## Intent

- Implement only PR 1 of the approved ZIA-first sequence.
- Fetch custom ZIA URL categories through the real OneAPI URL shape, import
  them through the pinned Terraform provider in an ephemeral local Oracle,
  project provider Read state, and persist pull/tfvars/import/lookup files in a
  caller-owned disposable workspace.
- Replace scoped Python `make fetch` plus scoped `make adopt` for
  `zia_url_categories` without adding an environment root, deployment plan,
  apply path, public process operation, schema, assertion, receipt, publisher,
  catalog, framework, or runtime comparison authorization.

## Base / Head

- Base: `154a74305d58f4ee88e2de21fb9cb8d826b45d70` (`origin/main`, merge of
  PR #190).
- Head: the review checkpoint on
  `feature/node-zia-url-categories-artifacts`; resolve with
  `git rev-parse HEAD`.
- Diff command:
  `git diff 154a74305d58f4ee88e2de21fb9cb8d826b45d70...HEAD`.

## Files Changed

- Narrow runtime entry and build output:
  `node-src/zia-url-categories-main.ts`, `scripts/build-node.mjs`.
- Product Fetch, Oracle, and persistence:
  `node-src/io/zia-url-categories-fetch.ts`,
  `node-src/io/zia-url-categories-oracle.ts`, and
  `node-src/io/zia-url-categories-artifacts.ts`.
- Identity, projection, and artifact rendering:
  `node-src/domain/zia-url-categories.ts`,
  `node-src/domain/provider-state-projection.ts`, and
  `node-src/domain/lookup-sidecar.ts`.
- One directly required extraction:
  `node-src/domain/python-identifiers.ts` plus the compatibility re-export in
  `node-src/domain/pull-transform.ts`. This prevents the ZIA runtime from
  importing the ZCC transform catalog and public validator graph merely to use
  the already shared snake/slug functions.
- Tests: `node-tests/zia-url-categories-artifacts.test.ts`.
- Files intentionally left untouched: public process request/response
  contracts, shared AJV validators, all ZCC product operations, ZPA, Python
  runtime behavior, provider/pack source artifacts, environment/module/root
  generation, import staging, deployment plan assessment, and apply.

## Source Inputs Consulted

- Provider schema: `packs/zia/schemas/provider/zia.json`, resource
  `zia_url_categories` at pin `4.7.26`.
- API contract: the existing source-backed registry entry
  `packs/zia/registry.json` (`urlCategories`, `customOnly=true`, ZIA pagination)
  and the shared OneAPI authority in
  `packs/_shared/zscaler/collector.py`.
- Provider source: the pinned v4.7.26 URL-category importer and Read
  implementation, including its ID-or-`configuredName` import matching and
  returned `category_id` behavior. No new provider-source operation claim is
  introduced.
- Pack metadata: `packs/zia/pack.json` and
  `packs/zia/overrides/zia_url_categories.json` for provider source/version,
  lookup display field, config key, and import ID.
- Existing Python authority: `engine/collectors/rest/__init__.py`,
  `engine/import_oracle.py`, `engine/adopt.py`, `engine/state_project.py`,
  `engine/lookup.py`, and `engine/transform.py`.
- Existing Node primitives: bounded Terraform command/show, lossless JSON,
  Python-compatible artifact JSON, and generated import rendering.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures or snapshots: none added; tests use inline sanitized data and
  disposable workspaces.
- Runtime artifacts: the production path writes
  `pulls/<tenant>/zia_url_categories.json`,
  `config/<tenant>/zia_url_categories.auto.tfvars.json`,
  `config/<tenant>/zia_url_categories.lookup.json`, and
  `imports/<tenant>/zia_url_categories_imports.tf`.
- Artifact drift intentionally expected: none in the repository.

## Expected Delta

- The new private executable performs a real side-effecting PR-1 workflow and
  exits only after the four runtime files have been written. It does not return
  a candidate, receipt, or versioned protocol object.
- Fetch uses the ZIA OneAPI endpoint with `customOnly=true`, `page`, and
  `pageSize=1000`; OAuth, 401 refresh, HTTP 429 retry, proxy, and additive CA
  behavior are implemented in the same runtime.
- Oracle uses an ephemeral local-only root with Terraform `1.15.4` and
  `zscaler/zia` `4.7.26`, admits only the reviewed plan JSON `1.2` exact
  import-only shape, applies that scratch plan, admits only reviewed state JSON
  `1.0` root-only observations, and removes the scratch directory. This is not
  deployment apply.
- Projection is driven by the committed provider schema. It excludes
  computed-only fields, rejects sensitive inputs, and preserves optional,
  required, repeated, and max-one block behavior.
- Identity joins bind raw `configured_name` key, raw import ID, scratch address,
  provider source, resource type, and provider-returned `category_id`. The
  provider's separate computed `values.id` is deliberately not assumed to equal
  the import ID.
- Existing ZCC behavior remains unchanged; only snake/slug definitions move to
  a smaller shared file and retain their old exports.

## Invariants Claimed

- Evidence must not be silently dropped: every derived raw identity must have
  one exact plan import and one exact provider-state observation before any
  artifact is rendered.
- Source precedence remains explicit: existing pack/registry/override/schema
  files are embedded and checked; no inferred or mutable runtime catalog is
  introduced.
- Provider-state values, not raw transform values, own tfvars projection.
- Sensitive provider inputs cannot be written to tfvars.
- Raw API identity owns import IDs and lookup IDs; provider state owns projected
  display/config values.
- The production bundle contains no Python invocation, public process schema,
  public dispatcher, publisher, receipt, or ZCC adoption/root operation.
- The only Python call in the new tests is a protected migration differential;
  production runtime never imports or starts Python.

## Tests Run

- `npm test`: 885 total, 884 passed, one platform skip, zero failures.
- Existing focused ZIA transform suite: 6/6 passed inside the full run.
- New PR-1 suite: 59/59 passed, covering provider projection, exact Python
  artifact-byte parity, empty output, sensitive/required failures, real URL
  composition and pagination, 429/401 behavior, Oracle stage order, actual
  disposable-workspace writes with Python unavailable, and bundle boundaries.
  It also covers 21 non-exact plan shapes rejected before apply, 19 non-exact
  state/identity shapes, invalid token and data UTF-8, and directory, FIFO,
  oversized, and same-size-mutating CA inputs.
- `npm run typecheck`, `npm run build`, and `git diff --check`: passed.
- `python3 -m engine.audit_vendor_boundary`: 187 allowed matches, zero
  violations.
- Focused Python collector/module/ZIA catalog tests: 58/58 passed.
- Credential-free real provider initialization downloaded and locked
  `registry.terraform.io/zscaler/zia` `4.7.26` successfully.
- Credentialed live endpoint plus provider Read: not run locally because no
  Zscaler credentials are present in this shell. This remains an explicit PR
  acceptance gate and must not be inferred from fixture tests or provider init.

## Known Deferrals

- Mandatory before PR acceptance: run the built Node executable in the
  credentialed ADO environment against a disposable workspace; verify a real
  URL-category pull, real provider import/read, and the four persisted files.
- PR 2 remains unchanged: narrow module/root generation, state-aware import
  staging, Terraform init/plan, tfplan plus fingerprint, and existing Node plan
  assessment. No apply.
- PR 3 remains unchanged: exact assessed-plan recheck/apply, cleanup, fresh
  workspace rerun/no-op, and ADO lane cutover with Python unavailable.
- No additional resource, legacy ZIA authentication, environment root,
  deployment plan, module generator, staging, apply, ZPA work, or ZCC work is
  included.

## Review Focus

- Verify the ZIA URL, query, pagination, OAuth authority, provider source/pin,
  key/import identity, and lookup name directly against the existing source
  inputs.
- Attack the import-only plan gate and address/provider/import joins; ensure a
  non-import change cannot reach scratch apply.
- Compare provider-state projection with `engine.state_project`, especially
  optional+computed attributes, computed-only identity fields, max-one set/list
  blocks, required nested IDs, empty blocks, and sensitivity masks.
- Verify raw identity and provider-state value domains cannot be accidentally
  swapped, particularly lookup ID/name behavior and the deliberate refusal to
  require `values.id == importId`.
- Verify all failure paths avoid credential, token, API body, import ID, state,
  and provider diagnostic disclosure.
- Confirm the runtime writes all four real artifacts and cannot import the
  public process protocol, schemas, publishers, ZCC operations, or Python.
- Treat the missing credentialed live run as an unresolved acceptance gate,
  not as evidence supplied by the builder.

## Initial Adversarial Findings and Remediation

- Initial verdict: request changes with four blockers.
- Exact plan authorization: remediated by pinning Terraform `1.15.4`, plan
  format `1.2`, and every complete/applyable/no-other-effect field before
  scratch apply. The rejection matrix proves apply is never called.
- Provider state identity and structure: remediated by accepting only root-only
  state format `1.0`, rejecting outputs/checks/children/deposed/tainted or
  sensitivity-less observations, and requiring provider-returned
  `category_id == raw import ID` before projection or artifact rendering.
- HTTP UTF-8: remediated by keeping production response bodies as bounded bytes
  and using fatal UTF-8 decoding before token or data JSON parsing.
- Custom CA input: remediated by the existing stable bounded-file reader, which
  opens non-blocking, preflights a regular file and size, bounds reads, and
  rechecks path/file identity after the read.
- Non-blocking initial risk retained: provider lockfile hash provenance is not
  added in PR 1. The source and version stay pinned, but reviewed multi-platform
  lock bytes remain a possible later hardening item; it is not inserted into
  the fixed three-PR sequence without approval.
- Re-review must inspect only these remediated surfaces plus their regression
  tests and must continue to treat the credentialed live run as unresolved.
