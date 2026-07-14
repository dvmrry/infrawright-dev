# ZCC Beta Provider Adoption Audit

This audit bounds what Infrawright can safely automate against the first public
ZCC provider release. It is both a source review and a downstream qualification
plan. It does not authorize a provider fork, deployment Apply, or a speculative
pack mapping.

## Pinned authority

- Terraform provider: `zscaler/zcc` `0.1.0-beta.1`, source commit
  [`3e7598fc`](https://github.com/zscaler/terraform-provider-zcc/tree/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef).
- Zscaler Go SDK: `v3.8.37`, source commit
  [`65276eca`](https://github.com/zscaler/zscaler-sdk-go/tree/65276eca609347a3776bfd0421a08e2f2b0b2a95).
- Provider scope: OneAPI/Zidentity only. The provider documents no legacy
  authentication path and no current OneAPI support for `zscalergov` or
  `zscalerten` ([provider documentation](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/docs/index.md#L29-L35)).
- Release state: `0.1.0-beta.1` is the only public provider release inspected.
  The provider repository currently has no source correction after the tag
  that changes the findings below.

Generated provider documentation and pinned source outrank the repository
README where they disagree. The README still contains older source/version and
Terraform-era examples.

## Decision summary

The seven generated ZCC resource types split into four classes:

| Class | Resources | Current position |
| --- | --- | --- |
| Source-backed and provisionally adoptable | `zcc_failopen_policy` | Retain the five existing boolean inversions; qualify singleton cardinality and out-of-domain values live. |
| Provider/API mismatch with an exact pack-policy candidate | `zcc_device_cleanup`, `zcc_forwarding_profile`, `zcc_web_privacy` | Do not fork the provider. Prove the exact emitted sentinel or omitted field, then omit only that provider-manufactured value through version-bound pack policy. |
| Cross-version discovery/import mismatch | `zcc_trusted_network` | Keep numeric import until v1 discovery and v2 provider identity are reconciled. Do not switch to name import on source inspection alone. |
| Source acquisition unavailable or unreliable | `zcc_notification_template`, `zcc_zia_posture` | Keep module/schema authoring, but do not enable automatic Fetch/Adopt until the endpoint and pagination gates pass. |

No current finding justifies direct raw-API-to-tfvars projection. Raw API data
continues to own enumeration and identity; provider import/Read continues to
own configuration projection.

## Resource matrix

### `zcc_device_cleanup`

The resource is a singleton. Its importer ignores the requested token, reads
the singleton, and stores the API ID
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/device_cleanup.go#L215-L228)).
The provider accepts `force_remove_type` only in `0,8..16`, but Read copies the
API string into state verbatim
([schema](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/device_cleanup.go#L71-L80),
[Read projection](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/device_cleanup.go#L256-L270)).

Downstream observed the exact JSON string `"1"`. That value cannot be emitted
as valid provider configuration at this pin. A version-scoped, exact-scalar
`unsupported_if` is the conservative immediate policy. A future
`drop_if_default` is acceptable only if a live plan proves that omission means
"leave the server-owned setting unchanged" rather than selecting a different
default. Do not generalize the rule to every invalid string without observed
API evidence.

The provider also converts malformed integer strings to zero. The live gate
must record whether the API returned a JSON string, and must separately cover
`device_exceed_limit`, `auto_removal_days`, and `auto_purge_days` before any
rule is added for them.

### `zcc_failopen_policy`

The provider treats this as a singleton and falls back to the first returned
policy if an ID lookup fails
([import](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/failopen_policy.go#L284-L310)).
The pack's five `invert_bool` fields exactly match the provider's symmetric
Read/write inversions. `enable_strict_enforcement_prompt` is correctly not in
that list: the provider maps it with ordinary `0/1` semantics
([conversion](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/failopen_policy.go#L319-L388)).

Keep the current mapping. The remaining gates are:

1. `listByCompany` returns exactly one policy for the tenant.
2. `enable_strict_enforcement_prompt` is observed only as `0` or `1`.
3. Import followed by a second plan is clean.

Do not change its durable key merely to model singleton intent; that would be a
state-address migration, not a provider-compatibility correction.

### `zcc_forwarding_profile`

Import accepts only a numeric ID
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/forwarding_profile.go#L550-L562)).
Read calls `listByCompany` once with no page arguments, then removes state when
the requested ID is absent from that response
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/forwarding_profile.go#L427-L468)).
This is an upstream pagination limitation; a more complete Infrawright Fetch
cannot repair the provider's later Read.

The provider's acceptance test deliberately ignores these fresh-import values:

- `evaluate_trusted_network`;
- every `forwarding_profile_actions[].is_same_as_on_trusted_network`;
- every `forwarding_profile_zpa_actions[].is_same_as_on_trusted_network`;
- `unified_tunnel[].system_proxy_data.proxy_action`.

The API omits them and provider Read can preserve them only from an existing
plan/state, which a fresh import does not have
([acceptance evidence](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/forwarding_profile_test.go#L40-L53),
[Read workaround](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/forwarding_profile.go#L958-L1016)).

The provider also works around four bad SDK JSON tags for ZPA action latency
fields by retaining plan values. Those fields are not equivalent to the four
API omissions above and must not be added to a drop rule
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/forwarding_profile.go#L1037-L1086)).

Candidate policy is exact default omission for only the four path families
above, after a live import proves the manufactured values are `false`/`0` and
that omitting them yields an import-only or no-op plan. If the tenant has more
profiles than the provider's unpaged Read returns, automatic adoption remains
unsupported regardless of pack policy.

### `zcc_notification_template`

The provider supports numeric ID or case-insensitive name import
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/notification_template.go#L298-L322)).
The SDK implements a v2 paginated list at
`/zcc/papi/public/v2/notification-templates`, and Infrawright already
implements the matching `zcc_v2` envelope.

This is not enough to enable Fetch. Downstream reported a live 404 for the v2
source in the current gateway. Retain the resource as source-less until the
same approved environment returns 200, reconciles every page against `total`,
and imports all returned IDs cleanly. A successful gate should use numeric ID
as the stable key/import identity and must compare list items with per-ID GET
state before authoring drops.

### `zcc_trusted_network`

Infrawright currently discovers the v1 collection, while the provider manages
the v2 resource. The provider accepts numeric ID or a case-insensitive v2 name
lookup
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/trusted_network.go#L305-L330)).

The current numeric import failed live for an observed v1 item. Switching the
pack to `{name}` is not yet justified:

- v1 `networkName` and v2 `name` are distinct source fields;
- v2 also carries `networkName`;
- name lookup is case-insensitive and returns the first match;
- forwarding-profile references still require v1 and v2 IDs to designate the
  same object;
- the v1 `ssids` field and v2 scalar `ssid` need value-shape evidence.

The exact gate is a fully paginated v1/v2 join proving, for every candidate,
byte-equal names, equal IDs, unique case-folded names, equal transformed
criteria, successful name and numeric imports, and a clean forwarding-profile
reference plan. Until then, classify unmatched objects as unsupported rather
than applying one observed `condition_type` as a proxy for all trusted
networks.

### `zcc_web_privacy`

This is a singleton whose importer ignores the requested token and reads the
API object
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/web_privacy.go#L193-L206)).
The provider's acceptance test excludes
`enforce_secure_pac_urls` and `enable_fqdn_match_for_vpn_bypasses` from import
verification because GET does not reconstruct them
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/web_privacy_test.go#L39-L46)).

These are exact pack-policy candidates: if fresh import emits `false`, omit
that provider-manufactured default rather than claiming it is the remote
setting. Qualification must prove the resulting import-only plan and a clean
second plan. Infrawright must not infer or write `true` from raw evidence the
provider cannot read.

### `zcc_zia_posture`

The provider supports numeric ID or name import
([source](https://github.com/zscaler/terraform-provider-zcc/blob/3e7598fcf4c9aed11a6bebe73c18fd472a7b5bef/internal/framework/resources/zia_posture.go#L258-L277)).
Its own data-source documentation says the list endpoint silently truncates
pagination, while the importer calls the SDK name lookup over that same
endpoint. Downstream also reported the v2 source as gateway-parked with a 404.

Keep module/schema support, but keep automatic Fetch/Adopt disabled. Re-open
only after an ID-complete paginated source exists and numeric import plus
nested posture projection produces a clean second plan. Do not use name import
as discovery evidence.

## Retained exact-five compatibility boundary

The generic Node Transform and Adopt engines already understand
`drop_if_default` and version-scoped `unsupported_if`. The retained frozen ZCC
exact-five catalogs do not encode those semantics, but they bind every ZCC
override and registry byte into their source digests. Therefore adding the
pack policies above without also changing the frozen contracts would make one
runtime apply them while another rejects the pack as stale or ignores the
meaning.

Do not solve that by silently weakening the digest. Before landing ZCC pack
semantics, make one explicit choice:

1. extend the frozen transform/adoption catalog representation and its tests;
2. retire the frozen exact-five consumer after downstream inventory; or
3. keep the policy downstream-only and do not claim repository-wide support.

This audit makes no such lifecycle choice. The production provider behavior is
unchanged by this document.

## Downstream qualification

Run only on an approved machine with the existing credential environment.
Never print environment variables, raw objects, names, IDs, URLs, state, plan
contents, or credentials. Capture raw logs in a mode-0700 disposable directory
and return only hashes, counts, exit statuses, and normalized classifications.
No deployment Apply is authorized. The Oracle's mechanically verified
backend-free import-only scratch Apply may run when already approved.

### Authority record

Return full SHA-256 values for:

- `dist/infrawright-cli.mjs`;
- ZCC registry, pack manifest, provider schema, and every tested override;
- active profile/catalog and deployment file;
- Terraform binary and loaded provider binary.

Also return Node, Terraform, provider, SDK, and engine Git versions, plus
Oracle batch/state-source modes. Do not truncate hashes.

### Test matrix

| Test | Required sanitized evidence | Pass condition |
| --- | --- | --- |
| Device cleanup | Raw JSON scalar type and normalized value class for all five projected fields; unsupported count; Oracle-command count | Exact observed `"1"` is rejected before Terraform; no artifact is published for a failed resource/root. |
| Fail-open | Collected count; out-of-domain count for each boolean encoding; Adopt exit; second-plan class | Exactly one object, domain-valid values, import-only/no-op then no-op. |
| Forwarding pagination | Total Fetch IDs, provider-Read-visible IDs, unique count, missing count, page parameters observed without tenant URLs | Provider Read sees every imported ID. Any missing ID blocks adoption. |
| Forwarding omitted fields | Per-path occurrence counts in generated-before, generated-after, provider state, and adopted tfvars; Adopt exit | Only the four documented path families are omitted, and plan is import-only/no-op. |
| Notification source | HTTP status class, page count, envelope total, collected count, per-ID import success count | 200, complete page reconciliation, and every returned object imports cleanly. A 404 keeps it source-less. |
| Trusted v1/v2 join | v1/v2 counts, matched IDs, byte-equal names, case-fold collisions, field mismatches, numeric/name import successes, reference-plan class | Complete one-to-one join with no collisions/mismatches and clean reference plan. |
| Web privacy omitted fields | Provider-state and adopted-tfvars occurrence counts for the two documented paths; Adopt exit; second-plan class | Provider-manufactured false values are omitted and both plans are import-only/no-op then no-op. |
| ZIA posture source | HTTP status class, page count, endpoint total, collected IDs, import successes | ID-complete pagination and clean numeric imports. A 404 or truncated total keeps it source-less. |

For every failed or unsupported resource, return:

```text
fetched=N system_skipped=S unsupported=U eligible=E published=P failed=F
oracle_commands=N
```

An unsupported object discovered during logical-root preflight requires
`published=0` and `oracle_commands=0` for that root. In per-resource mode the
same rule applies to the resource. Do not count a fetched or eligible object as
adopted when no artifact was published.

## Acceptance and next action

After downstream returns the matrix:

1. land only exact, provider-version-scoped policies supported by the evidence;
2. add Transform/Adopt artifact parity fixtures for every accepted omission;
3. add an import-only/no-op regression for every enabled resource;
4. keep provider-only pagination and Read omissions in the upstream issue
   queue rather than forking the provider;
5. re-run this audit when the provider pin changes, because beta behavior and
   the validity of every version-scoped rule may change.
