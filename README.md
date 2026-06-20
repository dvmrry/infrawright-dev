# infrawright

**Driftless infrastructure management across providers.**

infrawright turns a live provider's resources into modular Terraform / OpenTofu —
typed `map(object)` variables, native `import {}` blocks, and **identity-keyed
`moved {}` reconciliation** that survives console renames without destroy/recreate.

The engine carries **zero vendor knowledge**. Each provider is a **pack** under
`packs/<name>/` supplying its own collector, registry, overrides, and schema.
Zscaler is the reference pack (zero-drift baseline); Cloudflare is next.

## Layout

| Path | Role |
|------|------|
| `engine/` | vendor-agnostic: transform → modular TF + `import` + `moved` reconciliation |
| `collectors/rest/` | shared token-REST fetch base (provider collectors lean on it) |
| `packs/<name>/` | a provider bundle: `pack.json` manifest + `registry.json` + `overrides/` + `schemas/` + collector |

## Status

Phase 1 — Zscaler pack reproduces its demo tenant **byte-identically** through the
agnosticized engine (`make demo`). Phase 2 — Cloudflare pack (`nested_type` / `map`
nesting engine support + the CF collector).

## License

[FSL-1.1-Apache-2.0](LICENSE) — source-available; converts to Apache 2.0 two years
after each release.
