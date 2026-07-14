# Builder Handoff: Zscaler Adoption Follow-Up Runbook

## Intent

- Add a bounded work-machine runbook that distinguishes pack authority,
  generated-config policy, provider-state projection, stale outputs, and root
  topology without exposing tenant data.
- Let downstream execute only the portions available in its approved
  environment and return one normalized report.
- Keep provider behavior, pack metadata, Transform, Adopt, roots, plans, and
  Apply unchanged.

## Base / Head

- Base: `c8f9003c9336e0e6681eba10b5a0b09bfc53b40e`
- Head: the commit containing this handoff and
  `docs/provider-labs/zia-adoption-followup-runbook.md`
- Diff command: `git diff c8f9003c9336e0e6681eba10b5a0b09bfc53b40e..HEAD`

## Files Changed

- Files:
  - `docs/provider-labs/zia-adoption-followup-runbook.md`
  - `docs/review-handoffs/zscaler-adoption-followup-runbook.md`
- Files intentionally left untouched: all production code, pack metadata,
  generated catalogs, fixtures, tests, Make targets, and CI.

## Source Inputs Consulted

- Provider schemas: pinned ZIA schema entries for
  `zia_firewall_filtering_network_service` and `zia_browser_control_policy`.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A; no provider behavior claim is changed.
- Pack metadata: current ZIA and ZPA registries and override files at PR #218.
- Existing docs or design records: import Oracle, pack authoring, provider-lab
  conventions, adversarial-review workflow, PR #213 and #216 diffs.
- Other source evidence: user-supplied sanitized live-run screenshots. The
  referenced additive downstream commits are not available in this checkout.

## Generated Artifacts

- Reports: none generated; the runbook defines a future sanitized report.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: one local root-topology command was inspected and then
  discarded.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: maintainers can execute a precise read-only
  downstream diagnostic and classify each observed failure stage.
- Expected report/count/coverage changes: none until downstream returns the
  requested report.
- Expected generated-output changes: none.
- Expected no-op areas: all repository runtime behavior.

## Invariants Claimed

- Evidence must not be silently dropped: unavailable steps are reported as
  `NOT RUN`; ambiguous results remain `INCONCLUSIVE`.
- Generic matcher evidence must not outrank source-backed evidence: the runbook
  separates metadata presence from behavior classification.
- Source precedence/provenance must remain explicit: CLI, pack, profile,
  catalog, and deployment hashes are bound before comparison.
- Ambiguity must stay classified instead of being coerced to success: explicit
  result categories are defined.
- Provider-readiness counts must stay explainable: only field occurrence
  counts and resource-type topology are returned.
- Adoption safety invariants: no Terraform Apply, remote writes, credential
  printing, raw object output, state output, or plan output is authorized.

## Tests Run

- Commands:
  - `git diff --check`
  - Node CLI `roots` invocation against committed demo metadata.
  - `jq` sanitization of the resulting root topology.
  - Direct credential-free Node and Python calls covering optional string,
    Optional+Computed string, and dotted nested default omission using the
    pinned ZIA schema shapes.
- Relevant output summary: command syntax and sanitization worked; both engines
  removed all three default shapes when the override metadata was present.
- Tests not run and why: no live tenant run, because credentials and the
  downstream additive commits are not present in this environment.

## Known Deferrals

- Deferred work: recovering/restacking the downstream additive pack commits;
  any pack or engine remediation; ZPA root metadata changes.
- Reason it is safe to defer: this change is diagnostic documentation only and
  explicitly authorizes no runtime mutation.
- Follow-up owner or trigger: downstream report proving the first divergent
  stage, plus availability of the exact additive pack files.

## Review Focus

- Highest-risk files or paths:
  `docs/provider-labs/zia-adoption-followup-runbook.md`.
- Specific assumptions to attack:
  - commands use one explicit pack authority;
  - counts cannot print tenant values;
  - generated-HCL classification matches actual Oracle file lifecycle;
  - root selectors name materializable resources;
  - explicit groups are distinguished from automatic slug grouping;
  - partial execution cannot be misreported as success.
- Source evidence the reviewer should verify: CLI argument parsing, Oracle
  retained-file behavior, pack loader authority, roots output structure, and
  current ZIA/ZPA registry membership.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  missing `generated.tf.before-policy`, missing override files, stale output
  workspaces, explicit deployment groups, and raw API omission being mistaken
  for proof that `drop_if_default` executed.
