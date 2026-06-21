# infrawright

**Driftless infrastructure management across providers.**

infrawright reads a live provider's resources and emits modular Terraform / OpenTofu
that **imports what already exists without recreating it** — typed `map(object)`
variables, native `import {}` blocks, and **identity-keyed `moved {}` reconciliation**
so console renames and key changes resolve as *moves*, never destroy/recreate. A clean
`terraform plan` against your real state is the contract.

The engine carries **zero vendor knowledge**. Each provider is a **pack** under
`packs/<name>/` supplying its own collector, registry, overrides, and schema — the same
engine drives any provider. Zscaler is the reference pack (byte-identical, zero-drift
baseline); Cloudflare is next.

## Why it's safe to point at production

The fragile part of adopting IaC over live infrastructure is the Terraform **state** — a
resource key that shifts between runs reads as *destroy + recreate*, which on a real
tenant is an outage, not a diff. infrawright is built to keep the state stable:

- **Stable identity-derived keys** — the same live resource maps to the same `["key"]`
  every run, so its state address never moves.
- **Automatic `moved {}` reconciliation** — when a key *does* change, it's emitted as a
  move, not a recreate.
- **Deterministic, verified output** — `make check` proves the generated config,
  imports, and modules are byte-stable.

The acceptance bar isn't "0 to change" — it's **0 to destroy, 0 to create** after import.

## Layout

| Path | Role |
|------|------|
| `engine/` | vendor-agnostic: transform → modular TF + `import` + `moved` reconciliation |
| `collectors/rest/` | shared token-REST fetch base (provider collectors lean on it) |
| `packs/<name>/` | a provider bundle: `pack.json` + `registry.json` + `overrides/` + `schemas/` + collector |
| `[<overlay>/]config/<tenant>/<resource_type>.auto.tfvars.json` | generated tenant config |
| `[<overlay>/]imports/<tenant>/<resource_type>_imports.tf` | generated import blocks |
| `[<overlay>/]envs/<tenant>/<resource_type>/` | generated per-resource Terraform roots |

There is one generated output layout. `overlay` is an optional free-form prefix
owned by the adopter; the demo tenant uses none, so `make demo` writes at repo
root under `config/demo` and `imports/demo`.

## Quickstart

```
make check      # full gate: unit tests + byte-identical demo + module reproduction
make demo       # materialize the demo tenant (no credentials needed)
```

## Status

**0.1 — Zscaler** (`zia` · `zpa` · `zcc`): reproduces its demo tenant byte-identically
through the agnosticized engine; provider-first packs; identity-keyed reconciliation.
Cloudflare (tier-1 resources) is in progress on a branch.

## License

[FSL-1.1-Apache-2.0](LICENSE) — source-available; converts to Apache 2.0 two years after
each release.
