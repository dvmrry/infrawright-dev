# Pack Distributions And Modular Checks

An Infrawright distribution may intentionally install only the packs it uses.
Pack discovery already follows the effective `INFRAWRIGHT_PACKS` root; a pack
profile makes the intended contents of that root explicit so an accidental
deletion cannot pass as an intentional reduction.

## Pack Profiles

Versioned profiles live under `packsets/` and contain exact, sorted pack and
shared-component names:

```json
{
  "kind": "infrawright.pack-set",
  "version": 1,
  "packs": ["zcc", "zia", "zpa", "ztc"],
  "shared": ["zscaler"]
}
```

`make check PACK_PROFILE=packsets/zscaler.json` requires the effective pack
root to contain exactly that selection. Missing declared components and
undeclared extra components both fail before tests run.

`PACK_CATALOG` defaults to `packsets/full.json` and defines the allowed pack
and shared-component vocabulary. Profiles, example requirements, and test
requirements that reference a name outside that catalog fail as contract
errors rather than becoming permanent skips. A downstream distribution that
adds its own packs supplies its own catalog explicitly.

The profile does not filter a larger root. Build or install a root containing
only the selected directories, then point `INFRAWRIGHT_PACKS` at it. For
example:

```sh
export INFRAWRIGHT_PACKS="$PWD/.packs/zscaler"
make PACK_PROFILE=packsets/zscaler.json check
```

Copy `packs/{zcc,zia,zpa,ztc}` and `packs/_shared/zscaler` into that root. A
selected or independently distributed root is authoritative for Python
collectors and their `packs._shared` imports as well as metadata and registry
data; it cannot silently import a missing collector from this checkout.

Packs declare runtime shared-code dependencies with `requires_shared` in
`pack.json`. Exact profile validation and `check-pack` enforce that dependency
closure. Therefore every profile containing a Zscaler provider pack also names
the `zscaler` shared component. A downstream profile that drops unrelated packs
remains valid, but dropping a component required by a retained pack fails
before tests or collection begin.

Every top-level directory other than `_shared` counts as an installed pack,
even when it has no `pack.json`; every directory immediately below `_shared`
counts as a shared component. This is deliberate. Runtime loaders can consume
registry, adoption-status, schema-extract, and shared inputs from a partially
copied directory, so an exact profile must reject that stale directory instead
of omitting its tests while continuing to load its data. Recursively discovered
runtime inputs must live below one of those component directories; loose
`adoption_status.json` inputs at the pack root or directly under `_shared` are
ignored because no profile component owns them. The reserved `_shared` root is
also not itself a pack: loose `pack.json` and `registry.json` files directly
inside it are ignored. A top-level or shared
`schema-extract` directory is itself a component and must appear in the exact
profile/catalog or validation fails.

## Check Layers

- `make check` validates the active distribution: exact profile, selected unit
  tests, available examples, generated modules, pack metadata, formatting, and
  the vendor boundary.
- `make check-all` ignores a caller's selected root and proves the complete
  upstream catalog against `packsets/full.json`.
- `make check-core` runs the pack-independent test surface and generators with
  an empty pack root.
- `make check-pack-set PACK_PROFILE=<file>` validates only the exact installed
  set contract.
- `make check-pack PACK=<name>` remains the narrow pack-authoring metadata
  check.

Tests are discovered normally. `node-tests/pack-test-requirements.json`
declares the exact compiled Node test files that require committed pack data;
the retained Python compatibility suite uses the finer-grained unittest rules
in `tests/pack-test-requirements.json`. Tests without a declaration remain core
and run under every profile. Both requirement surfaces are fail-closed: stale
files or prefixes are errors, and the core/reduced CI profiles catch new
undeclared coupling.

## Examples

Examples declare subset requirements independently of exact distribution
profiles. The current `demo/pack-requirements.json` requires ZCC, ZIA, ZPA, and
the shared Zscaler component. `make check` runs it when those components are
available and reports an explicit skip otherwise. `make check-demo` and
`make demo-contract` remain strict when invoked directly.

## Distribution Safety

Do not make pack-specific tests unconditional and do not skip tests merely
because a directory is missing. Add the pack requirement and include the test
in a matching reduced-profile CI lane. Update the distribution profile when a
pack is intentionally added or removed; an unchanged profile must fail on
filesystem drift.
