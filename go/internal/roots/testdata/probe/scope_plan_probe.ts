// scope_plan_probe.ts is NOT part of the Node runtime or its build/test
// surface -- it is a throwaway differential-oracle probe, written for the
// Wave 5 go/internal/roots/{scopepaths.go,planroots.go} port (there is no
// node-tests/scope-paths.test.ts or node-tests/plan-roots.test.ts: the
// only committed vectors that exercise changedPathScope/planRoots live in
// node-tests/differential.test.ts, gated on a retired Python oracle, and
// node-tests/plan-cli.test.ts, a CLI-subprocess test). It calls
// changedPathScope/changedPathScopeLoaded (node-src/domain/scope-paths.ts)
// and planRoots/loadedPlanRoots (node-src/domain/plan-roots.ts) directly
// against synthetic RootCatalog/Deployment fixtures and a throwaway
// on-disk envs/ tree, then prints one JSON document to stdout: this is the
// source of go/internal/roots/testdata/scope_plan_probe.oracle.json, which
// scopepaths_test.go and planroots_test.go's provenance comments cite by
// scenario name.
//
// Regenerate with (run from the repo root):
//
//   npx esbuild go/internal/roots/testdata/probe/scope_plan_probe.ts \
//     --bundle --platform=node --format=esm --target=node24 \
//     --external:lossless-json \
//     --outfile=/tmp/scope_plan_probe.mjs
//   node /tmp/scope_plan_probe.mjs > \
//     go/internal/roots/testdata/scope_plan_probe.oracle.json
//
// --external:lossless-json is required here (unlike topology_probe.ts,
// which never pulls in metadata/loader.ts's dependency chain): scope-paths
// and plan-roots's own imports are lossless-json-free, but this probe also
// exercises changedPathScopeLoaded/loadedPlanRoots, whose LoadedPackRoot
// parameter type is imported from metadata/loader.ts, which pulls in
// lossless-json transitively; esbuild's bundler chokes on that package's
// dual ESM/CJS export map unless it is left external (Node's own resolver
// handles it fine at run time, since node_modules/lossless-json is
// present).
import { mkdtemp, mkdir, writeFile, symlink } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

import { changedPathScope } from "../../../../../node-src/domain/scope-paths.js";
import { planRoots } from "../../../../../node-src/domain/plan-roots.js";
import { ProcessFailure } from "../../../../../node-src/domain/errors.js";
import type {
  Deployment,
  RootCatalog,
} from "../../../../../node-src/domain/types.js";

// Mirrors go/internal/roots/roots_test.go's fixtureCatalog exactly (same
// resource types, slug labels, generated/derived/slug_group flags), so
// this probe's topology is the same one exercised by the existing Go
// RootTopologyFromCatalog tests -- scopepaths_test.go and
// planroots_test.go build the identical metadata.RootCatalog literal in
// Go and can therefore compare directly against this oracle.
const CATALOG: RootCatalog = {
  kind: "infrawright.root_catalog",
  schema_version: 1,
  declared_providers: ["zpa"],
  resources: [
    {
      type: "zpa_alpha_one", product: "zpa", provider: "zpa",
      bare_name: "alpha_one", slug_label: "zpa_alpha",
      generated: true, derived: false,
    },
    {
      type: "zpa_alpha_two", product: "zpa", provider: "zpa",
      bare_name: "alpha_two", slug_label: "zpa_alpha",
      generated: true, derived: false,
    },
    {
      type: "zpa_derived_reorder", product: "zpa", provider: "zpa",
      bare_name: "derived_reorder", slug_label: "zpa_derived",
      generated: true, derived: true,
    },
    {
      type: "zpa_known_only", product: "zpa", provider: "zpa",
      bare_name: "known_only", slug_label: "zpa_known",
      generated: false, derived: false,
    },
  ],
  source_files: ["zpa/pack.json", "zpa/registry.json"],
  sources_sha256: "0".repeat(64),
};

function errorShape(error: unknown): { code: string; category: string; message: string } | { message: string } {
  if (error instanceof ProcessFailure) {
    return { code: error.code, category: error.category, message: error.message };
  }
  return { message: error instanceof Error ? error.message : String(error) };
}

async function scopeScenario(
  name: string,
  options: { deployment: Deployment; deploymentPath: string; workspace: string; paths: readonly string[] },
): Promise<{ name: string; result?: unknown; error?: unknown }> {
  try {
    const result = changedPathScope({ ...options, catalog: CATALOG });
    return { name, result };
  } catch (error) {
    return { name, error: errorShape(error) };
  }
}

async function planScenario(
  name: string,
  options: { workspace: string; deployment: Deployment; tenant: string | null; selectors: readonly string[] },
): Promise<{ name: string; result?: unknown; error?: unknown }> {
  try {
    const result = await planRoots({ ...options, catalog: CATALOG });
    return { name, result };
  } catch (error) {
    return { name, error: errorShape(error) };
  }
}

async function main(): Promise<void> {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "scope-plan-probe-"));
  const deploymentPath = path.join(workspace, "deployment.json");

  // roots.zpa.strategy = "slug" merges zpa_alpha_one/zpa_alpha_two under
  // the shared "zpa_alpha" root label (their common slug_label); the
  // derived zpa_derived_reorder stays a single-member root regardless
  // (derived types always keep per-resource roots -- see
  // go/internal/roots/roots.go's validateMember). Every scenario below
  // uses this same slug-grouped topology so the "envs" root-label match
  // in scope-paths and the materialized-root discovery in plan-roots line
  // up with a genuine multi-member root, not an incidental single-member
  // one.
  const dotDeployment: Deployment = { overlay: ".", roots: { zpa: { strategy: "slug" } } };
  const overlayDeployment: Deployment = {
    overlay: "artifacts//staging/../current",
    roots: { zpa: { strategy: "slug" } },
  };
  const moduleDirDeployment: Deployment = {
    overlay: ".",
    module_dir: "custom/modules",
    roots: { zpa: { strategy: "slug" } },
  };

  const scopeScenarios: Array<Promise<{ name: string; result?: unknown; error?: unknown }>> = [
    // Basic per-kind matches under overlay ".": deployment path itself,
    // one config artifact (longest CONFIG_SUFFIXES match), one imports
    // artifact, one env_root directory, and one module path -- pins the
    // bare (no-leading-"./") root path shape overlay "." produces.
    scopeScenario("dot-overlay-all-kinds", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: [
        deploymentPath,
        "config/acme/zpa_alpha_one.generated.expressions.json",
        "config/acme/zpa_alpha_one.expressions.json",
        "imports/acme/zpa_alpha_two_imports.tf",
        "envs/acme/zpa_alpha",
        "modules/zpa_alpha_two/main.tf",
      ],
    }),
    // A path matching BOTH the deployment path (via sameContractPath) and
    // an env_root directory simultaneously would require deploymentPath
    // to literally sit inside envs/; instead this pins the more common
    // multi-kind case: a config path and an imports path that resolve to
    // the SAME resource, producing one path_match with two kinds is not
    // possible per-path (each path is matched independently), so this
    // scenario instead pins two DIFFERENT resources sharing one
    // affected_root via the zpa_alpha slug group.
    scopeScenario("shared-root-two-resources", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: [
        "config/acme/zpa_alpha_one.auto.tfvars.json",
        "config/acme/zpa_alpha_two.auto.tfvars",
      ],
    }),
    // Suffix precedence: CONFIG_SUFFIXES tries longest-first, so a name
    // ending in both ".auto.tfvars.json" and ".auto.tfvars" must resolve
    // via the LONGER suffix's resource-name stripping.
    scopeScenario("config-suffix-longest-match", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: ["config/acme/zpa_alpha_one.auto.tfvars.json"],
    }),
    // A non-"." overlay with an unnormalized "//..".  path segment: pins
    // that artifactRoot/moduleRoot join the RAW overlay string (via
    // pythonPosixJoin, never normalized) ahead of the changed-path
    // normalization that already ran on the input paths themselves.
    scopeScenario("unnormalized-overlay", {
      deployment: overlayDeployment,
      deploymentPath,
      workspace,
      paths: ["artifacts//staging/../current/config/acme/zpa_alpha_one.lookup.json"],
    }),
    // module_dir present on the deployment is used verbatim (no overlay
    // join at all), even though overlay is also present and non-".".
    scopeScenario("explicit-module-dir", {
      deployment: moduleDirDeployment,
      deploymentPath,
      workspace,
      paths: ["custom/modules/zpa_alpha_one/main.tf"],
    }),
    // No tenant validation happens in scope-paths at all: a "tenant"
    // path segment containing characters validateTenant would reject
    // (e.g. a slash-free but otherwise-invalid string like a bare space)
    // still produces a match with that raw segment recorded verbatim.
    scopeScenario("no-tenant-validation", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: ["config/bad tenant!/zpa_alpha_one.auto.tfvars.json"],
    }),
    // Error vectors: non-array paths input, an empty-string path, an
    // embedded NUL byte, and a deployment whose overlay is not a string.
    scopeScenario("non-array-paths", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: "not-an-array" as unknown as readonly string[],
    }),
    scopeScenario("empty-string-path", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: [""],
    }),
    scopeScenario("embedded-nul-path", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: ["config/acme/zpa_alpha_one\0.auto.tfvars.json"],
    }),
    scopeScenario("non-string-overlay", {
      deployment: { overlay: 7 as unknown as string, roots: {} },
      deploymentPath,
      workspace,
      paths: ["config/acme/zpa_alpha_one.auto.tfvars.json"],
    }),
    // Unmatched path: does not error, just lands in unmatched_paths.
    scopeScenario("unmatched-path", {
      deployment: dotDeployment,
      deploymentPath,
      workspace,
      paths: ["completely/unrelated/path.txt"],
    }),
  ];

  // --- plan-roots scenarios: real on-disk envs/ tree under workspace ---
  // envs/acme/zpa_alpha is the (only) two-member root under slug
  // grouping: "complete" (both artifacts present).
  await mkdir(path.join(workspace, "envs/acme/zpa_alpha"), { recursive: true });
  await writeFile(path.join(workspace, "envs/acme/zpa_alpha/tfplan"), "plan");
  await writeFile(path.join(workspace, "envs/acme/zpa_alpha/tfplan.sources"), "sources");

  // envs/acme/zpa_derived_reorder: derived types always keep their own
  // root regardless of the provider's grouping strategy -- "incomplete"
  // (tfplan present, tfplan.sources absent).
  await mkdir(path.join(workspace, "envs/acme/zpa_derived_reorder"), { recursive: true });
  await writeFile(path.join(workspace, "envs/acme/zpa_derived_reorder/tfplan"), "plan-only");

  // envs/other/zpa_alpha: directory exists but neither artifact does --
  // "absent".
  await mkdir(path.join(workspace, "envs/other/zpa_alpha"), { recursive: true });

  // envs/acme/unknown_root: a directory whose name is not any topology
  // root label at all -- discover() must silently skip it (it is neither
  // a validation error nor a WholeRootDiagnostic candidate).
  await mkdir(path.join(workspace, "envs/acme/unknown_root"), { recursive: true });
  await writeFile(path.join(workspace, "envs/acme/unknown_root/tfplan"), "plan");

  const planScenarios: Array<Promise<{ name: string; result?: unknown; error?: unknown }>> = [
    planScenario("complete-incomplete-absent-and-unknown-root-across-tenants", {
      workspace,
      deployment: dotDeployment,
      tenant: null,
      selectors: [],
    }),
    planScenario("tenant-scoped-excludes-other-tenants", {
      workspace,
      deployment: dotDeployment,
      tenant: "acme",
      selectors: [],
    }),
    // Selecting one member of a two-member slug group must still
    // materialize the WHOLE two-member root (env_dir/artifacts describe
    // the root, not the individual selected member) plus a
    // WHOLE_ROOT_SELECTION diagnostic on stderr.
    planScenario("partial-selector-materializes-whole-root-with-diagnostic", {
      workspace,
      deployment: dotDeployment,
      tenant: null,
      selectors: ["zpa_alpha_one"],
    }),
    planScenario("unknown-selector-fails-closed", {
      workspace,
      deployment: dotDeployment,
      tenant: null,
      selectors: ["not_a_real_resource"],
    }),
    planScenario("invalid-tenant-fails-closed", {
      workspace,
      deployment: dotDeployment,
      tenant: "../escape",
      selectors: [],
    }),
    planScenario("nonexistent-envs-directory", {
      workspace: await mkdtemp(path.join(os.tmpdir(), "scope-plan-probe-empty-")),
      deployment: dotDeployment,
      tenant: null,
      selectors: [],
    }),
  ];

  const [scopeResults, planResultsRaw] = await Promise.all([
    Promise.all(scopeScenarios),
    Promise.all(planScenarios),
  ]);

  process.stdout.write(JSON.stringify({ scope: scopeResults, plan: planResultsRaw }, null, 2));
  process.stdout.write("\n");
}

await main();
