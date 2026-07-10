import { chmod, mkdir } from "node:fs/promises";

import { build } from "esbuild";

await mkdir("dist", { recursive: true });
await build({
  bundle: true,
  entryPoints: ["node-src/process/main.ts"],
  format: "esm",
  outfile: "dist/infrawright.mjs",
  platform: "node",
  target: "node24",
  banner: {
    js: "#!/usr/bin/env node",
  },
});
await chmod("dist/infrawright.mjs", 0o755);
