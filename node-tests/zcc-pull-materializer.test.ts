import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  copyFileSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  compileZccPullArtifactsOperation,
  materializeZccPullArtifactsOperation,
  type ZccPullMaterializationOperationHooks,
} from "../node-src/domain/zcc-pull-operation.js";
import type {
  ZccPullArtifactSet,
  ZccPullResourceType,
  ZccTextArtifact,
} from "../node-src/domain/zcc-pull-artifacts.js";
import {
  compareZccPullArtifactDigests,
  type ZccPullArtifactParity,
} from "../node-src/domain/zcc-pull-parity.js";
import { materializeReadyZccPullArtifacts } from "../node-src/domain/zcc-pull-materialization.js";

const REPOSITORY = process.cwd();
const ROOT_CATALOG = path.join(
  REPOSITORY,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "materialize_test";

interface Fixture {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly pullPath: string;
  readonly resourceType: ZccPullResourceType;
  readonly outputRoot: string;
}

function rawPull(resourceType: ZccPullResourceType): string {
  if (resourceType === "zcc_device_cleanup") {
    return '[{"id":"device-1","active":"1"}]\n';
  }
  return readFileSync(
    path.join(REPOSITORY, `tests/fixtures/demo/${resourceType}.json`),
    "utf8",
  );
}

async function withFixture(
  resourceType: ZccPullResourceType,
  callback: (fixture: Fixture) => void | Promise<void>,
  options: {
    readonly overlay?: string;
    readonly outputRoot?: (workspace: string) => string;
  } = {},
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "zcc-materializer-"));
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  const overlay = options.overlay ?? ".";
  const requestedOutputRoot = options.outputRoot?.(workspace) ?? workspace;
  try {
    mkdirSync(pullDirectory, { recursive: true });
    mkdirSync(requestedOutputRoot, { recursive: true });
    const outputRoot = realpathSync(requestedOutputRoot);
    writeFileSync(
      deploymentPath,
      `${JSON.stringify({ overlay, roots: {} })}\n`,
    );
    copyFileSync(ROOT_CATALOG, catalogPath);
    const pullPath = path.join(pullDirectory, `${resourceType}.json`);
    writeFileSync(pullPath, rawPull(resourceType));
    await callback({
      workspace,
      deploymentPath,
      catalogPath,
      pullPath,
      resourceType,
      outputRoot,
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
    if (
      !requestedOutputRoot.startsWith(`${workspace}${path.sep}`)
      && requestedOutputRoot !== workspace
    ) {
      rmSync(requestedOutputRoot, { recursive: true, force: true });
    }
  }
}

function operationOptions(
  fixture: Fixture,
  assertion: ZccPullArtifactParity,
  hooks?: ZccPullMaterializationOperationHooks,
) {
  return {
    workspace: fixture.workspace,
    deploymentPath: fixture.deploymentPath,
    catalogPath: fixture.catalogPath,
    tenant: TENANT,
    resourceType: fixture.resourceType,
    assertion,
    outputRoot: fixture.outputRoot,
    ...(hooks === undefined ? {} : { hooks }),
  };
}

async function compile(fixture: Fixture): Promise<ZccPullArtifactSet> {
  return compileZccPullArtifactsOperation({
    workspace: fixture.workspace,
    deploymentPath: fixture.deploymentPath,
    catalogPath: fixture.catalogPath,
    tenant: TENANT,
    resourceType: fixture.resourceType,
  });
}

function cleanAssertion(candidate: ZccPullArtifactSet): ZccPullArtifactParity {
  return compareZccPullArtifactDigests({
    candidate,
    materialized: {
      imports: {
        sha256: candidate.artifacts.imports.sha256,
        size_bytes: candidate.artifacts.imports.size_bytes,
      },
      tfvars: {
        sha256: candidate.artifacts.tfvars.sha256,
        size_bytes: candidate.artifacts.tfvars.size_bytes,
      },
      lookup: candidate.artifacts.lookup === null
        ? null
        : {
            sha256: candidate.artifacts.lookup.sha256,
            size_bytes: candidate.artifacts.lookup.size_bytes,
          },
    },
  });
}

function targetPath(fixture: Fixture, artifact: ZccTextArtifact): string {
  return path.isAbsolute(artifact.path)
    ? artifact.path
    : path.resolve(fixture.workspace, artifact.path);
}

function writeArtifact(fixture: Fixture, artifact: ZccTextArtifact): void {
  const destination = targetPath(fixture, artifact);
  mkdirSync(path.dirname(destination), { recursive: true });
  writeFileSync(destination, artifact.content);
}

function finalPaths(
  fixture: Fixture,
  candidate: ZccPullArtifactSet,
): Record<"imports" | "tfvars", string> & { readonly lookup: string | null } {
  return {
    imports: targetPath(fixture, candidate.artifacts.imports),
    tfvars: targetPath(fixture, candidate.artifacts.tfvars),
    lookup: candidate.artifacts.lookup === null
      ? null
      : targetPath(fixture, candidate.artifacts.lookup),
  };
}

function tempFiles(root: string): string[] {
  const found: string[] = [];
  function visit(directory: string): void {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const target = path.join(directory, entry.name);
      if (entry.isDirectory() && !entry.isSymbolicLink()) {
        visit(target);
      } else if (entry.name.startsWith(".infrawright-") && entry.name.endsWith(".tmp")) {
        found.push(target);
      }
    }
  }
  visit(root);
  return found;
}

async function expectFailure(
  promise: Promise<unknown>,
  codes: string | readonly string[],
  forbidden: readonly string[] = [],
): Promise<ProcessFailure> {
  const accepted = new Set(Array.isArray(codes) ? codes : [codes]);
  try {
    await promise;
  } catch (error: unknown) {
    assert.ok(error instanceof ProcessFailure);
    assert.ok(accepted.has(error.code), `${error.code}: ${error.message}`);
    for (const value of forbidden) {
      assert.equal(error.message.includes(value), false);
      assert.equal(JSON.stringify(error.details).includes(value), false);
    }
    return error;
  }
  return assert.fail("operation unexpectedly succeeded");
}

test("fresh publication writes exact bytes in imports, lookup, tfvars order", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const candidate = await compile(fixture);
    const assertion = cleanAssertion(candidate);
    const linked: string[] = [];
    const result = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, assertion, {
        afterLink: (name) => {
          linked.push(name);
        },
      }),
    );

    assert.deepEqual(linked, ["imports", "lookup", "tfvars"]);
    assert.deepEqual(result.publication.created, ["imports", "lookup", "tfvars"]);
    assert.deepEqual(result.publication.reused, []);
    assert.equal(result.status, "complete");
    assert.deepEqual(result.verification, assertion);
    for (const artifact of [
      candidate.artifacts.imports,
      candidate.artifacts.lookup,
      candidate.artifacts.tfvars,
    ]) {
      assert.notEqual(artifact, null);
      if (artifact !== null) {
        const destination = targetPath(fixture, artifact);
        assert.equal(readFileSync(destination, "utf8"), artifact.content);
        assert.equal(statSync(destination).mode & 0o777, 0o666 & ~process.umask());
      }
    }
    const createdParents = new Set([
      path.dirname(targetPath(fixture, candidate.artifacts.imports)),
      path.dirname(targetPath(fixture, candidate.artifacts.tfvars)),
    ]);
    for (const directory of createdParents) {
      assert.equal(statSync(directory).mode & 0o777, 0o777 & ~process.umask());
    }
    assert.deepEqual(tempFiles(fixture.outputRoot), []);
    assert.equal(JSON.stringify(result).includes("content"), false);
    assert.equal(JSON.stringify(result).includes(fixture.outputRoot), false);
  });
});

test("all-existing exact artifacts are reused without rewriting identities", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const candidate = await compile(fixture);
    for (const artifact of [
      candidate.artifacts.imports,
      candidate.artifacts.lookup,
      candidate.artifacts.tfvars,
    ]) {
      if (artifact !== null) {
        writeArtifact(fixture, artifact);
      }
    }
    const paths = finalPaths(fixture, candidate);
    const identities = [paths.imports, paths.lookup, paths.tfvars].map((target) => {
      assert.notEqual(target, null);
      const metadata = lstatSync(target as string, { bigint: true });
      return { dev: metadata.dev, ino: metadata.ino };
    });
    const result = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, cleanAssertion(candidate)),
    );
    assert.deepEqual(result.publication.created, []);
    assert.deepEqual(result.publication.reused, ["imports", "lookup", "tfvars"]);
    assert.deepEqual(
      [paths.imports, paths.lookup, paths.tfvars].map((target) => {
        const metadata = lstatSync(target as string, { bigint: true });
        return { dev: metadata.dev, ino: metadata.ino };
      }),
      identities,
    );
  });
});

test("an exact prefix is completed forward and a non-prefix partial is rejected", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const candidate = await compile(fixture);
    writeArtifact(fixture, candidate.artifacts.imports);
    const result = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, cleanAssertion(candidate)),
    );
    assert.deepEqual(result.publication.reused, ["imports"]);
    assert.deepEqual(result.publication.created, ["lookup", "tfvars"]);
  });

  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    writeArtifact(fixture, candidate.artifacts.tfvars);
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate)),
      ),
      "INVALID_MATERIALIZATION_PREFIX",
    );
    assert.equal(lstatSync(targetPath(fixture, candidate.artifacts.tfvars)).isFile(), true);
    assert.equal(
      (() => {
        try {
          lstatSync(targetPath(fixture, candidate.artifacts.imports));
          return false;
        } catch {
          return true;
        }
      })(),
      true,
    );
  });
});

test("mismatches, special targets, and every unsupported residue fail before staging", async (t) => {
  for (const kind of [
    "mismatch",
    "symlink",
    "directory",
    "moves",
    "pending-moves",
    "hcl",
    "generated",
    "stale-lookup",
  ] as const) {
    await t.test(kind, async () => {
      await withFixture("zcc_device_cleanup", async (fixture) => {
        const candidate = await compile(fixture);
        const imports = targetPath(fixture, candidate.artifacts.imports);
        mkdirSync(path.dirname(imports), { recursive: true });
        if (kind === "mismatch") {
          writeFileSync(imports, "materializer-secret-value\n");
        } else if (kind === "symlink") {
          symlinkSync(fixture.pullPath, imports);
        } else if (kind === "directory") {
          mkdirSync(imports);
        } else if (kind === "moves") {
          writeFileSync(imports.replace(/_imports\.tf$/, "_moves.tf"), "moved {}\n");
        } else if (kind === "pending-moves") {
          writeFileSync(
            imports.replace(/_imports\.tf$/, "_moves.pending.json"),
            '{"state":"pending"}\n',
          );
        } else {
          const tfvars = targetPath(fixture, candidate.artifacts.tfvars);
          const residue = kind === "hcl"
            ? tfvars.replace(/\.json$/, "")
            : path.join(
                path.dirname(tfvars),
                kind === "generated"
                  ? `${candidate.resource_type}.generated.expressions.json`
                  : `${candidate.resource_type}.lookup.json`,
              );
          mkdirSync(path.dirname(residue), { recursive: true });
          writeFileSync(residue, "{}\n");
        }
        await expectFailure(
          materializeZccPullArtifactsOperation(
            operationOptions(fixture, cleanAssertion(candidate)),
          ),
          [
            "moves",
            "pending-moves",
            "hcl",
            "generated",
            "stale-lookup",
          ].includes(kind)
            ? "UNSUPPORTED_MATERIALIZATION_RESIDUE"
            : kind === "symlink"
              ? "MATERIALIZATION_TARGET_UNSAFE"
              : "MATERIALIZATION_TARGET_MISMATCH",
          ["materializer-secret-value", fixture.outputRoot],
        );
        assert.deepEqual(tempFiles(fixture.outputRoot), []);
      });
    });
  }
});

test("a relative overlay resolves against workspace exactly once", async () => {
  await withFixture(
    "zcc_device_cleanup",
    async (fixture) => {
      const candidate = await compile(fixture);
      const result = await materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate)),
      );
      assert.equal(result.status, "complete");
      const expected = path.join(
        fixture.workspace,
        "artifacts",
        "config",
        TENANT,
        "zcc_device_cleanup.auto.tfvars.json",
      );
      assert.equal(readFileSync(expected, "utf8"), candidate.artifacts.tfvars.content);
      assert.equal(
        (() => {
          try {
            lstatSync(path.join(fixture.outputRoot, "artifacts"));
            return true;
          } catch {
            return false;
          }
        })(),
        false,
      );
    },
    {
      overlay: "artifacts",
      outputRoot: (workspace) => path.join(workspace, "artifacts"),
    },
  );
});

test("an absolute external overlay is authorized directly, never workspace-joined", async () => {
  const externalLexical = mkdtempSync(
    path.join(os.tmpdir(), "zcc-materializer-overlay-"),
  );
  const external = realpathSync(externalLexical);
  try {
    await withFixture(
      "zcc_device_cleanup",
      async (fixture) => {
        const candidate = await compile(fixture);
        const result = await materializeZccPullArtifactsOperation(
          operationOptions(fixture, cleanAssertion(candidate)),
        );
        assert.equal(result.status, "complete");
        assert.equal(
          readFileSync(targetPath(fixture, candidate.artifacts.tfvars), "utf8"),
          candidate.artifacts.tfvars.content,
        );
        assert.equal(
          targetPath(fixture, candidate.artifacts.tfvars).startsWith(`${external}${path.sep}`),
          true,
        );
      },
      {
        overlay: external,
        outputRoot: () => external,
      },
    );
  } finally {
    rmSync(externalLexical, { recursive: true, force: true });
  }
});

test("an absolute external overlay outside the approved root is rejected", async () => {
  const overlayLexical = mkdtempSync(
    path.join(os.tmpdir(), "zcc-materializer-overlay-outside-"),
  );
  const authorityLexical = mkdtempSync(
    path.join(os.tmpdir(), "zcc-materializer-authority-"),
  );
  const overlay = realpathSync(overlayLexical);
  const authority = realpathSync(authorityLexical);
  try {
    await withFixture(
      "zcc_device_cleanup",
      async (fixture) => {
        const candidate = await compile(fixture);
        await expectFailure(
          materializeZccPullArtifactsOperation(
            operationOptions(fixture, cleanAssertion(candidate)),
          ),
          "MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
          [overlay, authority],
        );
        assert.deepEqual(readdirSync(authority), []);
      },
      {
        overlay,
        outputRoot: () => authority,
      },
    );
  } finally {
    rmSync(overlayLexical, { recursive: true, force: true });
    rmSync(authorityLexical, { recursive: true, force: true });
  }
});

test("invalid authority and paths outside authority never write", async (t) => {
  for (const kind of ["relative", "missing", "symlink", "root", "outside"] as const) {
    await t.test(kind, async () => {
      const externalLexical = mkdtempSync(
        path.join(os.tmpdir(), "zcc-materializer-external-"),
      );
      const external = realpathSync(externalLexical);
      try {
        await withFixture("zcc_device_cleanup", async (fixture) => {
          const candidate = await compile(fixture);
          let outputRoot = fixture.outputRoot;
          if (kind === "relative") {
            outputRoot = "relative-output";
          } else if (kind === "missing") {
            outputRoot = path.join(fixture.workspace, "missing-output");
          } else if (kind === "symlink") {
            outputRoot = path.join(fixture.workspace, "output-link");
            symlinkSync(fixture.workspace, outputRoot);
          } else if (kind === "root") {
            outputRoot = path.parse(fixture.workspace).root;
          } else {
            outputRoot = external;
          }
          const error = await expectFailure(
            materializeZccPullArtifactsOperation({
              ...operationOptions(fixture, cleanAssertion(candidate)),
              outputRoot,
            }),
            kind === "outside"
              ? "MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY"
              : "INVALID_MATERIALIZATION_AUTHORITY",
            [external, fixture.workspace],
          );
          if (kind !== "outside") {
            assert.equal(error.category, "io");
          }
          assert.deepEqual(tempFiles(fixture.workspace), []);
        });
      } finally {
        rmSync(externalLexical, { recursive: true, force: true });
      }
    });
  }
});

test("a pre-link failure removes every stage and publishes no final", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const candidate = await compile(fixture);
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate), {
          beforePublish: () => {
            throw new Error("materializer-secret-value");
          },
        }),
      ),
      "MATERIALIZATION_HOOK_FAILED",
      ["materializer-secret-value", fixture.outputRoot],
    );
    const paths = finalPaths(fixture, candidate);
    for (const target of [paths.imports, paths.lookup, paths.tfvars]) {
      assert.notEqual(target, null);
      assert.throws(() => lstatSync(target as string));
    }
    assert.deepEqual(tempFiles(fixture.outputRoot), []);
  });
});

test("a failure before temp identity binding removes the exclusive alias", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate), {
          afterTempOpen: () => {
            throw new Error("materializer-secret-value");
          },
        }),
      ),
      "MATERIALIZATION_HOOK_FAILED",
      ["materializer-secret-value", fixture.outputRoot],
    );
    const paths = finalPaths(fixture, candidate);
    assert.throws(() => lstatSync(paths.imports));
    assert.throws(() => lstatSync(paths.tfvars));
    assert.deepEqual(tempFiles(fixture.outputRoot), []);
  });
});

test("renamed staging aliases fail cleanup closed instead of disappearing", async (t) => {
  for (const phase of ["unbound", "bound"] as const) {
    await t.test(phase, async () => {
      await withFixture("zcc_device_cleanup", async (fixture) => {
        const candidate = await compile(fixture);
        let escaped = "";
        const renameStage = (): void => {
          const aliases = tempFiles(fixture.outputRoot);
          assert.ok(aliases.length > 0);
          const alias = aliases[0];
          assert.notEqual(alias, undefined);
          escaped = `${alias as string}.renamed`;
          renameSync(alias as string, escaped);
          throw new Error("materializer-secret-value");
        };
        const hooks = phase === "unbound"
          ? { afterTempOpen: renameStage }
          : { afterStaged: renameStage };
        await expectFailure(
          materializeZccPullArtifactsOperation(
            operationOptions(fixture, cleanAssertion(candidate), hooks),
          ),
          "MATERIALIZATION_CLEANUP_FAILED",
          ["materializer-secret-value", fixture.outputRoot],
        );
        assert.notEqual(escaped, "");
        assert.equal(lstatSync(escaped).isFile(), true);
        const paths = finalPaths(fixture, candidate);
        assert.throws(() => lstatSync(paths.imports));
        assert.throws(() => lstatSync(paths.tfvars));
      });
    });
  }
});

test("direct materializer snapshots candidate and path base before its first await", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const original = await compile(fixture);
    const candidate = structuredClone(original);
    const assertion = structuredClone(cleanAssertion(candidate));
    const originalImports = candidate.artifacts.imports.content;
    const mutableCandidate = candidate as unknown as {
      artifacts: {
        imports: { content: string; sha256: string; size_bytes: number };
      };
    };
    const options = {
      outputRoot: fixture.outputRoot,
      pathBase: realpathSync(fixture.workspace),
      candidate,
      assertion,
      recheckInputs: async () => undefined,
    };
    const operation = materializeReadyZccPullArtifacts(options);

    // These mutations run after the async function reaches bindAuthority's
    // first await. Without the transaction-entry snapshot, prepareArtifacts
    // would publish the changed bytes under the changed path base.
    const changed = "materializer-secret-value\n";
    mutableCandidate.artifacts.imports.content = changed;
    mutableCandidate.artifacts.imports.size_bytes = Buffer.byteLength(changed);
    mutableCandidate.artifacts.imports.sha256 = createHash("sha256")
      .update(changed)
      .digest("hex");
    const redirected = path.join(realpathSync(fixture.workspace), "redirected");
    options.pathBase = redirected;

    const result = await operation;
    assert.equal(result.status, "complete");
    assert.equal(result.verification.source.sha256, assertion.source.sha256);
    assert.throws(() => lstatSync(redirected));
    assert.equal(
      readFileSync(targetPath(fixture, original.artifacts.imports), "utf8"),
      originalImports,
    );
  });
});

test("direct materializer snapshots hook and recheck function references", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    const hooks: {
      afterPreflight?: () => void;
    } = {};
    let originalRechecks = 0;
    const options = {
      outputRoot: fixture.outputRoot,
      pathBase: realpathSync(fixture.workspace),
      candidate,
      assertion: cleanAssertion(candidate),
      recheckInputs: async () => {
        originalRechecks += 1;
      },
      hooks,
    };
    const operation = materializeReadyZccPullArtifacts(options);
    hooks.afterPreflight = () => {
      throw new Error("materializer-secret-value");
    };
    options.recheckInputs = async () => {
      throw new Error("materializer-secret-value");
    };

    const result = await operation;
    assert.equal(result.status, "complete");
    assert.equal(originalRechecks, 2);
  });
});

test("integrated materializer snapshots its assertion before compilation awaits", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    const assertion = structuredClone(cleanAssertion(candidate));
    const mutableAssertion = assertion as unknown as {
      source: { sha256: string };
    };
    const result = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, assertion, {
        afterInputsBound: () => {
          mutableAssertion.source.sha256 = "0".repeat(64);
        },
      }),
    );
    assert.equal(result.status, "complete");
    assert.equal(result.verification.source.sha256, candidate.source.sha256);
  });
});

test("failure immediately after link is indeterminate, leaves an exact prefix, and retry completes", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const candidate = await compile(fixture);
    const assertion = cleanAssertion(candidate);
    let failed = false;
    const error = await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, assertion, {
          afterLink: () => {
            if (!failed) {
              failed = true;
              throw new Error("materializer-secret-value");
            }
          },
        }),
      ),
      "MATERIALIZATION_INDETERMINATE",
      ["materializer-secret-value", fixture.outputRoot],
    );
    assert.equal(error.retryable, true);
    assert.equal(
      readFileSync(targetPath(fixture, candidate.artifacts.imports), "utf8"),
      candidate.artifacts.imports.content,
    );
    assert.deepEqual(tempFiles(fixture.outputRoot), []);

    const result = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, assertion),
    );
    assert.deepEqual(result.publication.reused, ["imports"]);
    assert.deepEqual(result.publication.created, ["lookup", "tfvars"]);
  });
});

test("concurrent final creation is never replaced", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    const imports = targetPath(fixture, candidate.artifacts.imports);
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate), {
          beforePublish: (name) => {
            if (name === "imports") {
              writeFileSync(imports, "concurrent-secret-value\n");
            }
          },
        }),
      ),
      "MATERIALIZATION_TARGET_CHANGED",
      ["concurrent-secret-value", fixture.outputRoot],
    );
    assert.equal(readFileSync(imports, "utf8"), "concurrent-secret-value\n");
    assert.deepEqual(tempFiles(fixture.outputRoot), []);
  });
});

test("source and control mutation after staging fail with no finals or temp aliases", async (t) => {
  for (const kind of ["source", "control"] as const) {
    await t.test(kind, async () => {
      await withFixture("zcc_device_cleanup", async (fixture) => {
        const candidate = await compile(fixture);
        const target = kind === "source" ? fixture.pullPath : fixture.deploymentPath;
        await expectFailure(
          materializeZccPullArtifactsOperation(
            operationOptions(fixture, cleanAssertion(candidate), {
              afterStaged: () => {
                writeFileSync(target, "materializer-secret-value\n");
              },
            }),
          ),
          kind === "source" ? "RAW_PULL_CHANGED" : "COMPILE_CONTROL_CHANGED",
          ["materializer-secret-value", fixture.outputRoot],
        );
        assert.throws(() => lstatSync(targetPath(fixture, candidate.artifacts.imports)));
        assert.throws(() => lstatSync(targetPath(fixture, candidate.artifacts.tfvars)));
        assert.deepEqual(tempFiles(fixture.outputRoot), []);
      });
    });
  }
});

test("postpublication input mutation is indeterminate and preserves exact finals", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    const original = readFileSync(fixture.pullPath, "utf8");
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate), {
          afterPublish: (name) => {
            if (name === "tfvars") {
              writeFileSync(fixture.pullPath, "materializer-secret-value\n");
            }
          },
        }),
      ),
      "MATERIALIZATION_INDETERMINATE",
      ["materializer-secret-value", fixture.outputRoot],
    );
    assert.equal(
      readFileSync(targetPath(fixture, candidate.artifacts.imports), "utf8"),
      candidate.artifacts.imports.content,
    );
    assert.equal(
      readFileSync(targetPath(fixture, candidate.artifacts.tfvars), "utf8"),
      candidate.artifacts.tfvars.content,
    );
    assert.deepEqual(tempFiles(fixture.outputRoot), []);
    writeFileSync(fixture.pullPath, original);
    const retry = await materializeZccPullArtifactsOperation(
      operationOptions(fixture, cleanAssertion(candidate)),
    );
    assert.deepEqual(retry.publication.created, []);
    assert.deepEqual(retry.publication.reused, ["imports", "tfvars"]);
  });
});

test("assertion mismatch and non-ready candidates do not create artifact directories", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const candidate = await compile(fixture);
    const assertion = JSON.parse(JSON.stringify(cleanAssertion(candidate))) as ZccPullArtifactParity;
    (assertion as { source: { sha256: string } }).source.sha256 = "0".repeat(64);
    await expectFailure(
      materializeZccPullArtifactsOperation(operationOptions(fixture, assertion)),
      "MATERIALIZATION_ASSERTION_MISMATCH",
    );
    assert.equal(readdirSync(fixture.workspace).includes("imports"), false);
    assert.equal(readdirSync(fixture.workspace).includes("config"), false);
  });

  await withFixture("zcc_device_cleanup", async (fixture) => {
    writeFileSync(
      fixture.pullPath,
      '[{"id":"device-1","active":"1","futureSecret":"materializer-secret-value"}]\n',
    );
    const candidate = await compile(fixture);
    assert.equal(candidate.status, "review_required");
    await expectFailure(
      materializeZccPullArtifactsOperation(
        operationOptions(fixture, cleanAssertion(candidate)),
      ),
      "MATERIALIZATION_CANDIDATE_REVIEW_REQUIRED",
      ["materializer-secret-value"],
    );
    assert.equal(readdirSync(fixture.workspace).includes("imports"), false);
    assert.equal(readdirSync(fixture.workspace).includes("config"), false);
  });
});

test("all-reused and prefix parent replacement is detected from bound identities", async (t) => {
  for (const kind of ["all-reused", "prefix"] as const) {
    await t.test(kind, async () => {
      await withFixture("zcc_device_cleanup", async (fixture) => {
        const candidate = await compile(fixture);
        writeArtifact(fixture, candidate.artifacts.imports);
        if (kind === "all-reused") {
          writeArtifact(fixture, candidate.artifacts.tfvars);
        }
        const importsDirectory = path.dirname(
          targetPath(fixture, candidate.artifacts.imports),
        );
        await expectFailure(
          materializeZccPullArtifactsOperation(
            operationOptions(fixture, cleanAssertion(candidate), {
              afterPreflight: () => {
                renameSync(importsDirectory, `${importsDirectory}.old`);
                mkdirSync(importsDirectory, { recursive: true });
              },
            }),
          ),
          "MATERIALIZATION_DIRECTORY_CHANGED",
        );
      });
    });
  }
});
