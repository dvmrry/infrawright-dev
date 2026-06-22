# source-evidence-ast

Experimental Go AST fact collector for Terraform provider source trees.

This is intentionally not wired into the Python readiness engine yet. It emits
JSON facts that the Python prototype can later consume once the fact contract
settles.

```bash
go run . --source-root /path/to/terraform-provider-example --out facts.json
```

The Python mapper can consume the facts as an experimental B-test path:

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
