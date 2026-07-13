import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  access,
  chmod,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rm,
  writeFile,
} from "node:fs/promises";
import { createServer as createHttpsServer } from "node:https";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();
const RESOURCE = "zia_url_categories";
const TENANT = "runtime-smoke";

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

async function exists(file: string): Promise<boolean> {
  try {
    await access(file);
    return true;
  } catch {
    return false;
  }
}

async function listen(server: ReturnType<typeof createHttpsServer>): Promise<number> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  assert.ok(typeof address === "object" && address !== null);
  return address.port;
}

async function close(server: ReturnType<typeof createHttpsServer>): Promise<void> {
  server.closeAllConnections?.();
  await new Promise<void>((resolve) => server.close(() => resolve()));
}

interface CommandResult {
  readonly stdout: string;
  readonly stderr: string;
}

async function runCli(options: {
  readonly arguments_: readonly string[];
  readonly cli: string;
  readonly environment: NodeJS.ProcessEnv;
  readonly workspace: string;
}): Promise<CommandResult> {
  return new Promise<CommandResult>((resolve, reject) => {
    const child = spawn(process.execPath, [options.cli, ...options.arguments_], {
      cwd: options.workspace,
      env: options.environment,
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk));
    child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
    const timeout = setTimeout(() => child.kill("SIGKILL"), 30_000);
    child.once("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once("close", (code) => {
      clearTimeout(timeout);
      const result = {
        stdout: Buffer.concat(stdout).toString("utf8"),
        stderr: Buffer.concat(stderr).toString("utf8"),
      };
      if (code !== 0) {
        reject(new Error(
          `infrawright ${options.arguments_.join(" ")} exited ${String(code)}:\n`
            + `${result.stdout}${result.stderr}`,
        ));
      } else {
        resolve(result);
      }
    });
  });
}

test("built generic CLI completes a Python-disabled import-only workflow", {
  skip: process.platform === "win32" ? "Terraform execution is unsupported on Windows" : false,
  timeout: 120_000,
}, async () => {
  const built = spawnSync(process.execPath, ["scripts/build-metadata-cli.mjs"], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(built.status, 0, built.stderr);

  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-runtime-smoke-"));
  const runtime = path.join(workspace, "runtime");
  const packs = path.join(runtime, "packs");
  const profile = path.join(runtime, "packsets", "zia.json");
  const catalog = path.join(runtime, "packsets", "full.json");
  const cli = path.join(runtime, "dist", "infrawright-cli.mjs");
  const deployment = path.join(workspace, "deployment.json");
  const modules = path.join(workspace, "modules");
  const pulls = path.join(workspace, "pulls", TENANT);
  const config = path.join(workspace, "config", TENANT, `${RESOURCE}.auto.tfvars.json`);
  const sourceImports = path.join(workspace, "imports", TENANT, `${RESOURCE}_imports.tf`);
  const envRoot = path.join(workspace, "envs", TENANT, RESOURCE);
  const stagedImports = path.join(envRoot, `${RESOURCE}_imports.tf`);
  const planPath = path.join(envRoot, "tfplan");
  const fingerprintPath = path.join(envRoot, "tfplan.sources");
  const reportPath = path.join(workspace, "assessment.json");
  const certificatePath = path.join(workspace, "server.pem");
  const keyPath = path.join(workspace, "server.key");
  const terraformLog = path.join(workspace, "terraform.log");
  const pythonLog = path.join(workspace, "python.log");
  const planJsonPath = path.join(workspace, "plan.json");
  const fakeTerraform = path.join(workspace, "terraform-fake");
  const intercept = path.join(workspace, "intercept");
  let server: ReturnType<typeof createHttpsServer> | undefined;

  try {
    await Promise.all([
      mkdir(path.join(runtime, "dist"), { recursive: true }),
      mkdir(path.join(runtime, "packsets"), { recursive: true }),
      mkdir(path.join(packs, "_shared"), { recursive: true }),
      mkdir(intercept, { recursive: true }),
    ]);
    await Promise.all([
      cp(path.join(ROOT, "package.json"), path.join(runtime, "package.json")),
      cp(path.join(ROOT, "dist", "infrawright-cli.mjs"), cli),
      cp(path.join(ROOT, "dist", "infrawright-cli.mjs.sha256"), `${cli}.sha256`),
      cp(path.join(ROOT, "packs", "zia"), path.join(packs, "zia"), { recursive: true }),
      cp(
        path.join(ROOT, "packs", "_shared", "zscaler"),
        path.join(packs, "_shared", "zscaler"),
        { recursive: true },
      ),
      cp(path.join(ROOT, "packsets", "zia.json"), profile),
      cp(path.join(ROOT, "packsets", "full.json"), catalog),
    ]);
    await chmod(cli, 0o755);
    assert.equal(await exists(path.join(runtime, "node_modules")), false);

    await writeFile(deployment, `${JSON.stringify({
      overlay: workspace,
      module_dir: modules,
      roots: {},
    }, null, 2)}\n`);
    const fixtureText = await readFile(
      path.join(ROOT, "tests", "fixtures", "transform", RESOURCE, "api.json"),
      "utf8",
    );
    const fixture = JSON.parse(fixtureText) as unknown;
    const plan = {
      format_version: "1.2",
      terraform_version: "1.15.4",
      complete: true,
      errored: false,
      resource_changes: [{
        address: `module.${RESOURCE}.${RESOURCE}.this["example_custom_category"]`,
        type: RESOURCE,
        change: { actions: ["create"], importing: { id: "CUSTOM_01" } },
      }],
      output_changes: {},
    };
    await writeFile(planJsonPath, `${JSON.stringify(plan)}\n`);

    const fakeSource = [
      "#!/bin/sh",
      `{ printf '%s' "$PWD"; for arg in "$@"; do printf '\\t%s' "$arg"; done; printf '\\n'; } >> ${shellLiteral(terraformLog)}`,
      "if [ \"$1\" = fmt ] && [ \"$2\" = - ] && [ \"$#\" -eq 2 ]; then /bin/cat; exit 0; fi",
      "if [ \"$1\" = init ] && [ \"$2\" = -input=false ] && [ \"$#\" -eq 2 ]; then exit 0; fi",
      "if [ \"$1\" = plan ]; then",
      "  found=0",
      "  for arg in \"$@\"; do [ \"$arg\" = -out=tfplan ] && found=1; done",
      "  [ \"$found\" -eq 1 ] || exit 92",
      "  printf '%s\\n' 'opaque saved plan fixture' > \"$PWD/tfplan\"",
      "  exit 0",
      "fi",
      `case "$1" in -chdir=*) [ "$2" = show ] && [ "$3" = -json ] && [ "$#" -eq 4 ] && /bin/cat ${shellLiteral(planJsonPath)} && exit 0 ;; esac`,
      "if [ \"$1\" = apply ]; then",
      "  [ \"$#\" -eq 3 ] && [ \"$2\" = -input=false ] && [ \"$3\" = tfplan ] && exit 0",
      "  exit 93",
      "fi",
      "printf '%s\\n' \"unexpected fake Terraform invocation: $*\" >&2",
      "exit 91",
      "",
    ].join("\n");
    await writeFile(fakeTerraform, fakeSource, "utf8");
    await chmod(fakeTerraform, 0o755);
    const pythonShim = [
      "#!/bin/sh",
      `printf '%s\\n' "$0 $*" >> ${shellLiteral(pythonLog)}`,
      "exit 97",
      "",
    ].join("\n");
    for (const name of ["python", "python3", "python-must-not-run"]) {
      const destination = path.join(intercept, name);
      await writeFile(destination, pythonShim, "utf8");
      await chmod(destination, 0o755);
    }

    const generated = spawnSync("openssl", [
      "req",
      "-x509",
      "-newkey",
      "rsa:2048",
      "-sha256",
      "-nodes",
      "-keyout",
      keyPath,
      "-out",
      certificatePath,
      "-days",
      "1",
      "-subj",
      "/CN=127.0.0.1",
      "-addext",
      "subjectAltName=IP:127.0.0.1",
    ], { encoding: "utf8" });
    assert.equal(generated.status, 0, generated.stderr);
    const requests: string[] = [];
    const serverFailures: string[] = [];
    server = createHttpsServer({
      cert: await readFile(certificatePath),
      key: await readFile(keyPath),
    }, (request, response) => {
      const requestUrl = new URL(request.url ?? "/", "https://127.0.0.1");
      requests.push(`${request.method ?? ""} ${requestUrl.pathname}${requestUrl.search}`);
      try {
        if (request.method === "POST" && requestUrl.pathname === "/api/v1/authenticatedSession") {
          response.setHeader("Set-Cookie", "JSESSIONID=fixture; Path=/; Secure; HttpOnly");
          response.setHeader("Content-Type", "application/json");
          response.end("{}\n");
          return;
        }
        if (request.method === "GET" && requestUrl.pathname === "/api/v1/urlCategories") {
          assert.match(request.headers.cookie ?? "", /JSESSIONID=fixture/u);
          assert.equal(requestUrl.searchParams.get("customOnly"), "true");
          assert.equal(requestUrl.searchParams.get("page"), "1");
          assert.equal(requestUrl.searchParams.get("pageSize"), "1000");
          response.setHeader("Content-Type", "application/json");
          response.end(`${JSON.stringify(fixture)}\n`);
          return;
        }
        response.statusCode = 404;
        response.end("not found\n");
      } catch (error: unknown) {
        serverFailures.push(error instanceof Error ? error.message : String(error));
        response.statusCode = 500;
        response.end("fixture assertion failed\n");
      }
    });
    const port = await listen(server);

    const environment: NodeJS.ProcessEnv = {
      ...process.env,
      BUILD_SOURCEBRANCH: "refs/heads/main",
      HTTP_PROXY: "",
      HTTPS_PROXY: "",
      NO_PROXY: "",
      NODE_PATH: "",
      PATH: intercept,
      PYTHON: path.join(intercept, "python-must-not-run"),
      PYTHONPATH: "",
      REQUESTS_CA_BUNDLE: certificatePath,
      SSL_CERT_FILE: "",
      ZIA_API_KEY: "abcdefghijklmnop",
      ZIA_LEGACY_BASE_URL: `https://127.0.0.1:${port}`,
      ZIA_PASSWORD: "fixture-password",
      ZIA_USERNAME: "fixture-user",
      ZSCALER_USE_LEGACY_CLIENT: "1",
      http_proxy: "",
      https_proxy: "",
      no_proxy: "",
    };
    const metadata = ["--root", packs, "--profile", profile, "--catalog", catalog];
    const deploymentArguments = ["--deployment", deployment];
    const execute = (arguments_: readonly string[]): Promise<CommandResult> => runCli({
      arguments_, cli, environment, workspace,
    });

    await execute(["check-pack", "--pack", "zia", "--root", packs]);
    await execute(["check-pack-set", ...metadata]);
    assert.equal(
      (await execute(["deployment", ...deploymentArguments, "module-dir"])).stdout,
      `${modules}\n`,
    );
    await execute([
      "modules", "generate", "--resource", RESOURCE, "--out", modules,
      "--terraform", fakeTerraform, ...deploymentArguments, ...metadata,
    ]);
    await execute([
      "modules", "validate", "--resource", RESOURCE, "--out", modules,
      ...deploymentArguments, ...metadata,
    ]);
    await execute([
      "fetch", "--tenant", TENANT, "--resource", RESOURCE, "--out", pulls,
      ...metadata,
    ]);
    assert.deepEqual(await readdir(pulls), [`${RESOURCE}.json`]);
    assert.deepEqual(JSON.parse(await readFile(path.join(pulls, `${RESOURCE}.json`), "utf8")), fixture);
    assert.deepEqual(requests.map((item) => item.split(" ", 1)[0]), ["POST", "GET"]);
    assert.deepEqual(serverFailures, []);

    await execute([
      "transform", "--in", pulls, "--tenant", TENANT, "--resource", RESOURCE,
      ...deploymentArguments, ...metadata,
    ]);
    assert.equal(
      await readFile(config, "utf8"),
      await readFile(
        path.join(ROOT, "tests", "fixtures", "transform", RESOURCE, "expected.auto.tfvars.json"),
        "utf8",
      ),
    );
    assert.equal(
      await readFile(sourceImports, "utf8"),
      await readFile(
        path.join(ROOT, "tests", "fixtures", "transform", RESOURCE, "expected_imports.tf"),
        "utf8",
      ),
    );
    assert.equal(
      await exists(path.join(workspace, "config", TENANT, `${RESOURCE}.lookup.json`)),
      true,
    );
    await execute([
      "gen-env", "--tenant", TENANT, "--resource", RESOURCE,
      "--terraform", fakeTerraform, ...deploymentArguments, ...metadata,
    ]);
    assert.equal(await exists(path.join(envRoot, "main.tf")), true);
    await execute([
      "stage-imports", "--tenant", TENANT, "--resource", RESOURCE,
      ...deploymentArguments, ...metadata,
    ]);
    assert.equal(await readFile(stagedImports, "utf8"), await readFile(sourceImports, "utf8"));
    await execute([
      "plan", "--tenant", TENANT, "--resource", RESOURCE, "--save",
      "--terraform", fakeTerraform, ...deploymentArguments, ...metadata,
    ]);
    const planBytes = await readFile(planPath);
    const fingerprintBytes = await readFile(fingerprintPath);
    const fingerprint = JSON.parse(fingerprintBytes.toString("utf8")) as unknown;
    const planSha256 = createHash("sha256").update(planBytes).digest("hex");
    await execute([
      "assert-adoptable", "--tenant", TENANT, "--resource", RESOURCE,
      "--report", reportPath, "--terraform", fakeTerraform,
      ...deploymentArguments, ...metadata,
    ]);
    const report = JSON.parse(await readFile(reportPath, "utf8")) as {
      readonly summary: unknown;
      readonly roots: readonly {
        readonly status: string;
        readonly findings: readonly unknown[];
        readonly plan: { readonly sha256: string };
        readonly plan_fingerprint: unknown;
      }[];
    };
    assert.deepEqual(report.summary, {
      status: "clean", checked: 1, clean: 1, tolerated: 0, blocked: 0,
    });
    assert.equal(report.roots[0]?.status, "clean");
    assert.deepEqual(report.roots[0]?.findings, []);
    assert.equal(report.roots[0]?.plan.sha256, planSha256);
    assert.deepEqual(report.roots[0]?.plan_fingerprint, fingerprint);
    assert.deepEqual(await readFile(planPath), planBytes);
    assert.deepEqual(await readFile(fingerprintPath), fingerprintBytes);

    const retainedBeforeApply = new Map<string, Buffer>(await Promise.all([
      config,
      sourceImports,
      stagedImports,
      path.join(envRoot, "main.tf"),
    ].map(async (file) => [file, await readFile(file)] as const)));
    await execute([
      "apply", "--tenant", TENANT, "--resource", RESOURCE,
      "--terraform", fakeTerraform, ...deploymentArguments, ...metadata,
    ]);
    assert.equal(await exists(planPath), false);
    assert.equal(await exists(fingerprintPath), false);
    for (const [file, bytes] of retainedBeforeApply) {
      assert.deepEqual(await readFile(file), bytes, file);
    }
    await execute([
      "unstage-imports", "--tenant", TENANT, "--resource", RESOURCE,
      ...deploymentArguments, ...metadata,
    ]);
    assert.equal(await exists(stagedImports), false);
    assert.deepEqual(await readFile(sourceImports), retainedBeforeApply.get(sourceImports));

    const calls = (await readFile(terraformLog, "utf8")).trim().split("\n").map((line) => {
      const [cwd, ...argv] = line.split("\t");
      return { cwd, argv };
    });
    assert.equal(calls.filter((call) => call.argv[0] === "plan").length, 1, "Apply replanned");
    assert.equal(calls.filter((call) => call.argv[0] === "init").length, 2);
    assert.equal(calls.filter((call) => call.argv[1] === "show").length, 2);
    const applyCalls = calls.filter((call) => call.argv[0] === "apply");
    assert.equal(applyCalls.length, 1);
    assert.deepEqual(applyCalls[0]?.argv, ["apply", "-input=false", "tfplan"]);
    assert.equal(applyCalls[0]?.cwd, await realpath(envRoot));
    assert.equal(await exists(pythonLog), false);
  } finally {
    if (server !== undefined) await close(server);
    await rm(workspace, { recursive: true, force: true });
  }
});
