import { createHash } from "node:crypto";
import { chmod, mkdir, rename, unlink, writeFile } from "node:fs/promises";

import { build } from "esbuild";

const CHILD_OUT = "dist/infrawright-zcc-collector-child.mjs";
const PARENT_OUT = "dist/infrawright.mjs";

function inputNames(result) {
  return Object.keys(result.metafile?.inputs ?? {}).map((name) => {
    return name.replaceAll("\\", "/");
  });
}

function rejectInputs(label, names, forbidden) {
  const violations = names.filter((name) => {
    return forbidden.some((fragment) => name.includes(fragment));
  });
  if (violations.length > 0) {
    throw new Error(`${label} bundle crossed its boundary: ${violations.join(", ")}`);
  }
}

await mkdir("dist", { recursive: true });

const childBuild = await build({
  bundle: true,
  entryPoints: ["node-src/process/zcc-collector-child.ts"],
  format: "esm",
  outfile: CHILD_OUT,
  platform: "node",
  target: "node24",
  metafile: true,
  write: false,
  banner: {
    js: "import { createRequire as __infrawrightCreateRequire } from 'node:module'; const require = __infrawrightCreateRequire(import.meta.url);",
  },
});
const child = childBuild.outputFiles?.find((file) => file.path.endsWith(
  "infrawright-zcc-collector-child.mjs",
));
if (child === undefined) throw new Error("child bundle output is missing");
const childNames = inputNames(childBuild);
rejectInputs("child", childNames, [
  "node-src/process/main.ts",
  "node-src/process/execute.ts",
  "node-src/contracts/validators.ts",
  "node-src/io/publisher-guard.ts",
  "node-src/io/zcc-pull-publisher.ts",
  "node-src/domain/zcc-pull-operation.ts",
  "node-src/domain/zcc-pull-collection.ts",
  "node-src/domain/zcc-adoption",
  "node-src/io/terraform",
  "node_modules/ajv/",
]);
const workerThreadImporters = Object.entries(childBuild.metafile?.inputs ?? {})
  .filter(([, input]) => {
    return (input.imports ?? []).some((entry) => entry.path === "node:worker_threads");
  })
  .map(([name]) => name.replaceAll("\\", "/"))
  .sort();
const expectedWorkerThreadImporters = [
  "node_modules/undici/lib/util/runtime-features.js",
  "node_modules/undici/lib/web/webidl/index.js",
];
if (
  workerThreadImporters.length !== expectedWorkerThreadImporters.length
  || workerThreadImporters.some((name, index) => {
    return name !== expectedWorkerThreadImporters[index];
  })
) {
  throw new Error(
    `child worker_threads importer set changed: ${workerThreadImporters.join(", ")}`,
  );
}
const childExternalImports = Object.values(childBuild.metafile?.outputs ?? {})
  .flatMap((output) => output.imports ?? [])
  .filter((entry) => entry.external)
  .map((entry) => entry.path);
for (const forbidden of ["node:child_process", "node:cluster"]) {
  if (childExternalImports.includes(forbidden)) {
    throw new Error(`child bundle imports forbidden runtime ${forbidden}`);
  }
}
const childText = new TextDecoder().decode(child.contents);
if (/\bnew\s+Worker\s*\(/.test(childText)) {
  throw new Error("child bundle constructs a worker thread");
}
const childSha256 = createHash("sha256").update(child.contents).digest("hex");
const childSize = child.contents.byteLength;

const parentBuild = await build({
  bundle: true,
  entryPoints: ["node-src/process/main.ts"],
  format: "esm",
  outfile: PARENT_OUT,
  platform: "node",
  target: "node24",
  metafile: true,
  write: false,
  banner: { js: "#!/usr/bin/env node" },
  define: {
    __INFRAWRIGHT_ZCC_CHILD_SHA256__: JSON.stringify(childSha256),
    __INFRAWRIGHT_ZCC_CHILD_SIZE__: JSON.stringify(childSize),
  },
});
const parent = parentBuild.outputFiles?.find((file) => file.path.endsWith(
  "infrawright.mjs",
));
if (parent === undefined) throw new Error("parent bundle output is missing");
rejectInputs("parent", inputNames(parentBuild), [
  "node-src/process/zcc-collector-child.ts",
  "node-src/io/zcc-oneapi-host.ts",
  "node-src/io/zcc-oneapi-transport.ts",
  "node-src/domain/zcc-oneapi-auth.ts",
  "node-src/domain/zcc-collector.ts",
  "node-src/domain/zcc-collector-catalog.ts",
  "node_modules/undici/",
]);

const childTemp = `${CHILD_OUT}.tmp-${process.pid}`;
const parentTemp = `${PARENT_OUT}.tmp-${process.pid}`;
try {
  await writeFile(childTemp, child.contents, { mode: 0o755 });
  await rename(childTemp, CHILD_OUT);
  await chmod(CHILD_OUT, 0o755);
  // Parent is the distribution commit point: it is published only after the
  // exact sibling bytes it embeds are visible.
  await writeFile(parentTemp, parent.contents, { mode: 0o755 });
  await rename(parentTemp, PARENT_OUT);
  await chmod(PARENT_OUT, 0o755);
} finally {
  await Promise.all([
    unlink(childTemp).catch(() => undefined),
    unlink(parentTemp).catch(() => undefined),
  ]);
}
