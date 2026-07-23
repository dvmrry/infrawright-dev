# Go operational cutover roadmap

Status: ARCHIVE EXECUTED 2026-07-22. The original revision assumed the seven
authoring commands would remain Node indefinitely; the final goal became
**full Node archival**,
so the authoring surface is in cutover scope and the master sequence
is:

1. Authoring port completes in Go against the current frozen Node
   behavior (complete; unaffected by degrouping — the authoring
   packages are largely independent of logical-root topology).
2. Authority handoff gate: final Node runtime frozen as the immutable
   v1 oracle, Go declared product authority
   (complete; singleton-state-topology-v2.md §3 D6).
3. Degrouping implemented Go-only as versioned v2 (**complete**; G1–G3 and
   credential-free full-151 gates landed, with no production/live execution).
4. State inventory, Kubernetes qualification, and Zscaler canaries. The
   credential-free full-151 generation/backend-key gates are complete; live
   qualification remains separately access- and human-gated.
5. Cutover and archive of all executable Node dependencies (this
   document's phases).

The Go operator runtime is built and fixture/lab-qualified, and the authoring
implementation passed the external Opus, GPT-5.6 Pro, and Fable review sequence
on 2026-07-20 at `c3e18a67e4b61b90860e02b782342b3e98ebbd80`. This does not
imply a production/provider-controlled exact Apply; that remains separately
human-gated. The formal authority handoff and singleton-state v2 implementation
are complete. Executable Node routing, build, CI, and release paths were
archived directly in this dev repository after downstream testing; the
credential-free archive does not claim that the separately human-gated live
qualification was performed here. See
[the archive record](archive/node-runtime-archive.md).

## 0. Preconditions

1. Authoring parity complete and the authority handoff gate passed. **Complete.**
2. Singleton-state v2 landed Go-only and re-qualified (all five gates
   in that document), so the cutover ships the simplified topology
   once instead of cutting over twice. **Partially complete:** implementation
   and credential-free gates 1–3 are complete at `2ebd37d`; live gates 4–5
   remain required before cutover.
3. `747f613` and subsequent parcels pushed; integration PR flow
   current.
4. Kubernetes qualification evidence recorded in-repo (sanitized) and
   the live-Apply status corrected in
   [go-runtime-v2.md](go-runtime-v2.md), which still describes
   controlled Apply as outstanding.
5. Go toolchain pinned to 1.26.5 (security prerequisite completed 2026-07-20)
   before any release artifact is produced.

## 1. CLI routing: complete

All operator and authoring Make targets use `IW ?= dist/iw`. The transitional
operator/maintainer split, bundle prerequisite, and rollback override are
deleted. The version-specific `zpa-provider-evidence` CLI did not cross the
handoff; its reviewed evidence remains in the generic source-analysis corpus.

## 2. Binary and distribution

- **Layout:** `dist/iw-<os>-<arch>` (darwin/arm64, darwin/amd64,
  linux/amd64, linux/arm64), plus a local `dist/iw` convenience copy
  for the host platform. Static builds, `CGO_ENABLED=0`.
- **Version embedding:** `-ldflags` inject version (git tag), commit,
  and the root-catalog `schema_version` it was built against; surfaced
  by `iw version` and stamped into reports where the Node CLI stamps
  its version today (byte-compat rule: the *shape* is fixed; the value
  changes with every release on both runtimes, so this is not a parity
  break).
- **Integrity:** `SHA256SUMS` over all release files, signed with
  **minisign** (decision: minisign over cosign — no keyless/OIDC
  dependency, verifiable fully offline in air-gapped consumers pulling
  from Artifactory; single keypair held by release owner). This closes
  the open signing decision in go-runtime-plan.md.
- **Discovery:** the binary must locate its package root (`packs/` including
  flat pack-set profiles, catalogs, and demo) when invoked from outside the repo:
  explicit `INFRAWRIGHT_PACKAGE_ROOT` wins; otherwise walk up from the
  binary's own directory. Relocated-binary verification (§4) proves it.

## 3. CI: one Go lane with tripwires

CI runs formatting, vet, the complete Go suite, distribution checks, root
catalog drift checks, and the flattened-profile reduced-root gate. Static
checks reject legacy Make/build/CI routing. Dynamic checks put failing `node`
and `npm` interceptors first on `PATH` while exercising the distribution.

## 4. Distribution testing

- Pack-profile matrix: full profile plus each pruned packset, run
  through the Go binary's catalog/topology/generation commands;
  module-selection counts recorded.
- Relocated-binary check: copy `iw` + release contents to a temp
  prefix outside any checkout, run the §3 smoke there.
- Release contents (target list): binaries, SHA256SUMS + signature,
  `catalogs/`, `packs/` (including `*.packset.json`), `demo/`, LICENSE, and a
  RELEASE.md stating the version and frozen-oracle lineage. No package-manager
  manifest or compatibility profile directory is part of the current tree.

## 5. Rollout phases

Historical sequence below. The dev repository moved directly from qualified
Go authority to archive on 2026-07-22 after the user confirmed downstream
testing and accepted Git history as the recovery path; no rollback executable
remains in this tree.

1. **Candidate (opt-in):** release `iw` binaries; downstream opts in
   via `INFRAWRIGHT_CLI=dist/iw` (or `IW_OPERATOR` directly). Node
   remains the default. Zscaler canary matrix runs here: the standing
   test-tenant read → import → plan → exact-Apply cycle on the Go
   binary, per provider family.
2. **Default switch:** `IW_OPERATOR ?= dist/iw` becomes the committed
   default; the Node path reachable only by explicit override. This is
   the **stable tag**: the full command surface — operator and
   authoring — routes to Go, documented in RELEASE.md and
   go-runtime-v2.md.
3. **Rollback window (one release):** the frozen Node bundle still
   built and published; `INFRAWRIGHT_CLI` override still honored. Any
   parity regression found downstream reverts by variable, not by
   release.
4. **Archive:** delete the `INFRAWRIGHT_CLI` compatibility override
   and the `IW_MAINTAINER` split, stop building and publishing the
   Node bundle (including the `package.json` bin entries), and retire
   `node-src` executable paths from the build. The frozen v1 oracle
   bundle is retained by SHA as an immutable provenance artifact; it
   is executed in CI only while pre-handoff differential gates remain,
   and never modified.

## 6. Authoring commands position

(Amended 2026-07-19; the previous revision kept these Node
indefinitely.) The six retained authoring commands are in cutover scope
under [go-authoring-port-roadmap.md](go-authoring-port-roadmap.md). Existing
command behavior remains differential-gated; the source-first,
OpenAPI-optional extension uses independently reviewed source-bound
goldens because Node has no equivalent output. The ZPA-specific validator
retires into that corpus rather than being ported as a seventh command.
Authoring authority is precondition 1 of this roadmap and the first leg
of the authority-handoff gate in singleton-state-topology-v2.md §3 D6.
After the archive phase, no executable Node dependency remains anywhere
in the product.

## 7. Exit criteria

- CI green with archive tripwires active and the Node lane retired.
- Flattened profiles and physically reduced roots checked in CI.
- `go-runtime-v2.md` and the archive record identify Go as sole authority and
  the v1 oracle as provenance-only.
