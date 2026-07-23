# GPT-5.6 Pro remediation: whole-candidate review handoff

## Intent

Close the three blockers reported against `7ad6513` without broadening the Go
rewrite, then close the two named release prerequisites:

1. Apply the exact assessed saved-plan inode rather than reopening public
   `tfplan` by pathname.
2. Publish state-aware filtered imports through the existing descriptor-bound
   atomic transaction.
3. Reject unpaired UTF-16 surrogates identically in Node and Go before the
   final Node authority freeze.
4. Pin Go 1.26.5, run `govulncheck`, and correct stale post-archive CLI wording.

## Base / Head

- Base: `7ad6513fa3e34e89352ac2b3e42cede01cf6e277`.
- Candidate parent: `7ed2934`. The reviewed 14-file provenance closure and
  this handoff are intended to be committed together.
- Reviewed commits:
  - `e5d1a81` — state-aware staging atomic publication.
  - `2397edc` — descriptor-bound exact Apply.
  - `c993546` — cross-runtime surrogate rejection and authority re-pin.
  - `7ed2934` — Go 1.26.5, vulnerability scan evidence, and stale docs.
- Pending reviewed closure: three authority fixtures now include their
  transitive `node-src/json/control.ts` source, every dependent lock is
  repinned, and all nine documented fixture size/SHA pairs match the files.
- Review diff: the complete candidate against `7ad6513`.
- Scope excluding this new handoff: 64 tracked files, `+2,039/-308`. The
  provenance closure itself is 14 tracked files, `+41/-35`; this handoff is
  included with that closure.

## Files and Design

### State-aware staging

`go/internal/adopt/import_staging.go` now routes both exact copies and filtered
text through one `os.Root` transaction: randomized private sibling, complete
write, source-mode preservation, close, descriptor-relative identity recheck,
and descriptor-relative rename. Tests cover symlink and hard-link destinations,
injected pre-rename failure, mode preservation, and no temporary remnants.

### Exact Apply

The evidence layer opens the private saved-plan snapshot once with
`O_RDONLY|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC`, verifies its regular-file identity
and digest under a fresh serial `ReadBudget`, and retains that descriptor
through Show and Apply. The runner exposes exactly one inherited read-only,
regular, seekable descriptor to Terraform as `/dev/fd/3` on Darwin or
`/proc/self/fd/3` on Linux. Unsupported platforms or unavailable descriptor
filesystems fail closed. Complete-plan and freshness gates remain before Apply.

The D5 transcript differential normalizes only Node's randomized snapshot path
and the two reviewed descriptor paths to `<assessment-snapshot>`; lower-level
tests continue to assert the concrete platform path and original bytes after
pathname replacement.

### UTF-16 contract and authority

Both runtimes reject unpaired surrogate units in every decoded JSON key/value
with reason `Unpaired UTF-16 surrogate` at the raw UTF-16 source offset. The
scanner records the first invalid unit but lets whole-document syntax and
control-contract errors retain priority. Invalid-surrogate Go keys do not enter
duplicate-key accounting, preventing U+FFFD normalization from inventing a
different error. Valid pairs, astral text, literal U+FFFD, numeric behavior,
ordinary invalid-UTF-8 normalization, and valid output bytes remain unchanged.

The reference-backend raw-token workaround is removed. A lifecycle regression
proves a mixed-escape surrogate fails before any Terraform initialize/plan
call. Nine authority fixtures changed only in source/provenance hashes; their
Node and Go consumers, the active authority manifest, A6/D5 locks, and runtime
record were repinned. Final authorities:

- Node bundle: `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`
  (3,040,955 bytes).
- Checksum file: `b955f56a128a590f7811472959ce580cb344ed4fe400906377e6a2e30263f63e`.
- Authoring manifest: `c9485be8b0c7a805247d54250c700c562ba8f32fa60f9e35ceb1b6c6e6671612`.

### Release prerequisite

`go/go.mod` pins `toolchain go1.26.5`; no application dependency changed.
Post-archive documentation now recognizes completed Cobra help/inventory and
removal of the old dispatch guard while retaining the real future cleanup.

## Source and Generated Evidence

- Frozen Node TypeScript is the compatibility source for JSON error priority,
  source offsets, and valid bytes.
- The reviewed safety invariant supersedes Node's pathname-based Apply handoff;
  temp lifecycle and process invocation do not produce committed artifact
  bytes.
- Official Go release history establishes 1.26.5 as the current 1.26 security
  patch: <https://go.dev/doc/devel/release#go1.26.0>.
- Recursive comparison of all nine changed authority fixtures found only the
  expected provenance/source-closure substitutions; no report or artifact
  payload changed.
- Two consecutive Node builds produced identical final bundle bytes and SHA.

## Verification

Final-tree results:

- `npm run typecheck` — pass.
- `npm run test:node` — 795 tests, 793 pass, 2 known optional skips, 0 fail.
- `go version` — `go1.26.5 darwin/arm64`.
- `go test -count=1 ./...` — pass.
- `go test -race -count=1 ./...` — pass.
- `go vet ./...` — pass.
- `go mod tidy -diff` — no diff.
- `go list -m all` — pass; application module graph unchanged.
- CGO-disabled Linux amd64/arm64 test binaries compiled for `cmd/iw`,
  `internal/assessment`, and `internal/terraformcmd` — pass.
- A6 and D5 frozen-Node differential lanes — pass.
- RootCatalog, Transform, Topology, and Generation byte gates — pass.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` —
  `No vulnerabilities found.`
- `gofmt` and `git diff --check` — clean.

Parcel reviews already completed:

- Staging: fresh approval, no findings.
- Exact Apply: initial request changes; remediation recheck approved with no
  remaining findings.
- Surrogate/authority: initial request changes; final recheck approved with no
  blocking or non-blocking findings.
- Whole candidate: initial request changes identified incomplete transitive
  provenance and stale documented fixture digests; the closure recheck approved
  with no blocking or non-blocking findings.

## Invariants Claimed

- The same open saved-plan inode is shown, classified, rechecked, and applied.
- `complete == true`, freshness, policy, branch, and destructive-change gates
  remain fail-closed before Apply.
- A failed staging publication cannot truncate a known-good destination or
  follow a destination symlink/hard link to mutate another inode.
- Unpaired surrogates cannot be silently normalized, collapsed, reordered, or
  emitted into infrastructure/evidence bytes.
- No live Apply is reachable from any test; all Terraform execution is injected
  or uses credential-free local fixtures/fake executables.

## Known Residuals / Qualification Boundaries

- Descriptor binding closes pathname replacement, not same-UID in-place writes
  to an already-open inode. A portable filesystem-only defense for that stronger
  actor is not available on the supported Darwin/Linux boundary.
- The raw plan descriptor may remain visible to Terraform provider children if
  Terraform itself does not close descriptor 3. It is read-only and contains
  already-authorized plan data; this residual is recorded, not silently hidden.
- Linux requires mounted procfs for `/proc/self/fd`; absence fails closed.
- Real-provider exact-Apply qualification remains a separate human-authorized
  event. These remediations performed no cluster, tenant, credential, remote
  backend, provider API, or live Terraform Apply operation.

## Review Focus

- Try to find any path where Show and Apply can receive different plan bytes or
  where descriptor lifetime ends before Terraform is fully reaped.
- Check every pre/post-success error precedence, especially Apply success
  followed by descriptor close or cleanup failure.
- Check reader ownership, source-mode capture, identity recheck, and rename
  behavior in both staging branches.
- Recheck surrogate error priority, raw UTF-16 offsets, invalid-key duplicate
  handling, and the provenance-only fixture claim.
- Confirm the toolchain bump did not alter dependencies, artifact bytes, or
  platform contracts, and that all user-facing governance remains unchanged.
