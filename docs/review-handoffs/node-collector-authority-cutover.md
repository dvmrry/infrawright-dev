# Node Collector Authority Cutover — Builder Handoff

## Intent

- Solve the operational `fetch --root` limitation that accepted built-in Node
  adapters only when the pack root was physically the installed bundle root.
- Permit copied and reduced external pack roots without Python files when their
  existing `provider_sources` values resolve to caller-approved Node adapters.
- Keep authentication, URL composition, pagination, selection, output bytes,
  retry behavior, and failure aggregation unchanged.
- Remove the active Node pack-authoring assumption that `collector.py` makes a
  pack directory subject to Python import-name rules.
- Keep all production Make Fetch routes Node-backed. No Make recipe semantic
  change was necessary; the operational smoke now exercises the real target.

## Base / Head

- Base: `df396ddaca19ac01d49df6ebacabb527944251df` (draft PR #222 head).
- Head: `c16f41fd00e65e0ecbc2b9dd85d60d1dfbb69cd3`.
- Diff command:
  `git diff df396ddaca19ac01d49df6ebacabb527944251df..c16f41fd00e65e0ecbc2b9dd85d60d1dfbb69cd3`.

## Files Changed

- Runtime: `node-src/collectors/authority.ts`,
  `node-src/collectors/zscaler-adapters.ts`, `node-src/cli/main.ts`, and
  `node-src/metadata/packs.ts`.
- Tests: collector authority, generic ZCC/ZTC Fetch, external-root operational
  smoke, CLI/metadata routing, test-suite selection, and split Python parity
  files under `node-tests/`.
- Current docs: `docs/operational-runtime.md`, `docs/pack-authoring.md`, and
  `docs/repo-surface.md`.
- Retained compatibility message: `packs/ztc/collector.py` now correctly says
  that legacy runs may scope to `RESOURCE="zia zpa"`; ZCC is also OneAPI-only.
- Files intentionally left untouched: `Makefile` recipes (already route Fetch
  to Node and propagate `INFRAWRIGHT_PACKS`), all transition catalogs and
  hashes, pack manifests/registries, provider schemas/overrides, legacy process
  bundles, and retained Python collector implementations apart from the ZTC
  diagnostic correction.

## Source Inputs Consulted

- Provider schemas: none changed; existing pack schemas were loaded by focused
  and full-suite tests.
- OpenAPI/API contracts: none changed.
- Provider source files: none changed or required.
- Pack metadata: committed `provider_prefixes`, `provider_sources`, registry
  Fetch paths, pack profiles, and the Zscaler shared component.
- Existing docs or design records: `docs/adversarial-review.md`,
  `docs/pack-authoring.md`, `docs/operational-runtime.md`, and
  `docs/repo-surface.md`.
- Other source evidence: the pre-change CLI realpath guard, the generic
  `CollectorAdapter` boundary, product selection and shared OneAPI auth logic,
  and root Make Fetch recipes.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none changed.
- Snapshots: none changed.
- Demo or lab outputs: disposable local fake-HTTP/fake-Terraform test output
  only; nothing persisted.
- Artifact drift intentionally expected: none. Transition catalogs remain
  byte-identical because authority reuses existing `provider_sources` instead
  of adding pack metadata.

## Expected Delta

- `fetch` and `fetch-diag` resolve only selected products through a closed map
  from pack-owned provider source to caller-approved Node adapter.
- Copied/reduced external roots using the shipped Zscaler provider sources can
  execute the built-in adapters without a colocated `collector.py`.
- Missing/unknown provider sources and source/product mismatches fail before
  credential parsing, CA loading, transport construction, or output creation.
- Library callers may supply a custom source-to-adapter map; the bundled CLI
  does not gain arbitrary custom adapters.
- Default Node test selection now runs the pure generic REST and ZCC collector
  suites; their live-Python byte comparisons remain in separately excluded
  parity files.
- No expected report, count, catalog, module, root, tfvars, import, lookup,
  move, binding, plan, assessment, or Apply output change.

## Invariants Claimed

- Evidence must not be silently dropped: registry selection and collection are
  unchanged; the resolver validates every selected resource's actual provider
  authority before returning the product adapter or failing closed.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the selected resource's
  provider determines its owning manifest, whose validated
  `provider_sources[provider]` is the pack authority; the caller's source map
  is the executable authority. Registry `product` cannot replace provider
  provenance.
- Ambiguity must stay classified instead of being coerced to success: missing,
  unknown, and mismatched mappings are explicit usage errors.
- Provider-readiness counts must stay explainable: N/A; no readiness output or
  count logic changed.
- Adoption safety invariants: Oracle, projection, artifact, plan, assessment,
  and exact-plan Apply code is untouched. The Python-free operational smoke
  runs Fetch through Make and then the complete fake-Terraform import-only
  workflow.

## Tests Run

- `npm run typecheck` — passed.
- `npm run build` — passed.
- Focused authority, metadata CLI/loader, REST collector, ZCC collector,
  Zscaler adapter, ZCC/ZTC generic Fetch, selector, and operational-smoke Node
  tests — passed.
- `node --test .node-test/node-tests/rest-collector-python-parity.test.js
  .node-test/node-tests/zcc-collector-python-parity.test.js` — 3 passed.
- `PYTHON=/definitely/not-available npm run test:node` — 808 passed at the
  implementation checkpoint; no failures/skips.
- `PYTHON=/definitely/not-available make check-node` — passed after commit;
  selected 70 files, excluded 43 explicit Python-oracle files, and completed
  all Make validation/generation/audit surfaces.
- `python3 -m unittest tests.test_collectors_rest tests.test_check_pack` — 27
  retained compatibility tests passed.
- `make audit-vendor-boundary` — 0 violations.
- `node scripts/test-runtime-release.mjs` — passed for
  `c16f41fd00e65e0ecbc2b9dd85d60d1dfbb69cd3`.
- `git diff --check` — passed.
- Tests not run: no credentialed live provider Fetch. Repository acceptance is
  covered by local HTTPS/fake transport and the external work environment owns
  credentialed qualification.

## Known Deferrals

- Retained Python collectors, transition catalogs, and frozen process bundles
  are not deleted in this change because legacy parity/catalog consumers still
  reference them. Archive/removal follows a separate external-consumer
  inventory and approval.
- Non-Zscaler operational adapters remain library-injected; the bundled CLI
  intentionally has no adapter for unknown provider sources.
- A real ZCC/ZTC credentialed Fetch remains downstream qualification evidence,
  not a repository test requirement.

## Review Focus

- Highest-risk paths: `node-src/collectors/authority.ts`, its CLI call sites,
  and external-root tests.
- Attack whether selected products can receive the wrong adapter, whether an
  unselected bad source can block a selected valid product, and whether a
  selected missing/unknown source can reach credentials or transport.
- Verify that `providerOwners` and `providerSources` originate from validated
  active pack metadata and that adapter product matching closes cross-product
  binding.
- Verify derived-resource selectors still authorize the fetch-bearing source
  product through existing selection behavior.
- Verify the operational smoke's pack root is physically outside the bundle,
  contains no Python artifacts, and invokes `make fetch`, not a test-only
  library shortcut.
- Verify moving Python assertions did not weaken pure collector coverage and
  that the parity files remain selected only by the explicit all-tests lane.
- Verify no catalog/schema/generated-output change is hidden in the diff.

## Adversarial Review Loop

The fresh-context review of `81d50926a38b249e773a05957cedc3f79ed17f99`
returned **Request changes** with one blocking finding: the initial resolver
accepted only selected products, so a resource owned by an unknown provider
could declare `product: "zia"` and borrow the bundled ZIA collector authority.

Accepted remediation:

- Authority resolution now accepts the selected fetch resource types.
- Each resource resolves through its actual provider, provider owner, and that
  owner's `provider_sources[provider]` value before adapter lookup.
- The resource registry product must equal the resolved adapter product.
- One product may not span multiple provider sources in a selected run.
- Direct resolver and CLI regressions use a `zia_url_categories` entry whose
  actual provider is `rogue` while its registry product remains `zia`; both
  fail before adapter acquisition, CA access, transport, or output creation.

The patch-focused re-review should inspect only this authority-chain
remediation and its tests, plus the updated explanatory claims.

Remediation validation:

- Focused authority, metadata CLI, external ZCC/ZTC Fetch, and Python-free
  operational-smoke suite — 22 passed.
- `PYTHON=/definitely/not-available npm run test:node` — 811 passed.
- Retained REST/ZCC Python parity — 3 passed.
- Retained collector/pack compatibility — 27 passed.
- Typecheck, production build, vendor-boundary audit, and whitespace check —
  passed.
