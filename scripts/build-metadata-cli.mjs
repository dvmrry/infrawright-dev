import { createHash } from "node:crypto";
import { chmod, mkdir, readFile, writeFile } from "node:fs/promises";

import { build } from "esbuild";

const output = "dist/infrawright-cli.mjs";
const checksum = `${output}.sha256`;

await mkdir("dist", { recursive: true });
await build({
  bundle: true,
  entryPoints: ["node-src/cli/main.ts"],
  format: "esm",
  outfile: output,
  platform: "node",
  target: "node24",
  banner: {
    js: "#!/usr/bin/env node\nimport { createRequire as __infrawrightCreateRequire } from 'node:module'; const require = __infrawrightCreateRequire(import.meta.url);",
  },
});
await chmod(output, 0o755);
const digest = createHash("sha256").update(await readFile(output)).digest("hex");
await writeFile(checksum, `${digest}  infrawright-cli.mjs\n`, "ascii");
