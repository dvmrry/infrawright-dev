# ADR 0001: Pipeline-Owned Publication Roots

- Status: Superseded
- Date: 2026-07-11
- Superseded: 2026-07-16

## Historical context

This decision governed the retired ZCC process-host publication architecture.
That architecture used a root-level publisher guard around candidate,
materialization, receipt, refresh, and acknowledgement operations. A repository
and downstream-consumer inventory found no callers, so the process host and its
publication protocols were removed together.

The old operation names, lock-file contract, and materialization output-root
authority are not supported runtime interfaces. Historical review handoffs
retain the detailed design at their reviewed commits.

## Current decision

The supported runtime is the generic Node 24 `iw` CLI distributed as
`dist/infrawright-cli.mjs` with its SHA-256 checksum. Persistent operational
commands write directly to the deployment-selected workspace; there is no
cross-process publisher lock in the generic runtime.

Pipeline jobs must therefore own distinct physical workspaces and serialize
persistent writers within each workspace. Two runs of the same branch are safe
only when their checkout, deployment overlay, generated artifacts, environment
roots, saved plans, and Terraform working directories are disjoint. Read-only
provider qualification does not relax that ownership rule.

Use the current [Integration Validation Runbook](../integration-validation.md)
and [Operational Node Runtime](../operational-runtime.md) for supported commands
and qualification boundaries.
