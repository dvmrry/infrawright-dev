# source-evidence-ast

Go AST fact collector for Terraform provider source trees.

This tool emits JSON facts consumed by the provider-readiness source evidence
workflow. `make source-evidence-eval` runs it automatically when
`SOURCE_FACTS=<facts.json>` is not supplied; `engine.source_operation_map` can
also consume a facts file directly.

```bash
go run . --source-root /path/to/terraform-provider-example --out facts.json
```

The Python mapper can consume the facts directly:

```bash
python3 -m engine.source_operation_map \
  --schema provider-schema.json \
  --openapi openapi.json \
  --source-root /path/to/terraform-provider-example \
  --provider-source registry.terraform.io/example/example \
  --resource-prefix example \
  --source-facts facts.json \
  --source-facts-compare source-facts-compare.json \
  --out source-facts-registry.json
```

Current facts:

- Go files, packages, imports, and functions
- Terraform-style resource registrations
- Terraform resource type string references
- Go identifier references
- read callback fields such as `ReadContext`
- selector calls such as `client.Repositories.Get`
- imported package calls such as `locationmanagement.Get`
- raw REST calls such as `client.NewRequest("GET", fmt.Sprintf(...))`
- minimal `go.mod` module and require metadata
