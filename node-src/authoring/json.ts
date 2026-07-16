import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import { LosslessNumber } from "lossless-json";

import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";
import { isObject } from "../metadata/validation.js";

function restorePythonFloatFields(value: unknown, field?: string): unknown {
  if (
    typeof value === "number"
    && (field === "coverage_ratio" || field === "operation_id_coverage_ratio")
  ) {
    return new LosslessNumber(Number.isInteger(value) ? `${String(value)}.0` : String(value));
  }
  if (Array.isArray(value)) {
    return value.map((item) => restorePythonFloatFields(item));
  }
  if (isObject(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => {
      return [key, restorePythonFloatFields(item, key)];
    }));
  }
  return value;
}

export function renderAuthoringJson(value: unknown): string {
  return renderPythonCompatibleJson(restorePythonFloatFields(value) as JsonValue);
}

export async function writeAuthoringJson(value: unknown, filename: string): Promise<void> {
  await mkdir(path.dirname(path.resolve(filename)), { recursive: true });
  await writeFile(filename, renderAuthoringJson(value), "utf8");
}
