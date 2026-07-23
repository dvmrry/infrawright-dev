# Dynamic Schema Diagnostics

Provider labs sometimes expose provider-state paths that are real enough to
affect adoption, but not statically represented as ordinary Terraform schema
members. Cloudflare showed this with paths such as `data.flags` and
`assets.config.run_worker_first`.

Evaluate new cases through provider schema, provider-state projection, and
saved-plan evidence before changing pack metadata.

You can also classify paths from a JSON fixture:

```json
{
  "cloudflare_workers_script": [
    "assets.config.run_worker_first"
  ]
}
```

Or classify `projection_omit` paths from a drift policy:

```json
{
  "version": 1,
  "resource_types": {
    "cloudflare_workers_script": {
      "projection_omit": [
        {
          "path": "assets.config.run_worker_first",
          "reason": "Provider dynamic value observed during lab.",
          "approved_by": "provider-lab",
          "ticket": "LAB-1"
        }
      ]
    }
  }
}
```

The retired report was diagnostic only. It did not project provider state,
render HCL, alter drift policy, or run Terraform/OpenTofu.

Important statuses:

- `schema_known`: the path resolves to a normal settable Terraform schema member.
- `pack_schema_gap`: the path descends into a dynamic value, map key, or open
  object/member that the provider schema cannot enumerate as ordinary inputs.
- `schema_computed_only`: the path starts at a provider-computed member that
  adoption should not project as config.
- `unknown_schema_path`: the path does not match the provider schema.

`pack_schema_gap` is the useful lab signal. It means the provider pack needs a
deliberate strategy for that path; it does not mean the core adoption engine
should automatically keep or drop the value.
