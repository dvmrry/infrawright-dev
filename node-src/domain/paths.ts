import { readlinkSync } from "node:fs";
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
type RealpathToken =
  | { readonly kind: "component"; readonly value: string }
  | { readonly kind: "leave_link"; readonly path: string };

/** Resolve symlinks in component order like Python realpath(strict=False). */
export function pythonPosixRealpath(absolutePath: string): string {
  const normalized = pythonPosixNormPath(absolutePath);
  let resolved = "/";
  let tokens: RealpathToken[] = normalized.split("/")
    .filter((component) => component.length > 0)
    .map((value) => ({ kind: "component", value }));
  const activeLinks = new Set<string>();
  while (tokens.length > 0) {
    const token = tokens.shift();
    if (token === undefined) {
      break;
    }
    if (token.kind === "leave_link") {
      activeLinks.delete(token.path);
      continue;
    }
    if (token.value === ".") {
      continue;
    }
    if (token.value === "..") {
      resolved = path.posix.dirname(resolved);
      continue;
    }
    const candidate = pythonPosixJoin(resolved, token.value);
    try {
      const target = readlinkSync(candidate, { encoding: "utf8" });
      if (activeLinks.has(candidate)) {
        // Python strict=False leaves a detected loop unresolved.
        resolved = candidate;
        continue;
      }
      activeLinks.add(candidate);
      if (target.startsWith("/")) {
        resolved = "/";
      }
      const targetTokens: RealpathToken[] = target.split("/")
        .filter((component) => component.length > 0)
        .map((value) => ({ kind: "component", value }));
      tokens = [
        ...targetTokens,
        { kind: "leave_link", path: candidate },
        ...tokens,
      ];
    } catch {
      // Missing and non-link components stay in the non-strict result. Keeping
      // the already-resolved prefix also matches Python for ELOOP/EACCES.
      resolved = candidate;
    }
  }
  return pythonPosixNormPath(resolved);
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
