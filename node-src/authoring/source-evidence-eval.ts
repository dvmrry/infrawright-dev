import { isDeepStrictEqual } from "node:util";

import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { compareSourceOperationReports } from "./source-operation-map.js";

const SHORTCOMING_SEVERITY: Readonly<Record<string, string>> = {
  ambiguous_source_operation: "review", calls_without_openapi_match: "gap", graphql_source: "notice",
  mapped_read_without_list: "notice", regression: "gap", resource_file_not_found: "gap",
  source_files_without_operation_calls: "gap", unmapped_without_reason: "gap",
};
const SEVERITY_ORDER: Readonly<Record<string, number>> = { gap: 0, review: 1, notice: 2 };
const CHANGE_CLASS_ORDER: Readonly<Record<string, number>> = { regression: 0, review: 1, acceptable: 2 };
export const MAX_MARKDOWN_CHANGE_ROWS = 100;

function object(value: unknown): JsonObject { return isObject(value) ? value : {}; }
function objects(value: unknown): readonly JsonObject[] { return Array.isArray(value) ? value.filter(isObject) : []; }
function strings(value: unknown): readonly string[] { return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []; }
function pythonTruthy(value: unknown): boolean {
  if (value == null || value === false) return false; if (typeof value === "number") return value !== 0; if (typeof value === "string" || Array.isArray(value)) return value.length > 0; if (isObject(value)) return Object.keys(value).length > 0; return true;
}
function pythonDisplay(value: unknown): string { if (value == null) return "None"; if (value === true) return "True"; if (value === false) return "False"; return String(value); }
function firstTruthy(...values: readonly unknown[]): unknown { return values.find(pythonTruthy) ?? values.at(-1); }
function getDefault(value: JsonObject, key: string, fallback: unknown): unknown { return Object.hasOwn(value, key) ? value[key] : fallback; }

function changedFilesOnly(before: JsonObject, after: JsonObject): boolean {
  const { files: beforeFiles = [], ...beforeRest } = before; const { files: afterFiles = [], ...afterRest } = after;
  return isDeepStrictEqual(beforeRest, afterRest) && !isDeepStrictEqual(beforeFiles, afterFiles);
}

export function classifySourceEvidenceChange(change: JsonObject): JsonObject {
  const before = object(change.before); const after = object(change.after); const beforeStatus = before.status; const afterStatus = after.status;
  const beforeRead = before.read_path; const afterRead = after.read_path; const beforeFiles = strings(before.files); const afterFiles = strings(after.files);
  if (beforeStatus === "mapped" && afterStatus === "unmapped") return { classification: "regression", reason: "mapped_to_unmapped" };
  if (beforeStatus === "mapped" && afterStatus === "mapped" && pythonTruthy(beforeRead) && pythonTruthy(afterRead) && beforeRead !== afterRead) return { classification: "regression", reason: "mapped_read_path_changed" };
  if (beforeFiles.length > 0 && afterFiles.length === 0) return { classification: "regression", reason: "source_files_dropped_to_zero" };
  if (changedFilesOnly(before, after)) return { classification: "acceptable", reason: afterFiles.length < beforeFiles.length ? "source_files_narrowed" : "source_files_changed" };
  if (beforeStatus !== "mapped" && afterStatus === "mapped") return { classification: "review", reason: "new_mapping" };
  if (beforeStatus === "mapped" && afterStatus === "ambiguous_source_operation") return { classification: "review", reason: "mapped_to_ambiguous" };
  if (before.list_path !== after.list_path) return { classification: "review", reason: "list_path_changed" };
  if (beforeRead !== afterRead) return { classification: "review", reason: "read_path_changed" };
  if (beforeStatus !== afterStatus) return { classification: "review", reason: "status_changed" };
  return { classification: "review", reason: "diagnostic_changed" };
}

export function classifySourceEvidenceComparison(compareReport: JsonObject): JsonObject {
  const changes: JsonObject[] = []; const counts: Record<string, number> = { acceptable: 0, regression: 0, review: 0 }; const reasons: Record<string, number> = {};
  for (const change of objects(compareReport.changes)) {
    const verdict = classifySourceEvidenceChange(change); const classification = String(verdict.classification); const reason = String(verdict.reason);
    counts[classification] = (counts[classification] ?? 0) + 1; reasons[reason] = (reasons[reason] ?? 0) + 1;
    changes.push({ ...change, classification, classification_reason: reason });
  }
  const summary = object(compareReport.summary);
  return { changes, summary: { acceptable: counts.acceptable, candidate: object(summary.candidate), changed: changes.length, control: object(summary.control), reasons: Object.fromEntries(sortedStrings(Object.keys(reasons)).map((reason) => [reason, reasons[reason]])), regressions: counts.regression, resources: getDefault(summary, "resources", 0), review_required: counts.review, unchanged: getDefault(summary, "unchanged", 0) } };
}

function operationCallCount(detail: JsonObject): number { return ["client_call_count", "package_call_count", "raw_rest_call_count"].reduce((sum, key) => sum + (pythonTruthy(detail[key]) ? Number(detail[key]) : 0), 0); }
function unmappedBucket(reason: unknown, detail: JsonObject): string {
  if (reason === "no_source_operation_match") return operationCallCount(detail) > 0 || pythonTruthy(detail.candidate_count) ? "calls_without_openapi_match" : "source_files_without_operation_calls";
  return pythonTruthy(reason) ? String(reason) : "unmapped_without_reason";
}
function candidateSamples(candidates: unknown, limit = 5): readonly JsonObject[] {
  const keys = ["client_symbol", "operation_id", "method", "path", "path_kind", "source_role", "read_score", "list_score"];
  return objects(candidates).slice(0, limit).map((candidate) => Object.fromEntries(keys.filter((key) => Object.hasOwn(candidate, key)).map((key) => [key, candidate[key]])));
}
function addShortcoming(shortcomings: Record<string, JsonObject>, bucket: string, resource: unknown, detail: JsonObject): void {
  const entry = shortcomings[bucket] ?? { count: 0, resources: [], severity: SHORTCOMING_SEVERITY[bucket] ?? "review" }; entry.count = Number(entry.count) + 1; (entry.resources as JsonObject[]).push({ resource: resource ?? null, ...detail }); shortcomings[bucket] = entry;
}

export function summarizeSourceEvidenceShortcomings(candidateReport: JsonObject, evaluation: JsonObject): JsonObject {
  const shortcomings: Record<string, JsonObject> = {}; const registry = object(candidateReport.registry); const diagnostics = new Map<unknown, JsonObject>();
  for (const item of objects(candidateReport.diagnostics)) diagnostics.set(item.resource, item);
  for (const change of objects(evaluation.changes)) if (change.classification === "regression") addShortcoming(shortcomings, "regression", change.resource, { after: object(change.after), before: object(change.before), reason: change.classification_reason });
  for (const resource of sortedStrings(Object.keys(registry))) {
    const entry = object(registry[resource]); const diagnostic = diagnostics.get(resource) ?? {}; const source = object(entry.source); const status = entry.status; const reason = entry.reason;
    const detail: JsonObject = { candidate_count: getDefault(source, "candidate_count", 0), client_call_count: getDefault(source, "client_call_count", 0), files: firstTruthy(source.files, diagnostic.files, []) as never, package_call_count: getDefault(source, "package_call_count", 0), raw_rest_call_count: getDefault(source, "raw_rest_call_count", 0), reason: reason ?? null, status: status ?? null };
    for (const key of ["client_calls", "package_calls", "raw_rest_calls"]) if (pythonTruthy(source[key])) detail[key] = (source[key] as unknown[]).slice(0, 10) as never;
    const candidates = firstTruthy(entry.candidates, diagnostic.ambiguous, diagnostic.hits, []); if (pythonTruthy(candidates)) detail.candidate_samples = candidateSamples(candidates) as never;
    if (status === "ambiguous_source_operation") { addShortcoming(shortcomings, "ambiguous_source_operation", resource, detail); continue; }
    if (status === "graphql_source") { addShortcoming(shortcomings, "graphql_source", resource, detail); continue; }
    if (status !== "mapped") { addShortcoming(shortcomings, unmappedBucket(reason, detail), resource, detail); continue; }
    const read = object(entry.read); if (pythonTruthy(read) && !pythonTruthy(object(entry.list))) addShortcoming(shortcomings, "mapped_read_without_list", resource, { ...detail, read_operation_id: read.operation_id ?? null, read_path: read.path ?? null });
  }
  const buckets: Record<string, JsonObject> = {};
  for (const bucket of sortedStrings(Object.keys(shortcomings))) { const detail = shortcomings[bucket] as JsonObject; const resources = [...objects(detail.resources)].sort((a, b) => comparePythonStrings(String(a.resource ?? ""), String(b.resource ?? ""))); buckets[bucket] = { count: detail.count, resources, severity: detail.severity }; }
  const severity: Record<string, number> = {}; for (const detail of Object.values(buckets)) { const name = String(detail.severity ?? "review"); severity[name] = (severity[name] ?? 0) + Number(detail.count ?? 0); }
  return { buckets, severity_summary: Object.fromEntries(sortedStrings(Object.keys(severity)).map((key) => [key, severity[key]])), summary: Object.fromEntries(Object.entries(buckets).map(([bucket, detail]) => [bucket, detail.count])) };
}

function statusTable(summary: JsonObject): string {
  const control = object(summary.control); const candidate = object(summary.candidate); const names = ["resources", "mapped", "ambiguous", "graphql_source", "unmapped", "resources_with_source_files"];
  return ["| Metric | Text Scanner | AST Facts |", "|---|---:|---:|", ...names.map((name) => `| \`${name}\` | \`${pythonDisplay(getDefault(control, name, 0))}\` | \`${pythonDisplay(getDefault(candidate, name, 0))}\` |`)].join("\n");
}

export function renderSourceEvidenceMarkdown(evaluation: JsonObject, title = "Source Evidence A/B Evaluation"): string {
  const summary = object(evaluation.summary); const lines = [`# ${title}`, "", statusTable(summary), "", "## Delta Summary", "", "| Classification | Count |", "|---|---:|", `| \`regression\` | \`${pythonDisplay(getDefault(summary, "regressions", 0))}\` |`, `| \`review\` | \`${pythonDisplay(getDefault(summary, "review_required", 0))}\` |`, `| \`acceptable\` | \`${pythonDisplay(getDefault(summary, "acceptable", 0))}\` |`, `| \`unchanged\` | \`${pythonDisplay(getDefault(summary, "unchanged", 0))}\` |`, ""];
  const reasons = object(summary.reasons); if (Object.keys(reasons).length > 0) { lines.push("## Reasons", "", "| Reason | Count |", "|---|---:|", ...sortedStrings(Object.keys(reasons)).map((reason) => `| \`${reason}\` | \`${pythonDisplay(reasons[reason])}\` |`), ""); }
  const changes = [...objects(evaluation.changes)].sort((a, b) => (CHANGE_CLASS_ORDER[String(a.classification)] ?? 99) - (CHANGE_CLASS_ORDER[String(b.classification)] ?? 99) || comparePythonStrings(String(a.resource ?? ""), String(b.resource ?? "")));
  if (changes.length > 0) { const shown = changes.slice(0, MAX_MARKDOWN_CHANGE_ROWS); lines.push("## Changes", "", "| Resource | Class | Reason | Before | After |", "|---|---|---|---|---|", ...shown.map((change) => { const before = object(change.before); const after = object(change.after); return `| \`${pythonDisplay(change.resource)}\` | \`${pythonDisplay(change.classification)}\` | \`${pythonDisplay(change.classification_reason)}\` | ${pythonDisplay(before.status)} \`${pythonDisplay(before.read_path)}\` | ${pythonDisplay(after.status)} \`${pythonDisplay(after.read_path)}\` |`; })); if (changes.length > shown.length) lines.push("", `Showing \`${shown.length}\` of \`${changes.length}\` changes; full detail is in JSON.`); lines.push(""); }
  const buckets = object(object(evaluation.shortcomings).buckets); if (Object.keys(buckets).length > 0) {
    const names = Object.keys(buckets).sort((a, b) => (SEVERITY_ORDER[String(object(buckets[a]).severity)] ?? 99) - (SEVERITY_ORDER[String(object(buckets[b]).severity)] ?? 99) || comparePythonStrings(a, b)); lines.push("## Shortcomings", "", "| Bucket | Severity | Count | Sample Resources |", "|---|---|---:|---|");
    for (const bucket of names) { const detail = object(buckets[bucket]); const resources = objects(detail.resources).slice(0, 8).map((item) => `\`${pythonDisplay(item.resource)}\``); if (Number(detail.count ?? 0) > resources.length) resources.push("..."); lines.push(`| \`${bucket}\` | \`${pythonDisplay(detail.severity ?? "review")}\` | \`${pythonDisplay(detail.count ?? 0)}\` | ${resources.join(", ")} |`); } lines.push("");
  }
  return lines.join("\n");
}

export function evaluateSourceEvidence(controlReport: JsonObject, candidateReport: JsonObject, comparisonReport?: JsonObject): JsonObject {
  const evaluation = classifySourceEvidenceComparison(comparisonReport ?? compareSourceOperationReports(controlReport, candidateReport)); evaluation.shortcomings = summarizeSourceEvidenceShortcomings(candidateReport, evaluation); return evaluation;
}
