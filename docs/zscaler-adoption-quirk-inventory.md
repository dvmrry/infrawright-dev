# Zscaler Adoption Quirk Inventory (WP1)

Prepared: 2026-07-09.

This is a behavior-neutral inventory that turns
[Zscaler Terraformer Oddball Gap Report](zscaler-terraformer-oddball-gap-report.md)
into mined follow-up data. It compares:

- local transform-path overrides in `packs/{zia,zpa,zcc}/overrides`;
- local adopt/oracle projection behavior in
  [engine/state_project.py](../engine/state_project.py#L23-L45);
- pinned `zscaler/zscaler-terraformer` v2.1.17 at commit
  [`8e117d34bc00a2ce47eadc7ea12aa998281e3f4f`](https://github.com/zscaler/zscaler-terraformer/tree/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f).

No generated output, pack behavior, or runtime code changes are implied by this
file. A rule moves from inventory to behavior only with pinned upstream evidence,
local provider/source/oracle evidence, exact path scope, regression coverage, and
adversarial review when the repository policy requires it.

## Core Finding

The local transform path already has a real Zscaler quirk catalog:

- 20 ZIA resources with semantic transform keys.
- 7 ZPA resources with semantic transform keys.
- 3 ZCC resources with semantic transform keys.

Those transformations run through
[engine/transform.py](../engine/transform.py#L445-L533). The adopt/oracle path
projects provider-observed state and then applies only projection policy:
`projection_sync`, `projection_fill`, and `projection_omit_if`
([engine/state_project.py](../engine/state_project.py#L199-L202)).

That means every semantic transform override is a candidate oracle-path
question, but not every transform override is an oracle-path gap. A clean first
plan is structural rather than proof of normalization: `project_item` copies
each present provider-state input into generated config
([engine/state_project.py](../engine/state_project.py#L48-L83)), so config and
post-Read state initially agree whether the provider stored a normalized or a
raw value.

Use this four-class taxonomy when that mirror can still be wrong:

1. Semantic projection mismatch: provider state is internally consistent but
   is not the intended config representation. `zia_dlp_engines.name` is the
   current concrete gate: provider Read stores raw `name`, while transform maps
   `predefinedEngineName` to required `name`.
2. Validation asymmetry: provider Read stores a value that explicit config
   rejects. The current lab gate is `size_quota=0` / `time_quota=0`: both rule
   resources validate explicit non-zero ranges while transform omits zero.
3. Refresh/apply instability: first plan is clean, but a later apply or refresh
   can rewrite mirrored values, ordering, or back-references. These require
   multi-apply tests rather than first-plan evidence.
4. Representational divergence: transform and adopt can be plan-equivalent but
   emit different tfvars bytes (for example omitted versus explicit zero or
   empty values), creating downstream delivery/drift churn.

Read omissions and read sentinels remain useful projection-policy mechanisms,
but they are not an exhaustive taxonomy of oracle gaps.

## Local Semantic Override Inventory

This table lists resources with semantic transform keys. It intentionally omits
pure metadata such as `sample`, `import_id`, `key_field`, `ranges`, and
`acknowledged_drops`.

| Resource | Semantic transforms | Oracle-path concern |
|---|---|---|
| `zia_url_filtering_rules` | `defaults.url_categories=["ANY"]`, `divide.size_quota=1024`, `drop_if_default.size_quota=0`, `drop_if_default.time_quota=0`, `skip_if.predefined=true`, `strip_prefix.source_countries="COUNTRY_"` ([source](../packs/zia/overrides/zia_url_filtering_rules.json#L30-L69)) | High: provider Read normalizes quota units and `ANY`, but explicit-zero validation, sub-1024 KB truncation, and transform/adopt byte parity remain gates. |
| `zia_cloud_app_control_rule` | `divide.size_quota=1024`, `drop_if_default.size_quota=0`, `drop_if_default.time_quota=0`, `skip_if.default_rule=true`, `skip_if.predefined=true` ([source](../packs/zia/overrides/zia_cloud_app_control_rule.json#L29-L58)) | High: same explicit-zero validation and representational-divergence gates as URL filtering. |
| `zcc_failopen_policy` | `invert_bool` for `active`, `enable_web_sec_on_proxy_unreachable`, `enable_web_sec_on_tunnel_failure`, `enable_captive_portal_detection`, `enable_fail_open` ([source](../packs/zcc/overrides/zcc_failopen_policy.json#L9-L15)) | Medium/high: the five named fields normalize symmetrically; `enable_strict_enforcement_prompt` still needs an out-of-domain-value gate because transform treats non-zero as true while provider tests equality to `1`. |
| `zpa_application_segment` | `drop_if_default.microtenant_id="0"`, `drop_if_default.policy_style="NONE"`, `value_map.policy_style.NONE=false`, `value_map.policy_style.DUAL_POLICY_EVAL=true`, `merge_blocks.server_groups` ([source](../packs/zpa/overrides/zpa_application_segment.json#L37-L50)) | Medium/high: `policy_style` normalizes, `microtenant_id` is mirrored raw, and `drop_if_default.policy_style="NONE"` is dead because `value_map` runs first. Later back-reference/order behavior remains a multi-apply concern. |
| `zpa_policy_access_rule` | nested `drop_if_default` for `microtenant_id`, drops operand drift fields, `html_escape_fields.custom_msg`, `merge_blocks.app_server_groups`, `merge_blocks.app_connector_groups`, `skip_if.default_rule=true` ([source](../packs/zpa/overrides/zpa_policy_access_rule.json#L72-L95)) | Medium/high: nested microtenant/default pruning and custom HTML behavior are local-only. |
| `zpa_app_connector_group` | `drop_if_default.microtenant_id="0"`, `no_html_unescape`, `renames.signing_cert_id=enrollment_cert_id` ([source](../packs/zpa/overrides/zpa_app_connector_group.json#L25-L42)) | Medium: signing cert rename is local-only and already regression-worthy. |
| `zcc_trusted_network` | 7 API-to-schema renames plus CSV splitting for 7 list fields ([source](../packs/zcc/overrides/zcc_trusted_network.json#L11-L28)) | Medium: renames align with schema, but `split_csv` is semantic. |
| `zpa_server_group` | `drop_if_default.microtenant_id="0"`, `merge_blocks.app_connector_groups`, `merge_blocks.applications`, `merge_blocks.servers` ([source](../packs/zpa/overrides/zpa_server_group.json#L54-L62)) | Medium: merge-block behavior affects reference/list shape. |
| `zpa_segment_group` | `drop_if_default.microtenant_id="0"`, `drops.applications` ([source](../packs/zpa/overrides/zpa_segment_group.json#L17-L23)) | Medium: broad default microtenant handling is not covered upstream. |
| `zpa_application_server` | `drop_if_default.microtenant_id="0"` ([source](../packs/zpa/overrides/zpa_application_server.json#L9-L12)) | Medium: same microtenant stub class. |
| `zpa_microtenant_controller` | `skip_if.id="0"` | Medium: upstream corroborates controller-only default skip. |
| `zia_ssl_inspection_rules` | `skip_if.default_rule=true`, `skip_if.predefined=true` | Medium: upstream has name-based default lists too. |
| `zia_dlp_engines` | `renames.predefined_engine_name=name` | High: provider Read stores raw `name`, while transform deliberately promotes `predefined_engine_name`; predefined engines can therefore adopt a wrong-but-first-plan-clean name. Resolve the intended name contract before adding policy. |
| `zia_location_management` | `renames.ipv6_dns64_prefix=ipv6_dns_64prefix` | Low/medium. |
| `zia_url_categories` | `sort_lists.urls` | Low/medium: set/list canonicalization. |
| `zia_url_filtering_and_cloud_app_settings` | singleton default id plus 6 prompt-setting renames | Low/medium. |
| ZIA singleton/default-id resources | `defaults.id=...` for advanced settings, ATP settings, auth settings URLs, browser control, EUN, FTP control, mobile malware, and similar singleton/settings resources | Mostly low for oracle once constant-key adoption is in place; still useful as no-match ledger items. |
| `zcc_forwarding_profile` | `no_html_unescape` | Low/medium: local HTML policy. |

## Existing Oracle Coverage

Known local coverage is narrow:

- `zia_url_filtering_rules.cbi_profile` has pack-level `projection_fill`
  ([packs/zia/pack.json](../packs/zia/pack.json#L2-L15)).
- `zia_url_filtering_rules.url_categories` has a local pack reference to
  `zia_url_categories` ([packs/zia/pack.json](../packs/zia/pack.json#L17-L36)).
- `zcc_forwarding_profile.trusted_network_ids` and
  `trusted_network_ids_selected` have local pack references to
  `zcc_trusted_network` ([packs/zcc/pack.json](../packs/zcc/pack.json#L2-L25)).
- ZPA currently has no pack-declared lookup sources or references
  ([packs/zpa/pack.json](../packs/zpa/pack.json#L1-L10)).

The main provider-read normalization probes are:

1. `zia_url_filtering_rules.size_quota` and
   `zia_cloud_app_control_rule.size_quota`: transform divides by 1024. The
   transform comment says the ZIA provider reads `resp.SizeQuota / 1024`
   ([engine/transform.py](../engine/transform.py#L479-L495)). RESOLVED - no
   oracle gap, for BOTH resources. zia 4.7.26 READ does
   `sizeQuotaMB := resp.SizeQuota / 1024` before `d.Set("size_quota", ...)` in
   each: URL filtering
   ([resource_zia_url_filtering_rules.go](https://github.com/zscaler/terraform-provider-zia/blob/v4.7.26/zia/resource_zia_url_filtering_rules.go))
   and cloud app control
   ([resource_zia_cloud_app_control_rules.go#L393-L395](https://github.com/zscaler/terraform-provider-zia/blob/v4.7.26/zia/resource_zia_cloud_app_control_rules.go#L393-L395)).
   Both CREATE/UPDATE paths convert back through the shared
   `convertAndValidateSizeQuota` helper
   ([utils.go#L709-L720](https://github.com/zscaler/terraform-provider-zia/blob/v4.7.26/zia/utils.go#L709-L720)).
   Provider state is already in config units (MB) for both. Note `time_quota`
   is passed through unconverted, which is why only `size_quota` carries a
   `divide` override.
2. `zcc_failopen_policy` inverted booleans:
   [engine/transform.py](../engine/transform.py#L496-L504) applies local
   inversion, but terraformer has no ZCC support. RESOLVED - no oracle gap.
   zcc 0.1.0-beta.1 `internal/framework/resources/failopen_policy.go`:
   `flattenFailOpenPolicy` applies `invertedStrToBool` / `invertedIntToBool`
   (API "0"/0 means true) on READ for `active`,
   `enable_captive_portal_detection`, `enable_fail_open`,
   `enable_web_sec_on_proxy_unreachable`, `enable_web_sec_on_tunnel_failure`;
   `expandFailOpenPolicy` inverts symmetrically on write (`boolToInvertedStr`
   / `boolToInvertedInt`). Oracle-projected state is already correct
   config-space booleans. The provider inverts exactly these five fields; the
   schema's sixth boolean `enable_strict_enforcement_prompt` is NOT inverted,
   and the local `invert_bool` list correctly excludes it (normal
   integer-to-bool coercion applies). Cite:
   https://github.com/zscaler/terraform-provider-zcc/blob/v0.1.0-beta.1/internal/framework/resources/failopen_policy.go
3. `zpa_application_segment.policy_style`: transform maps API enum strings to a
   schema boolean ([engine/transform.py](../engine/transform.py#L505-L510)).
   RESOLVED - no oracle gap. zpa 4.4.6 `resource_zpa_application_segment.go`
   READ does `d.Set("policy_style", PolicyStyleAPIToBool(resp.PolicyStyle))`;
   write uses `PolicyStyleBoolToAPIString`. State is already boolean. Cite:
   https://github.com/zscaler/terraform-provider-zpa/blob/v4.4.6/zpa/resource_zpa_application_segment.go
4. `zia_url_filtering_rules.url_categories=["ANY"]`: transform defaults missing
   or empty input ([engine/transform.py](../engine/transform.py#L524-L532)).
   Upstream terraformer independently injects the same default for this resource
   only. RESOLVED - no oracle gap and NOT a url-filtering test blocker. Same
   zia file READ: `if len(resp.URLCategories) == 0 { d.Set("url_categories",
   []string{"ANY"}) }`. The oracle sees ["ANY"] by construction.

### Provider Read And Oracle Mirroring Finding

The four audited provider READ paths above do normalize quota units, failopen
booleans, `policy_style`, and empty URL categories into their provider config
representations. That explains those specific state values, but it is not the
general reason the first oracle plan is clean. The oracle mirrors every present
provider input from post-Read state into config, so raw values such as
`microtenant_id="0"` can also plan clean on the first pass.

Therefore a first-plan no-op proves config/state equality at that moment, not
semantic correctness, transform/adopt artifact parity, config validation of an
explicit mirrored value, or stability after apply and refresh. The named DLP
engine and quota cases above remain explicit evidence gates. A general
transform-to-projection bridge would not resolve those distinctions.

`microtenant_id="0"` is not a provider-read normalization case, but it is also
not a current oracle gap. Provider source proves raw pass-through: zpa 4.4.6
[resource_zpa_application_segment.go](https://github.com/zscaler/terraform-provider-zpa/blob/v4.4.6/zpa/resource_zpa_application_segment.go)
READ sets `microtenant_id` raw, including `"0"`. The oracle projects optional
input attributes directly from post-read state
([engine/state_project.py](../engine/state_project.py#L48-L83)), so the current
path carries that value into config instead of applying transform's
`drop_if_default`. The operator-reported tenant result was consistent with this
mechanism, but no sanitized run record is retained in the repository.

The pinned zpa schema still makes this a useful guardrail. It declares
`zpa_application_segment.microtenant_id` as `optional` and NOT `computed`
([resource_zpa_application_segment.go#L231-L234](https://github.com/zscaler/terraform-provider-zpa/blob/v4.4.6/zpa/resource_zpa_application_segment.go#L231-L234);
local dump
[packs/zpa/schemas/provider/zpa.json](../packs/zpa/schemas/provider/zpa.json)),
so a future oracle policy that omits the attribute could turn a `"0"` state
value into a planned change. This is unique to the app segment. The other seven
dropped field paths are `zpa_server_group.microtenant_id`,
`zpa_segment_group.microtenant_id`, `zpa_application_server.microtenant_id`,
`zpa_app_connector_group.microtenant_id`,
`zpa_policy_access_rule.microtenant_id`,
`zpa_policy_access_rule.conditions[].microtenant_id`, and
`zpa_policy_access_rule.conditions[].operands[].microtenant_id`; all are
`optional` plus `computed` and are expected to plan clean when omitted
([resource_zpa_server_group.go#L96-L100](https://github.com/zscaler/terraform-provider-zpa/blob/v4.4.6/zpa/resource_zpa_server_group.go#L96-L100)).
Do not add a `projection_omit` or equivalent policy for the app-segment path
without a pinned import/show/plan test.

## Numeric Zero Phantom Reference

The `id==0` finding was not just a general follow-up. Before #144, it was a
concrete phantom-reference pattern for numeric reference lists.

For a list such as `[123, 0, 456]`, the old group-binding behavior accepted `0`
as a bindable integer. If `0` was not in the referent lookup, `_resolve_expr`
returned no expression
([engine/group_bindings.py](../engine/group_bindings.py#L113-L123)). In a
partially-bound list, the fallback appended `_literal_expr(child)`, rendering
`str(0)` as the HCL string literal `"0"`
([engine/group_bindings.py](../engine/group_bindings.py#L137-L138),
[engine/group_bindings.py](../engine/group_bindings.py#L247-L249)).

The resulting expression has real module references plus a phantom `"0"`, for
example:

```hcl
[module.zcc_trusted_network.items["hq_wired"].id, "0", module.zcc_trusted_network.items["branch_wifi"].id]
```

If the target is strict `list(number)`, this should fail loudly at plan time. If
the target is loosely typed or provider-coerced, the sentinel can persist as a
spurious reference to a nonexistent object. Terraformer treats numeric zero as a
sentinel and drops it before rendering list-ID blocks
([`helpers.go#L409-L438`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L409-L438)).

#144 implemented the fix direction: filter numeric `0` sentinels out of numeric
reference lists, then bind the remaining siblings. A list that is only `[0]`
now emits `[]`. The remaining evidence gate is semantic: confirm per affected
field that `0` means none/sentinel rather than any/all.

A live check with Terraform `v1.15.4` (2026-07-09) confirmed that when a list
is assigned to a target declared as `list(number)`, Terraform implicitly
converts string elements to numbers - verified for both all-string
(`["123","456"]`) and mixed (`[123,"456"]`) inputs. Module reference `.id`
values are strings. The string literal fallback for out-of-group numeric ids
is therefore type-safe, and no bare-numeric-literal change to `_literal_expr`
is needed.

## Terraformer Corroboration And Net-New Rules

### Covered Or Corroborated

| Finding | Upstream evidence | Local status |
|---|---|---|
| URL filtering `url_categories=["ANY"]` | Upstream injects `["ANY"]` for `zia_url_filtering_rules` only: [`generate.go#L2614-L2634`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2614-L2634). | Transform override exists. Oracle behavior RESOLVED by pinned provider source: zia 4.7.26 READ sets `["ANY"]` when the API returns an empty list, so the oracle sees it by construction. Scoped to URL filtering; cloud app control has no `url_categories`. |
| Empty/non-empty `cbi_profile` handling | Upstream has a resource-specific block writer for URL filtering and cloud app rules: [`nesting.go#L69-L115`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L69-L115). | Pack `projection_fill` exists for URL filtering. |
| Default microtenant controller skip | Import filters `ID!="0"` and generate filters `Name!="Default"`: [`import.go#L1077-L1100`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1077-L1100), [`generate.go#L1360-L1379`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1360-L1379). | Local `zpa_microtenant_controller` has `skip_if.id="0"`. |
| Numeric ID list blocks drop `id==0` | Upstream numeric list block helper skips numeric zero IDs: [`helpers.go#L409-L438`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L409-L438). | Implemented in #144 via numeric `0` sentinel filtering in group bindings. Tenant evidence gate remains: confirm `0` means none/sentinel, not any/all, for each affected field. |
| `appServerGroups` points at server groups | Data-source mapper maps both `serverGroups` and `appServerGroups` to `zpa_server_group`: [`datasource_processor.go#L75-L78`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/datasource_processor.go#L75-L78). | Treat as candidate only; another upstream helper disagrees. Verify with pinned ZPA schema before adopting. |

### Net-New Or Undercovered

| Finding | Upstream evidence | Local status | Candidate action |
|---|---|---|---|
| `zia_firewall_dns_rule` skip `order <= 0` | Config and filter: [`helpers.go#L82-L136`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L82-L136). Applied in import and generate: [`import.go#L2568-L2574`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L2568-L2574), [`generate.go#L2522-L2528`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2522-L2528). | Implemented in #144 as local `skip_if_lte.order <= 0`. | Confirm in a dev tenant that predefined/system DNS rules have `order <= 0` and real managed DNS rules have `order >= 1`. |
| ZPA-only `app_types` conversion (misattributed to ZIA DLP) | `ListNestedBlock` actively converts `applicationProtocol` for ZPA `praApps` and `inspectionApps`; those are its only call sites, and no ZIA DLP path calls it: [`nesting.go#L298-L306`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/nesting/nesting.go#L298-L306), [`helpers.go#L554-L604`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/helpers.go#L554-L604). | No upstream ZIA behavior is corroborated; the previous inventory row assigned active ZPA-only behavior to `zia_dlp_web_rules`. | Do not implement a ZIA transform from this evidence. Re-open only with pinned ZIA provider/API read-write evidence. |
| ZPA reference map | Field spellings for app connector groups, server groups, segment groups, applications, service edges, trusted networks, PRA portals/apps, profiles, and CBI objects: [`datasource_processor.go#L68-L106`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/datasource_processor.go#L68-L106). | Local ZPA reference graph is empty. | Build a WP3 reference inventory; adopt one relationship at a time with schema and fixture evidence. |
| ZIA reference/data-source map | Locations, groups, users, departments, network services, IP groups, application groups, labels: [`datasource_processor.go#L141-L178`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/datasource_processor.go#L141-L178). | Local ZIA declares only URL category lookup/reference. | Compare against current fixtures before expanding. |
| ZPA policy operands | Object-type-specific mappings for `SCIM`, `SCIM_GROUP`, `SAML`, `POSTURE`, `TRUSTED_NETWORK`, `MACHINE_GRP`: [`zpa_policy_processor.go#L35-L84`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/terraformutils/helpers/zpa_policy_processor.go#L35-L84). | Local ZPA policy operand behavior is custom transform policy, not pack references. | Inventory as reference candidates; avoid upstream regex-HCL implementation. |

## Predefined And Default Skip Matrix

These are upstream terraformer skips mined from `cmd/import.go` and
`cmd/generate.go`. They are version-sensitive candidate evidence, not local
truth.

| Resource class | Predicate or names | Evidence |
|---|---|---|
| ZPA timeout, forwarding, isolation policy rules | Skip name `Zscaler Deception`, name `Default_Rule`, or `DefaultRule`; inspection skips only `Zscaler Deception`. | Import: [`import.go#L930-L1006`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L930-L1006). Generate: [`generate.go#L1224-L1298`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1224-L1298). |
| `zia_firewall_filtering_rule` | `Default Firewall Filtering Rule` | Import: [`import.go#L1213-L1219`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1213-L1219). Generate: [`generate.go#L1456-L1463`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1456-L1463). |
| `zia_forwarding_control_rule` | `Client Connector Traffic Direct`, `ZPA Pool For Stray Traffic`, `ZIA Inspected ZPA Apps`, `Fallback mode of ZPA Forwarding` | Import: [`import.go#L1690-L1700`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1690-L1700). Generate: [`generate.go#L1803-L1814`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1803-L1814). |
| `zia_sandbox_rules` | `Default BA Rule` | Import: [`import.go#L1749-L1756`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1749-L1756). Generate: [`generate.go#L1848-L1855`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1848-L1855). |
| `zia_ssl_inspection_rules` | Import skips `Office365 Inspection`, `Zscaler Recommended Exemptions`, `Inspect Remote Users`, `Office 365 One Click`, `UCaaS One Click`, `Default SSL Inspection Rule`. Generate also skips `Smart Isolation One Click Rule` and `Default SSL_TLS Inspection Rule`. | Import: [`import.go#L1777-L1784`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1777-L1784). Generate: [`generate.go#L1869-L1876`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1869-L1876). |
| `zia_firewall_ips_rule` | `Default Cloud IPS Rule` | Import: [`import.go#L1826-L1835`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1826-L1835). Generate: [`generate.go#L1904-L1913`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1904-L1913). |
| `zia_firewall_dns_rule` | Name list: `ZPA Resolver for Road Warrior`, `ZPA Resolver for Locations`, `Critical risk DNS categories`, `Critical risk DNS tunnels`, `High risk DNS categories`, `High risk DNS tunnels`, `Risky DNS categories`, `Risky DNS tunnels`, `Office 365 One Click Rule`, `Block DNS Tunnels`, `Block Filesharing DNS`, `Block Gaming DNS`, `UCaaS One Click Rule`, `Fallback ZPA Resolver for Locations`, `Fallback ZPA Resolver for Road Warrior`, `Unknown DNS Traffic`, `Default Firewall DNS Rule`. Also generic `order <= 0` skip. | Import names: [`import.go#L1853-L1863`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L1853-L1863). Generate names: [`generate.go#L1924-L1934`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L1924-L1934). Order skip links above. |
| `ztc_traffic_forwarding_rule` | `Client Connector to ZPA`, `ZPA Forwarding Rule`, `ZPA Pool For Stray Traffic`, `Default Forwarding Rule` | Generate: [`generate.go#L2407-L2418`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2407-L2418). |
| `ztc_traffic_forwarding_dns_rule`, `ztc_traffic_forwarding_log_rule` | DNS skips `DefaultRule || Predefined`; log skips `DefaultRule`. | Import: [`import.go#L2478-L2506`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/import.go#L2478-L2506). Generate: [`generate.go#L2432-L2459`](https://github.com/zscaler/zscaler-terraformer/blob/8e117d34bc00a2ce47eadc7ea12aa998281e3f4f/cmd/generate.go#L2432-L2459). |

## No-Match Ledger

Terraformer does not cover these local oddities:

- ZCC resources, including `zcc_failopen_policy` and ZCC trusted-network CSV
  splitting.
- `signingCertId` / `signing_cert_id` to `enrollment_cert_id`.
- ZPA/ZCC HTML unescape and `zpa_policy_access_rule.custom_msg` HTML escaping.
- Broad nested `microtenantId=="0"` pruning outside the controller resource.
- The import-oracle architecture, projection policy, drift classification, and
  moved/stable identity behavior.

Keep these out of upstream-driven triage loops unless new upstream evidence
appears.

## Prioritized Get-Ahead Work

1. Fix and test numeric `id==0` reference-list sentinel handling. Implemented
   in #144; keep the tenant semantics gate below before treating this as proven
   for every field.
2. Verify provider-read behavior for value transforms. Prioritize
   `zcc_failopen_policy` inverted booleans first, then
   `zpa_application_segment.policy_style`, then mature-ZIA `size_quota`
   conversions. DONE via pinned provider source (2026-07-09) for all three
   named cases; see Existing Oracle Coverage above.
3. Test whether oracle/adopt emits or needs
   `zia_url_filtering_rules.url_categories=["ANY"]`. RESOLVED by provider
   source; see item 4 in Existing Oracle Coverage above.
4. Build a ZPA reference inventory from the upstream mapper. Start with
   `serverGroups`, `appServerGroups`, `appConnectorGroups`, and
   `segmentGroupId`, and verify each against the pinned ZPA 4.4.6 schema.
5. Turn the skip matrix into local candidate tests before adding behavior.
   `zia_firewall_dns_rule order <= 0` was implemented in #144; remaining
   matrix entries still need source/provider evidence and focused tests.
6. Decide whether to continue per-quirk projection policy or design an
   override-to-projection bridge. Do not bridge blindly: transforms have
   different oracle relevance, and some are intentionally transform-only.
   DECIDED: no bridge; per-quirk projection policy stays. The oracle mirror
   model means parity and later-plan behavior must be tested explicitly rather
   than inferred from a clean first plan.
7. Add a transform/adopt parity harness that renders both tfvars paths for the
   same logical resource and reports semantic and byte-level differences.
   Treat differences as evidence gates, not automatic failures, until the
   provider contract classifies them.

## Post-#144 Evidence Status

Provider-source conclusions and live-tenant validation are separate evidence
levels. The live statuses below came from an operator-provided photo handoff of
a private tenant run; commands, versions, sanitized outputs, and raw artifacts
were not retained in the repository. `Reported` therefore records useful
direction but does not satisfy the reproducible evidence-capture bar in
[Integration Validation](integration-validation.md#evidence-capture). These
items are not code blockers for #144, but reported, partial, and blocked rows
remain gates before relying on the affected behavior broadly.

| Evidence item | Status | Current evidence | Remaining gate or caveat |
| --- | --- | --- | --- |
| `zia_firewall_dns_rule.order <= 0` skip | Reported partial on `zs2` | The operator reported the gate confirmed on `zs2`; the retained summary explicitly records only that real managed DNS rules had positive `order` values. | Retain a sanitized two-population inventory proving every `order <= 0` item is predefined/system and no managed item is non-positive. The result remains tenant-specific; another tenant could still permit a managed `order: 0`. |
| Numeric reference-list `[0]` semantics | Reported blocked on ZCC provider bug | #144 maps a pure-sentinel `[0]` to `[]`, but the private run could not reach provider import. | Retain the exact provider version, import command, and sanitized diagnostic; after that bug is fixed, prove per affected field that zero means none/sentinel rather than any/all. |
| `-generate-config-out`: `url_categories=["ANY"]` | Reported partial / targeted run needed | The operator reported that ordinary category values round-tripped through the oracle. Pinned provider source independently supplies `ANY` when readback is empty. | Run and retain a true category-less match-any case; ordinary-category evidence does not prove the empty-input case. |
| `-generate-config-out`: non-zero `size_quota` | Source-confirmed / targeted live run needed | Pinned provider source resolves the bytes-to-MB read/write conversion; the private run did not exercise a non-zero quota. | Import a rule with a known non-zero quota and retain API, provider-state, generated-config, and clean re-plan unit summaries. |
| ZCC failopen inverted booleans | Source-confirmed / reported blocked live | Pinned provider source proves symmetric inversion for the five configured fields; the private run was blocked before import. | Retain the exact ZCC provider version and sanitized import diagnostic, then rerun live oracle confirmation. |
| `zpa_application_segment.policy_style` | Source-confirmed / reported live | Pinned provider source proves config-space boolean readback, and the operator reported the oracle path agreed. | Retain a sanitized live summary if tenant evidence is needed beyond the source-backed conclusion. |
| `-generate-config-out`: `cbi_profile` projection | Reported partial / exception open | The operator reported projection working for the observed URL-filtering cases except a private-run case labeled PI-3. | PI-3 has no retained diagnostic in this repository. Record its resource, command, versions, and sanitized failure before diagnosing and rerunning it. |
| `zpa_application_segment.microtenant_id="0"` | Confirmed by current code path / reported live | Provider source proves raw readback; the oracle code mirrors present optional inputs from post-read state. The operator reported no special `"0"` omission/default handling was needed. | Preserve the no-omission guardrail above; retain a sanitized live summary if the tenant observation is used as acceptance evidence. |

One non-tenant follow-up remains: #144 validates matcher shape and rename
conflicts, but it does not validate skip field names against a schema or raw
field vocabulary. A typo such as `{"ordr": 0}` fails open by keeping the item,
so it is safe; a future schema-aware validator could catch it earlier.

## Acceptance Bar

- Pinned upstream link.
- Local provider source, provider readback, or live oracle evidence.
- Exact resource and field-path scope.
- Regression test that fails for the oddity.
- No regex-HCL copying from upstream.
- Adversarial review before merge for generated-output, source-evidence,
  provider-readiness, reference-binding, projection, provenance, or
  adapter-edge-case behavior changes.
