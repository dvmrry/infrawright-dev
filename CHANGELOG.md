# Changelog

## Unreleased

### Breaking changes

#### Retired catalog compatibility surface removed

The catalog compatibility layer has been removed intentionally. Packs and
`packs/*.packset.json` profiles are now the sole runtime metadata authority.

This removes:

- the `--catalog` option from every `iw` command;
- the `iw root-catalog` command;
- the `root-catalog` and `check-root-catalog` Make targets;
- the `PACK_CATALOG` and `ROOT_CATALOG` Make variables;
- committed `catalogs/` artifacts and their root-catalog schemas.

There is no ignored-flag compatibility shim. Existing automation that still
passes `--catalog` fails with usage exit code 2 and `unknown flag: --catalog`.

To migrate:

1. Remove `--catalog`, `PACK_CATALOG`, and `ROOT_CATALOG` from downstream
   commands and Makefiles.
2. Select a pack distribution with `--profile packs/<name>.packset.json`, or
   use the default `packs/full.packset.json` profile.
3. Remove downstream `root-catalog` and `check-root-catalog` invocations.
4. Delete downstream copies of committed root-catalog artifacts after
   confirming that no separate consumer reads them directly.
