# Singleton-state topology 151-type qualification handoff

Status: APPROVED BY FRESH ADVERSARIAL REVIEW. This qualification adds tests and
records evidence only; it changes no production behavior. The reviewed parcel
is committed at `2ebd37d`.

## Intent

- Turn the singleton-state roadmap's credential-free full-surface claims into
  permanent, exact tests rather than relying on package-level spot checks.
- Prove the current full profile still contains exactly the frozen v1 set of
  151 generated types and that all 151 backend keys are unchanged.
- Exercise the real Go `gen-env` command across all 151 singleton roots with
  local data for every one of the seven committed reference fields.
- Prove omission is exactly equivalent to explicit true, explicit false is a
  complete literal-ID escape, and the enabled/disabled artifact delta is
  exactly the expected 17 paths.
- Record a one-time byte comparison between the G0 authority-handoff commit and
  this candidate, plus the boundary of what could safely be qualified with the
  locally installed Terraform and provider cache.
- Do not use credentials, provider APIs, Kubernetes, remote state, plan, or
  Apply.

## Base / Head

- Base: `1d0b4c3` (`Complete singleton lifecycle cleanup`).
- Head: `2ebd37d` (`Qualify singleton topology across full surface`) on
  `feature/go-authority-singleton-state-v2`.
- Diff command: `git diff 1d0b4c3..2ebd37d`.
- G0 comparison authority:
  `93f04b36728755c55567b0915b804cacb4cd3a65`.

## Files Changed

- `go/internal/roots/full_surface_qualification_test.go`.
- `go/cmd/iw/v2_full_surface_qualification_test.go`.
- This handoff.
- Intentionally untouched: all production Go, Node/TypeScript, catalogs,
  packs, schemas, fixtures/goldens, generated demo artifacts, Terraform
  invocation, reports/evidence, Make/release wiring, and live runbooks.

## Source Inputs Consulted

- Provider schemas: unchanged; the existing all-module generator/formatter
  qualification exercised the committed schema-driven modules.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: `packsets/full.json`, all active pack manifests, and the exact
  seven declared reference fields across ZCC, ZIA, and ZPA.
- Existing docs/design:
  `docs/singleton-state-topology-v2.md` qualification gates,
  the G0/G1/G2/G3 handoffs, and `docs/go-runtime-v2.md`.
- Other source evidence: immutable
  `catalogs/zscaler-root-catalog.v1.json` with SHA-256
  `844c6c4b7d88266086732b3a68a9266f9abcfb4b00ea1177e6b4fdff92d79f10`.

## Generated Artifacts

- Reports: this handoff records the qualification evidence; no product report
  changed.
- Schemas: none.
- Fixtures: test-owned JSON written only below private temporary directories.
- Snapshots/goldens: none added or changed.
- Demo or lab outputs: none changed.
- Artifact drift intentionally expected: none in committed artifacts. The
  test-owned default-vs-false environment trees differ at exactly 17 asserted
  paths.

## Expected Delta

- One root-package test proves 151 sorted singleton roots, identity
  `resource_roots`, exact provider/resource surface equality with v1, and
  exact equality of the 151 `qualification/<resource-type>.tfstate` keys.
- The frozen key-set digest is
  `9895329b146e360acfe06b47bc410333a66b08e3f95d74e1b2ae79751eedc4dd`.
- One command-package test builds and invokes the real Go CLI three times over
  the full profile: omitted, explicit true for ZCC/ZIA/ZPA/ZTC, and explicit
  false for ZCC/ZIA/ZPA/ZTC.
- Omitted and true output trees are byte-identical. Both contain exactly these
  seven reference fields:
  - `zcc_forwarding_profile.trusted_network_ids -> zcc_trusted_network`
  - `zcc_forwarding_profile.trusted_network_ids_selected -> zcc_trusted_network`
  - `zia_url_filtering_rules.url_categories -> zia_url_categories`
  - `zpa_application_segment.segment_group_id -> zpa_segment_group`
  - `zpa_application_segment.server_groups.id -> zpa_server_group`
    (concrete binding path `server_groups[0].id`)
  - `zpa_server_group.app_connector_groups.id -> zpa_app_connector_group`
    (concrete binding path `app_connector_groups[0].id`)
  - `zpa_server_group.servers.id -> zpa_application_server`
    (concrete binding path `servers[0].id`)
- The test independently loads the effective merged declarations, including
  each `name_field`, and compares that exact seven-entry set to the fixture.
  The six collection-valued fields use list expressions; only
  `segment_group_id` is scalar. Each generated binding file is compared
  byte-for-byte with an independently constructed field/path/expression
  literal byte oracle that does not call the production renderer or formatter.
- Explicit false contains no `terraform_remote_state` expression or
  `expression_bindings.tf` artifact.
- The exact default-vs-false delta is 17 paths: nine affected `main.tf` roots,
  four referrer smoke tests, and four referrer `expression_bindings.tf` files.
  Every other common file is byte-identical.

## Invariants Claimed

- Evidence must not be silently dropped: the qualification claims are
  asserted by executable tests and exact digests/counts.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the v1 catalog digest and
  G0 commit are named, and the current topology is derived from checked-in pack
  metadata.
- Ambiguity must stay classified instead of being coerced to success: any
  missing/extra edge, type, key, root, or changed path fails with its exact
  identity.
- Provider-readiness counts must stay explainable: N/A; readiness reports are
  outside this parcel.
- Adoption safety invariants: no adoption, state, plan, or Apply command is
  invoked. Tests use private HOME/TMPDIR and local files only.
- The fixture cannot silently qualify a subset: its sorted root labels must
  equal the exact full-profile generated type set, every artifact must live
  below that set, and each of all 151 `main.tf` files must contain exactly its
  same-named singleton module.

## Tests Run

- `go test -count=1 ./internal/roots -run '^TestFullProfileSingletonTopologyAndBackendKeysMatchFrozenV1$' -v`:
  pass.
- `go test -count=1 ./cmd/iw -run '^TestV2FullSurfaceSevenEdgeCommandQualification$' -v`:
  pass.
- Builder and coordinator normal/race focused runs: pass.
- `go vet ./...`: pass.
- `gofmt -d` for both files: empty.
- `git diff --check`: pass.
- `go test -count=1 ./...`: pass.
- `make v2-authority`: pass.
- `make differential`: pass.
- `make check-root-catalog`: pass.
- `make INFRAWRIGHT_CLI=dist/iw check-modules`: pass.

One-time G0-versus-candidate comparison used separately built Go binaries from
G0 and the candidate with the same absolute deployment/module/overlay paths
and a deterministic digest over sorted `relative-path NUL bytes NUL` entries:

- Modules: 151 roots, 1,057 files, zero byte/path drift; both tree digests
  `bdad7895f075ee3a4370e5cc78d4ba8fefd14195804652ff2392120ca4375a01`.
- Explicit-false/config-free environments: 151 roots, 453 files, zero byte/path
  drift; both tree digests
  `dc0a4c6a6717f79f720b8f3e226449bb27aefde25fd5eeb1dfec96eb6264f93d`.
- The only G0-to-candidate changes under committed demo/module/cmd golden paths
  are the intentionally added Go-v2 topology and transform authorities; no
  pre-existing golden changed.

Credential-free local formatter qualification ran with `env -i`, private
mode-0700 HOME/TMPDIR, `TF_CLI_CONFIG_FILE=/dev/null`, and Terraform v1.15.4
darwin_arm64:

- 151 modules / 1,057 files: pass.
- 151 environments / 453 files: pass.
- 68 committed HCL formatting goldens: pass.
- The only Terraform operation was `terraform fmt -`; no init, provider
  process, network/provider API, backend, state, plan, test, or Apply ran.

## Known Deferrals

- An all-151 Terraform mock-plan loop was not run. There is no checked-in lock
  file or provider-installation mirror, no local provider executables/cache for
  ZCC 0.1.0-beta.1, ZIA 4.7.26, ZPA 4.4.6, or ZTC 0.2.0, and no reviewed
  all-151 orchestration harness. Running it would require downloading four
  providers and creating a new harness, so the qualification stopped rather
  than broadening scope.
- Live ZPA/ZIA/ZCC read/import/plan qualification and real backend inventory
  remain credential/access gated.
- The disposable Kubernetes adopt -> saved-plan -> exact-Apply qualification
  remains deferred because the cluster is inaccessible from the current
  location and execution is separately human gated.

## Review Focus

- Reconstruct the current generated type surface independently and verify the
  test cannot accidentally compare 151 wrong types to itself.
- Recompute the v1 catalog SHA and 151-key digest; check provider grouping and
  identity root assertions are complete and deterministic.
- Reconstruct all seven effective reference declarations from active manifests
  and compare exact referrer/field/referent coverage to the CLI fixture.
- Attack the three-mode deployment construction, especially all four providers,
  and confirm omission/true equivalence is a full-tree byte comparison.
- Verify the 17-path set is exactly the union of nine affected roots, four
  smoke tests, and four binding artifacts, and that unchanged common files are
  compared byte-for-byte.
- Check the fixture cannot contact Terraform, a provider, a backend, or Apply,
  and that private filesystem paths do not leak into artifacts.
- Independently validate the recorded G0 tree counts/digests and the reason the
  local all-151 Terraform mock-plan run was correctly deferred rather than
  falsely reported as passed.

## Fresh Adversarial Review Result

Verdict: **Approve**, with no blocking findings. The final reviewer independently
reconstructed the 151-type surface, provider counts, seven merged reference
declarations and `name_field` values, schema shapes, backend-key digest, and G0
tree comparisons. The reviewer also confirmed that direct root files must be
exactly `<resource-type>/main.tf` and that the four binding files are checked
against literal byte oracles independent of production rendering code.

The review initially found six qualification-rigor defects: count-only root
coverage, incomplete merged-declaration proof, scalar expressions for six
collection-valued fields, weak field/expression pairing, a production-renderer
self-comparison, and nested `main.tf` paths being able to satisfy the root
count. All six were remediated in the reviewed tree and the reviewer re-ran the
focused gates before approving it. The only retained non-blocking risk is the
explicit all-151 Terraform/provider-level deferral recorded above.
