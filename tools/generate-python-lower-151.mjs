#!/usr/bin/env node

import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const REPOSITORY = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const OUTPUT = path.join(
  REPOSITORY,
  "node-src/generated/python-lower-15.1.ts",
);

const SOURCES = Object.freeze({
  "15.1.0": Object.freeze({
    "DerivedCoreProperties.txt": Object.freeze({
      sha256: "f55d0db69123431a7317868725b1fcbf1eab6b265d756d1bd7f0f6d9f9ee108b",
      url: "https://www.unicode.org/Public/15.1.0/ucd/DerivedCoreProperties.txt",
    }),
    "SpecialCasing.txt": Object.freeze({
      sha256: "55a477efd933a52cd27e6a9bf70265bb2d8814af31aab07767abc8eb421f27ef",
      url: "https://www.unicode.org/Public/15.1.0/ucd/SpecialCasing.txt",
    }),
    "UnicodeData.txt": Object.freeze({
      sha256: "2fc713e6a31a87c4850a37fe2caffa4218180fadb5de86b43a143ddb4581fb86",
      url: "https://www.unicode.org/Public/15.1.0/ucd/UnicodeData.txt",
    }),
  }),
  "16.0.0": Object.freeze({
    "DerivedCoreProperties.txt": Object.freeze({
      sha256: "39d35161f2954497f69e08bdb9e701493f476a3d30222de20028feda36c1dabd",
      url: "https://www.unicode.org/Public/16.0.0/ucd/DerivedCoreProperties.txt",
    }),
    "SpecialCasing.txt": Object.freeze({
      sha256: "8d5de354eef79f2395a54c9c7dcebbaf3d30fc962d0f85611ea97aa973a0c451",
      url: "https://www.unicode.org/Public/16.0.0/ucd/SpecialCasing.txt",
    }),
    "UnicodeData.txt": Object.freeze({
      sha256: "ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f",
      url: "https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt",
    }),
  }),
});

function usage(stream) {
  stream.write(
    "usage: node tools/generate-python-lower-151.mjs "
      + "--ucd-root <directory> (--check | --write)\n\n"
      + "The directory must contain 15.1.0/ and 16.0.0/ children with the "
      + "three pinned UCD files. This tool performs no downloads.\n",
  );
}

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exitCode = 2;
  return null;
}

function parseArgs(argv) {
  if (argv.includes("--help")) {
    usage(process.stdout);
    return { help: true };
  }
  let ucdRoot = null;
  let mode = null;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--ucd-root") {
      const value = argv[index + 1];
      if (value === undefined || value.startsWith("--")) {
        return fail("--ucd-root requires a directory");
      }
      ucdRoot = value;
      index += 1;
      continue;
    }
    if (argument === "--check" || argument === "--write") {
      if (mode !== null) {
        return fail("choose exactly one of --check or --write");
      }
      mode = argument.slice(2);
      continue;
    }
    return fail(`unknown argument ${JSON.stringify(argument)}`);
  }
  if (ucdRoot === null || mode === null) {
    usage(process.stderr);
    return fail("--ucd-root and exactly one mode are required");
  }
  return { help: false, mode, ucdRoot: path.resolve(ucdRoot) };
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

function loadSources(root) {
  const loaded = Object.create(null);
  for (const [version, files] of Object.entries(SOURCES)) {
    const versionFiles = Object.create(null);
    for (const [name, evidence] of Object.entries(files)) {
      const sourcePath = path.join(root, version, name);
      let bytes;
      try {
        bytes = readFileSync(sourcePath);
      } catch (error) {
        throw new Error(`cannot read ${sourcePath}: ${String(error)}`);
      }
      const actual = sha256(bytes);
      if (actual !== evidence.sha256) {
        throw new Error(
          `${sourcePath} has SHA-256 ${actual}; expected ${evidence.sha256}`,
        );
      }
      versionFiles[name] = bytes.toString("utf8");
    }
    loaded[version] = versionFiles;
  }
  return loaded;
}

function parseLowerMappings(unicodeData, specialCasing) {
  const mappings = new Map();
  for (const line of unicodeData.split("\n")) {
    if (line === "") {
      continue;
    }
    const fields = line.split(";");
    const source = fields[0];
    const lower = fields[13];
    if (source === undefined || lower === undefined) {
      throw new Error("malformed UnicodeData.txt row");
    }
    if (lower !== "") {
      mappings.set(
        Number.parseInt(source, 16),
        lower.split(" ").map((value) => Number.parseInt(value, 16)),
      );
    }
  }
  for (const rawLine of specialCasing.split("\n")) {
    const line = rawLine.split("#", 1)[0]?.trim() ?? "";
    if (line === "") {
      continue;
    }
    const fields = line.split(";").map((value) => value.trim());
    const source = fields[0];
    const lower = fields[1];
    const condition = fields[4];
    if (source === undefined || lower === undefined || condition === undefined) {
      throw new Error("malformed SpecialCasing.txt row");
    }
    if (condition === "") {
      mappings.set(
        Number.parseInt(source, 16),
        lower === ""
          ? []
          : lower.split(" ").map((value) => Number.parseInt(value, 16)),
      );
    }
  }
  return mappings;
}

function parseDerivedProperties(source) {
  const properties = new Map([
    ["Cased", new Set()],
    ["Case_Ignorable", new Set()],
  ]);
  for (const rawLine of source.split("\n")) {
    const line = rawLine.split("#", 1)[0]?.trim() ?? "";
    if (line === "") {
      continue;
    }
    const fields = line.split(";").map((value) => value.trim());
    const property = fields[1];
    if (property === undefined || !properties.has(property)) {
      continue;
    }
    const bounds = (fields[0] ?? "").split("..");
    const first = Number.parseInt(bounds[0] ?? "", 16);
    const last = Number.parseInt(bounds[1] ?? bounds[0] ?? "", 16);
    if (!Number.isInteger(first) || !Number.isInteger(last) || last < first) {
      throw new Error("malformed DerivedCoreProperties.txt range");
    }
    const values = properties.get(property);
    for (let codePoint = first; codePoint <= last; codePoint += 1) {
      values.add(codePoint);
    }
  }
  return properties;
}

function sameMapping(left, right) {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

function difference(left, right) {
  return new Set([...left].filter((value) => !right.has(value)));
}

function assertSet(label, values, count) {
  if (values.size !== count) {
    throw new Error(`${label} count is ${values.size}; expected ${count}`);
  }
}

function compactRanges(values) {
  const ordered = [...values].sort((left, right) => left - right);
  const ranges = [];
  for (const value of ordered) {
    const last = ranges.at(-1);
    if (last === undefined || value > last[1] + 1) {
      ranges.push([value, value]);
    } else {
      last[1] = value;
    }
  }
  return ranges;
}

function hex(value) {
  return `0x${value.toString(16)}`;
}

function renderRanges(ranges) {
  return ranges.map(([first, last]) => `  ${hex(first)}, ${hex(last)},`).join("\n");
}

function renderGenerated(sources) {
  const lower15 = parseLowerMappings(
    sources["15.1.0"]["UnicodeData.txt"],
    sources["15.1.0"]["SpecialCasing.txt"],
  );
  const lower16 = parseLowerMappings(
    sources["16.0.0"]["UnicodeData.txt"],
    sources["16.0.0"]["SpecialCasing.txt"],
  );
  const lower16Only = difference(new Set(lower16.keys()), new Set(lower15.keys()));
  const lower15Only = difference(new Set(lower15.keys()), new Set(lower16.keys()));
  const changedLower = new Set(
    [...lower15.keys()].filter((codePoint) => {
      const next = lower16.get(codePoint);
      return next !== undefined && !sameMapping(lower15.get(codePoint), next);
    }),
  );
  assertSet("Unicode 16-only lowercase sources", lower16Only, 27);
  assertSet("Unicode 15.1-only lowercase sources", lower15Only, 0);
  assertSet("changed lowercase mappings", changedLower, 0);

  const properties15 = parseDerivedProperties(
    sources["15.1.0"]["DerivedCoreProperties.txt"],
  );
  const properties16 = parseDerivedProperties(
    sources["16.0.0"]["DerivedCoreProperties.txt"],
  );
  const cased16Only = difference(
    properties16.get("Cased"),
    properties15.get("Cased"),
  );
  const cased15Only = difference(
    properties15.get("Cased"),
    properties16.get("Cased"),
  );
  const ignorable16Only = difference(
    properties16.get("Case_Ignorable"),
    properties15.get("Case_Ignorable"),
  );
  const ignorable15Only = difference(
    properties15.get("Case_Ignorable"),
    properties16.get("Case_Ignorable"),
  );
  assertSet("Unicode 16-only Cased points", cased16Only, 52);
  assertSet("Unicode 15.1-only Cased points", cased15Only, 0);
  assertSet("Unicode 16-only Case_Ignorable points", ignorable16Only, 43);
  assertSet("Unicode 15.1-only Case_Ignorable points", ignorable15Only, 1);
  if (!ignorable15Only.has(0x1171e)) {
    throw new Error("the Unicode 15.1-only Case_Ignorable point is not U+1171E");
  }

  const evidence = JSON.stringify(SOURCES, null, 2);
  return `// Generated by tools/generate-python-lower-151.mjs. DO NOT EDIT.\n`
    + `// Inputs are pinned official Unicode files; see docs/python-lower-unicode-contract.md.\n\n`
    + `export const PYTHON_LOWER_151_UCD_SOURCES = ${evidence} as const;\n\n`
    + `/** Unicode 16 lowercase sources that Python's Unicode 15.1 leaves unchanged. */\n`
    + `export const UCD16_ONLY_LOWERCASE_SOURCE_RANGES = [\n`
    + `${renderRanges(compactRanges(lower16Only))}\n] as const;\n\n`
    + `/** Unicode 16 Cased points absent from Unicode 15.1. */\n`
    + `export const UCD16_ONLY_CASED_RANGES = [\n`
    + `${renderRanges(compactRanges(cased16Only))}\n] as const;\n\n`
    + `/** Unicode 16 Case_Ignorable points absent from Unicode 15.1. */\n`
    + `export const UCD16_ONLY_CASE_IGNORABLE_RANGES = [\n`
    + `${renderRanges(compactRanges(ignorable16Only))}\n] as const;\n\n`
    + `/** Unicode 15.1 Case_Ignorable points absent from Unicode 16. */\n`
    + `export const UCD151_ONLY_CASE_IGNORABLE_RANGES = [\n`
    + `${renderRanges(compactRanges(ignorable15Only))}\n] as const;\n`;
}

const args = parseArgs(process.argv.slice(2));
if (args !== null && !args.help) {
  try {
    const generated = renderGenerated(loadSources(args.ucdRoot));
    if (args.mode === "write") {
      mkdirSync(path.dirname(OUTPUT), { recursive: true });
      writeFileSync(OUTPUT, generated, "utf8");
      process.stdout.write(`${OUTPUT}\n`);
    } else {
      const current = readFileSync(OUTPUT, "utf8");
      if (current !== generated) {
        throw new Error(`${OUTPUT} is stale; rerun with --write`);
      }
      process.stdout.write(`${OUTPUT} is current\n`);
    }
  } catch (error) {
    process.stderr.write(`error: ${String(error)}\n`);
    process.exitCode = 1;
  }
}
