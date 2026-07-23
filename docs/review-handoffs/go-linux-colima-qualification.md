# Go Linux qualification after GPT-5.6 Pro review

## Intent

Qualify the pushed Go candidate on a real Linux kernel before stable Linux
release review. This run was credential-free and provider-free. It did not
contact an API, use a remote backend, download a Terraform provider, or mutate
external infrastructure.

## Candidate and environment

- Candidate base: `fa993d71bf7945e6e4da91af386d8e9d418e8d47` on
  `feature/go-canonjson-foundation`.
- Colima: existing `default` profile, Linux/arm64, 2 CPUs, 2 GiB RAM.
- Kernel: Ubuntu `6.8.0-117-generic`, `aarch64`.
- Go: `go1.26.5 linux/arm64`, official `golang:1.26.5-bookworm` image.
- Node: `v24.15.0`, npm `11.12.1`, with the downloaded archive verified
  against Node's published `SHASUMS256.txt` before use.
- Terraform: `1.15.4 linux_arm64`, official
  `hashicorp/terraform:1.15.4` image.
- The frozen Node bundle was rebuilt inside Linux from the exact checkout. Its
  SHA-256 remained
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`.

The source was a detached exact-SHA worktree mounted into Colima. Build output
and temporary test binaries existed only in that disposable worktree.

## Initial Linux findings

After the frozen oracle was materialized and the source mount was made writable
for differential-test binaries, the full Go suite had exactly two failures:

1. `TestSameSizeMutationIsDetectedThroughOpenedDescriptor`
2. `TestAssessmentTemporaryCleanupRefusesUnexpectedOrReplacedEntries/replaced_snapshot`

Both failed 20/20 times on the container overlay filesystem and on an ext4
Docker volume.

The first test assumed an immediate same-size write must change the metadata
tuple observed by the preceding and following descriptor stats. Linux can
coalesce both operations into one filesystem timestamp tick. The production
reader still hashes the exact opened descriptor bytes and compares descriptor
and path identity; the test now pins a distinct mtime after its injected write
so it deterministically exercises that metadata comparison instead of the
filesystem clock granularity.

The second test assumed unlinking and recreating a file must produce a different
device/inode pair. Linux can immediately reuse the freed inode. The test now
renames the original snapshot before creating the replacement, keeping its
inode allocated and therefore constructing the different identity the test is
specified to verify. The production cleanup implementation is unchanged: it
removes only a zero-length regular entry matching the expected bound identity
through the already-open directory root.

No production source was changed for either finding.

## Linux verification

After the two deterministic test corrections:

- Both focused tests passed 20 consecutive times on overlayfs.
- Both focused tests passed 20 consecutive times on an ext4-backed volume.
- `go test -count=1 -p=1 ./...` passed, including every frozen-Node
  differential lane.
- `go test -race -count=1 -p=1 ./...` passed.
- `go vet ./...` passed.
- `gofmt -l .` was empty.
- `go mod tidy -diff` was empty.
- `go list -m all` passed.
- The explicit inherited-descriptor tests passed on `/proc/self/fd/3`,
  including original-byte retention after pathname replacement and rejection
  of closed, pipe, read-write, and spawn-failure cases.

## Real Terraform descriptor probe

An ephemeral mode-0700 directory inside the Terraform container contained an
empty `main.tf`, no provider requirements, local state only, and no backend
configuration. Terraform 1.15.4 performed:

1. `init -backend=false`;
2. provider-free `plan -out=tfplan`;
3. `show -json /proc/self/fd/3` with descriptor 3 opened on `tfplan`;
4. `apply /proc/self/fd/3` against the same provider-free saved plan;
5. a second plan with detailed exit code zero.

Observed result:

- descriptor Show: pass;
- descriptor Apply: `0 added, 0 changed, 0 destroyed`;
- second plan: no-op.

The container was removed after the command. No credentials, provider plugin,
API, cluster, tenant, remote state, or non-local object was reachable.

## Darwin verification

On `go1.26.5 darwin/arm64`:

- both focused corrected tests passed 20 consecutive times;
- `go test -count=1 ./...` passed;
- `go test -race -count=1 ./...` passed;
- `go vet ./...`, formatting, tidy diff, and whitespace checks passed.
- the explicit `RootCatalog|Transform|Topology|Generation`, A6, and Block D5
  `cmd/iw` gates passed;
- `npm run typecheck` passed, and the retained Node suite passed 793 of 795
  tests with only its two documented optional-external skips.

## Review focus

- Verify that forcing an observable mtime change preserves the same-size
  mutation test's intended metadata-identity assertion rather than concealing a
  production regression.
- Verify that rename-before-replacement is the correct portable construction of
  a different live file identity and that accepting immediate freed-inode reuse
  does not permit deletion of an unrelated live inode.
- Recheck the Linux `/proc/self/fd/3` implementation and descriptor lifetime
  against the exact-Apply source, not only this run report.
- Treat the previously documented same-UID in-place-write,
  provider-child-descriptor-inheritance, and procfs-availability boundaries as
  unchanged qualification residuals.
