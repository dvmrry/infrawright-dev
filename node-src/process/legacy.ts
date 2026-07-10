import type {
  ChangedPathScope,
  RootTopology,
  WholeRootDiagnostic,
} from "../domain/types.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";

export function renderLegacyRootTopology(topology: RootTopology): string {
  return renderPythonCompatibleJson(topology as unknown as JsonValue);
}

export function renderLegacyRootDiagnostics(
  diagnostics: readonly WholeRootDiagnostic[],
): string {
  return diagnostics.map((diagnostic) => `NOTE: ${diagnostic.message}\n`).join("");
}

export function renderLegacyChangedPathScope(scope: ChangedPathScope): string {
  return renderPythonCompatibleJson(scope as unknown as JsonValue);
}
