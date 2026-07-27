# Provider-neutral reference fixture

This fixture exercises the committed pack-layout, module-generation, and
cross-state environment-generation paths without naming or loading a real
provider. `registry.invalid/infrawright/fixture` is deliberately
non-installable; tests must not run provider initialization against it.

The fixture declares one relationship:

```text
fixture_consumer.source_id -> fixture_source
```

The regression asks Terraform itself to parse the generated module and
environment HCL with `terraform fmt -check`. A separate runtime-written
`terraform_data` configuration is initialized, validated, and planned to
qualify providerless Terraform execution in the normal Go test suite. That
execution gate uses only `terraform.io/builtin/terraform`; it is not a topology
oracle for this fixture.

The Terraform-only checks follow the package's existing executable convention:
local runs skip them when neither `TF` nor `terraform` on `PATH` is available.
The primary CI `make check` job installs its pinned Terraform version before
running the complete Go suite, so both checks execute in that promotion gate.
