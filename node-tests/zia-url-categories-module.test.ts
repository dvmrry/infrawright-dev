import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  access,
  link,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  ZIA_PROVIDER_VERSION,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
} from "../node-src/domain/zia-url-categories.js";
import { writeZiaUrlCategoriesModule } from "../node-src/io/zia-url-categories-module.js";

const MODULE_SET = `zia-v${ZIA_PROVIDER_VERSION}`;

const PYTHON_MODULE_ORACLE = String.raw`
import json
import os
import sys

from engine.gen_module import (
    _fmt,
    render_main,
    render_outputs,
    render_variables,
    render_versions,
)
from engine.tfschema import load_resource

payload = json.load(sys.stdin)
resource_type = "zia_url_categories"
rs = load_resource(resource_type)
result = {
    "main.tf": _fmt(render_main(resource_type, rs)),
    "variables.tf": _fmt(render_variables(resource_type, rs)),
    "outputs.tf": _fmt(render_outputs(resource_type, rs)),
    "versions.tf": _fmt(render_versions(resource_type, rs)),
}
json.dump(result, sys.stdout, indent=2, sort_keys=True)
sys.stdout.write("\n")
`;

const PYTHON_ENV_ORACLE = String.raw`
import json
import os
import sys

payload = json.load(sys.stdin)
os.environ["INFRAWRIGHT_DEPLOYMENT"] = payload["deployment"]

from engine.gen_env import _fmt, render_env_main

text = render_env_main(
    "zia_url_categories",
    ["zia_url_categories"],
    payload["tenant"],
    payload["env_dir"],
)
sys.stdout.write(_fmt(text))
`;

function failureCode(code: string): (error: unknown) => boolean {
  return (error) => error instanceof ProcessFailure && error.code === code;
}

function pythonOracle(script: string, input: unknown): string {
  const result = spawnSync("python3", ["-c", script], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: { ...process.env },
    input: JSON.stringify(input),
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, "");
  return result.stdout;
}

test("writes five Terraform files matching Python gen_module/gen_env authority", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-categories-module-"));
  try {
    const tenant = "acme_prod.1";
    const paths = await writeZiaUrlCategoriesModule({ tenant, workspace });

    // Writer canonicalizes via realpath (macOS may map /var -> /private/var).
    const moduleMarker = path.join("modules", MODULE_SET, ZIA_URL_CATEGORIES_RESOURCE_TYPE, "main.tf");
    assert.ok(
      paths.moduleMain.endsWith(moduleMarker),
      `module main path must end with ${moduleMarker}`,
    );
    const resolvedWorkspace = paths.moduleMain.slice(
      0,
      paths.moduleMain.length - moduleMarker.length - 1,
    );
    const moduleDir = path.join(
      resolvedWorkspace,
      "modules",
      MODULE_SET,
      ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    );
    const envDir = path.join(
      resolvedWorkspace,
      "envs",
      tenant,
      ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    );

    assert.equal(paths.moduleMain, path.join(moduleDir, "main.tf"));
    assert.equal(paths.moduleVariables, path.join(moduleDir, "variables.tf"));
    assert.equal(paths.moduleOutputs, path.join(moduleDir, "outputs.tf"));
    assert.equal(paths.moduleVersions, path.join(moduleDir, "versions.tf"));
    assert.equal(paths.envMain, path.join(envDir, "main.tf"));

    const [main, variables, outputs, versions, envMain] = await Promise.all([
      readFile(paths.moduleMain, "utf8"),
      readFile(paths.moduleVariables, "utf8"),
      readFile(paths.moduleOutputs, "utf8"),
      readFile(paths.moduleVersions, "utf8"),
      readFile(paths.envMain, "utf8"),
    ]);

    const moduleAuthority = JSON.parse(pythonOracle(PYTHON_MODULE_ORACLE, {})) as Record<
      string,
      string
    >;
    assert.equal(main, moduleAuthority["main.tf"]);
    assert.equal(variables, moduleAuthority["variables.tf"]);
    assert.equal(outputs, moduleAuthority["outputs.tf"]);
    assert.equal(versions, moduleAuthority["versions.tf"]);

    const deploymentPath = path.join(resolvedWorkspace, "deployment.json");
    const moduleSetAbs = path.join(resolvedWorkspace, "modules", MODULE_SET);
    await writeFile(
      deploymentPath,
      `${JSON.stringify({ module_dir: moduleSetAbs })}\n`,
      { encoding: "utf8" },
    );
    const envAuthority = pythonOracle(PYTHON_ENV_ORACLE, {
      deployment: deploymentPath,
      env_dir: envDir,
      tenant,
    });
    assert.equal(envMain, envAuthority);
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("rejects non-absolute workspace and path/tenant escape labels", async () => {
  await assert.rejects(
    () => writeZiaUrlCategoriesModule({ tenant: "acme", workspace: "relative/ws" }),
    failureCode("INVALID_ZIA_MODULE_WORKSPACE"),
  );
  await assert.rejects(
    () => writeZiaUrlCategoriesModule({ tenant: "acme", workspace: "/tmp/zia\0ws" }),
    failureCode("INVALID_ZIA_MODULE_WORKSPACE"),
  );

  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-categories-module-bad-"));
  try {
    for (const tenant of ["../escape", "a/b", "has space", ".", "..", ""]) {
      await assert.rejects(
        () => writeZiaUrlCategoriesModule({ tenant, workspace }),
        failureCode("INVALID_ZIA_MODULE_TENANT"),
      );
    }
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("rejects symlinked module, environment, and intermediate workspace authorities", async (t) => {
  for (const target of ["module", "environment", "intermediate"] as const) {
    await t.test(target, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), `zia-module-link-${target}-`));
      const outside = await mkdtemp(path.join(os.tmpdir(), `zia-module-outside-${target}-`));
      try {
        if (target === "module") {
          const parent = path.join(workspace, "modules", MODULE_SET);
          await mkdir(parent, { recursive: true });
          await symlink(outside, path.join(parent, ZIA_URL_CATEGORIES_RESOURCE_TYPE));
        } else if (target === "environment") {
          const parent = path.join(workspace, "envs", "tenant");
          await mkdir(parent, { recursive: true });
          await symlink(outside, path.join(parent, ZIA_URL_CATEGORIES_RESOURCE_TYPE));
        } else {
          await symlink(outside, path.join(workspace, "modules"));
        }
        await assert.rejects(
          writeZiaUrlCategoriesModule({ tenant: "tenant", workspace }),
          failureCode("UNSAFE_ZIA_URL_CATEGORY_WORKSPACE"),
        );
        await assert.rejects(access(path.join(outside, "main.tf")), /ENOENT/);
      } finally {
        await rm(workspace, { force: true, recursive: true });
        await rm(outside, { force: true, recursive: true });
      }
    });
  }
});

test("replaces a hard-linked runtime target without truncating the external inode", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-module-hardlink-"));
  const outside = path.join(workspace, "outside-sentinel");
  try {
    const first = await writeZiaUrlCategoriesModule({ tenant: "tenant", workspace });
    await rm(first.moduleMain);
    await writeFile(outside, "external-bytes-must-survive\n");
    await link(outside, first.moduleMain);
    const second = await writeZiaUrlCategoriesModule({ tenant: "tenant", workspace });
    assert.equal(await readFile(outside, "utf8"), "external-bytes-must-survive\n");
    assert.match(await readFile(second.moduleMain, "utf8"), /resource "zia_url_categories"/);
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});
