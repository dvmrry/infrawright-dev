# Operational Go runtime

Status: Go is the sole current-tree runtime and command authority. The former
Node implementation was archived on 2026-07-22; see
[the archive record](archive/node-runtime-archive.md).

Infrawright ships one Go CLI, built locally as `dist/iw`. Packs, profiles,
provider schemas, deployment metadata, and Terraform/OpenTofu remain external
inputs selected by the caller.

```text
packs + profiles + schemas + deployment
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

All root Make targets route through `IW ?= dist/iw`. There is no runtime
fallback, compatibility override, package-manager build, or second authoring
binary. The command inventory includes:

- Pack and metadata: `check-pack`, `check-pack-set`, `deployment`, `resources`,
  `root-catalog`.
- Generation and collection: `modules`, `fetch`, `fetch-diag`, `transform`,
  `adopt`, `gen-env`.
- State lifecycle: `roots`, `scope-paths`, `plan-roots`, `stage-imports`,
  `unstage-imports`, `plan`, `clean-plans`, `assert-clean`,
  `assert-adoptable`, `apply`.
- Authoring: `reconcile`, `openapi-map`, `source-operation-map`,
  `source-evidence-eval`, `provider-probe`, `transform-adopt-parity`.

`INFRAWRIGHT_PACKAGE_ROOT` may explicitly select runtime data. Otherwise `iw`
walks upward from its executable until it finds `packs/full.packset.json`.
Pack profiles live only at `packs/*.packset.json`.

## Build and verification

```sh
make dist/iw
make check
make check-all
make check-core
```

`make check` validates the active distribution, runs the complete Go suite,
and runs the archive tripwire. CI also repeats distribution checks with failing
`node` and `npm` interceptors first on `PATH`; invoking either fails the job.
The pack-profile tests physically reduce the pack root for every committed
profile and verify both loading and mechanical derivability.

Historical differential tests are not part of the normal gate. They run only
when `INFRAWRIGHT_FROZEN_NODE_ORACLE` explicitly names the frozen v1 bundle.
The expected bundle SHA-256 is recorded with the archive evidence. This opt-in
resurrection path does not restore a build, release, or rollback lane.

## Platform and Terraform support

- Linux is the production-supported Terraform execution platform.
- macOS is supported for development and testing.
- Windows Terraform execution is unsupported and rejected before spawn.
- Terraform/OpenTofu remains required for provider, formatting, import, plan,
  and Apply operations.

`plan --save` creates the bound `tfplan` and `tfplan.sources` pair. Assessment
and Apply recheck that exact pair; Apply executes the saved plan rather than
replanning. Credential-free repository tests do not claim live-provider,
live-backend, or deployment-Apply qualification.
