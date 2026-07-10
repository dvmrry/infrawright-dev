import { readlinkSync, realpathSync } from "node:fs";
import path from "node:path";

import { sortedStrings } from "../json/python-compatible.js";

export function pythonPosixJoin(...parts: readonly string[]): string {
  let result = "";
  for (const part of parts) {
    if (part.startsWith("/")) {
      result = part;
    } else if (result.length === 0 || result.endsWith("/")) {
      result += part;
    } else {
      result += `/${part}`;
    }
  }
  return result;
}

/** Match Python's posixpath.normpath, including its exactly-two-slashes rule. */
export function pythonPosixNormPath(value: string): string {
  if (value.length === 0) {
    return ".";
  }
  let initialSlashes = value.startsWith("/") ? 1 : 0;
  if (value.startsWith("//") && !value.startsWith("///")) {
    initialSlashes = 2;
  }
  const components: string[] = [];
  for (const component of value.split("/")) {
    if (component.length === 0 || component === ".") {
      continue;
    }
    if (
      component !== ".."
      || (initialSlashes === 0
        && (components.length === 0 || components.at(-1) === ".."))
    ) {
      components.push(component);
    } else if (components.length > 0) {
      components.pop();
    }
  }
  const normalized = `${"/".repeat(initialSlashes)}${components.join("/")}`;
  return normalized || ".";
}

export function pythonPosixAbspath(value: string, cwd: string): string {
  return pythonPosixNormPath(
    value.startsWith("/") ? value : pythonPosixJoin(cwd, value),
  );
}

/**
 * Match Python realpath(strict=False): resolve the deepest existing ancestor,
 * then reattach a missing leaf without requiring the changed file to exist.
 */
function weakRealpath(absolutePath: string, seen: ReadonlySet<string>): string {
  const normalized = pythonPosixNormPath(absolutePath);
  if (seen.has(normalized)) {
    return normalized;
  }
  let candidate = normalized;
  const missing: string[] = [];
  while (true) {
    try {
      const resolved = realpathSync.native(candidate);
      return pythonPosixNormPath(pythonPosixJoin(resolved, ...missing));
    } catch (error: unknown) {
      const code = typeof error === "object" && error !== null && "code" in error
        ? error.code
        : undefined;
      if (code !== "ENOENT" && code !== "ENOTDIR") {
        return normalized;
      }
      try {
        const target = readlinkSync(candidate, { encoding: "utf8" });
        const targetPath = target.startsWith("/")
          ? target
          : pythonPosixJoin(path.posix.dirname(candidate), target);
        return weakRealpath(
          pythonPosixJoin(targetPath, ...missing),
          new Set([...seen, normalized]),
        );
      } catch {
        // The candidate is an ordinary missing component, not a dangling link.
      }
      const parent = path.posix.dirname(candidate);
      if (parent === candidate) {
        return normalized;
      }
      missing.unshift(path.posix.basename(candidate));
      candidate = parent;
    }
  }
}

export function pythonPosixRealpath(absolutePath: string): string {
  return weakRealpath(absolutePath, new Set());
}

export function physicalWorkspace(workspace: string): string {
  return pythonPosixRealpath(pythonPosixNormPath(workspace));
}

export function pythonPathForms(value: string, workspace: string): string[] {
  const normalized = pythonPosixNormPath(value);
  const absolute = pythonPosixAbspath(normalized, physicalWorkspace(workspace));
  return sortedStrings(new Set([
    normalized,
    pythonPosixNormPath(absolute),
    pythonPosixRealpath(absolute),
  ]));
}

export function pythonRelativeUnder(
  value: string,
  root: string,
  workspace: string,
): string[] | null {
  for (const valueForm of pythonPathForms(value, workspace)) {
    for (const rootForm of pythonPathForms(root, workspace)) {
      if (valueForm.startsWith("/") !== rootForm.startsWith("/")) {
        continue;
      }
      const relativeBase = valueForm.startsWith("/")
        ? rootForm
        : pythonPosixAbspath(rootForm, physicalWorkspace(workspace));
      const relativeValue = valueForm.startsWith("/")
        ? valueForm
        : pythonPosixAbspath(valueForm, physicalWorkspace(workspace));
      const relative = path.posix.relative(relativeBase, relativeValue) || ".";
      if (relative === ".") {
        return [];
      }
      if (relative === ".." || relative.startsWith("../")) {
        continue;
      }
      return relative.split("/");
    }
  }
  return null;
}

export function sameContractPath(
  left: string,
  right: string,
  workspace: string,
): boolean {
  const rightForms = new Set(pythonPathForms(right, workspace));
  return pythonPathForms(left, workspace).some((form) => rightForms.has(form));
}
