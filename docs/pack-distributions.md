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
future external-loader change will make independently distributed pack roots
fully authoritative for Python collectors as well; until then, selected roots
are supported within a checkout of this repository.

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

Tests are discovered normally. `tests/pack-test-requirements.json` declares
the exact tests that require committed pack data. Tests without a declaration
remain core and run under every profile. Requirement entries are fail-closed:
stale test prefixes are errors, and the core/reduced CI profiles catch new
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
