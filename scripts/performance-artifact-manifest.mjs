#!/usr/bin/env node

import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, mkdir, readdir, writeFile } from "node:fs/promises";
import path from "node:path";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(2);
}

function argumentsFrom(argv) {
  const roots = [];
  let output;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument !== "--root" && argument !== "--out") {
      fail(`unknown argument ${String(argument)}`);
    }
    const value = argv[index + 1];
    if (value === undefined || value === "") fail(`${argument} requires a value`);
    index += 1;
    if (argument === "--out") {
      if (output !== undefined) fail("--out may be specified only once");
      output = path.resolve(value);
      continue;
    }
    const separator = value.indexOf("=");
    if (separator <= 0 || separator === value.length - 1) {
      fail("--root must use label=/absolute/or/relative/path");
    }
    const label = value.slice(0, separator);
    if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/u.test(label)) {
      fail("manifest root labels may contain letters, digits, dot, underscore, and hyphen");
    }
    if (roots.some((root) => root.label === label)) fail(`duplicate root label ${label}`);
    roots.push({ label, path: path.resolve(value.slice(separator + 1)) });
  }
  if (roots.length === 0) fail("at least one --root is required");
  if (output === undefined) fail("--out is required");
  return { output, roots };
}

function within(parent, child) {
  const relative = path.relative(parent, child);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

async function sha256(file) {
  const digest = createHash("sha256");
  for await (const chunk of createReadStream(file)) digest.update(chunk);
  return digest.digest("hex");
}

async function snapshot(root) {
  const files = [];
  const visit = async (directory, prefix) => {
    const names = (await readdir(directory)).sort((left, right) => left < right ? -1 : left > right ? 1 : 0);
    for (const name of names) {
      const absolute = path.join(directory, name);
      const relative = prefix === "" ? name : `${prefix}/${name}`;
      const metadata = await lstat(absolute);
      if (metadata.isSymbolicLink()) fail(`artifact root contains symbolic link ${root.label}/${relative}`);
      if (metadata.isDirectory()) {
        await visit(absolute, relative);
      } else if (metadata.isFile()) {
        files.push({
          path: relative,
          sha256: await sha256(absolute),
          size_bytes: metadata.size,
        });
      } else {
        fail(`artifact root contains unsupported entry ${root.label}/${relative}`);
      }
      if (files.length > 100_000) fail("artifact manifest exceeded 100000 files");
    }
  };
  await visit(root.path, "");
  return { files, label: root.label };
}

const options = argumentsFrom(process.argv.slice(2));
for (const root of options.roots) {
  if (within(root.path, options.output)) {
    fail("manifest output must be outside every hashed artifact root");
  }
}
const roots = [];
for (const root of [...options.roots].sort((left, right) => left.label < right.label ? -1 : 1)) {
  roots.push(await snapshot(root));
}
const tree = createHash("sha256");
for (const root of roots) {
  for (const file of root.files) {
    tree.update(root.label, "utf8");
    tree.update("\0", "utf8");
    tree.update(file.path, "utf8");
    tree.update("\0", "utf8");
    tree.update(String(file.size_bytes), "utf8");
    tree.update("\0", "utf8");
    tree.update(file.sha256, "ascii");
    tree.update("\0", "utf8");
  }
}
const manifest = {
  format: "infrawright-performance-artifact-manifest",
  roots,
  tree_sha256: tree.digest("hex"),
};
await mkdir(path.dirname(options.output), { recursive: true });
await writeFile(options.output, `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o600 });
