# Go Wave 7 Bounded Files Foundation — Replacement Builder Handoff

## Review status

- The first adversarial review returned two blocking findings: byte budgets
  narrowed Node `bigint` to `int64`, and snapshot-directory validation could be
  bypassed or raced through path spellings and parent replacement.
- Both blockers are remediated in the frozen package below. A subsequent
  builder spot check also found that a non-nil zero-value `ReadBudget` reached
  filesystem access before failing; every file/snapshot operation now rejects
  uninitialized state during preflight, with deterministic no-access tests.
- The follow-up review requested a live Node pin for V8's maximum string
  length, a written decision on the broader Go platform gate, an explicit
  deterministic-charging contract, a fresh race soak, and a zscalerctl
  comparison. All five are incorporated below. No runtime behavior was changed
  for the comparison-only findings.
- The next fresh review found that direct `os.OpenRoot(path)` could block on a
  raced FIFO, that a same-inode destination overwrite/truncate/chmod could
  survive the post-copy checks, and that the exact-v24.15 oracle skipped under
  the repository's floating Node-24 CI lane. Those findings are remediated with
  a nonblocking no-follow directory descriptor bridge, bound-destination
  reread/hash/final metadata validation, a CI-strict Node-24 compatibility
  oracle, and exact decoder-boundary/precedence tests.
- The subsequent frozen-byte review found two more security boundary gaps:
  macOS extended ACLs could grant non-owner access despite mode 0700/0600, and
  Go's derived build tags treated Android/iOS as Linux/Darwin. It also caught an
  inaccurate proposed-ledger attribution of the interim Go FIFO hang to Node.
  The new freeze rejects any Darwin extended ACL through descriptor-bound
  `fgetattrlist`, checks inherited destination ACLs before source access,
  exact-allowlists only Linux/macOS amd64/arm64, freezes Node's destination
  error classification, and corrects the ledger provenance below.
- The next 20-file freeze was rejected on four parity/evidence findings. An
  `O_RDONLY` directory anchor rejected Node-valid owner-only mode 0300
  directories; Go-only destination verification displaced Node's
  missing/non-regular path failure in compound races; Go 1.26's
  `crypto/rand.Read` made the apparent entropy-error branch process-fatal; and
  the ledger described only one direction of Node's raw-spelling versus
  normalized-creation behavior for `symlink/../directory`.
- The rejected 23-file freeze replaced the `os.Root` bridge with a
  search-only/path-only
  directory descriptor and direct descriptor-relative `openat`/`fstatat`
  operations, freezes the Node path-classification step before Go's reread
  hardening, reads `crypto/rand.Reader` through `io.ReadFull`, and adds native
  permission, entropy, compound-race, ACL-precedence, and bidirectional
  normalization regressions. The ledger text now states both normalization
  directions explicitly.
- Its fresh review found four remaining blockers: Darwin `O_SEARCH` ran before
  Node's initial `lstat` classification; bound-parent revalidation could
  intentionally displace Node's child-path error but the handoff claimed
  unqualified precedence parity; entropy failures were structured in Go but
  raw in Node without a ledger entry; and Darwin `openat`, `fstatat`, and
  `fgetattrlist` used Apple-private kernel trap numbers.
- The current 28-file freeze restores inspection/entropy/create phase order,
  freezes exact no-search and final-versus-ancestor classifications, scopes
  stable-parent precedence separately from a five-way compound parent/child
  divergence, adds a standalone entropy ledger entry, and routes all three
  Darwin operations through libSystem (including architecture-correct
  `fstatat`/`fstatat64`). Native arm64 and Rosetta amd64 regressions exercise
  the settled descriptor/ACL paths.
- This is a replacement builder handoff, not approval. The parcel is ready for
  a fresh-context adversarial recheck against the frozen hashes below.

## Intent

- Port `node-src/io/bounded-files.ts` into an isolated Go package that later
  plan fingerprint/evidence code can use for bounded, stable, no-follow reads
  and private snapshots.
- Preserve source-defined failure precedence, arbitrary-precision budget
  accounting, identity/TOCTOU checks, cleanup, and byte-scrubbing behavior.
- Deliberately harden snapshot-directory binding where the Node source is
  path-racy, and bind the completed destination to its returned digest, size,
  mode, owner, and identity; record those fail-closed differences in the
  runtime-plan divergence ledger before approval.
- Deliberately enforce the documented Linux/macOS amd64/arm64 release boundary
  inside the package even though the Node helper has no platform gate.
- Add no CLI wiring or generated-output change in this parcel.

## Base / Head

- Base: `c32479d6fdee4ce44944c4b8b8971900b97beda3`.
- Head: shared uncommitted working tree on
  `feature/go-canonjson-foundation`.
- Review scope: all twenty-eight files under `go/internal/artifacts/`, plus this
  handoff. Other shared-worktree changes are owned by separate parcels.
- Diff command: include every untracked file under `go/internal/artifacts/`
  and `docs/review-handoffs/go-wave7-bounded-files.md` from
  `git status --short`; do not infer scope from the rest of the dirty tree.

## Frozen package manifest

```text
4e53d336d1a02eb8c992cb896f6e710326b1c20eeb3c2b26841e1dd180b5caf1  go/internal/artifacts/acl_darwin.go
5d2193b12b3fba921f8d5c5321e19ab625d926a39be6c7eb89a37aea3417eed5  go/internal/artifacts/acl_other.go
3eef89a7d06cf84534824d988a4447701f1297f16b053cfec851072e092618a0  go/internal/artifacts/api.go
0960939d0ac1262352f80ccd0e7c24417ab7c98035d47469f6598ded873ea842  go/internal/artifacts/api_test.go
6a07ce9e1057ef1c9624fc93e76c360046516796bdb21c4eecff720df328cb9e  go/internal/artifacts/budget.go
ea91fcb6dba908fcde79ac326f3761e0b0ee96bc9eebe053e800cf390e4d10f3  go/internal/artifacts/budget_test.go
e753008818732992aec8ef4cc604bb688f825902540b4acf4695a6f50e2b7d2b  go/internal/artifacts/directory_darwin.go
2d8fef5a89f88e09f4c3e2c859e6a2d5173e5adbda6bab30116eae93ce897d83  go/internal/artifacts/directory_linux.go
8bf1eeda35d720cb3bbe631f00370f9410c673cbcd8f9c75fb9b499708b397d4  go/internal/artifacts/directory_root.go
f76ff28fe3c5d70953725e63abee26a8ebbb18bc97bd11dde07479319ef9d4a2  go/internal/artifacts/doc.go
23395ea9c55860368fd434a78184ef22152876a09f7bee435fcbcb1020bbb61e  go/internal/artifacts/identity.go
5eebc372182874465acc0b659888652f3df76eeb1d277d769ecfc76099e555f4  go/internal/artifacts/identity_darwin.go
9d8498125383b3caa874b817b486e6a80ec99f1c6ce8284d9b19af779f94b63c  go/internal/artifacts/identity_linux.go
f6fdca4d60cea5264932345148d04f5efb3b13e5c56c9dd1b83a69af875399b4  go/internal/artifacts/identity_unsupported.go
6a87dffa44d19d5dff658c73dcdab2cdf6830272f879ea2f392035f63a19ce6d  go/internal/artifacts/libsystem_darwin.go
3ab5f607295e1eaf41dc64608f516ae90f64b3cc76fdcbaf32be4ec5d2f0f87f  go/internal/artifacts/libsystem_darwin_amd64.go
4b800bfe204d331ea164c435ae81dd044050d1097848f1c0f26712a4b238dc9a  go/internal/artifacts/libsystem_darwin_amd64.s
cd7cfbadc186c279a48b9dfc4a0be6bb2a8851352ab4939d739a72e551fd38e1  go/internal/artifacts/libsystem_darwin_arm64.go
47d5771cb45843bb696cd68dfd09623ba4395f2de27a677c2dbb94ef156c24b9  go/internal/artifacts/libsystem_darwin_arm64.s
d8fb29cfb449a679537dc3d2c03597e3c90f7310fe1785dbd25439feea2a8522  go/internal/artifacts/platform_posix.go
c4c88a7af23bf76854dc29f4b953eabb15436bf243a32ec3a9dc9362f81956cf  go/internal/artifacts/platform_unsupported.go
627009ee70155221bc1720b77efdccfeb1ed6f3eb534db510b095d14d0491042  go/internal/artifacts/platform_unsupported_test.go
3d6b2247e78b1218a0723164e10ec2e66466b1c094025c1cb1e58746e68de2b7  go/internal/artifacts/snapshot.go
3fce132d923d4ece48217f6407c061d702226aa35e185da5006267fa05a6da55  go/internal/artifacts/snapshot_acl_darwin_test.go
7eec9ae3bcaa18ecdc908d2d666723bcfb2346af0a20b1c795a23827c6690966  go/internal/artifacts/snapshot_posix_test.go
8bec648b8981a125be0e996a4e0e72cd065f6207529e3641be977e8cec27ab2c  go/internal/artifacts/snapshot_test.go
f60734d7fc432272874050fa761a5e04337cb7fa55796a6fefbbc5c4498354c0  go/internal/artifacts/stable_file.go
6b135248030e1311993d8ab968e4a5f5dc55bff77197f150c960ec9ad7831d26  go/internal/artifacts/stable_file_posix_test.go
```

The handoff is frozen separately because including its own digest here would
be self-referential. The builder report accompanying review must supply the
handoff SHA-256.

## Source inputs consulted

- Provider schemas, OpenAPI/API contracts, provider source, and pack metadata:
  N/A.
- Existing docs or design records: `docs/go-runtime-plan.md` and the
  adversarial-review workflow/templates; `README.md` and
  `docs/operational-runtime.md` for the supported Terraform platforms.
- Other source evidence:
  - `node-src/io/bounded-files.ts` at commit
    `bd5db45c0571ae8ffaa61e94d59f2bdba5c9b664`, SHA-256
    `551249440b9a3bbda351021e388d4caf98e3f440a2f9ce0706c87178da371d7a`;
  - `node-tests/bounded-files.test.ts` at the same commit, SHA-256
    `b471d45c4f76e742dc61a8f36e78b79e3cb30eed19c594d095d942697f8bcd28`;
  - the live Node v24.15.0
    `buffer.constants.MAX_STRING_LENGTH` oracle, which returned `536870888`;
  - all seven production bounded-files consumers at base `c32479d6`:
    `reference-backend.ts`,
    `plan-evidence.ts`, `plan-policy.ts`, `exact-plan-apply.ts`,
    `plan-fingerprint.ts`, `plan-assessment.ts`, and `control-evidence.ts`;
  - the pre-dispatch platform gate in `node-src/cli/main.ts`, plus the repeated
    gates in `terraform-command.ts` and `terraform-show.ts`;
  - `node-src/domain/errors.ts`, the existing Go `procerr` spine, Go 1.26's
    Unix descriptor-relative patterns, and the Darwin libSystem/Linux syscall
    contracts used by the supported release targets;
  - zscalerctl at commit
    `79678e7c1f63f41a6a57e8b650a0525fd53379f3` in
    `/Users/dm/src/gh/dvmrry/zscalerctl` as a comparison lens only.

## Generated artifacts

- Reports, schemas, committed fixtures, snapshots, and demo/lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected delta

- Adds an unwired `internal/artifacts` foundation for validated shared budgets,
  bounded stable byte/UTF-8 reads, SHA-256 binding, and mode-0600 snapshots.
- `MaxTotalBytes` and `MaxFileBytes` are arbitrary-precision `*big.Int`
  boundaries. Construction, accessors, and reserve accounting defensively copy
  every mutable `big.Int`; internal addition and comparison never narrow to a
  machine integer.
- `ReadBudget` is a small pointer-to-state handle. Value copies share the same
  synchronized accounting state instead of copying a mutex or forking counts;
  nil and zero values fail closed. File and snapshot operations check that
  state before any path access. Atomicity is not an attribution guarantee:
  diagnostic-producing charges must be serial and deterministic, and counter
  snapshots must be taken after the charging phase.
- Linux/macOS amd64/arm64 stable source reads retain no-follow, nonblocking,
  and cloexec opens. Android, iOS, 32-bit aliases, and every other target fail
  closed with `UNSUPPORTED_BOUNDED_FILE_PLATFORM`; this is an intentional
  defense-in-depth enforcement of the release and Terraform platform boundary.
- `nodeMaximumStringLength` remains the Node v24.15.0 value `536870888`, pinned
  by the requested exact-version live oracle and a companion current-Node-24
  oracle that cannot silently skip when invoked under CI. The sibling Wave 6
  Makefile gate must land to invoke Go tests from repository CI. Sparse-file
  tests prove that equality is admitted before charging/reading and `MAX+1`
  wins before a conflicting exhausted-budget failure.
- Snapshot directories preserve Node's initial raw-spelling `lstat` failure
  phase, then validate the normalized creation target's type, mode, owner, and
  identity before entropy generation. The later authoritative bind requires no
  read permission: Linux uses
  `O_PATH|O_DIRECTORY|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC`, and Darwin uses
  `O_SEARCH|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC`. The descriptor is compared with
  the preflight identity and becomes the root for direct descriptor-relative
  `openat` creation and `fstatat`/`O_PATH` path inspection; no `/dev/fd`,
  `/proc/self/fd`, or second pathname open remains. Darwin calls `openat`,
  arm64 `fstatat` / amd64 `fstatat64`, and `fgetattrlist` through libSystem,
  never Apple-private kernel trap numbers. This preserves Node's success for
  owner-write/search mode 0300 and its create-phase failure for private modes
  without search permission while retaining nonblocking no-follow binding.
  Root and visible-path identities are revalidated around exclusive creation.
  After sync, Go preserves Node's descriptor-stat then child-classification
  precedence when the bound parent revalidates. A compound parent replacement
  deliberately returns `UNSAFE_SNAPSHOT_DIRECTORY` before child classification
  and is ledgered separately. Go then rereads and SHA-256 checks the `O_RDWR`
  destination through the same bound descriptor, compares full metadata
  through a final path recheck, and requires regular type, exact mode 0600,
  effective-UID ownership, and absence of Darwin extended ACLs.
  Directory/root ACLs are checked on bound descriptors at every validation,
  and an inherited destination ACL is rejected immediately after descriptor
  `Chmod` and before source access. Partial cleanup truncates/syncs only that
  already-bound descriptor.
- Snapshot-name entropy is read with `io.ReadFull(crypto/rand.Reader)` so an
  injected entropy failure remains nonfatal under Go 1.26 and is deliberately
  normalized to `SNAPSHOT_FAILED`. Node exposes the original raw entropy error;
  that error-shape difference is explicitly proposed for the ledger below.
- Existing CLI, report, plan, metadata, and generated bytes remain unchanged.

## Blocking findings and remediation

### 1. Node `bigint` domain was narrowed to `int64`

- Root cause: byte ceilings, aggregate accounting, and the `Bytes` accessor
  used `int64`, even though Node accepts arbitrary positive `bigint` values.
  The original value-shaped budget also made ordinary copying unsafe because
  it copied a live mutex and counters.
- Remediation: byte limits and all accounting now use `math/big.Int`; validated
  limits are copied into private state; `Reserve` copies its input before
  inspection; `Limits` and `Bytes` return detached copies. The public handle
  contains only a state pointer, so copies remain synchronized and atomic.
- Evidence: `TestReadBudgetArbitraryPrecisionAndBoundaryCopies` charges
  `2^62 + 2^62` under an exact `2^63` aggregate limit and exercises mutations
  of constructor inputs, reserve inputs, and accessor outputs. The concurrent
  reserve test deliberately mixes original and copied handles under `-race`.
  A direct Node 24.15 probe produced `9223372036854775808` for the same case.

### 2. Snapshot-directory path spelling and TOCTOU gaps

- Root cause: validating a caller spelling such as `link/`, `link/.`, or
  `link/../directory` can make Node's raw-path `lstat` inspect a different
  object from the lexically normalized path used for creation, and path-based
  creation can target a different directory after validation.
- Remediation: Go first performs the raw-spelling `Lstat` needed to preserve
  Node's inspection-failure phase, then cleans non-empty spellings and validates
  the actual normalized creation target. After entropy generation, a Linux
  `O_PATH` or Darwin `O_SEARCH` descriptor is opened with
  directory/nonblocking/no-follow/cloexec constraints; its privacy, owner, ACL,
  and device/inode are compared with the normalized preflight identity. That
  same live descriptor is the root for `openat` creation and
  `fstatat`/`O_PATH` path observation, preventing inode or descriptor-number
  reuse and eliminating a second pathname open. Root and visible-path
  identities must agree before descriptor-relative exclusive creation and
  again before success. The root remains open through descriptor-only cleanup.
- Evidence: deterministic tests cover `link/`, `link/.`, a legitimate real
  `dir/.`, both safe/unsafe directions of `link/../directory`, replacement
  after initial identity comparison and after descriptor binding,
  same-path/different-inode replacement, the exact revalidate-to-create race,
  a timeout-isolated FIFO replacement, final-success revalidation,
  non-redirection into a victim directory, bound file scrubbing, and
  destination identity replacement.

### 3. Non-nil zero-value budget reached filesystem access

- Root cause: `consumeStableFile` checked only `budget == nil`; a
  `&ReadBudget{}` opened the source before `Reserve` failed. Snapshot creation
  also bound the directory and opened a destination before reaching that
  failure.
- Remediation: an initialized-state preflight now gates all stable reads before
  source open and all snapshots before directory inspection/binding/create.
- Evidence: a zero-value budget reading a final symlink yields `READ_FAILED`
  rather than the `SYMLINK_NOT_ALLOWED` that proves an open occurred. The
  snapshot regression observes that its post-`Lstat` seam never runs and its
  private directory remains empty.

### 4. V8 maximum string length was an unproved literal

- Root cause: `nodeMaximumStringLength = 536870888` was copied from
  `buffer.constants.MAX_STRING_LENGTH` without a live executable pin.
- Remediation: `TestNodeMaximumStringLengthMatchesNode2415` locates Node,
  requires exact `v24.15.0`, runs
  `console.log(require("buffer").constants.MAX_STRING_LENGTH)`, parses the
  result as `int64`, and compares it with the Go literal. Missing or different
  Node versions skip; a probe failure or mismatch on the reviewed runtime
  fails. `TestNode24MaximumStringLengthRemainsCompatible` runs the same oracle
  on any Node 24 patch, and treats absent/non-24 Node as a failure when `CI` is
  set, so a floating Node-24 CI invocation cannot silently skip it. The
  repository-level invocation dependency is recorded below.
  Sparse exact-limit and `MAX+1` tests pin `>` rather than `>=` and prove the
  decoder ceiling precedes budget charging.
- Evidence: both live oracle tests passed on v24.15.0 and observed `536870888`;
  the sparse equality test reached `AfterOpen` after charging exactly one file
  and 536870888 bytes, while `MAX+1` returned `FILE_LIMIT_EXCEEDED` before an
  already exhausted file-count budget or `AfterOpen`.

### 5. Go's unsupported-platform gate is broader than the Node helper

- Decision: **intentional-and-safe; retain the broader Go gate**.
- Reachability evidence: exactly seven production modules import
  `bounded-files.ts`, and their operational paths belong to `plan`,
  `assert-clean`, `assert-adoptable`, or `apply`. `node-src/cli/main.ts` invokes
  the supported-platform check before dispatch, while `runTerraformCommand`
  and `terraformShowPlan` repeat it. The package is private and bin-only.
  In particular, the non-gated `root-catalog` command uses pack loading and a
  direct file read, never a bounded loader; the non-gated `deployment` command
  calls ordinary `loadDeployment`, whose read comes from `io/files`, not
  `bounded-files.ts`. The bounded `loadBoundAssessmentDeployment` is called
  only by the gated assertion/apply paths, and `loadBoundAssessmentRootCatalog`
  has no production caller at the reviewed base. Likewise,
  `environment-generator.ts` imports only the reference-backend variable
  constant; the bounded `referenceBackendEnvironment` function is called only
  inside the gated `planEnvironmentRoots` path.
  `README.md` and `docs/operational-runtime.md` define Linux as production,
  macOS as development/test, and Windows Terraform execution as rejected
  before preflight or spawn.
- Important boundary: some bounded reads occur before an actual Terraform
  initialize/show call, so the proof is the command-entry gate, not temporal
  ordering after a child process. Node's assertion explicitly rejects
  `win32`; it does not strictly allowlist Darwin/Linux on exotic Unix systems.
  Go deliberately exact-allowlists only the documented Linux/macOS amd64/arm64
  release targets until equivalent no-follow, ownership, ACL, and device/inode
  guarantees exist elsewhere. Build constraints explicitly exclude Go's
  Android→Linux and iOS→Darwin aliases and all 32-bit architectures.
- The package doc comment records this decision. Future Block C/D production
  consumers must remain within the supported, entry-gated CLI workflows and
  retain the package's independent OS/architecture gate; a naturally portable
  metadata/rendering path must not consume this package without reopening the
  platform decision.

### 6. Concurrent charging had nondeterministic failure attribution

- Root cause: the mutex makes `ReadBudget` data-race safe and each charge
  atomic, but lock acquisition decides which input crosses a limit. Scheduling
  can change both the failing path and, because file count precedes aggregate
  bytes, the reported failure code.
- Remediation: the `ReadBudget` doc comment now requires `Reserve`,
  `EnterDirectory`, and `ReserveDirectoryEntry` to run serially in a
  deterministic caller/traversal order, forbids sharing one charging budget
  across goroutines for observable work, and permits observable counter
  snapshots only after the serial charging phase. The existing concurrent test
  is explicitly scoped to atomicity/race safety, not attribution.
- Entry condition for Blocks C/D: establish sorted/deterministic traversal,
  serialized charging, and a collect-then-emit barrier before wiring any
  fingerprint, evidence, snapshot, or cleanup consumer to this package.

### 7. Direct `os.OpenRoot(path)` could block on a raced FIFO

- Root cause: the first **Go** root-binding remediation still called
  `os.OpenRoot(normalized)` after `Lstat`. Go 1.26's Unix implementation opens
  that path without `O_NONBLOCK`, `O_NOFOLLOW`, or `O_DIRECTORY` before its
  directory check. Replacing the path with a FIFO at that seam could stall the
  bounded operation indefinitely.
- Remediation: after Node-compatible path inspection and entropy generation,
  the implementation performs the authoritative search-only/path-only
  directory bind with `O_NONBLOCK|O_NOFOLLOW|O_DIRECTORY`, retries only
  `EINTR`, compares it to the preflight identity, and uses that same live
  descriptor directly for `openat` and final-path identity operations. A raced
  FIFO/symlink/non-directory is rejected without blocking; there is no second
  pathname or descriptor-path bridge.
- Evidence: `TestSnapshotDirectoryFIFOReplacementDoesNotBlock` runs the race in
  a three-second timeout-isolated child process, replaces the post-`Lstat` path
  with a FIFO, and completes in about 0.01 seconds with
  `UNSAFE_SNAPSHOT_DIRECTORY`. The test is included in every race-soak
  iteration. This is a Go implementation-regression guard, not a claim that
  Node's child `O_CREAT|O_EXCL` open blocks on a parent FIFO. Node receives
  `ENOTDIR` immediately and its public wrapper normalizes that error to
  `SNAPSHOT_FAILED` / `unable to create plan snapshot`; Go's extra bind check
  returns `UNSAFE_SNAPSHOT_DIRECTORY`. That public failure-class hardening is
  stated in the proposed ledger below.

### 8. Same-inode destination mutations were not bound to the result

- Root cause: the previous implementation returned the source digest/size
  after comparing only destination metadata observations taken after the
  source's final-stat hook. An in-place equal-size overwrite, truncate, or mode
  change preserved device/inode and could make those observations agree even
  though the returned digest, size, or private-mode claim no longer described
  the saved snapshot. Node has the same post-copy weakness.
- Remediation: the destination is opened `O_RDWR`; after copy and sync, Go
  repeats Node's descriptor-stat, child-path classification, and full identity
  comparison in that order whenever its bound parent revalidates. It then
  checks the bound file's
  regular/exact-0600/effective-UID-owned state, hashes exactly the
  expected bytes through the same descriptor with a fixed scrubbed buffer,
  compares the source and destination digest/size, requires full metadata to
  remain stable across that reread, revalidates the directory, and compares a
  final descriptor stat with the root-relative path identity. Content, size,
  mode, ACL, and regular-identity mismatches use the existing
  `SNAPSHOT_PATH_CHANGED` failure and flow through bound-descriptor scrub
  without displacing the primary structured error. With a stable parent, a
  missing/symlink/non-regular final destination path remains `FILE_CHANGED`;
  differential cases freeze that distinction. The compound case where the
  parent and child path both change is the separate finding 17 divergence.
- Evidence: deterministic `BeforeFinalStat` cases perform same-device/inode
  equal-size overwrite, truncate, and chmod-to-0400 mutations. All three return
  `SNAPSHOT_PATH_CHANGED` and leave the bound destination at size zero after
  cleanup. A 3×5 cross-product combines those mutations with missing, symlink,
  FIFO, directory, and regular path replacements: the first four retain
  Node's `FILE_CHANGED`, regular replacement returns `SNAPSHOT_PATH_CHANGED`,
  and every bound original is scrubbed. A Darwin ACL-plus-missing-path case
  separately freezes `FILE_CHANGED`. The success case verifies final ownership.

### 9. Exact-v24.15 oracle skipped in floating Node-24 CI

- Root cause: the requested provenance test correctly skips when Node is not
  exact v24.15.0, while repository CI installs floating `node-version: "24"`.
  A later 24.x patch therefore skipped the sole live executable check.
- Remediation: retain the exact requested v24.15 provenance test and add the
  current-Node-24 compatibility test described in finding 4. It executes under
  the existing CI toolchain and fails, rather than skips, if CI loses Node 24.
- Evidence: the exact and compatibility tests both execute and pass locally;
  no out-of-scope workflow or Makefile is modified here. The sibling Makefile
  integration dependency is explicit below.

### 10. Darwin mode/UID checks ignored extended ACLs

- Root cause: macOS extended ACLs are independent of Unix mode bits. A
  mode-0700 effective-UID-owned directory with an inheritable `everyone` ACL
  produced a mode-0600 destination whose inherited read/write ACL survived
  descriptor `Chmod`; the earlier privacy checks all accepted it.
- Remediation: Darwin amd64/arm64 uses descriptor-bound libSystem
  `fgetattrlist(ATTR_CMN_EXTENDED_SECURITY)` with a fixed 64 KiB buffer,
  `FSOPT_REPORT_FULLSIZE`, strict response/reference bounds, and fail-closed
  call/malformed/oversize handling. Any nonempty ACL rejects the directory.
  The opened root is rechecked on every binding validation; the destination is
  checked after `Chmod` before source open/charging, before and after reread,
  and at the final descriptor observation. This remediation is Darwin-specific;
  Linux retains the source mode/UID contract, under which effective POSIX ACL
  masks participate in the already-checked group mode class.
- Evidence: native Darwin regressions reject an ACL-public 0700 directory
  before creation and an inherited destination ACL before the source hook or
  budget charge. The latter confirms the ACL survives descriptor `Chmod`, then
  confirms bound cleanup leaves the rejected file at size zero. The ordinary
  clean-directory success test exercises the zero-ACL path repeatedly.

### 11. Derived platform tags bypassed the intended gate

- Root cause: Go makes the `linux` build constraint true for Android and the
  `darwin` constraint true for iOS. The OS-only split therefore selected the
  supported POSIX/identity files on both targets; it also admitted Linux 386
  despite the 64-bit V8 decoder literal and amd64/arm64 release matrix.
- Remediation: supported files and tests now require
  `(darwin || linux) && !ios && !android && (amd64 || arm64)`. The complement
  selects the unsupported implementation and regression tests.
- Evidence: `go list` on Android/arm64, iOS/arm64, and Linux/386 selects
  `identity_unsupported.go`, `platform_unsupported.go`, and
  `platform_unsupported_test.go`; all three test selections cross-compile.
  Linux/amd64 continues to select and compile the supported implementation.

### 12. The directory anchor added an unintended read-permission requirement

- Root cause: `O_RDONLY|O_DIRECTORY` requires owner read permission, while
  Node's private-directory contract requires only effective-UID ownership and
  zero group/other bits. A useful owner-only mode 0300 directory has write and
  search permission, so Node can create its snapshot even though the first Go
  anchor failed with `EACCES`.
- Remediation: Linux binds with `O_PATH`; Darwin binds with `O_SEARCH`. Both
  retain directory/nonblocking/no-follow/cloexec constraints, support
  descriptor-relative child operations, and avoid requiring directory read.
  Direct `openat` creation removes the read-requiring `os.OpenRoot` duplication.
- Evidence: an exact compiled Node v24.15 probe and
  `TestSnapshotAllowsOwnerWriteSearchDirectoryWithoutReadPermission` both
  succeed in a mode-0300 private directory and produce a mode-0600 snapshot.
  `TestSnapshotNoSearchPrivateDirectoryPreservesNodeCreateFailure` freezes
  private modes 0600, 0400, 0200, and 0000 as exact create-phase failures, and
  `TestSnapshotNoSearchUnsafeDirectoryPreservesNodePrivacyFailure` proves an
  unsafe unsearchable directory is rejected during privacy classification.
  Native Darwin arm64 and Rosetta amd64 exercise `O_SEARCH`; Linux O_PATH code
  and tests cross-compile on amd64 and arm64.

### 13. Go-only verification displaced Node's compound-race failure

- Root cause: destination reread/private-file verification ran before the
  visible path lookup. If a hook both mutated the bound inode and replaced the
  visible path with missing/symlink/FIFO/directory state, the Go-only check
  returned `SNAPSHOT_PATH_CHANGED` before Node's `pathIdentity` step could
  return `FILE_CHANGED`.
- Remediation: after sync, Go observes the bound descriptor, revalidates the
  parent, and then classifies and compares the root-relative child path. When
  parent revalidation succeeds, this preserves Node's descriptor-stat then
  child-classification order before reread/hash/private-file hardening. A
  second child lookup and final descriptor comparison remain for later races.
- Evidence: the exact Node v24.15 compound oracle and the stable-parent 3×5 overwrite,
  truncate, and chmod cross-product described in finding 8 agree on every
  code; bound cleanup and replacement/victim non-mutation are also asserted.

### 14. Go 1.26 `crypto/rand.Read` made entropy failure process-fatal

- Root cause: Go 1.26 documents that `crypto/rand.Read` never returns an error
  and irrecoverably calls the runtime fatal path when its Reader fails. The
  apparent `newSnapshotName` error branch was therefore dead, whereas Node's
  `randomBytes(16)` exception is catchable by its caller.
- Remediation: `newSnapshotName` uses `io.ReadFull(crypto/rand.Reader, ...)`.
  Normal operation retains the same system CSPRNG; an injected Reader error is
  returned and normalized through the Go `SNAPSHOT_FAILED` error spine instead
  of killing the process.
- Evidence: `TestSnapshotEntropyFailureIsCatchable` substitutes an erroring
  Reader after directory inspection, receives the fixed I/O `ProcessFailure`
  `SNAPSHOT_FAILED` / `unable to create plan snapshot`, observes no budget
  charge, and observes no created destination. Exact Node v24.15 independently
  returned the original catchable raw `Error` with no failure code at the same
  phase. This intentional error-shape difference is the second proposed ledger
  bullet below.

### 15. `symlink/../directory` has a bidirectional normalization divergence

- Root cause: Node validates the raw caller spelling but `path.join` lexically
  normalizes before creation. A symlink followed by `..` can therefore make
  the checked directory differ from the creation directory in either safety
  direction. Go preserves raw-spelling `Lstat` failures, then deliberately
  validates the actual normalized creation directory.
- Result: when raw resolution is safe 0700 but the normalized destination is
  unsafe 0755, Node can create in the unsafe directory and Go rejects it. When
  raw resolution is unsafe 0755 but the normalized destination is safe 0700,
  Node rejects and Go accepts the safe actual destination. This is intentional
  actual-target semantics, not a claim that Go always fails where Node passes.
- Evidence: exact Node v24.15 probes and both subtests of
  `TestSnapshotBindsNormalizedDirectoryAcrossSymlinkDotDot` freeze these two
  directions. The proposed ledger below now states both explicitly.

### 16. Darwin descriptor binding displaced Node's initial directory failure

- Root cause: the rejected freeze opened Darwin `O_SEARCH` before `Lstat`.
  An owner-private directory without search permission therefore returned
  `SNAPSHOT_FAILED` / `unable to inspect snapshot directory` before Node's
  privacy classification and entropy phases. Blanket `ELOOP`/`ENOTDIR`
  mapping also blurred final non-directory/symlink failures from ancestor
  lookup failures.
- Remediation: the public operation now preserves Node's raw-spelling `Lstat`
  failure phase, validates the normalized actual target, generates entropy,
  and only then performs the implementation-only descriptor bind. A bind
  failure while the same preflight identity remains visible is normalized to
  the create phase; a raced replacement is rejected as unsafe. The descriptor
  type/mode/owner/ACL and preflight device/inode must still agree before any
  destination or source access.
- Evidence: exact Node v24.15 probes and
  `TestSnapshotNoSearchPrivateDirectoryPreservesNodeCreateFailure` agree on
  code/message for modes 0600, 0400, 0200, and 0000; the unsafe no-search case
  returns `UNSAFE_SNAPSHOT_DIRECTORY`. The final/ancestor table agrees for
  final regular/FIFO/symlink versus trailing-regular, regular/FIFO ancestor,
  and looping-symlink ancestor paths. The timeout-isolated FIFO replacement
  remains nonblocking and the existing post-inspection replacement tests prove
  device/inode rejection.

### 17. Bound-parent revalidation intentionally precedes Node child attribution

- Root cause: the rejected handoff claimed unqualified Node descriptor-stat →
  child-path precedence, but Go revalidates its bound parent between those
  steps. If the visible parent and child both change, that extra security check
  can select a different failure before child classification.
- Decision: retain and explicitly ledger the fail-closed divergence. When the
  parent revalidates, Go preserves Node's child failure attribution. When the
  bound parent has been replaced, Go returns `UNSAFE_SNAPSHOT_DIRECTORY`
  before consulting an attacker-controlled visible child.
- Evidence: the exact source-built Node v24.15 five-way oracle returned
  `FILE_CHANGED` for missing/symlink/FIFO/directory children and
  `SNAPSHOT_PATH_CHANGED` for a regular replacement. The corresponding five
  subtests of
  `TestSnapshotParentReplacementIntentionallyPrecedesNodeChildClassification`
  return `UNSAFE_SNAPSHOT_DIRECTORY`, scrub the original bound inode to zero,
  and leave every replacement and symlink target untouched. The separate 3×5
  stable-parent matrix continues to agree with Node on every code.

### 18. Darwin used Apple-private raw kernel trap numbers

- Root cause: the rejected freeze issued hard-coded Darwin kernel traps for
  `openat`, `fstatat`, and `fgetattrlist`. Current native success did not make
  those Apple-private syscall numbers a supported cross-release ABI.
- Remediation: the package now uses the same libSystem dynamic-call pattern as
  Go/x/sys. Both architectures bind `openat` and `fgetattrlist`; arm64 binds
  `fstatat`, while amd64 binds `fstatat64` to match `syscall.Stat_t`. Tiny
  architecture-specific trampoline address slots work with `CGO_ENABLED=0`;
  no private kernel trap number remains.
- Maintenance boundary: trampoline dispatch uses the pinned Go 1.26
  `syscall.syscall6` linkname retained for x/sys-style libSystem callers. It is
  a discouraged non-public Go toolchain interface, not an Apple-private kernel
  ABI. Any Go toolchain upgrade must re-audit that handshake and repeat native
  arm64/Rosetta amd64 plus CGO-disabled link verification before widening the
  reviewed boundary.
- Evidence: native arm64 and Rosetta amd64 ACL, no-search, identity, and
  compound-race suites pass. CGO-disabled arm64/amd64 cross-links pass, and
  `nm -m` shows `_openat`, `_fgetattrlist`, arm64 `_fstatat`, and amd64
  `_fstatat64` as the expected external libSystem symbols.

## Invariants claimed

- Evidence is not silently dropped: source failure code/message and precedence
  survive budget, read, identity, close, cleanup, and UTF-8 paths except for
  the explicit snapshot, entropy, and platform divergences proposed in the
  ledger below.
- Initial descriptor stat charges the budget; later failure does not refund it;
  success requires matching pre/post descriptor and final-path identity.
- Byte arithmetic is exact across the complete positive Node `bigint` domain;
  no caller-owned mutable integer aliases internal limits or counters.
- Budget checks remain atomic across concurrent calls and copied handles;
  deterministic diagnostics additionally require the documented serial
  charging convention.
- Final symlinks, non-regular files, path swaps, unstable
  size/time/identity, Darwin extended ACLs, malformed UTF-8, unsupported
  OS/architecture targets, and cleanup failures fail closed.
- Owner-only mode 0300 snapshot directories remain usable without weakening
  descriptor binding; private directories without search permission retain
  Node's create-phase failure. Entropy-source errors remain catchable, are
  structured in Go as explicitly ledgered, and do not charge the read budget
  or leave a destination.
- Internal read/chunk buffers are cleared, successful byte results are
  detached, and failure details are copied before cleanup augmentation.
- Snapshot creation cannot block on or be redirected through a final-component
  replacement after binding. At its final observation, success binds the
  reread destination bytes/digest/size, exact private mode and owner, stable
  destination identity, absence of a Darwin extended ACL, bound ACL-free
  directory identity, root-relative destination path, and still-visible
  directory identity. A same-UID writer acting after that final observation
  remains outside any ordinary file-path API's point-in-time guarantee.
- With a successfully revalidated parent, descriptor-stat then child-path
  failure attribution matches Node. If parent and child both change, parent
  revalidation intentionally fails first and descriptor-only cleanup remains
  bound to the original directory and file.

## Proposed go-runtime-plan.md ledger addition

`docs/go-runtime-plan.md` is currently owned by the REST parcel and was not
edited here. The coordinator should append the following three bullets to the
**Allowed divergences** list, immediately after the final Undici
response-head/trailer resource-bound bullet and before
`### Filesystem error text decision (2026-07)`:

> - Node 24.15's saved-plan snapshot helper applies `lstat` to the caller's
>   directory spelling, lexically normalizes that spelling through `path.join`,
>   and then creates with a path-based `open`. Consequently, trailing `link/` or
>   `link/.` can bypass final-symlink rejection, a parent replacement between
>   validation and creation can redirect the file, and `link/../directory` can
>   make the checked and creation directories differ in either direction. With
>   raw-safe 0700 but normalized-unsafe 0755, Node can create in the unsafe
>   directory while Go rejects; with raw-unsafe 0755 but normalized-safe 0700,
>   Node rejects while Go accepts the safe actual destination. Go preserves a
>   raw-spelling `lstat` failure before deliberately validating the normalized
>   creation target. After entropy generation, it binds Linux with
>   `O_PATH|O_DIRECTORY|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC` and Darwin with
>   `O_SEARCH|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC`, then uses the same descriptor for
>   exclusive `openat` creation, root-relative `fstatat`/`O_PATH` observation,
>   and repeated root/visible identity validation. Search/path-only binding
>   retains Node's support for owner-only mode 0300 directories and its
>   create-phase failure for private directories without search permission,
>   without a second pathname open. On Darwin, `openat`, architecture-correct
>   arm64 `fstatat` / amd64 `fstatat64`, and `fgetattrlist` use the public
>   libSystem ABI; Apple-private kernel trap numbers are not supported. Node
>   returns the source digest after post-copy stats without rereading the
>   destination or rechecking final mode/owner, so a completed same-inode
>   overwrite, truncate, or chmod can invalidate the returned snapshot claim.
>   Its Darwin mode/effective-UID checks also accept nonempty extended ACLs that
>   grant another identity access. Go opens the destination `O_RDWR`. When its
>   bound parent revalidates, it preserves Node's descriptor-stat then child-path
>   failure attribution before rereading/hashing the bound descriptor and
>   requiring source/destination digest and size equality, stable full metadata,
>   exact mode 0600, effective-UID ownership, absence of Darwin extended ACLs,
>   and final descriptor/root-path identity. In the narrow compound race where
>   parent revalidation fails while Node's currently visible child is missing,
>   symlinked, FIFO, a directory, or a replacement regular file, Go returns
>   `UNSAFE_SNAPSHOT_DIRECTORY` before child classification; Node, which has no
>   bound-parent revalidation, returns `FILE_CHANGED` for the first four and
>   `SNAPSHOT_PATH_CHANGED` for the regular replacement. This exception applies
>   only to compound parent-plus-child races; stable-parent destination races
>   retain Node's ordering and attribution. The nonblocking directory bind also
>   prevents recurrence of an interim Go direct-`os.OpenRoot` FIFO hang. In a
>   raced post-inspection parent-FIFO case, Node's child open receives `ENOTDIR`
>   and its public wrapper returns `SNAPSHOT_FAILED` / `unable to create plan
>   snapshot`; Go's extra descriptor-bind classification returns
>   `UNSAFE_SNAPSHOT_DIRECTORY`. Removing any part requires equivalent Node
>   hardening and frozen
>   ACL/symlink/normalization/parent-swap/destination-mutation evidence;
>   weakening Go to raw-path, ACL-blind, stat-only, or unbound-parent behavior
>   is not an acceptable parity fix.

> - Node 24.15 generates a snapshot name with `randomBytes(16)` outside the
>   snapshot `try` block; an entropy failure escapes as the original raw `Error`
>   with no `ProcessFailure` code after private-directory inspection but before
>   budget charge or destination creation. Go reads `crypto/rand.Reader` with
>   `io.ReadFull` so the same failure remains nonfatal under Go 1.26, but
>   deliberately normalizes it to the fixed I/O `ProcessFailure`
>   `SNAPSHOT_FAILED` / `unable to create plan snapshot`. Both sides charge zero
>   budget and leave no destination. Removing this normalization requires a
>   nonfatal Go entropy path that reproduces Node's raw error type, code, and
>   message while retaining frozen zero-budget/no-destination evidence.

> - Node 24.15's bounded-files helper has no platform gate; on Windows its
>   snapshot check omits the effective-UID comparison and otherwise proceeds.
>   Its seven production consumers are private plan/show workflow modules whose
>   supported CLI commands execute a platform check before dispatch. That Node
>   check explicitly rejects Windows; separately, the operational contract
>   supports Linux in production and macOS for development/testing. Go
>   independently enforces that narrower documented boundary inside
>   `internal/artifacts`: Linux/macOS amd64/arm64 use no-follow descriptor opens
>   plus ownership, ACL where applicable, and device/inode identity checks;
>   Android, iOS, 32-bit aliases, and every other target fail closed with
>   `UNSUPPORTED_BOUNDED_FILE_PLATFORM`. This is broader than Node's explicit
>   `win32` rejection and prevents future Go callers from silently weakening
>   those proofs. Blocks C/D must keep production consumers within those
>   supported, entry-gated CLI workflows. Widening support requires a separately
>   reviewed handle-based ownership/ACL/identity implementation and platform
>   differential evidence; removing the gate without those proofs is not an
>   acceptable parity fix.

The coordinator's ledger edit and resulting plan hash must be included in the
integration review. This handoff records the proposed text but does not modify
or approve the coordinator-owned plan.

## zscalerctl comparison lens

- Reference: `/Users/dm/src/gh/dvmrry/zscalerctl`, origin
  `https://github.com/dvmrry/zscalerctl.git`, commit
  `79678e7c1f63f41a6a57e8b650a0525fd53379f3`.
- Aligned patterns: `internal/fileperm/fileperm_posix.go` uses
  `O_NOFOLLOW|O_CLOEXEC`, validates the opened descriptor, and has consumers
  read that same handle. `internal/diff/diff.go` uses `os.Root`, local-path
  validation, descriptor `Stat`, and a `LimitReader(max+1)` bound. These support
  the direction already taken here.
- Stronger bounded-files behavior retained: zscalerctl has no non-vendor
  dev/inode identity tuple, before/after descriptor identity comparison,
  descriptor-to-visible-path equality check, or opened-root identity
  revalidation. This parcel already provides those checks and must not weaken
  them for superficial consistency.
- No comparison-only behavior was adopted in this parcel. The destination
  reread/hash, search-only nonblocking root binding, Darwin ACL rejection,
  stable-parent compound-race precedence, parent-revalidation hardening, and
  catchable entropy handling were required by adversarial findings, not
  inferred from zscalerctl. The latter two error-attribution differences are
  explicitly proposed for the ledger; all preserve the outward success result
  while failing closed on races or access grants the Node and cited zscalerctl
  patterns do not bind.
- Post-cutover candidates, noted but not applied: same-directory temp + mode
  0600 + file-sync + close + rename for fixed-name mutable state; whole-set
  collision preflight before multi-file publication; manifest-last completion
  markers; and zscalerctl's handle-based Windows volume/DACL validation if
  Windows support is ever designed.
- Patterns not adopted: zscalerctl's reject-existing check followed by rename
  is not atomic no-overwrite behavior and lacks containing-directory sync; its
  dump manifest is published before later report/error files. Neither is a
  byte-compatible replacement for this parcel's unique exclusive snapshots or
  a transaction-complete state marker.

## Tests run against this freeze

- `go test -run '^TestNodeMaximumStringLengthMatchesNode2415$' -v
  ./internal/artifacts` — pass on exact Node v24.15.0; oracle value `536870888`.
- The focused `go test -count=1 -run
  '^(TestNodeMaximumStringLengthMatchesNode2415|TestNode24MaximumStringLengthRemainsCompatible|TestBoundedCollectionAllowsExactNodeMaximumBeforeRead|TestBoundedCollectionRejectsOversizedSparseFileBeforeAllocation|TestSnapshotDirectoryFIFOReplacementDoesNotBlock|TestSnapshotRejectsInPlaceDestinationMutationBeforeReturn|TestSnapshotRejectsPrivateDirectoryWithExtendedACLBeforeCreate|TestSnapshotRejectsInheritedDestinationACLBeforeCopy|TestSnapshotPreservesNodeFailureForMissingOrNonRegularDestinationPath|TestSnapshotAllowsOwnerWriteSearchDirectoryWithoutReadPermission|TestSnapshotNoSearchPrivateDirectoryPreservesNodeCreateFailure|TestSnapshotNoSearchUnsafeDirectoryPreservesNodePrivacyFailure|TestSnapshotDirectoryInspectionPreservesNodeFinalAndAncestorClassification|TestSnapshotEntropyFailureIsCatchable|TestSnapshotBindsNormalizedDirectoryAcrossSymlinkDotDot|TestSnapshotPreservesNodeFailurePrecedenceForStableParentCompoundDestinationRaces|TestSnapshotParentReplacementIntentionallyPrecedesNodeChildClassification|TestSnapshotPreservesNodeFailurePrecedenceForACLAndMissingDestination)$'
  -v ./internal/artifacts` command — pass; every named test/subtest executed.
- Fresh source-bound Node reference suite: `./node_modules/.bin/tsc -p
  tsconfig.test.json --outDir /private/tmp/infrawright-node-test.2G8WtZ`, copy
  `package.json` into that output root to preserve ESM type, then `node --test
  /private/tmp/infrawright-node-test.2G8WtZ/node-tests/bounded-files.test.js` —
  14/14 pass. This avoids relying on ignored/stale `.node-test` output.
- Exact v24.15 probes against that source-built output returned
  `SNAPSHOT_FAILED` / `unable to create plan snapshot` for owner-private mode
  0200, `UNSAFE_SNAPSHOT_DIRECTORY` for an unsafe no-search directory,
  `UNSAFE_SNAPSHOT_DIRECTORY` for final regular/symlink paths, and
  `SNAPSHOT_FAILED` / `unable to inspect snapshot directory` for
  regular-with-trailing-separator, regular/FIFO ancestor, and looping-symlink
  ancestor paths. The forced entropy probe returned raw `Error`, null code,
  zero budget, and no destination.
- The exact source-built five-way parent/child oracle returned
  `FILE_CHANGED` for missing/symlink/FIFO/directory children and
  `SNAPSHOT_PATH_CHANGED` for a regular replacement; the moved original was
  size zero and every replacement/target survived. The corresponding Go
  matrix returned `UNSAFE_SNAPSHOT_DIRECTORY` in all five rows with the same
  cleanup/non-mutation properties, freezing the proposed divergence.
- `go test -race -count=10 ./internal/artifacts` — pass in 16.949s package
  time (20.060s wall) against the current implementation.
- `go vet ./internal/artifacts` — pass with no output.
- `gofmt -l internal/artifacts/*.go` — exit 0 with no paths.
- Go error-handling skill audit, `check-errors.sh internal/artifacts` — pass,
  no anti-patterns found.
- `GOOS=android GOARCH=arm64 go list -f '{{join .GoFiles ","}} | {{join
  .TestGoFiles ","}}' ./internal/artifacts`, plus the same command for
  iOS/arm64 and Linux/386, each selected
  `identity_unsupported.go`, `platform_unsupported.go`, and
  `platform_unsupported_test.go` rather than the supported files.
- `go test -c -o /tmp/infrawright-artifacts-$target.test
  ./internal/artifacts` cross-builds passed with `GOOS/GOARCH` set to
  Android/arm64, Linux/386, Linux/amd64, Linux/arm64, Darwin/amd64, and
  FreeBSD/amd64. iOS/arm64 requires the Go toolchain's external-linking mode:
  the `CGO_ENABLED=0` command failed with the expected `ios/arm64 requires
  external (cgo) linking` setup error, while `CGO_ENABLED=1` passed. Its
  separately inspected assembly-file set was empty, proving the Darwin
  trampolines stay excluded. Native tests run on Darwin/arm64.
- Current libSystem-specific verification also passed with `CGO_ENABLED=0` for
  native Darwin arm64 and cross-linked Darwin arm64/amd64. `nm -m` resolved
  arm64 `_fstatat`, amd64 `_fstatat64`, and both `_openat`/`_fgetattrlist`.
  The amd64 binary's targeted ACL, no-search, path-classification, identity,
  and five-way parent/child suite passed under Rosetta.
- Final `go test ./...` — pass across the complete shared Go module, including
  `internal/artifacts`; no sibling breakage required a rerun.
- Not run natively in this final pass: Linux runtime tests or an unsupported
  platform runtime. The Linux and FreeBSD cross-builds are current; the
  required macOS race soak, live Node oracles, formatter, vet, Node source
  tests, and module-wide Go sweep are current.

## Integration dependencies

- The exact-v24.15 oracle is independently runnable as requested. The
  companion floating-Node-24 oracle becomes a required CI gate only when the
  sibling Wave 6 REST parcel's `Makefile` change (`check-go-vendor` in
  `make check`) lands; base `c32479d6` does not invoke Go tests from CI. The
  integration coordinator must land or replace that gate atomically. This
  parcel does not own or modify `Makefile`.
- The three proposed runtime-plan bullets above are still absent from the
  coordinator-owned authoritative ledger. Applying them and freezing the
  resulting `docs/go-runtime-plan.md` hash remains an integration condition,
  not an edit authorized to this parcel.
- The sibling REST parcel's current frozen manifest includes
  `docs/go-runtime-plan.md`; applying these bullets will invalidate that hash.
  The coordinator must re-freeze the REST handoff/manifest after the ledger
  edit rather than treating its prior hash as still authoritative.

## Known deferrals

- Deferred work: directory walking and depth/entry accounting in fingerprint;
  descriptor-bound evidence cleanup (verify directory/file identity, reopen
  with `O_RDWR|O_NONBLOCK|O_NOFOLLOW`, truncate, fsync); atomic report writes;
  and every CLI consumer.
- Reason it is safe to defer: this package is not wired to production. It
  exposes synchronized budget primitives but does not claim the downstream
  evidence sandwich or cleanup behavior.
- Follow-up owner or trigger: Blocks C/D fingerprint/evidence parcels must
  serialize budget charging and counter observation in deterministic traversal
  order, remain behind the Terraform platform entry gate, and either add a
  narrow reviewed artifacts primitive or own equivalent platform syscalls
  before consuming this foundation. Fixed-name atomic publication may use the
  zscalerctl pattern only after separately freezing directory-sync,
  no-overwrite, and manifest-last semantics.

## Review focus

- Verify the package and this handoff against their frozen hashes; reject scope
  drift before reviewing semantics.
- Reproduce Node arbitrary-precision behavior independently; attack aliases at
  every `big.Int` input/output boundary, zero/nil states, copied handles,
  failure precedence, and concurrent overflow attempts.
- Inspect the Linux O_PATH and Darwin O_SEARCH/libSystem
  openat/fstatat/fstatat64/fgetattrlist contracts rather than trusting comments.
  Attack mode 0300 and no-search permission cases,
  terminal-link and `symlink/../` spellings in both safety directions,
  FIFO/non-directory replacement, parent replacement after initial validation
  and immediately before creation, directory mode/owner/identity changes,
  same-inode destination content/size/mode/ACL mutation crossed with every
  destination path class under a stable parent, the five-way compound
  parent/child replacement, raw-Node versus structured-Go entropy failure, and
  cleanup identity.
- Verify Android, iOS, and 32-bit targets select only the unsupported files;
  Go's derived OS tags must not silently widen the release allowlist.
- Verify the intentional snapshot hardening is present in the runtime-plan
  allowed-divergence ledger before integration; verify the proposed platform
  bullet accurately distinguishes the CLI entry gate from direct helper parity.
- Attack the `ReadBudget` documentation as an API contract: future consumers
  must serialize charge attribution and take visible counters only after the
  barrier even though the mutex remains race-safe.
- Continue attacking stat precision, close/sync/truncate failure precedence,
  FIFO/non-regular handling, chunk ownership/scrubbing, BOM/malformed UTF-8,
  and unsupported-platform behavior.
