# Go Wave 6 Filesystem Error Compatibility — Builder Review Handoff

## Intent

- Replace the previously recorded host-native filesystem-error-text divergence
  with a narrow, operation-aware Node 24.15 compatibility layer.
- Apply that layer immediately at supported raw filesystem boundaries in the
  already-ported metadata, CLI, environment generation, module generation,
  collector publication, and transform-artifact paths.
- Preserve success behavior, partial output trees, diagnostic ordering,
  rollback ordering, concurrency determinism, and the original Go error chain
  for returned compatibility errors and raw filesystem passthroughs. Preserve
  Node-defined domain conversions that intentionally retain only translated
  error text.
- Fail closed for operations and native errno values that do not yet have
  source-backed Node contracts.

## Base / Head

- Base: `c32479d6fdee4ce44944c4b8b8971900b97beda3`.
- Head: shared uncommitted working tree on
  `feature/go-canonjson-foundation`.
- Diff command: inspect the files below with `git diff`, plus each untracked
  file from `git status --short`.

## Files Changed

- Compatibility core:
  - `go/internal/nodefserr/nodefserr.go`
  - `go/internal/nodefserr/errno_posix.go`
  - `go/internal/nodefserr/errno_other.go`
  - `go/internal/nodefserr/nodefserr_test.go`
  - `go/internal/nodefserr/errno_windows_test.go`
- Metadata and direct CLI boundaries:
  - `go/internal/metadata/validation.go`
  - `go/internal/metadata/packs.go`
  - `go/internal/metadata/resources.go`
  - `go/internal/metadata/rootcatalog.go`
  - `go/internal/metadata/filesystem_error_differential_test.go`
  - `go/cmd/iw/main.go` (filesystem-specific hunks)
  - `go/cmd/iw/commands_topology.go`
  - `go/cmd/iw/filesystem_cli_differential_test.go`
- Generation and collector boundaries:
  - `go/internal/envgen/environment_generator.go`
  - `go/internal/envgen/filesystem_error_differential_test.go`
  - `go/internal/modulesgen/generator.go`
  - `go/internal/modulesgen/filesystem_error_differential_test.go`
  - `go/internal/collectors/rest.go`
  - `go/internal/collectors/rest_test.go`
  - `go/internal/collectors/rest_filesystem_parity_test.go`
- Transform-artifact boundaries and authority fixture:
  - `go/internal/tfrender/transform_artifacts.go`
  - `go/internal/tfrender/transform_artifacts_fserr_test.go`
  - `go/internal/tfrender/testdata/node24-transform-filesystem-errors.json`
  - `go/internal/tfrender/testdata/capture-node24-transform-filesystem-errors.mjs`
- This handoff.
- Intentionally left untouched: unsupported unlink, `MkdirTemp`, chmod,
  recursive removal, and deleted-current-directory formatting; all tenant,
  API, Terraform, artifact, and success-path contracts outside these raw
  boundaries.

## Source Inputs Consulted

- Provider schemas: N/A; schema contents and provider evidence are unchanged.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: committed full and reduced pack roots were exercised only as
  inputs to the existing loaders and generators.
- Existing docs or design records:
  - `docs/go-runtime-plan.md`, especially the filesystem-error decision and
    differential self-comparison warning;
  - `docs/adversarial-review.md` and its templates.
- Other source evidence:
  - Node v24.15.0 commit
    `848430679556aed0bd073f2bc263331ad84fa119`;
  - the exact owning TypeScript sources under `node-src/metadata/`,
    `node-src/domain/environment-generator.ts`,
    `node-src/modules/generator.ts`, `node-src/collectors/rest.ts`, and
    `node-src/domain/transform-artifacts.ts`;
  - Node's `node:fs/promises` implementation and live Node v24.15.0 probes.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures:
  - one exact 11-vector Node transform-filesystem-error fixture;
  - its pinned capture source, authority checks, and deterministic
    `$ROOT`/`$SUFFIX` normalization.
- Snapshots: temporary separate Node and Go filesystem trees only.
- Demo or lab outputs: unchanged.
- Artifact drift intentionally expected: none.

## Expected Delta

- Supported raw errors render Node's exact English `SystemError` spelling for
  `readFile`, `writeFile`, recursive `mkdir`, `readdir`, `stat`, `lstat`, and
  `rename`, including requested source/destination paths.
- The compatibility error unwraps to the original Go `PathError` or
  `LinkError`; `errors.Is` and `errors.As` continue to work.
- Recursive-mkdir inspection is read-only. It reclassifies only direct,
  source-shaped errors supported by stable repeated observations; it never
  creates a referent or follows up a failed mutation with another mutation.
- Expected generated-output, diagnostic-order, rollback-order, and successful
  tree changes: none.
- Expected no-op areas: every unsupported operation/code and every domain
  error that merely retains a filesystem cause.

## Invariants Claimed

- Evidence must not be silently dropped: returned compatibility errors and raw
  filesystem passthroughs retain their causes; source-defined domain errors
  retain the exact translated detail; and transform aggregate children retain
  their original order.
- Generic matcher evidence must not outrank source-backed evidence: only the
  closed operation/code set established by Node source and live probes is
  translated.
- Source precedence/provenance must remain explicit: Node 24.15 is the error
  oracle; Go filesystem state and errno remain the retained cause whenever the
  boundary returns a compatibility error or raw passthrough, while Node-defined
  domain conversion remains authoritative at its owning call site.
- Ambiguity must stay classified instead of being coerced to success:
  unsupported errno values, changing probes, ELOOP, and unsupported operations
  return the original Go error unchanged.
- Provider-readiness counts: N/A.
- Adoption safety invariants:
  - ENOENT control-flow branches run before wrapping;
  - render/domain failures are not relabeled as filesystem failures;
  - collector fatal outcomes remain selection ordered under concurrency;
  - transform rollback remains reverse ordered and preserves recovery data;
  - no compatibility probe mutates a symlink target or output tree.

## Tests Run

- Exact final core/collector qualification:
  - `go test -count=20 ./internal/nodefserr`
  - `go test -race -count=5 ./internal/nodefserr`
  - `go test -run TestConcurrentWriteFailuresRetainSelectionOrderedPrimaryError -count=100 ./internal/collectors`
  - `go test -run TestMkdirFetchOutputDirectoryAgainstNode2415 -count=3 ./internal/collectors`, followed by one post-format run
  - `go test -count=3 ./internal/collectors`
  - `go test -race -count=1 ./internal/collectors`
  - `go test ./...` and `go vet ./...`
  - Linux/amd64 and Windows/amd64 test cross-compilation for
    `internal/nodefserr` and `internal/collectors`
  - `gofmt`, scoped/full diff checks, and the production/test error-pattern
    scan
- Final coverage: `nodefserr` 94.8%; `Call.Wrap` 100%, `adjustMkdirAll`
  92.4%, and the recursive-ancestor and matching-follow helpers each 100%.
- Core `nodefserr` focused, repeated, race, permission, symlink, and Windows
  cross-build tests.
- Live Node/Go differentials for metadata, root-catalog, scope-paths,
  environment generation, module generation, and collector publication.
- Envgen/modulesgen: 22 injected failure cases with exact errors and partial
  trees, repeated and race tested.
- Collector mkdir matrix: 103 live Node v24.15 cases covering files;
  absolute/relative direct and chained dangling links; nested missing
  referents; dangling intermediate components; existing-file referents;
  one/multiple trailing separators; intermediate descendants; self/two-link/
  chained-self loops; exact errors; and complete separate-root trees. Its
  42-case nested-intermediate product independently covers direct, two-link,
  and three-link absolute/relative targets across one, two, three, and four
  separators, including trailing-child variants through three separators.
  Collector write-order uses deterministic worker barriers rather than sleeps.
- Transform artifacts: deterministic aggregate cleanup injection, post-backup
  `lstat` injection, rollback/recovery assertions, full fixture payload checks,
  and byte-exact live fixture recapture.
- Focused package suites, `go test -race`, `go vet`, `gofmt`, error-handling
  scans, `git diff --check`, and Darwin/Linux/Windows compilation as applicable.
- Tests not run: credentialed tenant, provider, backend, plan, or Apply work;
  none is relevant to filesystem-error rendering.

## Review Remediation

- The core review tightened platform errno allowlisting, panic behavior,
  stable-tree documentation, and Windows/ELOOP test gating before approval.
- Direct CLI review corrected the package comment so it names only landed
  boundaries and keeps host-native wording for unported sites.
- Metadata review verified private typed-panic passthrough, registry
  `ReadFile` rather than `Stat`, and the deliberate source-evidence double read.
- The `go/internal/metadata/rootcatalog.go` `isFile` precheck to direct
  `ReadFile` change is an intentional **VERIFIED parity fix**: a
  directory-shaped `registry.json` now propagates `EISDIR`, matching
  `node-src/metadata/root-catalog.ts` and its direct `readFile` semantics.
- Collector review rejected a mutating dangling-symlink retry whose body had
  no oracle coverage and whose post-mutation checks could not fail closed. The
  retry was removed. A small read-only core extension now handles only stable,
  direct chained-symlink error classification, while ELOOP remains raw.
- Second-round core and collector reviews found three stable recursive-mkdir
  shapes missing from that extension:
  - final `link -> missing/child` plus trailing separators, where Go reports
    requested-path ENOENT and Node reports ENOTDIR at the bare link;
  - a dangling link used as an intermediate component, where Go reports
    EEXIST at the recursive ancestor and Node reports ENOTDIR there;
  - final `link -> existing-file` plus trailing separators, where Go reports
    ENOTDIR and Node reports requested-path EEXIST.
- Each fix is bound to the direct `os.MkdirAll` `PathError` shape, brackets two
  matching `Stat` observations with identical `Lstat`/`Readlink` snapshots,
  retains the raw cause, and performs no mutation or retry. Absolute/relative
  and one/multiple-separator regressions assert exact live Node errors and
  unchanged trees. A link whose referent crosses through a file stays
  ENOTDIR; ELOOP remains deliberately unsupported and raw.
- A third-round collector review then found two intermediate-follow classes:
  - `link -> regular/child` with a deeper requested descendant, where Go
    reports EEXIST or ENOTDIR at the recursive link ancestor while Node reports
    ENOTDIR at the full requested path;
  - `link -> denied/child` with `denied` mode `0000`, where Go reports EEXIST
    or EACCES at the recursive link ancestor while Node reports EACCES at the
    full requested path.
- The ancestor-EEXIST branch now maps followed ENOTDIR/EACCES only after the
  same bracketed stable-symlink proof. Repeated-separator EACCES is accepted
  only for the exact recursive-ancestor spelling plus a stable link snapshot
  and two matching full-request EACCES observations. Twelve new live cases
  cover absolute/relative links and `child`, `child/`, and `//child` request
  forms; actual-filesystem tests retain raw error identity and prove the link,
  existing file bytes, denied mode, and missing child remain unchanged.
- A fourth-round collector review found the remaining direct nested-dangling
  recursive spelling: for `link -> missing/child` and a request such as
  `link//grand`, Go reports direct mkdir ENOENT at the recursive `link/`
  ancestor, while Node reports ENOTDIR at the bare `link`. The ENOENT branch
  now admits only a trailing, strict `os.MkdirAll` recursive-ancestor spelling,
  strips separators only to name the active link, and requires the same
  bracketed stable symlink plus matching ancestor ENOENT observations. An
  ambiguous nonsymlink or ELOOP observation returns the original error.
  Actual-filesystem and live Node regressions cover direct/two-link/three-link
  absolute and relative targets across the full 42-case separator product,
  assert raw errno/op/path and cause identity, and prove every link and missing
  referent tree remains unchanged. The direct repeated-separator rows now name
  the bare link; chained rows retain Node's recursive separator spelling.
- Collector concurrency evidence replaced timing sleeps with a B/C/D barrier
  proving the later-index failure is recorded first but the earlier selection
  still supplies the primary error.
- Transform review replaced a permission-dependent `Skipf` with deterministic
  injection, added a reproducible Node fixture capture, exact-checks every
  vector field, and added post-backup `lstat`/rollback coverage.

## Known Deferrals

- Deferred work: Node-compatible formatting for unlink, `MkdirTemp`, chmod,
  recursive cleanup (`rmdir`/`scandir` variants), and deleted-current-directory
  behavior.
- Reason it is safe to defer: those operations remain visibly raw and are not
  guessed or mislabeled; the package fails closed outside its reviewed set.
- Follow-up owner or trigger: the owning plan/adopt/remaining-CLI slice must
  add a source-pinned operation contract before translating one of them.

## Review Focus

- Highest-risk paths: `nodefserr.adjustMkdirAll`, collector output-directory
  creation and selection-order publication, transform batch rollback and
  aggregation, and the capture fixture.
- Assumptions to attack: direct raw error shapes, requested versus native
  failure paths, stable-tree observations, trailing separators, symlink
  chains, permission errors, unsupported errno preservation, and partial-tree
  equality.
- Source evidence to verify: the exact owning Node calls and live Node 24.15
  errors; do not treat the Go formatter or a Go-regenerated fixture as oracle.
- Generated artifacts to compare: recapture the transform fixture with the
  committed Node script and compare bytes.
- Silent-overclaim risks: wrapping a domain error, translating after an ENOENT
  branch, choosing a wall-clock-first collector failure, reordering aggregate
  children, or presenting an unsupported code as Node compatible.
