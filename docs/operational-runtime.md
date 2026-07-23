# Operational runtime

Infrawright ships one Go CLI, built locally as `dist/iw`. Packs, profiles,
provider schemas, deployment metadata, and Terraform/OpenTofu are external
inputs selected by the caller.

```text
packs + profile + schemas + deployment
                    |
                  dist/iw
                    |
fetch / transform / adopt / modules / roots / staging
                    |
       plan / assessment / exact-plan Apply
                    |
                 Terraform
```

## Command authority

All root Make targets route through `IW ?= dist/iw`. The command inventory is:

- Pack and metadata: `check-pack`, `check-pack-set`, `deployment`, `resources`.
- Generation and collection: `modules`, `fetch`, `fetch-diag`, `transform`,
  `adopt`, `gen-env`.
- State lifecycle: `roots`, `scope-paths`, `plan-roots`, `stage-imports`,
  `unstage-imports`, `plan`, `clean-plans`, `assert-clean`,
  `assert-adoptable`, `apply`.
- Authoring: `reconcile`, `openapi-map`, `source-operation-map`,
  `source-evidence-eval`, `provider-probe`, `transform-adopt-parity`.

`INFRAWRIGHT_PACKAGE_ROOT` may explicitly select runtime data. Otherwise `iw`
walks upward from its executable until it finds `packs/full.packset.json`.
Pack profiles live only at `packs/*.packset.json`; packs and profiles are the
sole resource-metadata authority.

## Build and verification

```sh
make dist/iw
make check
make check-all
make check-core
```

`make check` validates the selected distribution, runs the Go suite, and
checks formatting and generated artifacts. The pack-profile tests physically
reduce the pack root for every committed profile and verify both loading and
mechanical derivability.

## Platform and Terraform support

- Linux is the production-supported Terraform execution platform.
- macOS is supported for development and testing.
- Windows Terraform execution is unsupported and rejected before spawn.
- Terraform/OpenTofu is required for provider, formatting, import, plan, and
  Apply operations.

`plan --save` creates the bound `tfplan` and `tfplan.sources` pair. Assessment
and Apply recheck that exact pair; Apply executes the saved plan rather than
replanning. Credential-free repository tests do not claim live-provider,
live-backend, or deployment-Apply qualification.
