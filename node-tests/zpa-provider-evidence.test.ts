import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  AUTHORING_COMMANDS,
  AuthoringCliUsageError,
  runAuthoringCommand,
} from "../node-src/authoring/cli.js";
import {
  auditZpaProviderEvidence,
  validateZpaProviderEvidenceLocal,
  validateZpaProviderEvidenceSource,
  ZPA_PROVIDER_COMMIT,
  ZPA_PROVIDER_REF,
  ZPA_PROVIDER_REPOSITORY,
  type ZpaEvidenceGitHost,
  type ZpaProviderEvidenceReport,
} from "../node-src/authoring/zpa-provider-evidence.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const ROOT = process.cwd();
const MATRIX = path.join(ROOT, "docs", "evidence", "zpa-provider-v4.4.6.json");
const BUNDLED_CLI = path.join(ROOT, "dist", "infrawright-cli.mjs");

function digest(value: Buffer | string): string {
  return createHash("sha256").update(value).digest("hex");
}

async function matrix(): Promise<JsonObject> {
  return JSON.parse(await readFile(MATRIX, "utf8")) as JsonObject;
}

function clone(value: JsonObject): JsonObject {
  return structuredClone(value);
}

function resources(report: JsonObject): JsonObject[] {
  return report.resources as JsonObject[];
}

async function validate(report: JsonObject): Promise<ZpaProviderEvidenceReport> {
  return validateZpaProviderEvidenceLocal({ report, repositoryRoot: ROOT });
}

test("committed ZPA provider matrix is locally current and source-bound", async () => {
  const report = await validate(await matrix());
  assert.deepEqual(report.summary, {
    fetch_backed_resources: 16,
    generated_config_runtime_gates: 16,
    numeric_or_alternate_importers: 14,
    passthrough_importers: 2,
    resources_with_sensitive_inputs: 1,
    schema_id_not_source_populated: 3,
  });
  assert.equal((report.local_inputs as unknown[]).length, 12);
  assert.equal(((report.provider as JsonObject).source_files as unknown[]).length, 17);
  const byType = Object.fromEntries(resources(report as JsonObject).map((item) => {
    return [item.resource_type as string, item];
  }));
  for (const resourceType of [
    "zpa_ba_certificate",
    "zpa_emergency_access_user",
    "zpa_inspection_profile",
  ]) {
    assert.equal(
      (byType[resourceType]?.read_identity as JsonObject).schema_id_attribute,
      "not_source_populated",
    );
  }
  assert.deepEqual(byType.zpa_application_segment?.read_identity, {
    schema_id_attribute: "read_response_id",
    terraform_instance_id: "current_id_lookup_with_response_schema_id",
  });
  assert.ok(
    (byType.zpa_inspection_profile?.exceptions as string[])
      .includes("importer_writes_undeclared_profile_id"),
  );
  const sensitive = Object.fromEntries(resources(report as JsonObject)
    .filter((item) => ((item.state_shape as JsonObject).sensitive_input_paths as unknown[]).length > 0)
    .map((item) => [
      item.resource_type as string,
      (item.state_shape as JsonObject).sensitive_input_paths,
    ]));
  assert.deepEqual(sensitive, {
    zpa_pra_credential_controller: ["passphrase", "password", "private_key"],
  });
  let anchors = 0;
  for (const item of resources(report as JsonObject)) {
    const source = item.source_evidence as JsonObject;
    const entries = [
      source.importer,
      source.read_identity,
      ...Object.values(source.exceptions as JsonObject),
    ] as JsonObject[];
    anchors += entries.length;
    for (const anchor of entries) {
      assert.ok(
        (anchor.url as string).startsWith(
          `${ZPA_PROVIDER_REPOSITORY}/blob/${ZPA_PROVIDER_REF}/`,
        ),
      );
      assert.match(anchor.sha256 as string, /^[0-9a-f]{64}$/u);
    }
  }
  assert.equal(anchors, 45);
});

test("local evidence validation fails closed across every authority class", async (t) => {
  const mutations: readonly {
    readonly message: RegExp;
    readonly mutate: (report: JsonObject) => void;
    readonly name: string;
  }[] = [
    { name: "unknown report key", message: /report keys differ/u, mutate: (r) => { r.extra = true; } },
    { name: "missing report key", message: /report keys differ/u, mutate: (r) => { delete r.summary; } },
    { name: "wrong kind", message: /unsupported evidence report/u, mutate: (r) => { r.kind = "other"; } },
    { name: "wrong version", message: /unsupported evidence report/u, mutate: (r) => { r.schema_version = 2; } },
    { name: "wrong provider pin", message: /provider source pin/u, mutate: (r) => { (r.provider as JsonObject).commit = "0".repeat(40); } },
    { name: "stale local hash", message: /local pack\/schema input bindings/u, mutate: (r) => { ((r.local_inputs as JsonObject[])[0] as JsonObject).sha256 = "0".repeat(64); } },
    { name: "resources is not a list", message: /resources must be a list/u, mutate: (r) => { r.resources = {}; } },
    { name: "resource order", message: /resource set\/order/u, mutate: (r) => { (r.resources as unknown[]).reverse(); } },
    { name: "fetch path", message: /fetch metadata/u, mutate: (r) => { ((resources(r)[0] as JsonObject).fetch as JsonObject).path = "different"; } },
    { name: "state count", message: /state-shape summary/u, mutate: (r) => { ((((resources(r)[0] as JsonObject).state_shape as JsonObject).counts as JsonObject).input_attributes as number) += 1; } },
    { name: "unsupported import mode", message: /import mode is unsupported/u, mutate: (r) => { ((resources(r)[0] as JsonObject).import as JsonObject).mode = "future"; } },
    { name: "stale import template", message: /engine import template is stale/u, mutate: (r) => { ((resources(r)[0] as JsonObject).import as JsonObject).engine_import_id_template = "{future}"; } },
    { name: "passthrough inconsistency", message: /passthrough import claim/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.import as JsonObject).mode === "passthrough") as JsonObject; (item.import as JsonObject).grammar = "future"; } },
    { name: "numeric grammar", message: /numeric import grammar/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.import as JsonObject).mode === "numeric_or_alternate_lookup") as JsonObject; (item.import as JsonObject).grammar = "base10_numeric_id_or_name_future"; } },
    { name: "numeric requirement", message: /numeric exactness requirement/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.import as JsonObject).mode === "numeric_or_alternate_lookup") as JsonObject; (item.import as JsonObject).numeric_exactness_requirement = "raw id must parse"; } },
    { name: "empty alternate", message: /alternate import claim/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.import as JsonObject).mode === "numeric_or_alternate_lookup") as JsonObject; (item.import as JsonObject).alternate_lookup = ""; } },
    { name: "unsupported read identity", message: /read identity claim/u, mutate: (r) => { ((resources(r)[0] as JsonObject).read_identity as JsonObject).terraform_instance_id = "future"; } },
    { name: "generated config overclaim", message: /overclaims generated-config/u, mutate: (r) => { ((resources(r)[0] as JsonObject).generated_config as JsonObject).qualification = "qualified"; } },
    { name: "duplicate exceptions", message: /exceptions must be sorted and unique/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.exceptions as unknown[]).length > 0) as JsonObject; (item.exceptions as unknown[]).push((item.exceptions as unknown[])[0]); } },
    { name: "missing exception anchor", message: /exception anchors are incomplete/u, mutate: (r) => { const item = resources(r).find((entry) => (entry.exceptions as unknown[]).length > 0) as JsonObject; const evidence = item.source_evidence as JsonObject; delete (evidence.exceptions as JsonObject)[(item.exceptions as string[])[0] as string]; } },
    { name: "absolute anchor path", message: /safe relative path/u, mutate: (r) => { ((resources(r)[0]?.source_evidence as JsonObject).importer as JsonObject).path = "/tmp/source.go"; } },
    { name: "empty anchor function", message: /is invalid/u, mutate: (r) => { ((resources(r)[0]?.source_evidence as JsonObject).importer as JsonObject).function = ""; } },
    { name: "reversed anchor range", message: /is invalid/u, mutate: (r) => { const anchor = (resources(r)[0]?.source_evidence as JsonObject).importer as JsonObject; anchor.end_line = (anchor.start_line as number) - 1; } },
    { name: "invalid anchor hash", message: /is invalid/u, mutate: (r) => { ((resources(r)[0]?.source_evidence as JsonObject).importer as JsonObject).sha256 = "bad"; } },
    { name: "unpinned anchor URL", message: /URL is not pinned/u, mutate: (r) => { ((resources(r)[0]?.source_evidence as JsonObject).importer as JsonObject).url = "https://example.test"; } },
    { name: "unsafe source file path", message: /safe relative path/u, mutate: (r) => { (((r.provider as JsonObject).source_files as JsonObject[])[0] as JsonObject).path = "../source.go"; } },
    { name: "unordered source files", message: /source-file bindings are incomplete or unordered/u, mutate: (r) => { ((r.provider as JsonObject).source_files as unknown[]).reverse(); } },
    { name: "invalid source file hash", message: /source-file binding is invalid/u, mutate: (r) => { (((r.provider as JsonObject).source_files as JsonObject[])[0] as JsonObject).sha256 = "bad"; } },
    { name: "stale summary", message: /report summary is stale/u, mutate: (r) => { (r.summary as JsonObject).fetch_backed_resources = 15; } },
  ];
  const baseline = await matrix();
  for (const entry of mutations) {
    await t.test(entry.name, async () => {
      const report = clone(baseline);
      entry.mutate(report);
      await assert.rejects(validate(report), entry.message);
    });
  }
});

function sourceReport(filename: string, bytes: Buffer): ZpaProviderEvidenceReport {
  const lines = bytes.toString("utf8").split(/(?<=\n)/u);
  const range = (line: number): JsonObject => ({
    end_line: line,
    function: `line${String(line)}`,
    path: filename,
    sha256: digest(lines[line - 1] as string),
    start_line: line,
    url: `${ZPA_PROVIDER_REPOSITORY}/blob/${ZPA_PROVIDER_REF}/${filename}`
      + `#L${String(line)}-L${String(line)}`,
  });
  return {
    provider: {
      source_files: [{ path: filename, sha256: digest(bytes) }],
    },
    resources: [{
      source_evidence: {
        exceptions: {},
        importer: range(1),
        read_identity: range(2),
      },
    }],
  };
}

class FakeGitHost implements ZpaEvidenceGitHost {
  head = ZPA_PROVIDER_COMMIT;
  tag = ZPA_PROVIDER_COMMIT;
  status = "";
  error: Error | null = null;

  async run(_root: string, arguments_: readonly string[]): Promise<string> {
    if (this.error !== null) throw this.error;
    if (arguments_[0] === "status") return this.status;
    return arguments_[1] === "HEAD" ? this.head : this.tag;
  }
}

test("pinned provider checkout validation binds commits, files, and inclusive ranges", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "iw-zpa-evidence-"));
  try {
    const filename = "zpa/resource.go";
    const bytes = Buffer.from("line one\nline two\n", "utf8");
    await mkdir(path.join(root, "zpa"), { recursive: true });
    await writeFile(path.join(root, filename), bytes);
    const report = sourceReport(filename, bytes);
    const host = new FakeGitHost();
    assert.equal(
      await validateZpaProviderEvidenceSource({ host, providerRoot: root, report }),
      report,
    );

    host.head = "0".repeat(40);
    await assert.rejects(
      validateZpaProviderEvidenceSource({ host, providerRoot: root, report }),
      /checkout is not the pinned commit/u,
    );
    host.head = ZPA_PROVIDER_COMMIT;
    host.tag = "1".repeat(40);
    await assert.rejects(
      validateZpaProviderEvidenceSource({ host, providerRoot: root, report }),
      /tag does not resolve/u,
    );
    host.tag = ZPA_PROVIDER_COMMIT;
    host.status = " M zpa/resource.go";
    await assert.rejects(
      validateZpaProviderEvidenceSource({ host, providerRoot: root, report }),
      /source files are modified/u,
    );
    host.status = "";
    await writeFile(path.join(root, filename), "changed\n");
    await assert.rejects(
      validateZpaProviderEvidenceSource({ host, providerRoot: root, report }),
      /source binding failed/u,
    );
    await writeFile(path.join(root, filename), bytes);
    const badRange = structuredClone(report) as JsonObject;
    const anchor = (((badRange.resources as JsonObject[])[0] as JsonObject)
      .source_evidence as JsonObject).importer as JsonObject;
    anchor.end_line = 3;
    await assert.rejects(
      validateZpaProviderEvidenceSource({
        host,
        providerRoot: root,
        report: badRange,
      }),
      /source range exceeds file/u,
    );
    anchor.end_line = 1;
    anchor.sha256 = "0".repeat(64);
    await assert.rejects(
      validateZpaProviderEvidenceSource({
        host,
        providerRoot: root,
        report: badRange,
      }),
      /source range binding failed/u,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("ZPA evidence CLI is Python-free and preserves success and failure exits", async () => {
  assert.ok(AUTHORING_COMMANDS.has("zpa-provider-evidence"));
  const stdout: string[] = [];
  const stderr: string[] = [];
  const context = {
    command: "zpa-provider-evidence",
    environment: {
      PATH: "/python-tripwire-does-not-exist",
      PYTHON: "/python-tripwire-does-not-exist",
    },
    repositoryRoot: ROOT,
    stderr: (text: string) => stderr.push(text),
    stdout: (text: string) => stdout.push(text),
  };
  assert.equal(await runAuthoringCommand({ ...context, arguments: [] }), 0);
  assert.deepEqual(stdout, [
    "ZPA provider evidence valid (16 resources; source=not requested)\n",
  ]);
  assert.deepEqual(stderr, []);

  const temp = await mkdtemp(path.join(os.tmpdir(), "iw-zpa-evidence-cli-"));
  try {
    const invalid = path.join(temp, "invalid.json");
    await writeFile(invalid, "{}\n", "utf8");
    stdout.length = 0;
    assert.equal(await runAuthoringCommand({
      ...context,
      arguments: ["--matrix", invalid],
    }), 1);
    assert.equal(stdout.length, 0);
    assert.match(stderr.at(-1) ?? "", /^error: report keys differ/u);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }

  await assert.rejects(
    runAuthoringCommand({ ...context, arguments: ["unexpected"] }),
    (error: unknown) => error instanceof AuthoringCliUsageError,
  );
});

test("bundled iw dispatches the ZPA evidence audit without Python", async (context) => {
  const environment = {
    ...process.env,
    PATH: "/python-tripwire-does-not-exist",
    PYTHON: "/python-tripwire-does-not-exist",
  };
  const built = spawnSync(process.execPath, ["scripts/build-metadata-cli.mjs"], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment,
  });
  assert.equal(built.status, 0, built.stderr);
  const run = (arguments_: readonly string[]) => spawnSync(
    process.execPath,
    [BUNDLED_CLI, ...arguments_],
    {
      cwd: ROOT,
      encoding: "utf8",
      env: environment,
    },
  );
  const result = run(["zpa-provider-evidence"]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(
    result.stdout,
    "ZPA provider evidence valid (16 resources; source=not requested)\n",
  );
  assert.equal(result.stderr, "");

  const temporary = await mkdtemp(path.join(os.tmpdir(), "iw-zpa-bundle-"));
  context.after(async () => rm(temporary, { force: true, recursive: true }));
  const invalid = path.join(temporary, "invalid.json");
  await writeFile(invalid, "{}\n", "utf8");
  const rejected = run(["zpa-provider-evidence", "--matrix", invalid]);
  assert.equal(rejected.status, 1);
  assert.equal(rejected.stdout, "");
  assert.match(rejected.stderr, /^error: report keys differ/u);

  const usage = run(["zpa-provider-evidence", "unexpected"]);
  assert.equal(usage.status, 2);
  assert.equal(usage.stdout, "");
  assert.match(usage.stderr, /does not accept positional arguments/u);

  const help = run(["--help"]);
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /iw zpa-provider-evidence/u);
});

test("optional external ZPA provider checkout remains auditable", async (t) => {
  const providerRoot = process.env.ZPA_PROVIDER_SOURCE;
  if (providerRoot === undefined || providerRoot === "") {
    t.skip("set ZPA_PROVIDER_SOURCE to audit the external pinned checkout");
    return;
  }
  await auditZpaProviderEvidence({ providerRoot, repositoryRoot: ROOT });
});
