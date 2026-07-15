# Operational Node Runtime

Infrawright's production adoption path is an ordinary typed Node 24 library
behind a thin `infrawright` CLI. Packs, registries, provider schemas, profiles,
and deployment metadata remain caller-selected inputs:

```text
packs + registries + schemas + deployment
                    |
          generic Node 24 library/CLI
                    |
fetch / transform / adopt / modules / roots / staging
                    |
       plan / assessment / exact-plan Apply
                    |
                 Terraform
```

The operational runtime and maintained provider-authoring commands are
Python-independent. This is not a claim that the repository contains no
Python: Python remains temporarily available as a differential oracle and for
legacy migration checks while the Node implementation is qualified for the
final archive/removal step.

## Authoritative Command Inventory

The table below is the production command inventory consumed by the focused
Make/CLI routing test. Each Make target must expand to the listed command in
the built generic CLI and must not invoke Python. The CLI command—not Make—is
the behavior owner.

<!-- operational-command-inventory:start -->
| Surface | Make target | CLI route |
|---|---|---|
| Pack validation | `check-pack` | `check-pack` |
| Profile/catalog validation | `check-pack-set` | `check-pack-set` |
| Deployment metadata/path query | `deployment` | `deployment` |
| Generated-resource listing | `resources` | `resources` |
| Reference-ordered resource listing | `resources-reference-order` | `resources --order=references` |
| Module generation | `gen-modules` | `modules generate` |
| Module validation | `validate-modules` | `modules validate` |
| Fetch | `fetch` | `fetch` |
| Fetch diagnostics | `fetch-diag` | `fetch-diag` |
| Transform | `transform` | `transform` |
| Adopt | `adopt` | `adopt` |
| Environment/root generation | `gen-env` | `gen-env` |
| Root query | `roots` | `roots` |
| Changed-path scoping | `scope-paths` | `scope-paths` |
| Saved-plan root query | `plan-roots` | `plan-roots` |
| Import staging | `stage-imports` | `stage-imports` |
| Import unstaging | `unstage-imports` | `unstage-imports` |
| Plan | `plan` | `plan` |
| Saved-plan cleanup | `clean-plans` | `clean-plans` |
| Clean-plan assessment | `assert-clean` | `assert-clean` |
| Adoptable-plan assessment | `assert-adoptable` | `assert-adoptable` |
| Exact-plan Apply | `apply` | `apply` |
<!-- operational-command-inventory:end -->

The inventory test asks Make itself to expand each recipe. It therefore sees
multiline recipes, continuations, variables, target-specific values, and the
delegated `resources-reference-order` alias. It separately inspects built-CLI
help so a documented Make route cannot point at a missing command.

Operational Make targets consume the shipped bundle and build it only when it
is absent; source-file timestamps from a fresh checkout do not authorize a
runtime rebuild. Maintainers explicitly rebuild changed Node sources with
`make metadata-cli` or the normal `npm run build` development gate.

Make resolves one deployment authority for the entire invocation and exports
it to every recipe and nested Make: command-line/Make `DEPLOYMENT` wins, then a
nonempty imported `INFRAWRIGHT_DEPLOYMENT`, then `deployment.json`. Any
explicit CLI `--deployment` emitted by Make uses that same resolved value.

The metadata and module surfaces are ordinary Make adapters too:

```sh
make deployment DEPLOYMENT_QUERY=module-dir
make resources RESOURCE=zia_url_categories
make resources-reference-order RESOURCE=zia_url_categories
make gen-modules RESOURCE=zia_url_categories
make validate-modules RESOURCE=zia_url_categories
```

With an explicit deployment, module selectors are closed under its root
topology. Selecting either member of a grouped root generates or validates all
members that `gen-env` will reference; ungrouped resources remain narrow.

The maintained authoring targets `reconcile`, `openapi-map`,
`source-operation-map`, `source-evidence-eval`, and `provider-probe` use the
same bundled Node CLI. Tests that execute Python comparison implementations
and the frozen ZCC migration machinery are explicitly retained parity and
compatibility surfaces, not runtime prerequisites.
Node migration differentials resolve their test-only Python oracle from a
nonempty `PYTHON`, then `python3`, then `python`. The retained parity authority
accepts Python 3.12/UCD 15.0.0 and Python 3.13/UCD 15.1.0; set `PYTHON` to one
of those interpreters when the system default is newer. This selection affects
tests only and does not add Python to any operational command.

The default repository/Make qualification path is Python-independent:

```sh
npm run test:node
make check-node
```

The Node selector runs every compiled test file without a Python-oracle import,
rejects direct hardcoded Python subprocesses in that selected set, and includes
the import Oracle tests plus the bundled operational workflow smoke. It reports
the exact selected/excluded counts so the remaining migration differential
surface cannot be mistaken for Node coverage. During the archive window,
`npm run check:all` and `make test-python-legacy` remain explicit compatibility
gates for the retained Python authorities.

## Runtime and Release Contract

The primary release artifact and package binary are:

```text
infrawright -> dist/infrawright-cli.mjs
              dist/infrawright-cli.mjs.sha256
```

The bundle targets Node 24, contains its runtime npm dependencies, and is
executable where the platform preserves executable mode. Running the accepted
artifact requires Node 24, the adjacent `package.json` used to locate the
bundle root, selected pack/profile/deployment data, and Terraform for Terraform
operations. It does not require npm, package installation, `node_modules`, the
TypeScript source tree, esbuild, tsgo, or Python. The checksum detects
accidental byte corruption but is not a signature; release/tag trust remains
authoritative.

Verify the already-built runtime without authorizing a rebuild:

```sh
make verify-runtime \
  DEPLOYMENT=deployment.json \
  PACK_PROFILE=packsets/full.json \
  PACK_CATALOG=packsets/full.json
```

The target has no dependency on the `dist` build target. It verifies Node 24,
the SHA-256, a coherent package root and package binary, CLI startup and
operational help, pack/profile consistency, and the selected deployment input.
It rejects nearer package metadata that would change the root discovered by the
bundle. It neither invokes npm nor Python.

The exact-archive stripped-runtime smoke runs `make verify-runtime`, a pure
resource query, and `make demo-contract` from a tree without `node_modules`,
`node-src`, `node-tests`, the lockfile, TypeScript configuration, Python
operational sources, transition catalogs, or legacy bundles. npm, npx, and
Python tripwires prove the demo consumes only the shipped generic bundle plus
fake Terraform.

The bundle discovers the package-root `package.json` above `dist/`, while
operational inputs may be selected explicitly with `--root`, `--profile`,
`--catalog`, and `--deployment` (or their documented environment equivalents).
A complete release therefore includes package metadata, profiles, and all
manifests, registries, schemas, and overrides selected by those profiles.

### Source-build registry contract

Rebuilding the bundle is a maintainer/build-host action, not a work-machine
runtime prerequisite. A faithful source build requires the pinned ordinary npm
packages and the current platform's optional build-tool binaries from
`package-lock.json`. A restricted corporate registry must mirror those exact
packages before it can run `npm ci --ignore-scripts` and `npm run build`.

Inspect the configured registry without installing or rewriting configuration:

```sh
make source-build-preflight
node scripts/build-environment-preflight.mjs --manifest
```

The preflight reports only sanitized registry hosts and exact package
name/version failures. It verifies registry metadata against the lockfile,
detects omitted optional or development build packages, rejects registry-host
rewrite policies that would leave public lock URLs authoritative, detects
public scoped-registry bypasses, and
does not expose npm credentials, proxy credentials, or `.npmrc` contents. It
does not fall back to the public registry or download vendor binaries. The
derived manifest separates ordinary packages, each supported platform's
optional packages, and install-script packages that could attempt an
out-of-band download when optional binaries are absent.

This diagnostic answers registry-metadata readiness for the pinned lockfile; it
does not replace `npm ci`'s package/lock synchronization check. The checked-in
package and lock metadata remain synchronized, and a later source build still
uses the supported `npm ci --ignore-scripts` command before building.

If the preflight says the source build is unavailable, obtain the approved
`dist/infrawright-cli.mjs` and checksum from the trusted build path, run
`make verify-runtime`, and use that artifact with Node 24. Mirror readiness is
required only when the restricted environment must compile the bundle itself.

`dist/infrawright.mjs` and
`dist/infrawright-zcc-collector-child.mjs` remain frozen legacy migration
artifacts with their existing package entries. They are not the primary
operational CLI and are not prerequisites of its self-containment check. They
remain staged until their possible external consumers are inventoried.

## Terraform and Platform Support

- Linux is the production-supported Terraform execution platform.
- macOS is supported for development and testing.
- Windows Terraform execution through Infrawright is unsupported and fails
  before filesystem preflight or process spawn.
- Pure metadata and rendering functions may remain portable where they are
  naturally portable; that does not imply Windows Terraform support.

Terraform/OpenTofu remains required for module/root formatting and all provider
or plan operations. The Adopt Oracle's mechanically verified import-only
`terraform apply` writes only ephemeral local scratch state and is not a
deployment Apply. Deployment Apply is a separate command and accepts only the
exact already-saved `tfplan` after its fingerprint and assessment gates are
rechecked.

`plan --save` creates the pair `tfplan` and `tfplan.sources`. Assessment reads
and binds that exact pair. `apply` rechecks the pair and executes exactly the
saved plan rather than replanning; success removes only that saved pair. See
[Adoption Command Surface](adoption-command-surface.md) for the full lifecycle.

This repository readiness slice uses fake Terraform and local fixtures. It
does not establish live-provider, live-backend, or deployment-Apply
qualification.

## External Qualification Checklist

These checks are for a later approved work environment. This repository slice
does not provide credentials, execute either qualification, switch ADO, or
authorize deployment mutation.

### A. Read-only qualification

1. Select the exact accepted CLI and verify its SHA-256 checksum.
2. Make Python unavailable.
3. Supply approved read-only credentials out of band.
4. Fetch a bounded resource cohort.
5. Run Adopt import/provider Read.
6. Verify scratch Oracle cleanup.
7. Generate the selected modules and roots.
8. Stage imports state-aware against the intended backend.
9. Save a plan.
10. Run Node `assert-adoptable`.
11. Require zero create without import, update, replace, and destroy actions;
    only imports/no-op may remain.
12. Retain sanitized hashes, versions, and assessment reports.

### B. Separately authorized import-only Apply qualification

1. Recheck the exact approved plan and fingerprint.
2. Confirm branch, backend, policy, and destroy gates.
3. Apply only that exact import-only saved plan.
4. Unstage imports and moves.
5. Start from a fresh workspace against the same remote state.
6. Repeat Fetch, Adopt, generation, staging, and planning.
7. Require a clean/no-op second plan.
8. Switch an external operational lane only after separate approval.

## Frozen Architecture Inventory

Nothing in this inventory is deleted, refactored, archived, or declared
unused by this slice.

| Frozen surface | Current in-repository consumers | External-consumer risk | Likely later action | Prerequisite |
|---|---|---|---|---|
| Transition catalogs under `catalogs/` | Legacy process-host operations and migration tests | Unknown callers may supply or bind their digests | Keep, then archive/delete candidate by catalog | External consumer inventory and accepted generic-runtime qualification |
| ZCC compare/parity operations | Frozen process host and Python/Node differential tests | Existing migration jobs may call the operations | Archive candidate | Confirm no work-side or ADO callers and preserve any required regression fixtures |
| Assertions and content-free receipts | ZCC comparison/publication flows | Callers may treat them as authorization records | Extract or archive | Consumer inventory and replacement decision |
| Acknowledgements | Protected ZCC refresh/publication transitions | Unknown retained migration coordination | Archive candidate | No active transition runs and documented record-retention decision |
| Materializers and publishers | ZCC bootstrap/refresh artifact flows | They may own external artifact lifecycles | Keep until inventoried | Identify every writer/reader and migrate or retire each workflow |
| Old process-host operations | `infrawright-process`, schemas, validators, extensive tests | Public package entry may have unknown users | Extract retained consumers or archive | Versioned external-consumer inventory and deprecation plan |
| `dist/infrawright.mjs` | Legacy `infrawright-process` package binary and CI smokes | Unknown direct bundle consumers | Keep | Usage inventory plus approved compatibility retirement |
| `dist/infrawright-zcc-collector-child.mjs` | Legacy process parent and ZCC child tests | Parent/bundle users require the sibling | Keep | Retire or replace the parent/child protocol together |
| Draft PR #191 | Historical ZIA resource-specific migration path | Branch may be referenced during audit/recovery | Archive or close candidate | Independent review and explicit approval |
| Draft PR #192 | Historical ZIA plan workflow stacked on #191 | Same, plus stack relationship | Archive or close candidate | Independent review and explicit approval |
| Python operational implementations | Python tests, differential baselines, and possible unknown external callers | Highest unknown-caller risk | Archive/delete candidate after extraction | Live generic qualification, pipeline cutover, and consumer inventory |
| Python migration tests | Differential evidence for the port and frozen ZCC lanes | Little runtime risk; high regression value | Keep, then selectively archive | Final stack integration and an approved cleanup plan |

The detailed ZCC operation and schema contracts remain in
[Node Process API Migration](node-process-api.md). That document describes
frozen migration infrastructure, not the primary operational runtime.
