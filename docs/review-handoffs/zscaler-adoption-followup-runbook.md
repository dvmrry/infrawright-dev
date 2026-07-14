# Builder Handoff: Zscaler Adoption Follow-Up Runbook

## Intent

- Add a bounded work-machine runbook that distinguishes pack authority,
  generated-config policy, provider-state projection, stale outputs, and root
  topology without exposing raw tenant values.
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

- Expected behavior change: maintainers can execute a precise downstream
  diagnostic and classify each observed failure stage. Adopt may execute only
  its mechanically verified import-only Apply against backend-free scratch
  local state; deployment Apply and remote mutation remain prohibited.
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
  counts and resource-type topology are returned, and counts are identified as
  tenant-derived evidence for the approved channel.
- Adoption safety invariants: no deployment Apply, remote writes, credential
  printing, raw object output, state output, or plan output is authorized.
  The Oracle's exact import-only scratch Apply must be explicitly acknowledged.
- Output containment: path-relocated deployment copies differ only in overlay
  and module_dir, and every output directory must resolve physically within
  one of four disjoint private lanes.

## Tests Run

- Commands:
  - `git diff --check`
  - Node CLI `roots` invocation against committed demo metadata.
  - `jq` sanitization of the resulting root topology.
  - Tenant-free full-product `resources` and `roots` commands for ZIA and
    ZPA, including exact-registry source-less classification.
  - Physical output-containment command design against the deployment path
    semantics.
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
  - private logs prevent raw diagnostics from reaching reportable output;
  - relocated lanes are contained, disjoint, and semantics-preserving;
  - counts cannot print raw tenant values;
  - generated-HCL classification matches actual Oracle file lifecycle;
  - successful dispatch requires target-before evidence, not mere absence;
  - tenant-free product selectors resolve active topology;
  - explicit groups are distinguished from automatic slug grouping;
  - exact registry evidence identifies source-less members;
  - materialized module/variable comparison is required for stale causality;
  - partial execution cannot be misreported as success.
- Source evidence the reviewer should verify: CLI argument parsing, Oracle
  retained-file behavior, pack loader authority, roots output structure, and
  current ZIA/ZPA registry membership.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  missing `generated.tf.before-policy`, unrelated generated-config edits,
  missing override files, HCL tfvars, path or symlink escape, stale output
  workspaces, explicit deployment groups, and raw API omission being mistaken
  for proof that `drop_if_default` executed.

## Initial Review And Remediation

- Initial verdict: Request changes.
- Accepted findings:
  - distinguish deployment Apply from the Oracle's local import-only Apply;
  - capture tenant-bearing diagnostics privately and omit tenant from topology;
  - prove relocated output lanes are contained and disjoint;
  - require positive target-before evidence before claiming policy dispatch;
  - derive source-less membership from the bound registries and compare an
    actual materialized root before assigning stale-root causality;
  - require JSON tfvars for jq counts and identify counts as tenant-derived.
- Patch recheck findings:
  - corrected the retained work-directory prefix to
    `infrawright-oracle-*`;
  - made semantic and physical containment fail fast;
  - bound Adopt to `per-resource-type` and `applied-state`;
  - replaced filename-bearing `shasum` output with fixed labels and extracted
    digests.
- Remediation: all accepted findings are addressed in the runbook. No runtime,
  pack, provider, schema, fixture, or generated artifact changed.
