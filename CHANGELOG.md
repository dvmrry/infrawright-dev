# Changelog

## Unreleased

### Provider packs

- Refresh the ZIA pack from provider `4.7.26` to `4.8.0` and regenerate its
  provider schema (`74 -> 83` resources, `105 -> 115` data sources).
- Admit the newly supported DNS/filtering rule fields `eun_template_id`,
  `exclude_context_shield_end_point`, and `is_eun_enabled`.
- Keep endpoint-application blocks explicitly excluded for DNS, filtering,
  IPS, and SSL rules: raw transform drops them, endpoint-bearing adoption
  inputs fail closed before the provider Oracle, and pack projection policy
  removes either block from representable provider state and generated import
  config. ZIA `4.8.0` readback is either shape-incompatible or unwired; empty
  endpoint collections remain eligible. The nine new resource types remain
  schema-visible but are not yet registry-enabled.
- Refresh the ZPA pack and its source-bound evidence from provider `4.4.6` to
  `4.4.9`. The inventory remains 55 provider resource schemas and 71
  data-source schemas; the change is field-semantic rather than catalog
  expansion.
- Admit `device_posture_failure_notification_enabled` for access policy rules,
  and regenerate module shapes for the provider's browser/PRA list-to-set and
  portal/capability changes. The same device-posture field is not claimed for
  four schemas that inherit it without provider Read/expand wiring.
- Keep portal capabilities singleton at the generated-module boundary because
  provider `4.4.9` still expands only element zero despite removing the schema's
  one-item limit. The generated input is now a strict one-element tuple when
  present; two-element lists and keyed-object collection bypasses fail before
  the provider can silently discard them.

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
