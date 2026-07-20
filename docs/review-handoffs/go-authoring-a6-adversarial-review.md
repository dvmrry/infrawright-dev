# Go authoring A6: adversarial review record

This records the outcome of the fresh, read-only Codex adversarial review of
the A6 working tree based on `6c31ebf6b7a4b73fe1f1867df8385deb8bc85480`.
The reviewer did not edit files or implement fixes.

## Initial Blocking Findings

1. V2 source-operation warnings were emitted before complete-set publication.
   A publication failure could therefore expose a diagnostic for a bundle that
   was never committed. Required change: publish first, then emit the sealed
   status warning and evaluate the conflict decision.
2. The legacy source-operation and source-evidence command adapters did not
   preserve Node's invalid-argument validation priority. Some invalid calls
   could read an earlier file before returning the usage error. Required
   change: validate the whole legacy argument surface before domain reads.
3. Qualified provider-probe validated its required work root only after the
   pipeline ran, and a fresh root was not created. Required change: preflight
   the recipe mode and work root, provide a private work directory, and fail
   closed if the recipe mode changes before execution.

## Non-Blocking Risks From Initial Review

- Broad help normalization could conceal authoring flag drift.
- The command-level differential corpus did not directly prove the optional
  provider-probe artifact's present-to-absent and failed-replacement lifecycle.

## Consolidated Remediation

- Source-bundle publication now precedes warnings and conflict decisions.
  Publisher-failure tests cover unavailable and degraded status.
- Legacy source commands use explicit up-front validators. Frozen-Node
  bad-input differential cases and direct no-read tests pin usage priority.
- `providerprobe.InspectRecipeMode` and `RunOptions.ExpectedMode` provide a
  categorical fail-closed check. The CLI requires the qualified work root
  before the run and creates/verifies a private mode-0700 work directory.
- The exact authoring help block and all six Go-backed Make targets are pinned
  by tests, while the existing operator help differential cases remain active.
- A real command-level test proves optional artifact creation, removal on the
  next successful publication, and preservation after a failed replacement.

## Source and Artifact Review

- Diff and builder handoff inspected against the frozen Node CLI and authoring
  contracts.
- No provider schemas, OpenAPI documents, source evidence, fixtures, snapshots,
  or checked-in generated artifacts changed.
- The publisher and command tests use fixture-local paths only. No network,
  credentials, live provider, Terraform, or infrastructure operation ran or
  was reachable.

## Recheck Verdict

The fresh reviewer rechecked every blocking finding and both non-blocking test
gaps, ran the targeted tests, reported **no findings**, and returned
**Approve**.

This approval closes the local A6 implementation review. It does not perform
the final Node freeze/tag or formal Go product-authority transfer, which remain
pending the user's external Opus, GPT-5.6 Pro, and Fable review sequence.
