# Repository surface

The current tree contains product code, product metadata, current tests, and
operator documentation. Historical handoffs and retired runtime trees belong
in Git history, not beside the production surface.

| Path | Purpose | Keep criteria |
|---|---|---|
| `go/` | `iw` runtime, command tree, and tests | Current behavior protected by direct tests and reviewed goldens |
| `packs/` | Provider metadata, schemas, registries, overrides, and exact distribution profiles | Validated and loaded by a current profile or current provider workflow |
| `tests/fixtures/` | Current integration corpora | Protects a current external or cross-package contract and cannot be derived from shipped artifacts |
| `tools/` | Maintained developer/operator tooling | Documented current workflow plus tests or fixtures |
| `docs/` | Current contracts, runbooks, schemas, and provider evidence | Describes current behavior or an active workflow |
| `demo/` | Shipped credential-free demo overlay | Required by `make demo`, `make check-demo`, or `make demo-contract` |
| `Makefile` | Stable product workflow | Public build, validation, adoption, or authoring command |
| `deployment.json` | Default deployment selector | Points a fresh clone at the shipped demo |

## Boundaries

- `packs/` and `packs/*.packset.json` are the only resource-metadata and
  distribution authorities.
- Root `Makefile` targets are the stable product command surface.
- `local.mk`, `<overlay>/Makefile`, and `<overlay>/local.mk` are optional local
  extension hooks.
- Exactly one overlay is active per command.
- Generated tenant artifacts live under `[<overlay>/]config/<tenant>/`,
  `[<overlay>/]imports/<tenant>/`, and `[<overlay>/]envs/<tenant>/`.
- Generated module trees are local build output and remain untracked.

Delete files when no current code, test, workflow, or operator contract refers
to them. Migration notes, completed review handoffs, frozen executables, and
duplicate generated authorities are recoverable from Git history.
