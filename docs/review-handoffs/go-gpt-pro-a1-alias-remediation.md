# Builder Review Handoff: Fetch Destination Alias Remediation

## Intent

- Prevent distinct registry resource keys from resolving to one physical raw-evidence file on case-folding or Unicode-normalizing filesystems.
- Reject non-canonical resource identities before authentication, output-directory creation, invalidation, or resource requests.
- Preserve all current checked-in pack behavior, successful fetch bytes, stale-evidence invalidation, selection, concurrency, diagnostics, and Terraform safety behavior.

## Base / Head

- Base: `e085875cc34faa209efa6e2fbc3bd1f37b1bf855`
- Head: uncommitted working-tree candidate based on that exact SHA
- Diff command: `git diff -- e085875cc34faa209efa6e2fbc3bd1f37b1bf855 -- go/internal/metadata go/internal/collectors docs/review-handoffs/go-gpt-pro-a1-alias-remediation.md`

## Files Changed

- Files:
  - `go/internal/metadata/resources.go`
  - `go/internal/metadata/loader_test.go`
  - `go/internal/metadata/rootcatalog_test.go`
  - `go/internal/collectors/rest.go`
  - `go/internal/collectors/rest_test.go`
  - this handoff
- Files intentionally left untouched:
  - Node oracle sources and bundle
  - checked-in pack registries, provider schemas, overrides, and packsets
  - transform/adopt/assessment/Apply code
  - artifact renderers and generated fixtures

## Source Inputs Consulted

- Provider schemas: checked only to confirm the candidate does not rewrite or reinterpret them.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: every key from checked-in `packs/**/registry.json`; all 151 unique keys match `^[a-z][a-z0-9_]*$`.
- Existing docs or design records: `docs/adversarial-review.md`; GPT-Pro's review of exact base SHA `e085875...`.
- Other source evidence: `go/internal/tfrender/import_moves.go` already applies the identical canonical resource-type grammar to byte-reaching import/moved artifacts.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: no new checked-in fixture; table-driven tests construct invalid registry/root inputs in memory.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: registry keys and collector-internal resource identities outside `^[a-z][a-z0-9_]*$` fail closed. This removes case-folding and Unicode-normalization aliases.
- Expected report/count/coverage changes: none for checked-in packs.
- Expected generated-output changes: none.
- Expected no-op areas: all 151 checked-in resources; successful fetch artifacts; selection; diagnostics; root catalog/topology/transform/generation bytes; assessment and exact Apply.

## Invariants Claimed

- Evidence must not be silently dropped: one accepted selected resource identity maps to one unambiguous raw-evidence filename.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged.
- Ambiguity must stay classified instead of being coerced to success: filesystem-ambiguous identities are rejected rather than selected or reported processed.
- Provider-readiness counts must stay explainable: all checked-in pack counts remain unchanged.
- Adoption safety invariants: stale selected files remain invalidated; unselected files remain untouched; invalid identities fail before authentication or filesystem mutation; assessment and exact saved-plan Apply are untouched.

## Tests Run

- Commands:
  - `go test ./internal/metadata -run 'TestRegistryResourceKeysRequireCanonicalTerraformTypes|TestStrictVocabulariesRejectSilentTypos' -count=1 -v`
  - `go test ./internal/collectors -run 'TestFetchRejectsUnsafeResourceDestinationBeforeAuthOrMutation|TestFetchRejectsFilesystemEquivalentResourceNamesBeforeAuthOrMutation|TestBoundedResourceWorkersOverlapWithoutChangingBytesOutcomesAuthDiagnostics' -count=1 -v`
  - `go test ./... -count=1`
  - `go test -race ./internal/collectors ./internal/metadata -count=1`
  - `go vet ./...`
  - `gofmt -l .`
  - `go mod tidy -diff`
  - `go test ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation' -count=1 -v`
- Relevant output summary: every command passed; the four standing Node-oracle differential groups remained byte-identical.
- Tests not run and why: no live provider/API/Terraform execution was relevant or authorized.

## Known Deferrals

- Deferred work: generation manifests/atomic multi-resource publication; aggregate fetch budgets; release routing/provenance.
- Reason it is safe to defer: these are separately recorded pre-stable-release qualifications and do not permit same-invocation filename aliasing after this change.
- Follow-up owner or trigger: existing cutover and post-archive roadmaps.

## Review Focus

- Highest-risk files or paths: `metadata.validateRegistry`, `metadata.IsCanonicalResourceType`, and `collectors.fetchDestination` before the authentication boundary.
- Specific assumptions to attack: whether any checked-in or legitimately supported Terraform resource type requires uppercase, non-ASCII, hyphen, or a leading digit; whether a manually constructed `LoadedPackRoot` can bypass the collector check; whether validation happens before any output mutation.
- Source evidence the reviewer should verify: all checked-in registry keys; the existing import/moved resource grammar; selection and fetch worker construction.
- Generated artifacts the reviewer should compare: the four standing RootCatalog/Transform/Topology/Generation oracle groups.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: case pairs, NFC/NFD pairs, traversal strings, invalid root values bypassing metadata load, unrelated victim preservation, and prior stale-file/concurrency/error-ordering tests.
