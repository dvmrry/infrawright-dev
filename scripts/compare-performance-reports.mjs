#!/usr/bin/env node

import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

import { validateArtifactManifest } from "./performance-manifest.mjs";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(2);
}

function record(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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

function nonNegativeNumber(value, label) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    throw new Error(`${label} must be a finite non-negative number`);
  }
  return value;
}

function nonNegativeInteger(value, label) {
  nonNegativeNumber(value, label);
  if (!Number.isSafeInteger(value)) throw new Error(`${label} must be a safe integer`);
  return value;
}

function roundedDuration(value) {
  return Math.round(value * 1_000) / 1_000;
}

function optionalCount(value, label) {
  return value === undefined ? 0 : nonNegativeInteger(value, label);
}

function validatePerformanceReport(value, name) {
  if (
    !record(value)
    || value.format !== "infrawright-performance-report"
    || typeof value.command !== "string"
    || !/^[a-z][a-z0-9.-]*$/u.test(value.command)
    || value.command_status !== "success"
    || !Array.isArray(value.spans)
    || !Array.isArray(value.http)
    || !record(value.summary)
  ) {
    throw new Error(`${name} is not a successful Infrawright performance report`);
  }
  nonNegativeNumber(value.command_duration_ms, `${name} command duration`);
  if (
    value.selected_concurrency !== null
    && (!Number.isSafeInteger(value.selected_concurrency) || value.selected_concurrency <= 0)
  ) {
    throw new Error(`${name} selected concurrency is invalid`);
  }

  let pages = 0;
  let logicalRequests = 0;
  let terraformCommands = 0;
  for (const [index, span] of value.spans.entries()) {
    if (
      !record(span)
      || typeof span.phase !== "string"
      || span.phase === ""
      || !["failed", "skipped", "success"].includes(span.status)
    ) {
      throw new Error(`${name} span ${index} is invalid`);
    }
    nonNegativeNumber(span.duration_ms, `${name} span duration`);
    pages += optionalCount(span.pages, `${name} span pages`);
    logicalRequests += optionalCount(span.logical_requests, `${name} span logical requests`);
    terraformCommands += optionalCount(
      span.terraform_commands,
      `${name} span Terraform commands`,
    );
  }

  let httpRequests = 0;
  let rateLimitedRequests = 0;
  let retries = 0;
  let retryDelayMs = 0;
  for (const [index, row] of value.http.entries()) {
    if (
      !record(row)
      || !["action", "authentication", "detail", "list"].includes(row.classification)
      || typeof row.endpoint_family !== "string"
      || row.endpoint_family === ""
      || typeof row.phase !== "string"
      || row.phase === ""
      || row.status !== null && (
        !Number.isSafeInteger(row.status) || row.status < 100 || row.status > 599
      )
    ) {
      throw new Error(`${name} HTTP row ${index} is invalid`);
    }
    const requests = nonNegativeInteger(row.request_count, `${name} HTTP request count`);
    httpRequests += requests;
    if (row.status === 429) rateLimitedRequests += requests;
    retries += nonNegativeInteger(row.retries, `${name} HTTP retry count`);
    retryDelayMs += nonNegativeNumber(row.retry_delay_ms, `${name} HTTP retry delay`);
    nonNegativeNumber(row.duration_ms_p50, `${name} HTTP p50`);
    nonNegativeNumber(row.duration_ms_p95, `${name} HTTP p95`);
    nonNegativeNumber(row.duration_ms_total, `${name} HTTP duration`);
  }

  const expected = {
    http_requests: httpRequests,
    logical_requests: logicalRequests,
    pages,
    rate_limited_requests: rateLimitedRequests,
    retries,
    retry_delay_ms: roundedDuration(retryDelayMs),
    terraform_commands: terraformCommands,
  };
  for (const [field, count] of Object.entries(expected)) {
    const reported = field === "retry_delay_ms"
      ? nonNegativeNumber(value.summary[field], `${name} summary ${field}`)
      : nonNegativeInteger(value.summary[field], `${name} summary ${field}`);
    if (reported !== count) throw new Error(`${name} summary ${field} is inconsistent`);
  }
  return value;
}

async function reports(directory) {
  const output = new Map();
  for (const name of (await readdir(directory)).sort()) {
    if (!name.endsWith(".performance.json")) continue;
    let value;
    try {
      value = validatePerformanceReport(
        JSON.parse(await readFile(path.join(directory, name), "utf8")),
        name,
      );
    } catch (error) {
      fail(error instanceof Error ? error.message : `${name} is invalid`);
    }
    if (output.has(value.command)) fail(`duplicate ${value.command} performance report`);
    output.set(value.command, value);
  }
  if (output.size === 0) fail("variant has no *.performance.json files");
  if (!output.has("fetch")) fail("variant is missing its Fetch performance report");
  return output;
}

async function manifest(directory) {
  let value;
  try {
    value = JSON.parse(await readFile(path.join(directory, "artifacts.sha256.json"), "utf8"));
  } catch (error) {
    if (error?.code === "ENOENT") fail("variant is missing artifacts.sha256.json");
    fail("variant artifact manifest is not valid JSON");
  }
  try {
    return validateArtifactManifest(value);
  } catch (error) {
    fail(error instanceof Error ? error.message : "variant artifact manifest is invalid");
  }
}

function sum(values, select) {
  return values.reduce((total, value) => total + select(value), 0);
}

function escapeCell(value) {
  return String(value).replaceAll("|", "\\|");
}

const variants = argumentsFrom(process.argv.slice(2));
const evidence = [];
for (const variant of variants) {
  evidence.push({
    ...variant,
    manifest: await manifest(variant.path),
    reports: await reports(variant.path),
  });
}
const baseline = evidence[0];
const baselineCommands = [...baseline.reports.keys()].sort();
const baselineRoots = baseline.manifest.roots.map((root) => root.label);
for (const variant of evidence.slice(1)) {
  const commands = [...variant.reports.keys()].sort();
  if (JSON.stringify(commands) !== JSON.stringify(baselineCommands)) {
    fail(`variant ${variant.label} does not contain the baseline command set`);
  }
  const roots = variant.manifest.roots.map((root) => root.label);
  if (JSON.stringify(roots) !== JSON.stringify(baselineRoots)) {
    fail(`variant ${variant.label} does not cover the baseline artifact roots`);
  }
}

const rows = evidence.map((variant, index) => {
  const values = [...variant.reports.values()];
  const fetch = variant.reports.get("fetch");
  const adopt = variant.reports.get("adopt");
  const http = values.flatMap((report) => report.http);
  const summaries = values.map((report) => report.summary);
  return {
    detail: sum(http, (row) => row.classification === "detail" ? row.request_count : 0),
    fetch: fetch.command_duration_ms,
    http: sum(summaries, (summary) => summary.http_requests),
    label: variant.label,
    list: sum(http, (row) => row.classification === "list" ? row.request_count : 0),
    oracle: adopt?.command_duration_ms ?? 0,
    parity: index === 0
      ? "baseline"
      : variant.manifest.tree_sha256 === baseline.manifest.tree_sha256 ? "yes" : "NO",
    rateLimited: sum(summaries, (summary) => summary.rate_limited_requests),
    retries: sum(summaries, (summary) => summary.retries),
    retryDelayMs: sum(summaries, (summary) => summary.retry_delay_ms),
    terraform: sum(summaries, (summary) => summary.terraform_commands),
    total: sum(values, (report) => report.command_duration_ms),
  };
});

process.stdout.write("| Variant | Fetch time (ms) | Oracle time (ms) | Total time (ms) | HTTP requests | Detail GETs | List GETs | TF commands | Parity |\n");
process.stdout.write("|---|---:|---:|---:|---:|---:|---:|---:|---|\n");
for (const row of rows) {
  process.stdout.write(`| ${escapeCell(row.label)} | ${row.fetch.toFixed(3)} | ${row.oracle.toFixed(3)} | ${row.total.toFixed(3)} | ${row.http} | ${row.detail} | ${row.list} | ${row.terraform} | ${row.parity} |\n`);
}
process.stdout.write("\n| Variant | HTTP 429s | Retries | Retry delay (ms) |\n");
process.stdout.write("|---|---:|---:|---:|\n");
for (const row of rows) {
  process.stdout.write(`| ${escapeCell(row.label)} | ${row.rateLimited} | ${row.retries} | ${row.retryDelayMs.toFixed(3)} |\n`);
}
