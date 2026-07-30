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

### Cross-state references

- Committed config stops carrying tenant IDs for declared reference
  fields: transform and adopt now write qualified reference tokens
  (`"<referent_type>.<key>"`, e.g.
  `"zia_firewall_filtering_network_service.dns"`) wherever the referent's
  lookup book knows the ID. Unknown IDs, sentinels, and fields without a
  declared pack edge keep their literal values; old-shape (raw-ID) configs
  remain valid indefinitely — the next transform/adopt run rewrites them,
  with no flag day.
- gen-env renders tokenised roots with lookup-first resolvers:
  `try(<remote-state lookup>, local.infrawright_reference_book_<referent>["<key>"])`,
  where the book local is a plan-time, `fileexists()`-guarded read of the
  committed lookup sidecar. State truth wins whenever the referent is
  applied. On the azurerm backend (confirmed live: a missing blob reads as
  empty state) the book literal serves an unapplied referent and retires
  automatically on its first apply; on the local backend a missing state
  file still fails the reader before any fallback can run — apply the
  referent first. The state-probe drop filter is superseded for tokenised
  roots; operator-authored bindings are never wrapped and keep failing
  loudly.
- The lookup sidecar gains `id_by_key` (books written before the field
  decode both directions via parser inversion), HCL display comments
  resolve tokens to the same names raw IDs showed, and retiring a book
  that committed tokens still decode through is refused, naming the
  dependents.
- Tokens are a JSON-tfvars contract: HCL-format deployments keep literal
  IDs entirely (only JSON configs can be leaf-verified by the totality
  gate), and a token-shaped value appearing in an HCL config is refused at
  generation rather than passed through. Detection at JSON reference
  leaves is shape-based, so a token stranded by a pack referent
  reassignment (old prefix) is caught and refused, not skipped.
- Totality is enforced leaf-by-leaf at both ends: transform refuses to
  publish a minted token its own derivation did not cover, and gen-env
  refuses to render a root while any committed token lacks a covering
  binding — including on string-typed provider fields, where a module's
  type check could not have caught a leaked token. Committed tokens under
  a since-disabled `cross_state_references` are likewise a loud refusal,
  never a silent passthrough.

- `gen-env --state-aware` now probes the referent's *backend* (scratch
  `terraform init` + `state pull` keyed `<tenant>/<label>.tfstate`) instead of
  inspecting the local workspace for `<root>/.terraform`. In clean-workspace
  pipelines the old probe read every referent as absent, so `STATE_AWARE=1`
  silently rewrote every cross-state reference to its tfvars literal on every
  run; lookups now engage whenever the referent's state carries
  `infrawright_reference_ids`.
- `iw gen-env` accepts `--backend-config` (the same JSON file `iw plan`
  consumes) and `make gen-env` forwards `BACKEND_CONFIG=<file>` plus the
  pinned `--terraform` binary alongside `STATE_AWARE=1`.
- Local-backend tenants probe the state file beside each generated root and
  no longer resolve a terraform executable for `--state-aware`.

### Breaking changes

#### State-aware generation against azurerm requires BACKEND_CONFIG

`make gen-env STATE_AWARE=1` for a tenant on the azurerm backend now fails
with a usage error unless `BACKEND_CONFIG=<file>` is also passed; previously
it exited 0 while silently degrading every cross-state reference to a
literal. Pass the same backend file the pipeline already gives `make plan`.
Expect generated roots to flip literals to remote-state lookups on the first
run after each referent root is applied; the flip plans as a no-op because
the lookup resolves to the same ID.

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
