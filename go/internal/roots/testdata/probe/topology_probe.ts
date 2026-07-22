// topology_probe.ts is NOT part of the Node runtime or its build/test
// surface -- it is a throwaway differential-oracle probe, written for the
// Wave 4 go/internal/roots port's topology tests
// (go/internal/roots/roots_test.go) and committed here (alongside the
// fixtures it produced) purely for provenance: so a reviewer can see
// exactly which node-src exports were called, with what arguments, to
// produce testdata/loaded_topology.oracle.json and
// testdata/validate_tenant.oracle.json.
//
// It calls loadPackRoot (node-src/metadata/loader.ts) and
// loadedRootTopology/validateTenant (node-src/domain/roots.ts) --
// the same node-src functions go/internal/roots ports -- against this
// repo's own committed packs/, packs/full.packset.json, and demo/deployment.json,
// then prints one JSON document to stdout: the resulting RootTopology,
// its diagnostics, and the exact error text/code/category
// validateTenant raises for each of node-tests/roots.test.ts's invalid-
// tenant vectors.
//
// The current tree intentionally lacks the imported node-src files. To
// regenerate, first recover the immutable source tree and run there:
//
//   git worktree add --detach /tmp/infrawright-node-oracle node-oracle-v1-final
//   cd /tmp/infrawright-node-oracle
//   npm ci --ignore-scripts
//   npx esbuild go/internal/roots/testdata/probe/topology_probe.ts \
//     --bundle --platform=node --format=esm --target=node24 \
//     --outfile=/tmp/topology_probe.mjs
//   node /tmp/topology_probe.mjs > /tmp/topology_probe.out.json
//
// then split /tmp/topology_probe.out.json's top-level "topology"/
// "diagnostics" and "tenantErrors" fields into the two committed fixture
// files by hand and compare them with the committed authorities consumed by
// roots_test.go. This two-step process -- bundle, then run -- is the
// "npx esbuild with --bundle" probe the port's task brief calls for;
// esbuild itself never touches Go, it only resolves and bundles the
// TypeScript source (including its ".js"-suffixed relative imports,
// which esbuild's resolver maps back to the ".ts" files that actually
// exist on disk, the same NodeNext-style resolution node-src's own build
// already relies on -- see scripts/build-metadata-cli.mjs).
import { loadPackRoot } from "../../../../../node-src/metadata/loader.js";
import {
  loadedRootTopology,
  validateTenant,
} from "../../../../../node-src/domain/roots.js";
import { loadDeployment } from "../../../../../node-src/domain/deployment.js";
import { ProcessFailure } from "../../../../../node-src/domain/errors.js";

async function main(): Promise<void> {
  const packsRoot = "packs";
  const profilePath = "packs/full.packset.json";
  const catalogPath = "packs/full.packset.json";
  const deploymentPath = "demo/deployment.json";
  const tenant = "demo";

  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot, profilePath, catalogPath }),
    loadDeployment(deploymentPath),
  ]);

  const result = loadedRootTopology({
    root: packRoot,
    deployment: loadedDeployment,
    tenant,
    selectors: [],
  });

  const tenantErrors: Array<{
    tenant: string;
    message: string;
    code: string;
    category: string;
  }> = [];
  for (const invalidTenant of ["", ".", "..", "bad/tenant", "é"]) {
    try {
      validateTenant(invalidTenant);
      tenantErrors.push({
        tenant: invalidTenant,
        message: "(did not throw)",
        code: "",
        category: "",
      });
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        tenantErrors.push({
          tenant: invalidTenant,
          message: error.message,
          code: error.code,
          category: error.category,
        });
      } else {
        throw error;
      }
    }
  }

  process.stdout.write(
    JSON.stringify(
      {
        topology: result.topology,
        diagnostics: result.diagnostics,
        tenantErrors,
      },
      null,
      2,
    ),
  );
  process.stdout.write("\n");
}

await main();
