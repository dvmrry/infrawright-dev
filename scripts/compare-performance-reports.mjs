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
  let oracleAb = false;
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === "--oracle-ab") {
      if (oracleAb) fail("--oracle-ab may be supplied only once");
      oracleAb = true;
      continue;
    }
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
  return { oracleAb, variants };
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
  const oracleStateSources = new Set();
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
    if (span.oracle_state_source !== undefined) {
      if (!["accepted-plan", "applied-state"].includes(span.oracle_state_source)) {
        throw new Error(`${name} span ${index} Oracle state source is invalid`);
      }
      oracleStateSources.add(span.oracle_state_source);
    }
    pages += optionalCount(span.pages, `${name} span pages`);
    logicalRequests += optionalCount(span.logical_requests, `${name} span logical requests`);
    terraformCommands += optionalCount(
      span.terraform_commands,
      `${name} span Terraform commands`,
    );
  }
  if (oracleStateSources.size > 1) {
    throw new Error(`${name} contains conflicting Oracle state sources`);
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

function reportOracleStateSource(report) {
  const sources = new Set(report.spans.flatMap((span) => {
    return span.phase !== "oracle.state_source" || span.oracle_state_source === undefined
      ? []
      : [span.oracle_state_source];
  }));
  return sources.size === 1 ? [...sources][0] : null;
}

function exactOracleEvidence(report, label) {
  const labeled = report.spans.filter((span) => span.oracle_state_source !== undefined);
  if (labeled.some((span) => span.phase !== "oracle.state_source")) {
    fail(`variant ${label} records Oracle state source outside oracle.state_source`);
  }
  const sourceSpans = report.spans.filter((span) => span.phase === "oracle.state_source");
  if (sourceSpans.length === 0) {
    fail(`variant ${label} is missing Oracle state-source evidence`);
  }
  const sources = new Set(sourceSpans.map((span) => span.oracle_state_source));
  if (sources.size !== 1 || sources.has(undefined)) {
    fail(`variant ${label} does not contain one Oracle state source`);
  }
  const source = [...sources][0];
  const families = new Set();
  for (const span of sourceSpans) {
    const family = span.resource_family;
    if (
      span.status !== "success"
      || span.terraform_commands !== 0
      || typeof family !== "string"
      || !/^[A-Za-z0-9][A-Za-z0-9_.-]*$/u.test(family)
      || families.has(family)
    ) {
      fail(`variant ${label} has invalid Oracle state-source coverage`);
    }
    families.add(family);
  }
  return { families, source };
}

function assertOraclePhase(report, phase, evidence) {
  const spans = report.spans.filter((span) => span.phase === phase);
  const status = evidence.source === "accepted-plan" ? "skipped" : "success";
  const commands = evidence.source === "accepted-plan" ? 0 : 1;
  const families = new Set();
  for (const span of spans) {
    if (
      typeof span.resource_family !== "string"
      || !evidence.families.has(span.resource_family)
      || families.has(span.resource_family)
      || span.status !== status
      || span.terraform_commands !== commands
    ) {
      fail(`${evidence.source} Adopt report does not contain exact ${phase} evidence`);
    }
    families.add(span.resource_family);
  }
  if (families.size !== evidence.families.size) {
    fail(`${evidence.source} Adopt report does not contain exact ${phase} evidence`);
  }
}

const parsedArguments = argumentsFrom(process.argv.slice(2));
const variants = parsedArguments.variants;
const evidence = [];
for (const variant of variants) {
  evidence.push({
    ...variant,
    manifest: await manifest(variant.path),
    reports: await reports(variant.path),
  });
}
if (parsedArguments.oracleAb) {
  if (evidence.length !== 2) fail("--oracle-ab requires exactly two variants");
  const observed = new Set();
  for (const variant of evidence) {
    const adopt = variant.reports.get("adopt");
    if (adopt === undefined) fail(`variant ${variant.label} is missing its Adopt report`);
    const oracle = exactOracleEvidence(adopt, variant.label);
    if (variant.label !== oracle.source) {
      fail(`variant ${variant.label} contains Oracle state source ${oracle.source}`);
    }
    observed.add(oracle.source);
    assertOraclePhase(adopt, "oracle.scratch_apply", oracle);
    assertOraclePhase(adopt, "oracle.state_show", oracle);
  }
  if (!observed.has("applied-state") || !observed.has("accepted-plan")) {
    fail("--oracle-ab requires one applied-state and one accepted-plan report");
  }
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
    oracleSource: adopt === undefined ? "-" : reportOracleStateSource(adopt) ?? "missing",
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

process.stdout.write("| Variant | Oracle source | Fetch time (ms) | Oracle time (ms) | Total time (ms) | HTTP requests | Detail GETs | List GETs | TF commands | Parity |\n");
process.stdout.write("|---|---|---:|---:|---:|---:|---:|---:|---:|---|\n");
for (const row of rows) {
  process.stdout.write(`| ${escapeCell(row.label)} | ${row.oracleSource} | ${row.fetch.toFixed(3)} | ${row.oracle.toFixed(3)} | ${row.total.toFixed(3)} | ${row.http} | ${row.detail} | ${row.list} | ${row.terraform} | ${row.parity} |\n`);
}
process.stdout.write("\n| Variant | HTTP 429s | Retries | Retry delay (ms) |\n");
process.stdout.write("|---|---:|---:|---:|\n");
for (const row of rows) {
  process.stdout.write(`| ${escapeCell(row.label)} | ${row.rateLimited} | ${row.retries} | ${row.retryDelayMs.toFixed(3)} |\n`);
}
