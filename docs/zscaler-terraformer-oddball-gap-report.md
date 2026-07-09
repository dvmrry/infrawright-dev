# Zscaler Terraformer Oddball Gap Report

Prepared: 2026-07-09

External source inspected: `zscaler/zscaler-terraformer` v2.1.17 at commit
[`8e117d34bc00a2ce47eadc7ea12aa998281e3f4f`](https://github.com/zscaler/zscaler-terraformer/tree/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f).

## Executive Summary

`zscaler-terraformer` does not address all of the provider-side edge cases this
repo has been triaging. It is still useful prior art: several ZIA/ZPA oddities
that showed up locally also appear there as explicit hand-written workarounds.

The safe path is to treat `zscaler-terraformer` as an evidence catalog, not as a
drop-in implementation source. Its fixes can help us avoid rediscovering known
Zscaler behavior, but every borrowed rule still needs local provider/source
evidence and a regression test before it changes generated output.

The biggest split is:

- Covered upstream: selected ZIA/ZPA one-off transformations, default/predefined
  skips, `cbi_profile`, URL filtering defaults, some country-prefix cleanup,
  ZPA list-ID blocks, and several data-source/reference bindings.
- Not covered upstream: ZCC-specific behavior, `signingCertId` to
  `enrollment_cert_id`, broad `microtenantId == "0"` pruning, HTML
  unescape/escape policy, generic provider-state oracle policy, and the
  projection/readback contracts this repo has been building.

## Scope

This report compares the upstream terraformer behavior against the local classes
of Zscaler provider weirdness represented in `tests/test_transform.py` and the
import-oracle policy described in [Import Oracle Adoption](import-oracle.md).

This is not a recommendation to vendor or copy `zscaler-terraformer` code. It
uses the upstream repository as prior art for targeted follow-up work.

## Coverage Matrix

| Edge case class | Upstream status | Evidence | Recommended use |
|---|---:|---|---|
| `zia_url_filtering_rules` / `zia_cloud_app_control_rule` `cbi_profile` omission and forced fields | Covered | Upstream has a resource-specific `cbi_profile` block writer that skips empty blocks and writes `id`, `name`, and `url` when present: [`nesting.go#L69-L115`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L69-L115). | Good candidate for parity tests and source-backed policy comparison. |
| Empty/missing `zia_url_filtering_rules.url_categories` readback as `["ANY"]` | Covered | Upstream injects `["ANY"]` for this resource only: [`generate.go#L2614-L2634`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2614-L2634). | Good candidate for test-only confirmation before any behavior change. |
| Default `zpa_microtenant_controller` object | Covered for controller resource only | Import filters `ID != "0"`: [`import.go#L1077-L1100`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1077-L1100). Generate filters `Name != "Default"`: [`generate.go#L1360-L1379`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1360-L1379). | Useful corroboration for the controller skip only. Does not prove broad nested `microtenantId == "0"` pruning. |
| `microtenantId == "0"` on arbitrary ZPA resources and nested operands | Not covered | Upstream only filters the microtenant controller list and has CLI/provider microtenant configuration. No general nested scrubber was found. Local tests cover top-level and nested default stubs. | Keep our own rule and evidence path. Do not infer broad behavior from upstream. |
| `zia_firewall_dns_rule` predefined/system rules with non-positive order | Covered for DNS rules | Upstream has a configured `order <= 0` skip for `zia_firewall_dns_rule`: [`helpers.go#L82-L136`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L82-L136). | Strong candidate for a local skip matrix entry, with provider/source verification. |
| Other default/predefined rule skips | Partial | Upstream hardcodes several name-based skips, including SSL inspection defaults: [`import.go#L1760-L1784`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1760-L1784), [`generate.go#L1859-L1880`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1859-L1880); ZIA forwarding rule defaults: [`import.go#L1690-L1700`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1690-L1700); ZTW forwarding defaults: [`generate.go#L2407-L2418`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2407-L2418). | Mine as candidate data, but require local provider evidence because name lists can drift by product/version. |
| `zia_cloud_app_control_rule` rule type enumeration | Covered | Upstream fetches exact API rule types with `GetByRuleType`: [`import.go#L1243-L1269`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1243-L1269), [`generate.go#L1486-L1512`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1486-L1512). | Good prior art for fetch/list behavior and fixture coverage. |
| Country values with `COUNTRY_` prefix | Partial | Upstream strips the prefix for selected fields and rules: [`nesting.go#L567-L575`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L567-L575), [`import.go#L1695-L1698`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1695-L1698). | Useful signal, but local behavior also needs canonical sorting/set handling. |
| ZPA `server_groups`, `app_server_groups`, and similar list-ID blocks | Covered structurally | Upstream emits list-ID blocks from raw `serverGroups` / `appServerGroups`: [`nesting.go#L287-L335`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L287-L335), [`helpers.go#L454-L494`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L454-L494). | Good source of candidate bindings; verify with provider schema and local fixtures before adopting. |
| ZPA data-source/reference bindings | Partial | Upstream maps common ZPA fields to data sources: [`datasource_processor.go#L63-L90`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/datasource_processor.go#L63-L90), and gives imported resources priority over data sources: [`datasource_processor.go#L1001-L1018`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/datasource_processor.go#L1001-L1018). | Useful prior art, but inspect inconsistencies before adopting. For example, one helper maps `appServerGroups` differently than the data-source processor. |
| ZPA policy operands for SCIM/SAML/posture/trusted network/machine group | Covered as reference-generation prior art | Upstream has object-type-specific operand mapping: [`zpa_policy_processor.go#L35-L82`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/zpa_policy_processor.go#L35-L82), and scans policy files for those operands: [`zpa_policy_processor.go#L138-L214`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/zpa_policy_processor.go#L138-L214). | Good candidate for a structured local binding inventory. Avoid copying regex-HCL processing. |
| Nested computed/read-only attributes such as `appId`, `portal`, `hidden`, `certificate_name`, and `zia_url_categories.val` | Partial | Upstream skips several nested attributes: [`nesting.go#L529-L534`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L529-L534). | Candidate evidence for local omit rules, but schema and provider-state evidence should decide the final set. |
| `signingCertId` to `enrollment_cert_id` | Not covered | No specific upstream mapping was found. A generic lower-camel conversion would not turn `signingCertId` into `enrollmentCertId`. Local tests cover both API spellings. | Keep local handling. This still needs our own regression guard. |
| ZPA/ZCC HTML unescape and `custom_msg` escape policy | Not covered | Upstream formats selected multiline string fields as heredocs, but no equivalent HTML unescape or `custom_msg` escape policy was found: [`generate.go#L2597-L2602`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2597-L2602). | Keep local policy. Do not use upstream as support for this class. |
| ZCC `failopen` inverted booleans | Not covered | The inspected upstream tree has no `zcc_` resource support and no `failopen` handling. | Keep local handling and tests. |
| Terraform `-generate-config-out`, provider-state-as-oracle, `projection_fill`, drift classification, and moved/stable identity policy | Not covered as a general architecture | Upstream generates HCL directly from API/provider helper logic. It does not implement this repo's import-oracle contract in [Import Oracle Adoption](import-oracle.md). | Keep this repo's oracle model. Upstream can only inform individual Zscaler policy entries. |

## Risks In Upstream Prior Art

Upstream fixes are useful, but several are implemented as string generation or
regex post-processing over `.tf` files. That is not the shape we want to import
directly into this repo's source-backed evidence and projection pipeline.

Specific caution points:

- Some rules are hardcoded by product name, rule name, or field name. Those are
  version-sensitive and should become explicit local policy entries with
  regression tests.
- Reference replacement is partly file-text based. Local work should prefer
  structured state, schema, and HCL handling where available.
- At least one mapping deserves review before use: `appServerGroups` maps to
  `zpa_server_group` in the data-source processor, while another helper maps it
  to `zpa_application_server`.
- Upstream release notes and code comments are useful corroboration, but they
  are not provider-source evidence by themselves.

## Safe Plan

1. Build a local prior-art inventory.
   Record each upstream workaround with resource type, field path, local edge
   case class, upstream link, local test coverage, and adoption status:
   `adopt-candidate`, `compare-only`, or `reject/no-match`.

2. Start with test-only parity work.
   Add or extend fixtures that assert the local behavior we already believe is
   correct, without changing generated output. Good first targets are
   `cbi_profile`, `url_categories = ["ANY"]`, non-positive DNS rule order,
   exact cloud-app rule type enumeration, and country-prefix normalization.

3. Promote only source-backed, provider-backed rules.
   A rule can move from inventory to behavior change only when it has all of:
   upstream prior-art link, provider source/readback or live oracle evidence,
   local fixture/regression coverage, and an explicit reason in policy or code.

4. Keep high-risk local-only cases out of the shortcut lane.
   Do not rely on upstream for `signingCertId`, HTML escaping, ZCC failopen,
   broad microtenant stub pruning, projection oracle policy, moved/stable
   identity reconciliation, or any behavior that can silently drop evidence.

5. Use adversarial review for behavior changes.
   Any implementation that changes provider source-operation mapping,
   provider-readiness reporting, generated evidence, projection behavior,
   provenance, ambiguity classification, or adapter-specific edge cases falls
   under the repository's adversarial review workflow.

## Work Packages

| Package | Goal | Safe first output | Risk level |
|---|---|---|---:|
| WP1: Prior-art inventory | Convert this report into a resource/field checklist. | One committed markdown or JSON inventory, no behavior changes. | Low |
| WP2: Upstream parity tests | Lock down local behavior for cases upstream also solved. | Focused unit tests and fixtures for already-known transforms. | Low |
| WP3: Reference binding comparison | Compare local binding logic against upstream data-source/resource priority. | Report mismatches and candidate mappings; do not auto-rewrite bindings. | Medium |
| WP4: Default/predefined skip matrix | Enumerate rule skips by resource, predicate, and source of evidence. | Matrix with upstream links plus local provider/source evidence requirements. | Medium |
| WP5: Explicit no-match ledger | Document cases upstream does not cover so they are not re-triaged repeatedly. | Entries for ZCC failopen, HTML, signing cert rename, broad microtenant pruning, and oracle policy. | Low |
| WP6: Candidate behavior PRs | Adopt one or two low-risk, well-evidenced rules at a time. | Small PRs with tests and adversarial-review handoff when required. | Medium to high |

## Acceptance Criteria For Any Borrowed Fix

- The change has a pinned upstream link and a local rationale.
- The change has local provider source, provider readback, or live oracle
  evidence; upstream code alone is not enough.
- The rule is scoped to exact resource and field paths. No broad string
  matching unless there is no structured alternative and the risk is documented.
- The change has a regression test that would fail if the provider oddity
  reappears.
- Generated-output or source-evidence changes run the relevant checks, such as
  `make demo-contract` for demo artifacts or targeted provider/adoption tests
  for live-backed behavior.
- Review-required changes use the repository adversarial review workflow before
  merge.

## Immediate Next Move

The safest next action is WP1 plus WP2: create the inventory and add test-only
coverage for the upstream-confirmed cases. That lets us get ahead of repeated
triage without changing behavior based on third-party assumptions.

After that, pick one low-risk covered case, such as `cbi_profile` or
`url_categories = ["ANY"]`, and run it through the full local evidence path
before making a behavior PR.
