# Go operational cutover roadmap

Status: DECIDED — second of two roadmaps; runs strictly after
[singleton-state-topology-v2.md](singleton-state-topology-v2.md)
completes its qualification gates. The Go operator runtime is built
and lifecycle-qualified; what remains is routing, release engineering,
and a controlled default switch. Nothing here changes artifact bytes.

## 0. Preconditions

1. Singleton-state v2 landed and re-qualified (all five gates in that
   document), so the cutover ships the simplified topology once instead
   of cutting over twice.
2. `747f613` and the v2 parcels pushed; integration PR flow current.
3. Kubernetes qualification evidence recorded in-repo (sanitized) and
   the live-Apply status corrected in
   [go-runtime-v2.md](go-runtime-v2.md), which still describes
   controlled Apply as outstanding.
4. Go toolchain bumped 1.26.3 → 1.26.5 (security) before any release
   artifact is produced.

## 1. CLI routing: two lanes, one variable each

The Makefile's single `INFRAWRIGHT_CLI` currently routes everything to
the Node bundle. It splits along the v2 command boundary:

```
IW_OPERATOR   ?= dist/iw                          # Go binary
IW_MAINTAINER ?= $(NODE) dist/infrawright-cli.mjs # Node, unchanged
```

- All operator targets (fetch, transform, adopt, roots, scope-paths,
  plan-roots, stage-imports, plan, assert-*, apply, gen-env, modules,
  check-pack, check-pack-set, deployment, resources, …) use
  `IW_OPERATOR` and drop their `dist/infrawright-cli.mjs` build
  prerequisite entirely.
- The seven maintainer commands (reconcile, openapi-map,
  source-operation-map, source-evidence-eval, provider-probe,
  transform-adopt-parity, zpa-provider-evidence) use `IW_MAINTAINER`
  and keep the Node build prerequisite.
- During the candidate period only, `INFRAWRIGHT_CLI` remains honored
  as an explicit override of `IW_OPERATOR` (this is the opt-in/rollback
  lever); it is deleted at the end of the rollback window.

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
- **Discovery:** the binary must locate its package root (packs,
  packsets, catalogs, demo) when invoked from outside the repo:
  explicit `INFRAWRIGHT_PACKAGE_ROOT` wins; otherwise walk up from the
  binary's own directory. Relocated-binary verification (§4) proves it.

## 3. CI: split lanes with tripwires

`check.yml` becomes two lanes:

- **Go operator lane:** build all platforms, `gofmt`/`go vet`, full
  `go test ./...` including the differential corpus against a *pinned
  oracle artifact* (the frozen bundle checked in or fetched by SHA —
  the lane needs Node only to execute the oracle, never to build it).
- **Node maintainer/oracle lane:** existing Node build + tests, scoped
  to maintainer tools and oracle-bundle reproducibility.

Tripwires, both enforced in the Go lane:
1. Static: operator Make targets must not reference `$(NODE)`, `npm`,
   or `dist/infrawright-cli.mjs` (grep-based check target).
2. Dynamic: a smoke job runs the operator lifecycle
   (resources → roots → gen-env → modules generate/validate →
   check-pack) with `node` and `npm` removed from `PATH`.

## 4. Distribution testing

- Pack-profile matrix: full profile plus each pruned packset, run
  through the Go binary's catalog/topology/generation commands;
  module-selection counts recorded.
- Relocated-binary check: copy `iw` + release contents to a temp
  prefix outside any checkout, run the §3 smoke there.
- Release contents (frozen list): binaries, SHA256SUMS + signature,
  `catalogs/`, `packs/`, `packsets/`, `demo/`, LICENSE, and a
  RELEASE.md stating the version, oracle SHA lineage, and the
  operator/maintainer boundary. `package.json` continues to publish
  the Node CLI unchanged until the archive phase.

## 5. Rollout phases

1. **Candidate (opt-in):** release `iw` binaries; downstream opts in
   via `INFRAWRIGHT_CLI=dist/iw` (or `IW_OPERATOR` directly). Node
   remains the default. Zscaler canary matrix runs here: the standing
   test-tenant read → import → plan → exact-Apply cycle on the Go
   binary, per provider family.
2. **Default switch:** `IW_OPERATOR ?= dist/iw` becomes the committed
   default; Node operator path reachable only by explicit override.
   This is the **stable tag**: operator = Go, maintainer = Node, the
   coexistence boundary documented in RELEASE.md and go-runtime-v2.md.
3. **Rollback window (one release):** Node operator bundle still built
   and published; `INFRAWRIGHT_CLI` override still honored. Any parity
   regression found downstream reverts by variable, not by release.
4. **Archive:** delete the `INFRAWRIGHT_CLI` compatibility override,
   stop building the Node operator bundle, retire operator-command
   code paths from the Node build (maintainer tools and the frozen
   differential oracle remain). The oracle bundle stays pinned by SHA
   for as long as the differential corpus is retained.

## 6. Maintainer lane position

The seven maintainer commands remain Node indefinitely; they are
authoring-time tools whose migration has no operator-facing payoff.
Any future port is an independent workstream with its own
justification — it is explicitly out of scope for the stable tag and
must not be treated as cutover debt.

## 7. Exit criteria

- Default switch shipped and tagged; rollback window elapsed with no
  parity regressions.
- CI green on both lanes with tripwires active.
- Relocated-binary and pruned-profile checks in CI, not just run once.
- go-runtime-v2.md updated: cutover recorded as complete, remaining
  Node surface enumerated as maintainer-only.
