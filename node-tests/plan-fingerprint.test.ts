import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, relative } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  backendFingerprint,
  canonicalPlanSourcesJson,
  captureInitSourcesPayload,
  capturePlanSourcesPayload,
  initSourcesSha256,
  localModulePath,
  planFingerprintV2,
  planSourcesSha256,
  rootTfFingerprints,
  rootModuleSources,
  treeFingerprints,
  varFileFingerprints,
  type InitSourcesPayload,
  type PlanFingerprintInput,
  type PlanSourcesPayload,
} from "../node-src/domain/plan-fingerprint.js";
import { ReadBudget } from "../node-src/io/bounded-files.js";

const PYTHON_FINGERPRINT = String.raw`
import hashlib
import json
import sys
from engine import ops

i = json.loads(sys.stdin.read())
payload = {
    "backend": ops._backend_fingerprint(i.get("backend_config"), i.get("backend_key")),
    "member_types": sorted(i["member_types"]),
    "modules": ops._module_fingerprints(i["env_dir"], i["member_types"]),
    "root_tf": ops._root_tf_fingerprints(i["env_dir"]),
    "var_files": ops._var_file_fingerprints(i["var_files"]),
}
canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"))
init_payload = {
    "backend": ops._backend_fingerprint(i.get("backend_config"), i.get("backend_key")),
    "modules": ops._module_fingerprints(i["env_dir"], i["member_types"]),
    "root_config": ops._root_config_fingerprints(i["env_dir"]),
}
init_canonical = json.dumps(init_payload, sort_keys=True, separators=(",", ":"))
print(json.dumps({
    "canonical": canonical,
    "digest": hashlib.sha256(canonical.encode("utf-8")).hexdigest(),
    "init_digest": hashlib.sha256(init_canonical.encode("utf-8")).hexdigest(),
    "init_payload": init_payload,
    "module_sources": ops._root_module_sources(i["env_dir"]),
    "payload": payload,
}, sort_keys=True))
`;

const PYTHON_SCANNER = String.raw`
import json
import sys
from engine import ops

env_dir = json.loads(sys.stdin.read())["env_dir"]
try:
    result = {"ok": True, "sources": ops._root_module_sources(env_dir)}
except Exception as exc:
    result = {"ok": False, "message": str(exc), "type": type(exc).__name__}
print(json.dumps(result, sort_keys=True))
`;

interface PythonFingerprintResult {
  readonly canonical: string;
  readonly digest: string;
  readonly init_digest: string;
  readonly init_payload: InitSourcesPayload;
  readonly module_sources: Readonly<Record<string, string>>;
  readonly payload: PlanSourcesPayload;
}

interface ScannerResult {
  readonly ok: boolean;
  readonly sources?: Readonly<Record<string, string>>;
  readonly message?: string;
  readonly type?: string;
}

function python<T>(script: string, input: unknown): T {
  const result = spawnSync("python3", ["-c", script], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: JSON.stringify(input),
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout) as T;
}

async function withTemp(
  callback: (root: string) => void | Promise<void>,
): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "node-plan-fingerprint-"));
  try {
    await callback(root);
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
}

function write(path: string, content: string | Uint8Array): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, content);
}

function moduleBlock(
  resourceType: string,
  source: string,
  itemName = resourceType,
): string {
  return [
    `module "${resourceType}" {`,
    `  source = "${source}"`,
    `  items = var.${itemName}_items`,
    "}",
    "",
  ].join("\n");
}

test("fingerprint v2 payload and digest are byte-identical to Python", async () => {
  await withTemp(async (temp) => {
    const envDir = join(temp, "envs", "tenant", "zpa_custom");
    const firstType = "zpa_segment_group";
    const secondType = "zpa_server_group";
    const firstModule = join(temp, "modules", `segment-\u007f-é-😀`);
    const missingModule = join(temp, "modules", "missing-server-group");
    mkdirSync(envDir, { recursive: true });
    mkdirSync(firstModule, { recursive: true });

    const firstSource = relative(envDir, firstModule);
    const secondSource = relative(envDir, missingModule);
    write(
      join(envDir, "main.tf"),
      [
        "# module text in comments is not configuration",
        "/* module \"ignored\" { source = \"remote/example\" } */",
        moduleBlock(firstType, firstSource),
        moduleBlock(secondType, secondSource),
      ].join("\n"),
    );
    write(join(envDir, "providers.tf"), "terraform { required_version = \">= 1.8\" }\n");
    write(join(envDir, "é-\u007f.tf.json"), "{}\n");
    write(join(envDir, ".terraform.lock.hcl"), "# lock\n");
    write(join(envDir, "terraform.tfvars"), "root = true\n");
    write(join(envDir, "terraform.tfvars.json"), "{\"root\":true}\n");
    write(join(envDir, "a.auto.tfvars"), "a = 1\n");
    write(join(envDir, "b.auto.tfvars.json"), "{\"b\":2}\n");
    write(join(envDir, "manual.tfvars"), "ignored = true\n");
    write(join(envDir, "tfplan"), "ignored plan bytes");
    write(join(envDir, "tfplan.sources"), "ignored fingerprint");
    mkdirSync(join(envDir, "directory.tf"));

    write(join(firstModule, "main.tf"), "# module main\n");
    write(join(firstModule, "nested", "binary.bin"), new Uint8Array([0, 1, 255]));
    write(join(firstModule, ".terraform", "ignored.bin"), "ignored\n");
    write(join(firstModule, "__pycache__", "ignored.pyc"), "ignored\n");
    symlinkSync(join(firstModule, "main.tf"), join(firstModule, "linked-main.tf"));
    const outsideDir = join(temp, "outside-tree");
    write(join(outsideDir, "must-not-appear.txt"), "outside\n");
    symlinkSync(outsideDir, join(firstModule, "linked-directory"));

    const linkedRootTarget = join(temp, "linked-root.tf");
    write(linkedRootTarget, "# linked root\n");
    symlinkSync(linkedRootTarget, join(envDir, "linked.tf"));

    const configA = join(temp, "config-a", "shared.auto.tfvars.json");
    const configB = join(temp, "config-b", "shared.auto.tfvars.json");
    const configUnicode = join(temp, "config", `vars-\u007f-é.auto.tfvars.json`);
    write(configA, "{\"a\":1}\n");
    write(configB, "{\"b\":2}\n");
    write(configUnicode, "{\"unicode\":true}\n");

    const backendTarget = join(temp, "backend-target.hcl");
    const backendLink = join(temp, "backend.hcl");
    write(backendTarget, "bucket = \"example\"\n");
    symlinkSync(backendTarget, backendLink);

    const input: PlanFingerprintInput = {
      backendConfig: backendLink,
      backendKey: `tenant/zpa-\u007f-é-😀.tfstate`,
      envDir,
      memberTypes: [secondType, firstType],
      varFiles: [
        configB,
        join(temp, "missing.auto.tfvars.json"),
        configA,
        configUnicode,
      ],
    };
    const oracle = python<PythonFingerprintResult>(PYTHON_FINGERPRINT, {
      backend_config: input.backendConfig,
      backend_key: input.backendKey,
      env_dir: input.envDir,
      member_types: input.memberTypes,
      var_files: input.varFiles,
    });

    const payload = await capturePlanSourcesPayload(input);
    const initPayload = await captureInitSourcesPayload(input);
    assert.deepEqual(payload, oracle.payload);
    assert.deepEqual(initPayload, oracle.init_payload);
    assert.equal(canonicalPlanSourcesJson(payload), oracle.canonical);
    assert.equal(planSourcesSha256(payload), oracle.digest);
    assert.equal(initSourcesSha256(initPayload), oracle.init_digest);
    assert.deepEqual(await planFingerprintV2(input), {
      sha256: oracle.digest,
      version: 2,
    });
    assert.deepEqual(
      Object.fromEntries(await rootModuleSources(envDir)),
      oracle.module_sources,
    );

    assert.match(oracle.canonical, /\\u007f/);
    assert.match(oracle.canonical, /\\u00e9/);
    assert.match(oracle.canonical, /\\ud83d\\ude00/);
    assert.equal(oracle.canonical.includes("\u007f"), false);

    assert.deepEqual(payload.root_tf.map(([name]) => name), [
      ".terraform.lock.hcl",
      "a.auto.tfvars",
      "b.auto.tfvars.json",
      "linked.tf",
      "main.tf",
      "providers.tf",
      "terraform.tfvars",
      "terraform.tfvars.json",
      "é-\u007f.tf.json",
    ]);

    const missing = payload.modules.find((entry) => {
      return entry.resource_type === secondType;
    });
    assert.deepEqual(missing?.files, []);
    assert.equal(missing?.present, false);
    const present = payload.modules.find((entry) => {
      return entry.resource_type === firstType;
    });
    assert.ok(present?.files.some(([name]) => name === "linked-main.tf"));
    assert.ok(!present?.files.some(([name]) => name.includes("must-not-appear")));
    assert.ok(!present?.files.some(([name]) => name.includes("ignored")));

    const duplicateBasenames = payload.var_files.filter(([name]) => {
      return name === basename(configA);
    });
    assert.equal(duplicateBasenames.length, 2);
  });
});

test("module tree top-level symlinks follow Python os.walk semantics", async () => {
  await withTemp(async (temp) => {
    const target = join(temp, "target");
    const link = join(temp, "link");
    write(join(target, "file.txt"), "content\n");
    symlinkSync(target, link);
    const expected = python<readonly (readonly [string, string])[]>(
      "import json,sys; from engine import ops; print(json.dumps(ops._tree_fingerprints(json.loads(sys.stdin.read())['root'])))",
      { root: link },
    );
    assert.deepEqual(await treeFingerprints(link), expected);
    assert.equal((await treeFingerprints(link)).length, 1);
  });
});

test("leading U+FEFF filename bytes are preserved in root and module fingerprints", async () => {
  await withTemp(async (temp) => {
    const envDir = join(temp, "env");
    const moduleDir = join(temp, "module");
    mkdirSync(envDir, { recursive: true });
    mkdirSync(moduleDir, { recursive: true });
    write(join(envDir, "main.tf"), moduleBlock(
      "zpa_sample",
      relative(envDir, moduleDir),
    ));
    write(join(envDir, "\ufeffroot.tf"), "# leading FEFF root\n");
    write(join(moduleDir, "\ufeffmodule.tf"), "# leading FEFF module\n");
    const input: PlanFingerprintInput = {
      envDir,
      memberTypes: ["zpa_sample"],
      varFiles: [],
    };
    const oracle = python<PythonFingerprintResult>(PYTHON_FINGERPRINT, {
      env_dir: envDir,
      member_types: input.memberTypes,
      var_files: [],
    });
    const payload = await capturePlanSourcesPayload(input);
    assert.deepEqual(payload, oracle.payload);
    assert.equal((await planFingerprintV2(input)).sha256, oracle.digest);
    assert.ok(payload.root_tf.some(([name]) => name === "\ufeffroot.tf"));
    assert.ok(payload.modules[0]?.files.some(([name]) => name === "\ufeffmodule.tf"));
  });
});

test("backend, var-file, and local-path edge semantics match Python", async () => {
  await withTemp(async (temp) => {
    const missingBackend = join(temp, "missing-backend.hcl");
    assert.deepEqual(await backendFingerprint("", "ignored"), null);
    assert.deepEqual(await backendFingerprint(missingBackend, null), {
      key: null,
      present: false,
    });
    assert.deepEqual(
      await varFileFingerprints([join(temp, "missing.tfvars")]),
      [],
    );
    assert.equal(localModulePath(temp, "registry/module/provider"), null);
    assert.equal(localModulePath(temp, ""), null);
    assert.equal(localModulePath(temp, "./module"), join(temp, "module"));
    assert.equal(localModulePath(temp, "../module"), join(temp, "..", "module"));
  });
});

test("one bounded budget covers every file read for a payload", async () => {
  await withTemp(async (temp) => {
    const envDir = join(temp, "env");
    const moduleDir = join(temp, "module");
    write(
      join(envDir, "main.tf"),
      moduleBlock("alpha", relative(envDir, moduleDir)),
    );
    write(join(moduleDir, "main.tf"), "# module\n");
    const budget = new ReadBudget({
      maxFiles: 2,
      maxDirectories: 10,
      maxDirectoryEntries: 100,
      maxDepth: 8,
      maxFileBytes: 1024n,
      maxTotalBytes: 2048n,
    });
    await assert.rejects(
      capturePlanSourcesPayload({
        envDir,
        memberTypes: ["alpha"],
        varFiles: [],
      }, budget),
      (error: unknown) => {
        return error instanceof ProcessFailure
          && error.code === "FILE_COUNT_EXCEEDED";
      },
    );
  });
});

async function scannerResult(envDir: string): Promise<ScannerResult> {
  try {
    return {
      ok: true,
      sources: Object.fromEntries(await rootModuleSources(envDir)),
    };
  } catch (error: unknown) {
    assert.ok(error instanceof Error);
    return { ok: false, message: error.message, type: "RuntimeError" };
  }
}

test("generated-root HCL scanner acceptance matches Python", async () => {
  await withTemp(async (temp) => {
    const envDir = join(temp, "root");
    write(
      join(envDir, "main.tf"),
      [
        "# module \"commented\" {",
        "terraform { required_version = \">= 1.8\" }",
        "module \"alpha\" {",
        "  /* source = \"remote/ignored\" */",
        "  source = \"../modules/alpha\" // trailing comment",
        "  items = local.alpha_items # trailing comment",
        "}",
        "",
      ].join("\r\n"),
    );
    write(join(envDir, "ignored.tf.json"), "not HCL");
    write(
      join(envDir, "python-whitespace.tf"),
      [
        "\u001fmodule\u001f\"beta\"\u001f{",
        "\u001fsource\u001f=\u001f\"../modules/beta\"",
        "\u001fitems\u001f=\u001flocal.beta_items",
        "\u001f}",
        "",
      ].join("\n"),
    );
    // encoding="utf-8" preserves FEFF, and Python re \s does not consume it.
    // The block remains structurally balanced but is not a recognized module.
    write(
      join(envDir, "utf8-bom.tf"),
      "\ufeffmodule \"bom_ignored\" {\n"
        + "  source = \"../modules/bom\"\n"
        + "  items = var.bom_items\n"
        + "}\n",
    );
    const oracle = python<ScannerResult>(PYTHON_SCANNER, { env_dir: envDir });
    assert.deepEqual(await scannerResult(envDir), oracle);
    assert.deepEqual(oracle.sources, {
      alpha: "../modules/alpha",
      beta: "../modules/beta",
    });
  });
});

test("generated-root HCL scanner failures match Python exactly", async () => {
  const cases: ReadonlyArray<readonly [name: string, text: string]> = [
    [
      "template source",
      moduleBlock("alpha", "../modules/$${alpha}", "alpha"),
    ],
    [
      "heredoc",
      ["value = <<EOF", "text", "EOF", ""].join("\n"),
    ],
    [
      "duplicate source",
      [
        "module \"alpha\" {",
        "  source = \"../modules/alpha\"",
        "  source = \"../modules/beta\"",
        "  items = var.alpha_items",
        "}",
        "",
      ].join("\n"),
    ],
    [
      "duplicate items",
      [
        "module \"alpha\" {",
        "  source = \"../modules/alpha\"",
        "  items = var.alpha_items",
        "  items = local.alpha_items",
        "}",
        "",
      ].join("\n"),
    ],
    [
      "unexpected module field",
      [
        "module \"alpha\" {",
        "  source = \"../modules/alpha\"",
        "  count = 1",
        "  items = var.alpha_items",
        "}",
        "",
      ].join("\n"),
    ],
    [
      "missing items",
      [
        "module \"alpha\" {",
        "  source = \"../modules/alpha\"",
        "}",
        "",
      ].join("\n"),
    ],
    ["unexpected closing brace", "}\n"],
    ["unbalanced braces", "terraform {\n"],
    ["unterminated quote", "value = \"unterminated\n"],
    ["unterminated block comment", "/* never closed\n"],
    [
      "unicode line separator line number",
      "# first\u2028value = \"unterminated\n",
    ],
  ];

  for (const [name, text] of cases) {
    await withTemp(async (temp) => {
      const envDir = join(temp, "root");
      write(join(envDir, "main.tf"), text);
      const oracle = python<ScannerResult>(PYTHON_SCANNER, { env_dir: envDir });
      assert.equal(oracle.ok, false, name);
      assert.deepEqual(await scannerResult(envDir), oracle, name);
    });
  }
});

test("duplicate modules across root files fail like Python", async () => {
  await withTemp(async (temp) => {
    const envDir = join(temp, "root");
    write(join(envDir, "a.tf"), moduleBlock("alpha", "../modules/alpha"));
    write(join(envDir, "b.tf"), moduleBlock("alpha", "../modules/alpha"));
    const oracle = python<ScannerResult>(PYTHON_SCANNER, { env_dir: envDir });
    assert.equal(oracle.ok, false);
    assert.deepEqual(await scannerResult(envDir), oracle);
  });
});

test("Linux filenames with undecodable bytes fail closed instead of disappearing", async (t) => {
  if (process.platform !== "linux") {
    t.skip("Linux permits non-UTF-8 directory entry bytes");
    return;
  }
  for (const kind of ["root file", "module file", "module directory"] as const) {
    await t.test(kind, async () => {
      await withTemp(async (temp) => {
        const envDir = join(temp, "env");
        const moduleDir = join(temp, "module");
        mkdirSync(envDir, { recursive: true });
        mkdirSync(moduleDir, { recursive: true });
        write(join(moduleDir, "main.tf"), "# module\n");
        write(
          join(envDir, "main.tf"),
          moduleBlock("zpa_sample", relative(envDir, moduleDir)),
        );
        const input: PlanFingerprintInput = {
          envDir,
          memberTypes: ["zpa_sample"],
          varFiles: [],
        };
        const before = python<PythonFingerprintResult>(PYTHON_FINGERPRINT, {
          env_dir: envDir,
          member_types: input.memberTypes,
          var_files: [],
        });
        const parent = kind === "root file" ? envDir : moduleDir;
        const rawPath = Buffer.concat([
          Buffer.from(`${parent}/bad-`),
          Buffer.from([0xff]),
          Buffer.from(kind === "module directory" ? "" : ".tf"),
        ]);
        if (kind === "module directory") {
          mkdirSync(rawPath);
          writeFileSync(
            Buffer.concat([rawPath, Buffer.from("/child.tf")]),
            "# nested raw path\n",
          );
        } else {
          writeFileSync(rawPath, "# raw filename\n");
        }
        const after = python<PythonFingerprintResult>(PYTHON_FINGERPRINT, {
          env_dir: envDir,
          member_types: input.memberTypes,
          var_files: [],
        });
        assert.notEqual(after.digest, before.digest);
        await assert.rejects(
          kind === "root file"
            ? rootTfFingerprints(envDir)
            : planFingerprintV2(input),
          (error: unknown) => {
            return error instanceof ProcessFailure
              && error.code === "INVALID_FILENAME_ENCODING";
          },
        );
      });
    });
  }
});
