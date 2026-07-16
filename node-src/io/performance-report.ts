import { randomBytes } from "node:crypto";
import { mkdir, open, rename, unlink } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";

function failure(): ProcessFailure {
  return new ProcessFailure({
    category: "io",
    code: "PERFORMANCE_REPORT_WRITE_FAILED",
    message: "unable to write performance report",
  });
}

export function renderPerformanceReport(report: Readonly<Record<string, unknown>>): string {
  return renderPythonCompatibleJson(report as unknown as JsonValue);
}

/** Write one private, same-directory, atomically replaced performance report. */
export async function writePerformanceReport(options: {
  readonly path: string;
  readonly report: Readonly<Record<string, unknown>>;
}): Promise<void> {
  const target = path.resolve(options.path);
  const directory = path.dirname(target);
  let temporary: string | null = null;
  try {
    await mkdir(directory, { recursive: true });
    for (let attempt = 0; attempt < 32; attempt += 1) {
      temporary = path.join(
        directory,
        `.infrawright-performance-${process.pid}-${randomBytes(8).toString("hex")}`,
      );
      try {
        const file = await open(temporary, "wx", 0o600);
        try {
          await file.writeFile(renderPerformanceReport(options.report), "utf8");
        } finally {
          await file.close();
        }
        break;
      } catch (error: unknown) {
        if (
          typeof error === "object"
          && error !== null
          && "code" in error
          && error.code === "EEXIST"
        ) {
          temporary = null;
          continue;
        }
        throw error;
      }
    }
    if (temporary === null) throw failure();
    await rename(temporary, target);
    temporary = null;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    throw failure();
  } finally {
    if (temporary !== null) {
      try {
        await unlink(temporary);
      } catch {
        // Preserve the primary report-write failure.
      }
    }
  }
}
