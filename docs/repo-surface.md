# Repository Surface

Infrawright is at a soft-freeze point for the core engine. The public repository
surface should stay small enough that users can tell which paths are stable
product contract, which paths are shipped demo material, and which paths are
maintained development evidence.

## Surface Inventory

| Path | Purpose | Owner | Keep criteria | Notes |
|---|---|---|---|---|
| `engine/` | Core product logic: provider-agnostic transform, import oracle, projection, diagnostics, provider-readiness mapping, shared collectors, and CLI entrypoints. | Core maintainers | Keep only behavior used by root commands, tests, docs, or provider-pack contracts. | Engine changes require tests and should not absorb provider-specific one-offs. |
| `packs/` | Declarative provider metadata, schemas, registries, overrides, adoption metadata, and provider collectors. | Pack maintainers | Keep metadata that is validated, referenced by tests, or backed by provider-lab/readiness evidence. | Shared helpers belong under `packs/_shared/`; provider-specific behavior should not leak into `engine/`. |
| `tools/` | Maintained developer/operator tooling outside the Python engine. | Tool maintainers | Keep tools with documented input/output, tests or fixtures, and a current workflow reference. | `tools/source-evidence-ast/` is used by provider-readiness source evidence evaluation. |
| `docs/recipes/` | Small pinned provider-readiness workflows. | Provider-readiness maintainers | Keep recipes that are current, pinned, credential-free, and runnable from a fresh clone. | Stale, aspirational, or private-provider recipes should be archived or deleted. |
| `scripts/` | Maintained release/operator wrappers that are not product commands. | Core maintainers | Keep scripts with narrow purpose, clear inputs/outputs, and current docs/tests references. | One-off migration scripts should not remain here after their PR lands. |
| `demo/` | Shipped demo overlay: demo workflow Makefile, demo deployment config, and demo config/import artifacts. | Demo maintainers | Keep files required by `make demo`, `make check-demo`, or the shipped no-credential demo. | The demo deployment uses `overlay: demo` and generates `demo/modules/default` on demand. |
| `docs/` | Current contracts, usage docs, design records, provider labs, schemas, and archived context. | Documentation owners | Keep docs that describe current behavior or clearly archived historical context. | Stale layout docs should move under `docs/archive/` with an archival notice. |
| `tests/` | Current product contracts and regression fixtures. | Core and pack maintainers | Keep tests that protect current behavior, metadata contracts, or documented workflows. | Tests may cover transitional fallbacks when the fallback remains intentional. |
| `Makefile` | Stable product command surface and extension hook loader. | Core maintainers | Keep commands that are public product workflows or compatibility gates. | Demo-only workflows should live in `demo/Makefile`; root `check-demo` delegates to the demo overlay. |
| `deployment.json` | Default deployment selector for fresh clones. | Core/demo maintainers | Keep as a tiny root pointer to the shipped demo overlay unless the default demo changes. | Current default: `{"overlay": "demo", "module_dir": "demo/modules/default"}`; that module dir is generated locally and ignored. |
| `demo/deployment.json` | Pinned demo deployment selector used by `make check-demo`. | Demo maintainers | Keep aligned with the shipped demo overlay and module set. | Allows local `deployment.json` overrides without breaking demo validation. |
| `LICENSE` | Source license. | Project owner | Keep. | Required in public releases. |
| `README.md` | Public entrypoint and quickstart. | Core maintainers | Keep concise and current with stable repo layout and command surface. | Detailed design belongs under `docs/`. |

## Current Layout Boundaries

- Root `Makefile` targets are the stable product command surface.
- `local.mk`, `<overlay>/Makefile`, and `<overlay>/local.mk` are extension
  hooks for local or overlay-specific workflow targets.
- Exactly one overlay is active per command. Use separate deployment files for
  separate domains such as Zscaler, AWS, or GCP; multi-overlay composition is
  not part of the current contract.
- The shipped demo owns `demo/Makefile`; demo-only helpers should not expand the
  root product API.
- Generated tenant artifacts live under `[<overlay>/]config/<tenant>/`,
  `[<overlay>/]imports/<tenant>/`, and `[<overlay>/]envs/<tenant>/`.
- Generated env roots source modules from deployment-configured `module_dir`.
- The shipped demo module set is generated on demand under ignored
  `demo/modules/default`.
- Root-global `modules/` is not required for demo operation after the
  overlay-scoped module-dir migration. The root `modules/` fallback exists only
  for deployments with no overlay and no explicit `module_dir`.

## Prune Policy

Delete, move, or archive files when they no longer match their surface:

- Demo-only helpers belong under `demo/`.
- Historical design notes belong under `docs/archive/` with a clear archival
  notice.
- Provider-readiness recipes belong under `docs/recipes/`, and must be pinned,
  credential-free, and runnable.
- Tools must have a current workflow reference plus tests or fixtures.
- Root-level scripts must have a documented operator/developer purpose.
- Generated demo modules must remain untracked; `make demo` materializes them
  locally when needed.
- Stale Zscaler-as-code migration artifacts and one-off PoC scripts should not
  remain in the public surface unless they are promoted into maintained packs,
  tools, recipes, or archived documentation.

## Audit Notes

This audit removed root-level `collectors/` and `recipes/`, stopped committing
the generated demo module tree, and found no remaining root-level generated
`config/`, `imports/`, `envs/`, or `modules/` directories after the
overlay/module-dir migration. The stale generated/authored ownership model was
archived because it described the old root demo layout and provider-subdirectory
config shape.
