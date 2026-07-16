import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

import { comparePythonStrings } from "../json/python-compatible.js";

export const VENDOR_TOKENS = [
  "zscaler",
  "zia",
  "zpa",
  "zcc",
  "cloudflare",
  "netbox",
  "aws",
  "google",
] as const;

const SKIP_BASENAMES = new Set(["audit_vendor_boundary.py"]);
const ALLOWLIST_KEYS = new Set(["path", "token", "pattern", "reason"]);

export interface VendorBoundaryMatch {
  readonly path: string;
  readonly line: number;
  readonly token: string;
  readonly excerpt: string;
}

export interface VendorBoundaryAllowance {
  readonly path: string;
  readonly token: string;
  readonly pattern: string;
  readonly reason: string;
}

export interface VendorBoundaryAudit {
  readonly root: string;
  readonly allowlist: string;
  readonly matches: readonly VendorBoundaryMatch[];
  readonly allowed: readonly VendorBoundaryMatch[];
  readonly violations: readonly VendorBoundaryMatch[];
}

export interface VendorBoundaryCommandResult {
  readonly exitCode: 0 | 1 | 2;
  readonly stdout: string;
  readonly stderr: string;
}

export class VendorBoundaryAuditError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "VendorBoundaryAuditError";
  }
}

function regexEscape(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

function tokenPattern(tokens: readonly string[]): RegExp {
  const alternatives = [...tokens]
    .sort((left, right) => right.length - left.length)
    .map(regexEscape)
    .join("|");
  return new RegExp(`(?<![A-Za-z0-9])(${alternatives})(?![A-Za-z0-9])`, "giu");
}

function portableRelative(root: string, filename: string): string {
  return path.relative(root, filename).split(path.sep).join("/");
}

async function enginePythonFiles(root: string): Promise<readonly string[]> {
  const files: string[] = [];
  async function walk(directory: string): Promise<void> {
    let entries;
    try {
      entries = await readdir(directory, { withFileTypes: true });
    } catch {
      // Match os.walk without an onerror callback: an unreadable or missing
      // directory contributes no files to the audit.
      return;
    }
    entries.sort((left, right) => comparePythonStrings(left.name, right.name));
    // os.walk yields all files in the current directory before descending
    // into its sorted child directories.
    for (const entry of entries) {
      if (
        entry.isFile()
        && entry.name.endsWith(".py")
        && !SKIP_BASENAMES.has(entry.name)
      ) {
        files.push(path.join(directory, entry.name));
      }
    }
    for (const entry of entries) {
      if (
        entry.isDirectory()
        && entry.name !== "__pycache__"
        && !entry.name.startsWith(".")
      ) {
        await walk(path.join(directory, entry.name));
      }
    }
  }
  await walk(path.join(root, "engine"));
  return files;
}

function universalLines(contents: string): readonly string[] {
  // Python text mode translates CRLF and bare CR before iteration.
  return contents.replace(/\r\n?/gu, "\n").split("\n");
}

export async function scanVendorBoundary(
  root: string,
  tokens: readonly string[] = VENDOR_TOKENS,
): Promise<readonly VendorBoundaryMatch[]> {
  const absoluteRoot = path.resolve(root);
  const expression = tokenPattern(tokens);
  const matches: VendorBoundaryMatch[] = [];
  for (const filename of await enginePythonFiles(absoluteRoot)) {
    const relative = portableRelative(absoluteRoot, filename);
    const contents = await readFile(filename, "utf8");
    const lines = universalLines(contents);
    for (let index = 0; index < lines.length; index += 1) {
      const excerpt = lines[index] ?? "";
      const seen = new Set<string>();
      expression.lastIndex = 0;
      for (const match of excerpt.matchAll(expression)) {
        const token = match[1]?.toLowerCase();
        if (token === undefined || seen.has(token)) continue;
        seen.add(token);
        matches.push({ path: relative, line: index + 1, token, excerpt });
      }
    }
  }
  return matches;
}

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function loadVendorBoundaryAllowlist(
  filename: string,
): Promise<readonly VendorBoundaryAllowance[]> {
  const absolute = path.resolve(filename);
  let data: unknown;
  try {
    data = JSON.parse(await readFile(absolute, "utf8"));
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new VendorBoundaryAuditError(
      `failed to read allowlist ${absolute}: ${detail}`,
    );
  }
  if (!object(data)) {
    throw new VendorBoundaryAuditError(`${absolute}: allowlist must be a JSON object`);
  }
  if (!Array.isArray(data.allow)) {
    throw new VendorBoundaryAuditError(`${absolute}: allow must be a list`);
  }
  const output: VendorBoundaryAllowance[] = [];
  for (let index = 0; index < data.allow.length; index += 1) {
    const entry = data.allow[index];
    const prefix = `${absolute}: allow[${index}]`;
    if (!object(entry)) {
      throw new VendorBoundaryAuditError(`${prefix} must be an object`);
    }
    const unknown = Object.keys(entry)
      .filter((key) => !ALLOWLIST_KEYS.has(key))
      .sort(comparePythonStrings);
    if (unknown.length > 0) {
      throw new VendorBoundaryAuditError(`${prefix} unknown keys: ${unknown.join(", ")}`);
    }
    for (const key of ALLOWLIST_KEYS) {
      if (typeof entry[key] !== "string" || entry[key].length === 0) {
        throw new VendorBoundaryAuditError(`${prefix}.${key} must be a non-empty string`);
      }
    }
    output.push({
      path: entry.path as string,
      token: (entry.token as string).toLowerCase(),
      pattern: entry.pattern as string,
      reason: entry.reason as string,
    });
  }
  return output;
}

export function classifyVendorBoundaryMatches(
  matches: readonly VendorBoundaryMatch[],
  allowlist: readonly VendorBoundaryAllowance[],
): {
  readonly allowed: readonly VendorBoundaryMatch[];
  readonly violations: readonly VendorBoundaryMatch[];
} {
  const allowed: VendorBoundaryMatch[] = [];
  const violations: VendorBoundaryMatch[] = [];
  for (const match of matches) {
    const accepted = allowlist.some((entry) => (
      entry.path === match.path
      && entry.token === match.token
      && match.excerpt.includes(entry.pattern)
    ));
    (accepted ? allowed : violations).push(match);
  }
  return { allowed, violations };
}

export async function auditVendorBoundary(options: {
  readonly root?: string;
  readonly allowlist?: string;
} = {}): Promise<VendorBoundaryAudit> {
  const root = path.resolve(options.root ?? process.cwd());
  const allowlistPath = path.resolve(
    options.allowlist ?? path.join(root, "engine", "vendor_boundary_allowlist.json"),
  );
  const allowlist = await loadVendorBoundaryAllowlist(allowlistPath);
  const matches = await scanVendorBoundary(root);
  const classified = classifyVendorBoundaryMatches(matches, allowlist);
  return {
    root,
    allowlist: allowlistPath,
    matches,
    allowed: classified.allowed,
    violations: classified.violations,
  };
}

function printedMatch(match: VendorBoundaryMatch): string {
  return `${match.path}:${match.line}: ${match.token}: ${match.excerpt.trim()}\n`;
}

export async function runVendorBoundaryAudit(options: {
  readonly root?: string;
  readonly allowlist?: string;
} = {}): Promise<VendorBoundaryCommandResult> {
  let result: VendorBoundaryAudit;
  try {
    result = await auditVendorBoundary(options);
  } catch (error) {
    if (!(error instanceof VendorBoundaryAuditError)) throw error;
    return { exitCode: 2, stdout: "", stderr: `error: ${error.message}\n` };
  }
  let stdout = "vendor boundary audit\n";
  stdout += `tokens: ${VENDOR_TOKENS.join(", ")}\n`;
  stdout += `allowed matches: ${result.allowed.length}\n`;
  stdout += `violations: ${result.violations.length}\n`;
  if (result.violations.length > 0) {
    stdout += "\nviolations:\n";
    stdout += result.violations.map(printedMatch).join("");
    return { exitCode: 1, stdout, stderr: "" };
  }
  return { exitCode: 0, stdout, stderr: "" };
}
