#!/usr/bin/env node

import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(2);
}

function argumentsFrom(argv) {
  const variants = [];
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] !== "--variant") fail(`unknown argument ${String(argv[index])}`);
    const value = argv[index + 1];
    if (value === undefined || value === "") fail("--variant requires label=run-directory");
    index += 1;
    const separator = value.indexOf("=");
    if (separator <= 0 || separator === value.length - 1) {
      fail("--variant requires label=run-directory");
    }
    const label = value.slice(0, separator);
    if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/u.test(label)) fail(`invalid variant label ${label}`);
    if (variants.some((variant) => variant.label === label)) fail(`duplicate variant ${label}`);
    variants.push({ label, path: path.resolve(value.slice(separator + 1)) });
  }
  if (variants.length === 0) fail("at least one --variant is required");
  return variants;
}

async function reports(directory) {
  const output = [];
  for (const name of (await readdir(directory)).sort()) {
    if (!name.endsWith(".performance.json")) continue;
    const value = JSON.parse(await readFile(path.join(directory, name), "utf8"));
    if (value?.format !== "infrawright-performance-report") {
      fail(`${name} is not an Infrawright performance report`);
    }
    output.push(value);
  }
  if (output.length === 0) fail(`no *.performance.json files in ${directory}`);
  return output;
}

async function manifest(directory) {
  try {
    const value = JSON.parse(await readFile(path.join(directory, "artifacts.sha256.json"), "utf8"));
    if (
      value?.format !== "infrawright-performance-artifact-manifest"
      || typeof value.tree_sha256 !== "string"
    ) {
      fail(`invalid artifacts.sha256.json in ${directory}`);
    }
    return value.tree_sha256;
  } catch (error) {
    if (error?.code === "ENOENT") return null;
    throw error;
  }
}

function sum(rows, select) {
  return rows.reduce((total, row) => total + select(row), 0);
}

function number(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function escapeCell(value) {
  return String(value).replaceAll("|", "\\|");
}

const variants = argumentsFrom(process.argv.slice(2));
const rows = [];
const baselineHash = await manifest(variants[0].path);
for (const variant of variants) {
  const values = await reports(variant.path);
  const hash = await manifest(variant.path);
  const fetch = values.find((report) => report.command === "fetch");
  const adopt = values.find((report) => report.command === "adopt");
  const http = values.flatMap((report) => Array.isArray(report.http) ? report.http : []);
  const summaries = values.map((report) => report.summary ?? {});
  rows.push({
    detail: sum(http, (row) => row.classification === "detail" ? number(row.request_count) : 0),
    fetch: number(fetch?.command_duration_ms),
    http: sum(summaries, (summary) => number(summary.http_requests)),
    label: variant.label,
    list: sum(http, (row) => row.classification === "list" ? number(row.request_count) : 0),
    oracle: number(adopt?.command_duration_ms),
    parity: hash === null || baselineHash === null
      ? "unknown"
      : hash === baselineHash ? (variant === variants[0] ? "baseline" : "yes") : "NO",
    terraform: sum(summaries, (summary) => number(summary.terraform_commands)),
    total: sum(values, (report) => number(report.command_duration_ms)),
  });
}

process.stdout.write("| Variant | Fetch time (ms) | Oracle time (ms) | Total time (ms) | HTTP requests | Detail GETs | List GETs | TF commands | Parity |\n");
process.stdout.write("|---|---:|---:|---:|---:|---:|---:|---:|---|\n");
for (const row of rows) {
  process.stdout.write(`| ${escapeCell(row.label)} | ${row.fetch.toFixed(3)} | ${row.oracle.toFixed(3)} | ${row.total.toFixed(3)} | ${row.http} | ${row.detail} | ${row.list} | ${row.terraform} | ${row.parity} |\n`);
}
