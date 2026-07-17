#!/usr/bin/env node

import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

const PROVENANCE_POINTER =
  "See docs/python-oracle-contracts.md for the exact clean-checkout resurrection command.";
const ENVIRONMENT_GENERATOR_COMMIT = "c86ac17eb312da9bfdb89d2e2fc132daa0e501b7";
const ENVIRONMENT_GENERATOR_SHA256 =
  "dc3e9b139894df18da93a985955d8c469a0e0a83230222c41f85ff956fb0e5bb";

const PROFILES = Object.freeze({
  "engine-ops": Object.freeze([
    ["python-assessment-cli-v1.json", "clean_checkout_resurrection", "generate-engine-ops-authority.py"],
    ["python-differential-v1.json", "clean_checkout_resurrection", "generate-engine-ops-authority.py"],
    ["python-plan-cli-v1.json", "clean_checkout_resurrection", "generate-engine-ops-authority.py"],
  ]),
  environment: Object.freeze([
    ["python-environment-roots-v1.json", "producing_command", "generate-python-environment-roots-authority.py"],
  ]),
  "reconcile-openapi": Object.freeze([
    ["python-reconcile-schema-api-v1.json", "producing_command", "generate-reconcile-openapi-authority.py"],
    ["python-openapi-resource-map-v1.json", "producing_command", "generate-reconcile-openapi-authority.py"],
  ]),
  "source-operation": Object.freeze([
    ["python-source-operation-map-v1.json", "resurrection", "generate-source-operation-authority.py"],
    ["python-sdk-path-evidence-v1.json", "resurrection", "generate-source-operation-authority.py"],
  ]),
});

function replaceStringProperty(source, property, expectedGenerator) {
  const marker = `${JSON.stringify(property)}:`;
  const markerIndex = source.indexOf(marker);
  if (markerIndex < 0 || source.indexOf(marker, markerIndex + marker.length) >= 0) {
    throw new Error(`expected exactly one ${property} property`);
  }
  let start = markerIndex + marker.length;
  while (/\s/u.test(source[start] ?? "")) start += 1;
  if (source[start] !== '"') {
    throw new Error(`${property} must contain a JSON string`);
  }
  let end = start + 1;
  let escaped = false;
  for (; end < source.length; end += 1) {
    const character = source[end];
    if (escaped) {
      escaped = false;
    } else if (character === "\\") {
      escaped = true;
    } else if (character === '"') {
      break;
    }
  }
  if (end >= source.length) throw new Error(`unterminated ${property} string`);
  const previous = JSON.parse(source.slice(start, end + 1));
  if (typeof previous !== "string" || !previous.includes(expectedGenerator)) {
    throw new Error(`${property} does not reference ${expectedGenerator}`);
  }
  return `${source.slice(0, start)}${JSON.stringify(PROVENANCE_POINTER)}${source.slice(end + 1)}`;
}

function addEnvironmentGeneratorProvenance(source) {
  const commitProperty = `  "generator_commit": ${JSON.stringify(ENVIRONMENT_GENERATOR_COMMIT)},\n`;
  const hashProperty = `  "generator_sha256": ${JSON.stringify(ENVIRONMENT_GENERATOR_SHA256)},\n`;
  if (source.includes('"generator_commit"') || source.includes('"generator_sha256"')) {
    if (source.includes(commitProperty) && source.includes(hashProperty)) return source;
    throw new Error("environment generator provenance conflicts with the frozen authority");
  }
  const kind = '  "kind": "infrawright.python-environment-roots-authority",\n';
  const first = source.indexOf(kind);
  if (first < 0 || source.indexOf(kind, first + kind.length) >= 0) {
    throw new Error("expected exactly one environment authority kind property");
  }
  return source.replace(kind, `${commitProperty}${hashProperty}${kind}`);
}

async function normalizeProfile(profile, fixturesRoot) {
  const entries = PROFILES[profile];
  if (entries === undefined) {
    throw new Error(`unknown frozen-authority profile: ${profile}`);
  }
  const outputs = [];
  for (const [name, property, expectedGenerator] of entries) {
    const file = path.join(fixturesRoot, name);
    const source = await readFile(file, "utf8");
    let normalized = replaceStringProperty(source, property, expectedGenerator);
    if (profile === "environment") {
      normalized = addEnvironmentGeneratorProvenance(normalized);
    }
    JSON.parse(normalized);
    outputs.push([file, normalized]);
  }
  for (const [file, normalized] of outputs) {
    await writeFile(file, normalized, "utf8");
  }
}

function parseArguments(argv) {
  if (argv.length !== 4 || argv[0] !== "--profile" || argv[2] !== "--fixtures-root") {
    throw new Error(
      "usage: node scripts/normalize-frozen-authority-provenance.mjs "
      + "--profile <engine-ops|environment|reconcile-openapi|source-operation> "
      + "--fixtures-root <path>",
    );
  }
  return { profile: argv[1], fixturesRoot: path.resolve(argv[3]) };
}

try {
  const { profile, fixturesRoot } = parseArguments(process.argv.slice(2));
  await normalizeProfile(profile, fixturesRoot);
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
