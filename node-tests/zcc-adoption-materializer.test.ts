import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { compareZccAdoptionArtifactDigests } from "../node-src/domain/zcc-adoption-artifact-parity.js";
import type { ZccAdoptionArtifactParity } from "../node-src/domain/zcc-adoption-artifact-parity.js";
import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
  type ZccAdoptionArtifactSet,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import { materializeReadyZccAdoptionArtifacts } from "../node-src/domain/zcc-adoption-materialization.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly outputRoot: string;
}

function fixture(): Fixture {
  const root = realpathSync(mkdtempSync(path.join(os.tmpdir(), "zcc-adoption-materializer-")));
  const workspace = path.join(root, "workspace");
  const outputRoot = path.join(workspace, "overlay");
  mkdirSync(outputRoot, { recursive: true });
  return { root, workspace, outputRoot };
}

function candidate(sourceSha = "a".repeat(64)): ZccAdoptionArtifactSet {
  return compileZccAdoptionArtifactSet({
    catalog: loadZccAdoptionCatalog(),
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    rawItems: [{
      active: true,
      conditionType: 1,
      id: "tn-1",
      networkName: "Raw &amp; Identity",
    }],
    observedStates: [{
      address: "zcc_trusted_network.iw_4265560b890c8eb2",
      import_id: "tn-1",
      key: "raw_amp_identity",
      provider_name: "registry.terraform.io/zscaler/zcc",
      resource_type: "zcc_trusted_network",
      sensitive_values: {},
      values: {
        active: true,
        condition_type: "1",
        id: "tn-1",
        name: "Provider & Identity",
      },
    }],
    source: {
      path: "pulls/demo/zcc_trusted_network.json",
      sha256: sourceSha,
      size_bytes: 41,
    },
    target: {
      tenant: "demo",
      resourceType: "zcc_trusted_network",
      rootLabel: "zcc_bundle",
      rootMembers: ["zcc_forwarding_profile", "zcc_trusted_network"],
      variableName: "zcc_trusted_network_items",
      configPath: "overlay/config/demo/zcc_trusted_network.auto.tfvars.json",
      importsPath: "overlay/imports/demo/zcc_trusted_network_imports.tf",
      lookupPath: "overlay/config/demo/zcc_trusted_network.lookup.json",
    },
  });
}

function parity(value: ZccAdoptionArtifactSet): ZccAdoptionArtifactParity {
  return compareZccAdoptionArtifactDigests({
    candidate: value,
    materialized: {
      tfvars: {
        sha256: value.artifacts.tfvars.sha256,
        size_bytes: value.artifacts.tfvars.size_bytes,
      },
      imports: {
        sha256: value.artifacts.imports.sha256,
        size_bytes: value.artifacts.imports.size_bytes,
      },
      lookup: value.artifacts.lookup === null
        ? null
        : {
            sha256: value.artifacts.lookup.sha256,
            size_bytes: value.artifacts.lookup.size_bytes,
          },
    },
  });
}

function paths(current: Fixture): Readonly<Record<"imports" | "lookup" | "tfvars", string>> {
  return {
    imports: path.join(current.outputRoot, "imports/demo/zcc_trusted_network_imports.tf"),
    lookup: path.join(current.outputRoot, "config/demo/zcc_trusted_network.lookup.json"),
    tfvars: path.join(current.outputRoot, "config/demo/zcc_trusted_network.auto.tfvars.json"),
  };
}

async function failure(operation: Promise<unknown>): Promise<ProcessFailure> {
  try {
    await operation;
    assert.fail("expected materialization failure");
  } catch (error: unknown) {
    assert.equal(error instanceof ProcessFailure, true);
    return error as ProcessFailure;
  }
}

function invoke(current: Fixture, options: {
  readonly candidate?: ZccAdoptionArtifactSet;
  readonly assertion?: ZccAdoptionArtifactParity;
  readonly recheckInputs?: () => Promise<void>;
  readonly hooks?: Parameters<typeof materializeReadyZccAdoptionArtifacts>[0]["hooks"];
  readonly outputRoot?: string;
} = {}) {
  const value = options.candidate ?? candidate();
  return materializeReadyZccAdoptionArtifacts({
    outputRoot: options.outputRoot ?? current.outputRoot,
    pathBase: current.workspace,
    candidate: value,
    assertion: options.assertion ?? parity(value),
    recheckInputs: options.recheckInputs ?? (async () => undefined),
    ...(options.hooks === undefined ? {} : { hooks: options.hooks }),
  });
}

test("adoption publication preserves imports, lookup, tfvars ordering and exact reuse", async () => {
  const current = fixture();
  const linked: string[] = [];
  try {
    const first = await invoke(current, {
      hooks: {
        afterLink: (name) => {
          linked.push(name);
        },
      },
    });
    assert.deepEqual(linked, ["imports", "lookup", "tfvars"]);
    assert.deepEqual(first.publication.created, ["imports", "lookup", "tfvars"]);
    assert.deepEqual(first.publication.reused, []);
    assert.equal(first.verification.status, "ready");
    assert.equal(JSON.stringify(first).includes("content"), false);

    const before = Object.fromEntries(Object.entries(paths(current)).map(([name, filePath]) => {
      return [name, readFileSync(filePath)];
    }));
    const retry = await invoke(current);
    assert.deepEqual(retry.publication.created, []);
    assert.deepEqual(retry.publication.reused, ["imports", "lookup", "tfvars"]);
    for (const [name, filePath] of Object.entries(paths(current))) {
      assert.deepEqual(readFileSync(filePath), before[name]);
    }
  } finally {
    rmSync(current.root, { recursive: true, force: true });
  }
});

test("post-link failure is retryable and advances only the exact prefix", async () => {
  const current = fixture();
  let links = 0;
  try {
    const error = await failure(invoke(current, {
      hooks: {
        afterLink: () => {
          links += 1;
          if (links === 1) {
            throw new Error("private hook detail");
          }
        },
      },
    }));
    assert.equal(error.code, "MATERIALIZATION_INDETERMINATE");
    assert.equal(error.category, "io");
    assert.equal(error.retryable, true);
    assert.equal(JSON.stringify(error).includes("private hook detail"), false);
    assert.equal(existsSync(paths(current).imports), true);
    assert.equal(existsSync(paths(current).lookup), false);
    assert.equal(existsSync(paths(current).tfvars), false);

    const retry = await invoke(current);
    assert.deepEqual(retry.publication.reused, ["imports"]);
    assert.deepEqual(retry.publication.created, ["lookup", "tfvars"]);
  } finally {
    rmSync(current.root, { recursive: true, force: true });
  }
});

test("input races before and after publication retain the established crash boundary", async () => {
  for (const phase of ["before", "after"] as const) {
    const current = fixture();
    let rechecks = 0;
    try {
      const error = await failure(invoke(current, {
        recheckInputs: async () => {
          rechecks += 1;
          if ((phase === "before" && rechecks === 1) || (phase === "after" && rechecks === 2)) {
            throw new ProcessFailure({
              code: "COMPILE_CONTROL_CHANGED",
              category: "io",
              message: "control input changed",
            });
          }
        },
      }));
      if (phase === "before") {
        assert.equal(error.code, "COMPILE_CONTROL_CHANGED");
        assert.equal(existsSync(paths(current).imports), false);
        assert.equal(existsSync(paths(current).lookup), false);
        assert.equal(existsSync(paths(current).tfvars), false);
      } else {
        assert.equal(error.code, "MATERIALIZATION_INDETERMINATE");
        assert.equal(error.retryable, true);
        assert.equal(existsSync(paths(current).imports), true);
        assert.equal(existsSync(paths(current).lookup), true);
        assert.equal(existsSync(paths(current).tfvars), true);
        const retry = await invoke(current);
        assert.deepEqual(retry.publication.reused, ["imports", "lookup", "tfvars"]);
      }
      const aliases = readdirSync(current.outputRoot, { recursive: true })
        .map(String)
        .filter((name) => name.includes(".infrawright-") && name.endsWith(".tmp"));
      assert.deepEqual(aliases, []);
    } finally {
      rmSync(current.root, { recursive: true, force: true });
    }
  }
});

test("a valid ready assertion for another fresh source fails before artifact directories", async () => {
  const current = fixture();
  try {
    const error = await failure(invoke(current, {
      assertion: parity(candidate("b".repeat(64))),
    }));
    assert.equal(error.code, "MATERIALIZATION_ASSERTION_MISMATCH");
    assert.equal(error.category, "domain");
    assert.deepEqual(readdirSync(current.outputRoot), []);
  } finally {
    rmSync(current.root, { recursive: true, force: true });
  }
});

test("candidate and assertion are inert snapshots across retained-object mutation", async () => {
  const current = fixture();
  const retainedCandidate = structuredClone(candidate());
  const retainedAssertion = structuredClone(parity(retainedCandidate));
  try {
    const result = await invoke(current, {
      candidate: retainedCandidate,
      assertion: retainedAssertion,
      hooks: {
        afterPreflight: () => {
          (retainedCandidate as any).tenant = "mutated";
          (retainedCandidate.artifacts.imports as any).content = "foreign bytes";
          (retainedAssertion as any).tenant = "mutated";
        },
      },
    });
    assert.equal(result.tenant, "demo");
    assert.equal(readFileSync(paths(current).imports, "utf8").includes("foreign bytes"), false);
  } finally {
    rmSync(current.root, { recursive: true, force: true });
  }
});

test("cyclic, proxy, and accessor candidate/assertion graphs fail without hooks or value leaks", async () => {
  const secret = "private-materialization-graph-secret";
  for (const side of ["candidate", "assertion"] as const) {
    for (const shape of ["cycle", "proxy", "accessor"] as const) {
      const current = fixture();
      let trapCalls = 0;
      let rechecks = 0;
      const cleanCandidate = structuredClone(candidate());
      const cleanAssertion = structuredClone(parity(cleanCandidate));
      let hostile: unknown;
      if (shape === "cycle") {
        hostile = side === "candidate" ? cleanCandidate : cleanAssertion;
        (hostile as Record<string, unknown>).cycle = hostile;
      } else if (shape === "proxy") {
        const target = side === "candidate" ? cleanCandidate : cleanAssertion;
        hostile = new Proxy(target, {
          ownKeys: () => {
            trapCalls += 1;
            throw new Error(secret);
          },
        });
      } else {
        hostile = side === "candidate" ? cleanCandidate : cleanAssertion;
        Object.defineProperty(hostile, "tenant", {
          configurable: true,
          enumerable: true,
          get: () => {
            trapCalls += 1;
            throw new Error(secret);
          },
        });
      }
      try {
        const error = await failure(invoke(current, {
          candidate: (side === "candidate" ? hostile : cleanCandidate) as ZccAdoptionArtifactSet,
          assertion: (side === "assertion" ? hostile : cleanAssertion) as ZccAdoptionArtifactParity,
          recheckInputs: async () => {
            rechecks += 1;
          },
        }));
        assert.equal(error.code, "INVALID_MATERIALIZATION_INPUT", `${side}:${shape}`);
        assert.equal(trapCalls, 0, `${side}:${shape}`);
        assert.equal(rechecks, 0, `${side}:${shape}`);
        assert.equal(JSON.stringify(error).includes(secret), false, `${side}:${shape}`);
        assert.deepEqual(readdirSync(current.outputRoot), [], `${side}:${shape}`);
      } finally {
        rmSync(current.root, { recursive: true, force: true });
      }
    }
  }
});

test("outer candidate and assertion accessors are rejected without invocation", async () => {
  for (const key of ["candidate", "assertion"] as const) {
    const current = fixture();
    const value = candidate();
    let getterCalls = 0;
    const options: Record<string, unknown> = {
      outputRoot: current.outputRoot,
      pathBase: current.workspace,
      candidate: value,
      assertion: parity(value),
      recheckInputs: async () => undefined,
    };
    Object.defineProperty(options, key, {
      configurable: true,
      enumerable: true,
      get: () => {
        getterCalls += 1;
        throw new Error("private outer getter detail");
      },
    });
    try {
      const error = await failure(materializeReadyZccAdoptionArtifacts(
        options as unknown as Parameters<typeof materializeReadyZccAdoptionArtifacts>[0],
      ));
      assert.equal(error.code, "INVALID_MATERIALIZATION_INPUT");
      assert.equal(getterCalls, 0);
      assert.equal(JSON.stringify(error).includes("private outer getter detail"), false);
      assert.deepEqual(readdirSync(current.outputRoot), []);
    } finally {
      rmSync(current.root, { recursive: true, force: true });
    }
  }
});

test("wrong authority, foreign bytes, and reserved residue never replace targets", async () => {
  const cases = ["authority", "foreign", "pending"] as const;
  for (const kind of cases) {
    const current = fixture();
    try {
      if (kind === "foreign") {
        mkdirSync(path.dirname(paths(current).imports), { recursive: true });
        writeFileSync(paths(current).imports, "foreign artifact bytes\n");
      }
      if (kind === "pending") {
        const pending = paths(current).imports.replace("_imports.tf", "_moves.pending.json");
        mkdirSync(path.dirname(pending), { recursive: true });
        writeFileSync(pending, "{}\n");
      }
      const error = await failure(invoke(current, {
        ...(kind === "authority" ? { outputRoot: current.workspace } : {}),
      }));
      assert.equal(
        error.code,
        kind === "authority"
          ? "OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY"
          : kind === "foreign"
            ? "MATERIALIZATION_TARGET_MISMATCH"
            : "UNSUPPORTED_MATERIALIZATION_RESIDUE",
      );
      if (kind === "foreign") {
        assert.equal(readFileSync(paths(current).imports, "utf8"), "foreign artifact bytes\n");
      }
      assert.equal(existsSync(paths(current).tfvars), false);
    } finally {
      rmSync(current.root, { recursive: true, force: true });
    }
  }
});
