# Builder Review Handoff: ZIA Empty-Enum Pack Overrides

## Intent

- Omit eight exact empty-string enum sentinels that ZIA provider 4.7.26 was
  reported to emit during live import/Read for DLP dictionaries, HTTP header
  profiles, locations, and SSL inspection rules.
- Exclude the live-reported protected-name capitalization variant while
  retaining the four existing exact-name protections.
- Apply the same committed pack rules in Transform, generated import config,
  and final Adopt provider-state projection without changing generic matcher
  semantics.
- Preserve existing `null` semantics (Transform/generated HCL retain explicit
  `null`; Adopt treats optional `null` as absent), every nonempty enum, managed
  forwarding rules, all deployment topology defaults, and provider behavior.

## Base / Head

- Base: `26c17ce34e126a8320f1b0e95bd3d47db35c2cc5` (draft PR #226 head).
- Head: `12052f4fc6f38c2ceb8d357e89e32063058cb319` (implementation commit; this
  handoff is a review-only follow-up commit).
- Diff command:
  `git diff 26c17ce34e126a8320f1b0e95bd3d47db35c2cc5..12052f4fc6f38c2ceb8d357e89e32063058cb319`.

## Files Changed

- Pack behavior:
  - `packs/zia/overrides/zia_dlp_dictionaries.json`
  - `packs/zia/overrides/zia_http_header_profile.json`
  - `packs/zia/overrides/zia_location_management.json`
  - `packs/zia/overrides/zia_ssl_inspection_rules.json`
  - `packs/zia/overrides/zia_forwarding_control_rule.json`
- Coverage and evidence:
  - `node-tests/adopt-runner.test.ts`
  - `node-tests/generic-transform-core.test.ts`
  - `node-tests/generated-config-policy.test.ts`
  - `node-tests/state-project.test.ts`
  - `node-tests/fixtures/zia-adoption-classification-v4.7.26.json`
  - `node-tests/metadata-loader.test.ts`
  - `node-tests/transform-runtime-artifacts.test.ts`
  - `tests/test_transform.py`
  - `docs/zscaler-adoption-quirk-inventory.md`
- Files intentionally left untouched: deployment configuration, root
  topology, Node and Python production engines, provider code and schema,
  ZIA registry, generated modules, Terraform roots, runtime bundle, and the
  DLP notification-template handling.

## Source Inputs Consulted

- Provider schemas: committed ZIA 4.7.26 provider schema at
  `packs/zia/schemas/provider/zia.json`. All eight paths are writable optional
  strings and are not computed or required.
- OpenAPI/API contracts: none.
- Provider source files: ZIA 4.7.26 forwarding-control schema validation at
  `resource_zia_forwarding_control_rule.go#L167-L173` and protected-name Read
  handling at `#L239-L246`, cited in the committed fixture.
- Pack metadata: the five named ZIA overrides, ZIA registry, provider pin, and
  the generic default `{id}` adoption import template.
- Existing docs or design records:
  `docs/zscaler-adoption-quirk-inventory.md` and inherited PR #226 provider
  limitation notes.
- Other source evidence: a private downstream live run against tenant `zs2`
  reported exact `""` readback for the eight paths and a protected forwarding
  rule named `Fallback Mode of ZPA Forwarding` at `order=-5`. No tenant values,
  raw responses, state, generated HCL, or credentials are committed.

## Generated Artifacts

- Reports: no tenant report committed.
- Schemas: none changed.
- Fixtures: the sanitized ZIA classification fixture adds the provider source
  anchor and a negative-order protected forwarding-rule shape.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: Transform and Adopt tfvars, plus
  generated import HCL, omit only the eight exact `""` leaves; forwarding
  rules matching the retained exact-name set or the reported exact
  capitalization variant do not reach Transform/Oracle output.

## Expected Delta

- Expected behavior change: exact empty strings at the eight paths are omitted
  in all three pack-policy consumers; the reported protected-name variant is
  classified before identity derivation and Oracle work.
- Expected report/count/coverage changes: committed override count 73 -> 74;
  ZIA resources with semantic overrides 20 -> 21.
- Expected generated-output changes: only affected empty-string leaves and
  system-owned forwarding objects disappear.
- Expected no-op areas: nonempty strings, arbitrary non-positive and
  positive-order forwarding rules, the existing four exact-name skips,
  provider execution, identity and import-ID defaults, deployment grouping,
  cross-state references, and every non-ZIA pack. Transform/generated HCL
  preserve explicit `null`; Adopt retains its existing optional-null-as-absent
  schema projection.

## Invariants Claimed

- Evidence must not be silently dropped: each default is exact `""` equality
  on a schema-optional path; all nonempty values and explicit raw `null` values
  remain distinct in Transform/generated-HCL tests, while Adopt's pre-existing
  optional-null omission is explicit. The new forwarding classification is an
  exact name, additive to rather than a replacement for the source-backed set.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  implementation changes; the fixture retains exact provider-source anchors.
- Source precedence/provenance must remain explicit: provider 4.7.26 is the
  pinned authority; live claims remain labeled private/reported rather than
  reproduced repository evidence.
- Ambiguity must stay classified instead of being coerced to success: no broad
  falsey matching, case folding, substring matching, or missing-order skip was
  added.
- Provider-readiness counts must stay explainable: only the new DLP override
  file changes the committed override inventory.
- Adoption safety invariants: exact forwarding system skips run before identity
  and Oracle; arbitrary non-positive and positive-order objects survive to the
  Oracle; no deployment Apply or provider mutation is introduced.

## Tests Run

- `npm run build:test`.
- Focused Node suites for Adopt, Transform, generated-config policy, provider
  state projection, adoption metadata, metadata loading, and runtime override
  compilation: 111 passed.
- Focused Python override, numeric skip, projection, Adopt, and generated-config
  policy tests: 6 passed with `/run/current-system/sw/bin/python3` 3.13.13.
- `make check-pack PACK=zia`: passed.
- `npm run typecheck`: passed.
- `npm test`: 848 passed, 0 failed after replacing a temporary cross-worktree
  `node_modules` symlink with an isolated dependency tree. The first broad run
  had 847 passed and one bundler-path failure caused solely by that symlink.
- `git diff --check`: passed.
- Tests not run: live provider qualification. The private downstream run is
  reported evidence only and must be repeated when the provider pin changes.

## Known Deferrals

- Deferred work: retain sanitized raw API/provider-state/generated-HCL evidence
  for all eight enum paths and a forwarding-rule inventory during the next
  approved live qualification.
- Reason it is safe to defer: exact-path metadata is backed by the committed
  schema and strict synthetic regression coverage; claims remain calibrated as
  reported rather than repository-reproduced evidence.
- Follow-up owner or trigger: downstream operator on the next tenant run or
  every ZIA provider pin change.
- Deferred work: `zia_dlp_notification_templates` dollar-placeholder provider
  defect on 4.7.26.
- Reason it is safe to defer: required user content cannot safely be dropped;
  PR #226 already documents portal-managed or hand-authored handling.

## Review Focus

- Highest-risk files or paths: the five overrides, all eight dotted paths, the
  generated-HCL policy test, and forwarding classification fixture.
- Specific assumptions to attack: every path is optional and writable; dotted
  paths correctly traverse nested singleton/list blocks; exact matching does
  not collapse `null` or nonempty values; the new exact forwarding name cannot
  hide an unrelated managed rule under provider 4.7.26.
- Source evidence the reviewer should verify: committed schema flags, provider
  `order` validation, provider protected-name handling, generic `{id}` import
  fallback, and version pin.
- Generated artifacts the reviewer should compare: Transform output, generated
  import HCL after policy, final Adopt JSON tfvars, and classification survivors.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  explicit `null`, nested empty lists, repeated nested blocks, absent or
  nonnumeric order, arbitrary `order=0`/negative rules, retained exact protected
  names, and a provider upgrade changing the protected-name contract.
