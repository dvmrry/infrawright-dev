# State topology

Each generated Terraform root represents exactly one resource type.

For tenant `<tenant>` and resource `<resource_type>`, the root is:

```text
[<overlay>/]envs/<tenant>/<resource_type>/
```

The root label and its only member are both `<resource_type>`. Resource
selection may accept a provider/product selector, but selection expands to
individual singleton roots; it does not create a grouped state unit.

Pack `registry.json` entries must not define `slug_group`. Deployment root
entries reject these retired fields: `strategy`, `groups`, and
`bind_references`. The only supported provider-root option is
`cross_state_references`, a boolean that defaults to enabled; setting it to
`false` disables generated cross-state bindings. Other fields, including
`root_slug`, fail validation as unknown keys.

The `roots`, `scope-paths`, `plan-roots`, `gen-env`, plan, assessment, and Apply
commands all consume this topology. Packs and the active profile are the sole
resource authority.
