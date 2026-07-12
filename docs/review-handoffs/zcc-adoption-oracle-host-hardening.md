# Builder Review Handoff: ZCC Adoption Oracle Host Hardening

## Intent

- Close three executor prerequisites before the private exact-five ZCC adoption
  oracle can be considered for a protected public process adapter: cumulative
  deadline ownership, exact Terraform JSON/binary compatibility, and immutable
  provider archive authority.
- Replace five independently selectable subprocess ceilings with one fixed
  host-owned transaction deadline. Give verified cleanup a separate fixed
  window so an exhausted transaction still attempts private-tree removal.
- Bind every nonempty run to HashiCorp Terraform 1.15.4, plan JSON format 1.2,
  state JSON format 1.0, and the reviewed ZCC 0.1.0-beta.1 archive set.
- Preserve the existing exact five-resource transaction, artifact bytes,
  closed environment, no-shell execution, bounded output, protected paths,
  scratch cleanup, and value-free error behavior.
- Keep the oracle private. Do not add a process request/schema branch, release
  reachability, REST collection, materialization, shared plugin cache, or any
  resource outside the existing five.

## Base / Head

- Base: `fc962f81dd637e40d869e320359a16beb8999e9c` (`origin/main` at branch
  creation).
- Head: the checked-out review checkpoint on
  `feature/node-zcc-oracle-host-hardening`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff fc962f81dd637e40d869e320359a16beb8999e9c...HEAD`.

## Files Changed

- Runtime and contract:
  - `node-src/domain/zcc-adoption-provider-lock.ts`
  - `node-src/domain/zcc-adoption-oracle.ts`
  - `node-src/io/zcc-adoption-oracle-adapters.ts`
- Authoritative dependency input:
  - `catalogs/zcc-adoption-provider-lock/main.tf`
  - `catalogs/zcc-adoption-provider-lock/.terraform.lock.hcl`
  - `catalogs/zcc-adoption-provider-lock/provenance.json`
  - `.gitignore`
- Regressions:
  - `node-tests/zcc-adoption-provider-lock.test.ts`
  - `node-tests/zcc-adoption-oracle.test.ts`
  - `node-tests/zcc-adoption-oracle-adapters.test.ts`
  - `node-tests/zcc-adoption-oracle-integration.test.ts`
  - `node-tests/zia-transform-cohort.test.ts` (existing production-bundle
    exclusion proof widened to cover the still-private oracle inputs/markers)
- Documentation:
  - `docs/node-process-api.md`
  - `docs/zcc-adoption-oracle-parity-contract.md`
  - this handoff.
- Files intentionally left untouched:
  - process request/response schemas, validators, dispatch, and entry point;
  - ZCC transform/adoption catalogs and schemas;
  - HTTP collectors, pull compilers, refresh, publication, and materialization;
  - Python adoption/oracle code and all non-ZCC products/resources.

## Source Inputs Consulted

- Provider schemas:
  - existing `packs/zcc/schemas/provider/zcc.json` through the unchanged exact
    five-resource adoption catalog.
- OpenAPI/API contracts: N/A; this slice does not call or interpret the ZCC API.
- Provider source files:
  - existing catalog pin `registry.terraform.io/zscaler/zcc`
    `0.1.0-beta.1` / upstream tag `v0.1.0-beta.1`.
  - Terraform Registry download discovery for Darwin/Linux amd64/arm64.
  - upstream partner-signed
    `terraform-provider-zcc_0.1.0-beta.1_SHA256SUMS` and signature key ID
    `289EF1F15F4B3846`.
- Pack metadata:
  - `catalogs/zcc-adoption-catalog.v1.json`, unchanged SHA-256
    `ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7`.
- Existing docs or design records:
  - `docs/review-handoffs/zcc-adoption-oracle-foundation.md` deferrals.
  - `docs/zcc-adoption-oracle-parity-contract.md` trust boundary.
  - `docs/node-process-api.md` private-executor boundary.
- Other source evidence:
  - `/run/current-system/sw/bin/terraform` reported Terraform `1.15.4`,
    `darwin_arm64`.
  - retained Terraform-core structural fixture
    `node-tests/fixtures/terraform-import-structure-v1.15.4.json` proves the
    reviewed plan `1.2` and state `1.0` envelopes only; it is not ZCC provider
    or tenant evidence.
  - exact lock generation command:
    `terraform providers lock -platform=linux_amd64 -platform=linux_arm64 -platform=darwin_amd64 -platform=darwin_arm64`.
  - read-only validation command:
    `terraform init -backend=false -input=false -no-color -lockfile=readonly`.

## Generated Artifacts

- Reports: provider-lock provenance JSON only; it contains public tooling,
  platform, version, signature-key, and digest metadata, no tenant data.
- Schemas: none.
- Fixtures:
  - new authoritative multi-platform `.terraform.lock.hcl`, SHA-256
    `9a097955041338130f344c525e10a3f34513eef307678df5e80abcf604ee60fa`.
  - its exact bytes are embedded in the private Node module and compared with
    the committed file in tests.
- Snapshots: no plan, state, generated HCL, credentials, import IDs, or tenant
  observations were added.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: only the new lock/provenance input.
  The existing adoption and transform catalogs, schemas, parity fixtures, and
  public bundle surface remain unchanged.

## Expected Delta

- Expected behavior change:
  - every nonempty private ZCC oracle run writes and protects the reviewed lock
    alongside `main.tf` and `imports.tf`;
  - `init` adds `-backend=false -lockfile=readonly`;
  - generated roots require HashiCorp Terraform exactly `1.15.4`;
  - plan/state gates require exact `format_version` `1.2`/`1.0` and exact
    `terraform_version` `1.15.4`;
  - init, plan, both show+parse stages, and apply share one monotonic 300-second
    host deadline. Each runner receives only the remaining milliseconds and a
    successful stage is rejected if post-stage binding crosses the deadline;
  - cleanup does not consume that budget and has a separate fixed 30 seconds;
  - callers can no longer provide command/show timeouts, output limits, or a
    plugin-cache authority.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: no tfvars/import/lookup byte changes. The
  private scratch root and init argv change; the retained lock is new.
- Expected no-op areas: empty derived identity sets still perform no Terraform
  or temporary effects; every successful nonempty one-resource run produces
  the same existing artifact contract for the same provider observation.

## Invariants Claimed

- Evidence must not be silently dropped: plan and state gates are narrower,
  not weaker. The lock joins every protected-path set and is rebound/rechecked
  before and after every Terraform stage.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or provider-evidence classification changes.
- Source precedence/provenance must remain explicit: provider address/version,
  exact Terraform baseline, lock SHA, official archive SHA values, generation
  platforms, and signing key are retained separately.
- Ambiguity must stay classified instead of being coerced to success: unknown
  Terraform JSON formats/versions and any lock/version/archive drift fail
  closed. No compatibility range or OpenTofu equivalence is claimed.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  count changes.
- Adoption safety invariants:
  - exact five-resource scope only;
  - one import-only plan and exact root-state join;
  - provider-returned `values.id` still equals the requested import ID;
  - generated config and plan are bound before apply; state is bound before
    projection;
  - no shell, inherited environment, CLI config, or shared plugin cache;
  - fixed bounded stdout/stderr with discarded child diagnostics;
  - credentials, state values, import IDs, lock contents, archive hashes, and
    scratch paths never enter a runtime failure;
  - cleanup after timeout retains the original timeout code, adding only the
    existing generic cleanup detail if cleanup also fails;
  - private oracle modules and lock markers remain absent from the production
    process bundle.

## Tests Run

- Focused hardening/build-exclusion suite:
  - `npm run typecheck`
  - `npm run build:test`
  - explicit provider-lock, oracle core, adapter, integration, and bundle tests:
    92 passed, 0 failed.
- Full Node 24.15.0 / Unicode 16.0:
  - `npm run check`: 636 total, 635 passed, 1 existing platform skip, 0 failed.
- Full Node 24.14.0 / Unicode 17.0:
  - explicit `.node-test/node-tests/*.test.js`: 636 total, 635 passed,
    1 existing platform skip, 0 failed.
- Full Python:
  - `make test`: 1,383 total, 1,382 passed, 1 optional external-provider skip,
    0 failed.
- Provider authority:
  - four-platform `terraform providers lock`: all four selected archives
    retrieved and partner-signature verified; retained lock SHA matched.
  - real Terraform 1.15.4 read-only init: installed
    `zscaler/zcc v0.1.0-beta.1` with partner key `289EF1F15F4B3846`; lock bytes
    remained SHA-identical.
- Other gates:
  - `npm run build`: passed.
  - `python3 -m engine.audit_vendor_boundary`: 187 allowed matches,
    0 violations.
  - exact ZCC adoption-catalog and transform-catalog checks: passed.
  - `git diff --check`: passed.
- Tests not run and why:
  - live ZCC tenant import/adoption: no tenant credentials or authority were
    used, and this slice must not claim live provider parity.

## Known Deferrals

- Public protected process operation, request/schema branch, response
  qualification, release-bundle reachability, and downstream cutover gate.
- Immutable Python runner construction and host-derived parity-report build
  bindings remain the separate report/cutover workstream.
- Live shared-observation and independent-executor runs for all five resources.
- Batching resource types; the private executor remains one resource per
  transaction.
- Shared provider cache support is deliberately not included. A future cache
  would need its own immutable archive authority, ownership, and serialization
  review; it must not bypass the read-only lock.
- Provider/Terraform upgrades, other Terraform JSON formats, OpenTofu, Windows,
  and architectures outside the retained Linux/macOS amd64/arm64 generation
  set require new official lock evidence and adversarial review.
- Node's recursive `fs.rm` promise is not abortable. The separate cleanup timer
  bounds how long the adapter waits and makes timeout terminal; an underlying
  OS cleanup already in flight can still finish later. The isolated job-owned
  parent sweep remains the ultimate cleanup boundary.
- Same-UID scratch interference and a deliberately daemonized descendant that
  creates a new POSIX session remain job/container trust-boundary concerns.

## Review Focus

- Verify the retained lock independently with Terraform Registry discovery,
  upstream signed SHA256SUMS, and the four requested platform archives; do not
  rely on the embedded constant or this summary as evidence.
- Confirm `init -lockfile=readonly` and protected-path joins make provider-lock
  mutation fail before execution and prevent Terraform from rewriting the
  authority after init.
- Attack the exact Terraform/JSON gates: missing, old, future, or lookalike
  versions must fail before apply/projection.
- Trace one transaction's monotonic deadline through prechecks, init, plan,
  both show parsers, apply, post-stage bindings, and timeout mapping. Confirm
  no stage receives a fresh 300 seconds and no caller option can select time.
- Verify cleanup is separate from the transaction budget, remains attempted on
  every timeout path, and cannot replace the primary failure with secret data.
- Attack child environment closure for inherited Terraform CLI configuration,
  provider cache, credentials, and proxy/certificate values.
- Confirm all five success paths retain the existing artifact bytes and empty
  identity sets remain effect-free.
- Confirm no process schema, dispatch, materializer, HTTP client, resource
  catalog, or production bundle reachability was added accidentally.
