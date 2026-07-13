import { randomBytes } from "node:crypto";
import { mkdir, open, rename, unlink } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import type { SavedPlanAssessmentReport } from "../domain/plan-report.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";

function failure(): ProcessFailure {
  return new ProcessFailure({
    code: "ASSESSMENT_REPORT_WRITE_FAILED",
    category: "io",
    message: "unable to write saved-plan assessment report",
  });
}

/** Exact Python-compatible v1 report bytes. */
export function renderAssessmentReport(
  report: SavedPlanAssessmentReport,
): string {
  return renderPythonCompatibleJson(report as unknown as JsonValue);
}

/** Write through a same-directory private temporary file and atomic rename. */
export async function writeAssessmentReport(options: {
  readonly path: string | null;
  readonly report: SavedPlanAssessmentReport;
  readonly stdout?: (text: string) => void;
}): Promise<void> {
  if (options.path === null) return;
  const rendered = renderAssessmentReport(options.report);
  if (options.path === "-") {
    (options.stdout ?? ((text) => process.stdout.write(text)))(rendered);
    return;
  }
  const target = path.resolve(options.path);
  const directory = path.dirname(target);
  let temporary: string | null = null;
  try {
    await mkdir(directory, { recursive: true });
    for (let attempt = 0; attempt < 32; attempt += 1) {
      temporary = path.join(
        directory,
        `.infrawright-report-${process.pid}-${randomBytes(8).toString("hex")}`,
      );
      try {
        const file = await open(temporary, "wx", 0o600);
        try {
          await file.writeFile(rendered, "utf8");
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
