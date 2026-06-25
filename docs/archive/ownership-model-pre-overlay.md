# Archived: Generated Control-Plane Ownership Model

This note is intentionally archived and no longer describes the active repo
layout. It predated the overlay/module-dir migration and used stale examples
such as root `config/`, root `imports/`, root `envs/`, provider-subdirectory
config paths, and committed demo module trees.

Current contracts live in:

- [Repository Surface](../repo-surface.md)
- [README layout](../../README.md#layout)
- [Import Oracle Adoption](../import-oracle.md)

Do not use this archived note as implementation guidance. It is kept only to
preserve the old design decision that generated provider-control-plane output
and hand-authored cloud infrastructure should not share Terraform state.
