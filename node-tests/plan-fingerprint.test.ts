import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
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

const AUTHORITY_SHA256 =
  "69ebf724f468e72c37ffaac33f78055e37cc944397fa923a31ff08331030a1b6";

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

interface PythonPlanFingerprintAuthority {
  readonly authority: {
    readonly implementation: string;
    readonly python: string;
    readonly unicode: string;
  };
  readonly baseline: string;
  readonly kind: string;
  readonly leading_feff: PythonFingerprintResult;
  readonly linux_invalid_filename: {
    readonly authority: {
      readonly implementation: string;
      readonly platform: string;
      readonly python: string;
      readonly unicode: string;
    };
    readonly results: readonly {
      readonly after_digest: string;
      readonly before_digest: string;
      readonly kind: string;
    }[];
  };
  readonly main: PythonFingerprintResult;
  readonly scanner: {
    readonly accepted: ScannerResult;
    readonly duplicate_modules: ScannerResult;
    readonly failures: readonly {
      readonly name: string;
      readonly result: ScannerResult;
    }[];
  };
  readonly source_blobs: {
    readonly node_implementation: string;
    readonly python_authority: string;
    readonly test: string;
  };
  readonly top_level_symlink_tree: readonly (readonly [string, string])[];
  readonly version: number;
}

function pythonAuthority(): PythonPlanFingerprintAuthority {
  const bytes = readFileSync(join(
    process.cwd(),
    "node-tests",
    "fixtures",
    "python-plan-fingerprint-v1.json",
  ));
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    AUTHORITY_SHA256,
    "frozen Python plan-fingerprint authority changed without re-adjudication",
  );
  const authority = JSON.parse(
    bytes.toString("utf8"),
  ) as PythonPlanFingerprintAuthority;
  assert.deepEqual({
    authority: authority.authority,
    baseline: authority.baseline,
    kind: authority.kind,
    source_blobs: authority.source_blobs,
    version: authority.version,
  }, {
    authority: {
      implementation: "cpython",
      python: "3.13.13",
      unicode: "15.1.0",
    },
    baseline: "b999edfb3255c644100935991171ad4fcee003c9",
    kind: "infrawright.python-plan-fingerprint-authority",
    source_blobs: {
      node_implementation: "8c57fda681df654f956646b2adbf09d485a689f8",
      python_authority: "f160a796f6078d96ee423d1ca7f1d169598c8160",
      test: "40de74a1738ce2d0773a0687e4d102f56d71ce33",
    },
    version: 1,
  });
  return authority;
}

function expandScannerResult(
  result: ScannerResult,
  envDir: string,
): ScannerResult {
  return result.message === undefined
    ? result
    : { ...result, message: result.message.replace("{env_dir}", envDir) };
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
    const oracle = pythonAuthority().main;

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
    const expected = pythonAuthority().top_level_symlink_tree;
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
    const oracle = pythonAuthority().leading_feff;
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
    const oracle = pythonAuthority().scanner.accepted;
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

  const authority = pythonAuthority().scanner.failures;
  assert.deepEqual(authority.map((item) => item.name), cases.map(([name]) => name));
  for (const [index, [name, text]] of cases.entries()) {
    await withTemp(async (temp) => {
      const envDir = join(temp, "root");
      write(join(envDir, "main.tf"), text);
      const frozen = authority[index];
      if (frozen === undefined) {
        assert.fail(`missing frozen scanner authority for ${name}`);
      }
      const oracle = expandScannerResult(frozen.result, envDir);
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
    const oracle = expandScannerResult(
      pythonAuthority().scanner.duplicate_modules,
      envDir,
    );
    assert.equal(oracle.ok, false);
    assert.deepEqual(await scannerResult(envDir), oracle);
  });
});

test("Linux filenames with undecodable bytes fail closed instead of disappearing", async (t) => {
  if (process.platform !== "linux") {
    t.skip("Linux permits non-UTF-8 directory entry bytes");
    return;
  }
  const linux = pythonAuthority().linux_invalid_filename;
  assert.deepEqual(linux.authority, {
    implementation: "cpython",
    platform: "linux",
    python: "3.13.13",
    unicode: "15.1.0",
  });
  assert.deepEqual(linux.results, [
    {
      after_digest: "f55bfc8f268b952751975428560aa040426782e42c72fd85576163451981b4f5",
      before_digest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
      kind: "root file",
    },
    {
      after_digest: "6c29bcfd5f334e3e039a8b9d1865a36c4a8e97b728a842dca8fa7a387a65756f",
      before_digest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
      kind: "module file",
    },
    {
      after_digest: "655895ac143b2de15c777d38ab61ce910b72cd37e756b802413cdabacc988212",
      before_digest: "14f7eaba7c0e5e38f4b9e54c06de86279b6dfd0a0419cfb57b02347a6e1675ca",
      kind: "module directory",
    },
  ]);
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
        const frozen = linux.results.find((item) => item.kind === kind);
        if (frozen === undefined) {
          assert.fail(`missing frozen Linux authority for ${kind}`);
        }
        assert.notEqual(frozen.after_digest, frozen.before_digest);
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
