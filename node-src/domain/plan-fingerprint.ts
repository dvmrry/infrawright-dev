import {
  lstatSync,
  statSync,
} from "node:fs";
import { createHash } from "node:crypto";
import { opendir } from "node:fs/promises";
import { posix as path } from "node:path";

import {
  pythonPosixAbspath,
  pythonPosixJoin,
  pythonPosixNormPath,
} from "./paths.js";
import { ProcessFailure } from "./errors.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
} from "../io/bounded-files.js";
import { sortedStrings } from "../json/python-compatible.js";

export const PLAN_FINGERPRINT_VERSION = 2 as const;

export const MODULE_FINGERPRINT_IGNORED_DIRS = new Set([
  ".git",
  ".mypy_cache",
  ".pytest_cache",
  ".ruff_cache",
  ".terraform",
  "__pycache__",
]);

export type FileFingerprint = readonly [path: string, sha256: string];

export interface BackendFingerprint {
  readonly key: string | null;
  readonly present: boolean;
  readonly sha256?: string;
}

export interface ModuleFingerprint {
  readonly files: readonly FileFingerprint[];
  readonly local: true;
  readonly present: boolean;
  readonly resource_type: string;
  readonly source: string;
}

export interface PlanSourcesPayload {
  readonly backend: BackendFingerprint | null;
  readonly member_types: readonly string[];
  readonly modules: readonly ModuleFingerprint[];
  readonly root_tf: readonly FileFingerprint[];
  readonly var_files: readonly FileFingerprint[];
}

export interface InitSourcesPayload {
  readonly backend: BackendFingerprint | null;
  readonly modules: readonly ModuleFingerprint[];
  readonly root_config: readonly FileFingerprint[];
}

export interface PlanFingerprintInput {
  readonly envDir: string;
  readonly varFiles: readonly string[];
  readonly memberTypes: readonly string[];
  readonly backendConfig?: string | null;
  readonly backendKey?: string | null;
}

export interface InitFingerprintInput {
  readonly envDir: string;
  readonly memberTypes: readonly string[];
  readonly backendConfig?: string | null;
  readonly backendKey?: string | null;
}

export interface PlanFingerprintV2 {
  readonly version: typeof PLAN_FINGERPRINT_VERSION;
  readonly sha256: string;
}

type CanonicalJson =
  | null
  | boolean
  | number
  | string
  | readonly CanonicalJson[]
  | { readonly [key: string]: CanonicalJson | undefined };

function pythonStringCompare(left: string, right: string): number {
  const ordered = sortedStrings([left, right]);
  if (left === right) {
    return 0;
  }
  return ordered[0] === left ? -1 : 1;
}

function isDirectory(path: string): boolean {
  try {
    return statSync(path).isDirectory();
  } catch {
    return false;
  }
}

function isFile(path: string): boolean {
  try {
    return statSync(path).isFile();
  } catch {
    return false;
  }
}

function isSymbolicLink(path: string): boolean {
  try {
    return lstatSync(path).isSymbolicLink();
  } catch {
    return false;
  }
}

async function directoryNames(
  directory: string,
  budget: ReadBudget,
  depth: number,
): Promise<string[]> {
  budget.enterDirectory(depth);
  try {
    // Node supports the documented fs "buffer" encoding here at runtime, but
    // @types/node's OpenDirOptions still narrows it to BufferEncoding.
    const handle = await opendir(directory, { encoding: "buffer" as BufferEncoding });
    const names: string[] = [];
    for await (const entry of handle) {
      budget.reserveDirectoryEntry();
      if (!Buffer.isBuffer(entry.name)) {
        throw new ProcessFailure({
          code: "INVALID_FILENAME_ENCODING",
          category: "io",
          message: "fingerprint input name is not valid UTF-8",
        });
      }
      try {
        names.push(new TextDecoder("utf-8", {
          fatal: true,
          ignoreBOM: true,
        }).decode(entry.name));
      } catch {
        throw new ProcessFailure({
          code: "INVALID_FILENAME_ENCODING",
          category: "io",
          message: "fingerprint input name is not valid UTF-8",
        });
      }
    }
    return names;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw new ProcessFailure({
      code: "DIRECTORY_READ_FAILED",
      category: "io",
      message: "unable to enumerate fingerprint inputs",
    });
  }
}

async function fileSha256(path: string, budget: ReadBudget): Promise<string> {
  return (await sha256StableFile(path, budget, { followSymlinks: true })).sha256;
}

function isPlanInput(name: string): boolean {
  return name.endsWith(".tf")
    || name.endsWith(".tf.json")
    || name === ".terraform.lock.hcl"
    || name === "terraform.tfvars"
    || name === "terraform.tfvars.json"
    || name.endsWith(".auto.tfvars")
    || name.endsWith(".auto.tfvars.json");
}

export async function rootTfFingerprints(
  envDir: string,
  budget = new ReadBudget(),
): Promise<FileFingerprint[]> {
  const out: FileFingerprint[] = [];
  if (!isDirectory(envDir)) {
    return out;
  }
  for (const name of sortedStrings(await directoryNames(envDir, budget, 0))) {
    if (!isPlanInput(name)) {
      continue;
    }
    const filePath = pythonPosixJoin(envDir, name);
    if (isFile(filePath)) {
      out.push([name, await fileSha256(filePath, budget)]);
    }
  }
  return out;
}

export async function rootConfigFingerprints(
  envDir: string,
  budget = new ReadBudget(),
): Promise<FileFingerprint[]> {
  return (await rootTfFingerprints(envDir, budget)).filter(([name]) => {
    return name.endsWith(".tf") || name.endsWith(".tf.json");
  });
}

async function walkTree(
  root: string,
  current: string,
  out: FileFingerprint[],
  budget: ReadBudget,
  depth: number,
): Promise<void> {
  const names = await directoryNames(current, budget, depth);

  const directories: string[] = [];
  const files: string[] = [];
  for (const name of names) {
    const filePath = pythonPosixJoin(current, name);
    if (isDirectory(filePath)) {
      if (!MODULE_FINGERPRINT_IGNORED_DIRS.has(name)) {
        directories.push(name);
      }
    } else {
      files.push(name);
    }
  }

  for (const name of sortedStrings(files)) {
    const filePath = pythonPosixJoin(current, name);
    if (!isFile(filePath)) {
      continue;
    }
    const relativePath = path.relative(root, filePath);
    out.push([relativePath, await fileSha256(filePath, budget)]);
  }

  for (const name of sortedStrings(directories)) {
    const path = pythonPosixJoin(current, name);
    // os.walk(..., followlinks=False) lists directory symlinks but does not
    // recurse into them. It does traverse a symlink supplied as the top path.
    if (!isSymbolicLink(path)) {
      await walkTree(root, path, out, budget, depth + 1);
    }
  }
}

export async function treeFingerprints(
  root: string,
  budget = new ReadBudget(),
): Promise<FileFingerprint[]> {
  const out: FileFingerprint[] = [];
  if (!isDirectory(root)) {
    return out;
  }
  await walkTree(root, root, out, budget, 0);
  return out;
}

function splitLinesKeepEnds(text: string): string[] {
  const out: string[] = [];
  let start = 0;
  let index = 0;
  while (index < text.length) {
    const code = text.charCodeAt(index);
    let end = -1;
    if (code === 0x0d) {
      end = index + (text.charCodeAt(index + 1) === 0x0a ? 2 : 1);
    } else if (
      code === 0x0a
      || code === 0x0b
      || code === 0x0c
      || code === 0x1c
      || code === 0x1d
      || code === 0x1e
      || code === 0x85
      || code === 0x2028
      || code === 0x2029
    ) {
      end = index + 1;
    }
    if (end >= 0) {
      out.push(text.slice(start, end));
      start = end;
      index = end;
    } else {
      index += 1;
    }
  }
  if (start < text.length) {
    out.push(text.slice(start));
  }
  return out;
}

async function readUtf8Strict(path: string, budget: ReadBudget): Promise<string> {
  return (await readBoundedUtf8File(
    path,
    budget,
    { followSymlinks: true },
  )).text;
}

// Python re's Unicode \s/str.strip set. JavaScript \s differs for FEFF,
// C0 separators, and NEL, all of which can alter structural recognition.
const PYTHON_WHITESPACE = "[\\t\\n\\v\\f\\r \\x1c-\\x1f\\x85\\xa0"
  + "\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]";

function pythonStrip(value: string): string {
  return value.replace(
    new RegExp(`^${PYTHON_WHITESPACE}+|${PYTHON_WHITESPACE}+$`, "g"),
    "",
  );
}

function hclStructureLines(text: string, path: string): string[] {
  const out: string[] = [];
  let blockComment = false;
  const lines = splitLinesKeepEnds(text);
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const line = lines[lineIndex] ?? "";
    const lineNumber = lineIndex + 1;
    const code: string[] = [];
    let inString = false;
    let escaped = false;
    let index = 0;
    while (index < line.length) {
      if (blockComment) {
        const end = line.indexOf("*/", index);
        if (end < 0) {
          if (line.endsWith("\r") || line.endsWith("\n")) {
            code.push("\n");
          }
          index = line.length;
          continue;
        }
        code.push(" ".repeat(end + 2 - index));
        blockComment = false;
        index = end + 2;
        continue;
      }

      const character = line[index] ?? "";
      if (inString) {
        code.push(character);
        if (escaped) {
          escaped = false;
        } else if (character === "\\") {
          escaped = true;
        } else if (character === "\"") {
          inString = false;
        }
        index += 1;
        continue;
      }
      if (character === "\"") {
        code.push(character);
        inString = true;
        index += 1;
        continue;
      }
      if (character === "#" || line.startsWith("//", index)) {
        if (line.endsWith("\r") || line.endsWith("\n")) {
          code.push("\n");
        }
        break;
      }
      if (line.startsWith("/*", index)) {
        code.push("  ");
        blockComment = true;
        index += 2;
        continue;
      }
      if (line.startsWith("<<", index)) {
        const heredoc = /^<<(-?)([A-Za-z_][A-Za-z0-9_-]*)/.exec(
          line.slice(index),
        );
        if (heredoc !== null) {
          throw new Error(
            `${path}:${lineNumber} contains a heredoc outside the generated-root `
              + "contract; run make gen-env to regenerate the root",
          );
        }
      }
      code.push(character);
      index += 1;
    }

    if (inString) {
      throw new Error(`${path}:${lineNumber} contains an unterminated quoted string`);
    }
    out.push(code.join(""));
  }

  if (blockComment) {
    throw new Error(`${path} contains an unterminated block comment`);
  }
  return out;
}

function hclBraceDelta(line: string): number {
  let delta = 0;
  let inString = false;
  let escaped = false;
  for (const character of line) {
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === "\"") {
        inString = false;
      }
      continue;
    }
    if (character === "\"") {
      inString = true;
    } else if (character === "{") {
      delta += 1;
    } else if (character === "}") {
      delta -= 1;
    }
  }
  return delta;
}

export async function rootModuleSources(
  envDir: string,
  budget = new ReadBudget(),
): Promise<ReadonlyMap<string, string>> {
  const sources = new Map<string, string>();
  if (!isDirectory(envDir)) {
    return sources;
  }
  const moduleStart = new RegExp(
    `^${PYTHON_WHITESPACE}*module${PYTHON_WHITESPACE}+"([^"]+)"`
      + `${PYTHON_WHITESPACE}*\\{${PYTHON_WHITESPACE}*$`,
  );
  const sourceLine = new RegExp(
    `^${PYTHON_WHITESPACE}*source${PYTHON_WHITESPACE}*=`
      + `${PYTHON_WHITESPACE}*"([^"\\\\]+)"${PYTHON_WHITESPACE}*$`,
  );
  const itemsLine = new RegExp(
    `^${PYTHON_WHITESPACE}*items${PYTHON_WHITESPACE}*=`
      + `${PYTHON_WHITESPACE}*(?:var|local)\\.[A-Za-z_][A-Za-z0-9_]*`
      + `${PYTHON_WHITESPACE}*$`,
  );

  for (const name of sortedStrings(await directoryNames(envDir, budget, 0))) {
    if (!name.endsWith(".tf")) {
      continue;
    }
    const path = pythonPosixJoin(envDir, name);
    if (!isFile(path)) {
      continue;
    }
    const lines = hclStructureLines(await readUtf8Strict(path, budget), path);
    let current: string | null = null;
    let source: string | null = null;
    let itemsSeen = false;
    let moduleDepth: number | null = null;
    let depth = 0;

    for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
      const line = lines[lineIndex] ?? "";
      const lineNumber = lineIndex + 1;
      if (current === null && depth === 0) {
        const match = moduleStart.exec(line);
        if (match !== null) {
          current = match[1] ?? "";
          source = null;
          itemsSeen = false;
          moduleDepth = 1;
        }
      } else if (current !== null && depth === moduleDepth) {
        const stripped = pythonStrip(line);
        const sourceMatch = sourceLine.exec(line);
        if (sourceMatch !== null) {
          if (source !== null) {
            throw new Error(
              `${path}:${lineNumber} module ${current} has multiple source values`,
            );
          }
          const candidate = sourceMatch[1] ?? "";
          if (candidate.includes("${") || candidate.includes("%{")) {
            throw new Error(
              `${path}:${lineNumber} module ${current} source uses HCL template syntax `
                + "outside the generated-root contract; run make gen-env to regenerate the root",
            );
          }
          source = candidate;
        } else if (itemsLine.test(line)) {
          if (itemsSeen) {
            throw new Error(
              `${path}:${lineNumber} module ${current} has multiple items values`,
            );
          }
          itemsSeen = true;
        } else if (stripped !== "" && stripped !== "}") {
          throw new Error(
            `${path}:${lineNumber} module ${current} is outside the generated-root `
              + "contract; run make gen-env to regenerate the root",
          );
        }
      }

      depth += hclBraceDelta(line);
      if (depth < 0) {
        throw new Error(`${path}:${lineNumber} has an unexpected closing brace`);
      }
      if (
        current !== null
        && moduleDepth !== null
        && depth < moduleDepth
      ) {
        if (source === null || !itemsSeen) {
          throw new Error(
            `${path} module ${current} is outside the generated-root contract; `
              + "run make gen-env to regenerate the root",
          );
        }
        if (sources.has(current)) {
          throw new Error(`${envDir} contains duplicate module ${current}`);
        }
        sources.set(current, source);
        current = null;
        source = null;
        itemsSeen = false;
        moduleDepth = null;
      }
    }
    if (depth !== 0) {
      throw new Error(`${path} has unbalanced braces`);
    }
  }
  return sources;
}

export function localModulePath(envDir: string, source: string): string | null {
  if (source === "") {
    return null;
  }
  if (source.startsWith("/")) {
    return pythonPosixNormPath(source);
  }
  if (source.startsWith("./") || source.startsWith("../")) {
    return pythonPosixNormPath(pythonPosixJoin(envDir, source));
  }
  return null;
}

export async function moduleFingerprints(
  envDir: string,
  memberTypes: readonly string[],
  budget = new ReadBudget(),
): Promise<ModuleFingerprint[]> {
  const sources = await rootModuleSources(envDir, budget);
  const out: ModuleFingerprint[] = [];
  for (const resourceType of sortedStrings(memberTypes)) {
    const source = sources.get(resourceType);
    if (source === undefined) {
      throw new Error(
        `${envDir} member ${resourceType} has no module source; run make gen-env to `
          + "regenerate the root",
      );
    }
    const path = localModulePath(envDir, source);
    if (path === null) {
      throw new Error(
        `${envDir} member ${resourceType} module source ${JSON.stringify(source)} `
          + "is not local; generated roots must use local module sources",
      );
    }
    out.push({
      files: await treeFingerprints(path, budget),
      local: true,
      present: isDirectory(path),
      resource_type: resourceType,
      source,
    });
  }
  return out;
}

export async function backendFingerprint(
  backendConfig: string | null | undefined,
  backendKey: string | null | undefined,
  budget = new ReadBudget(),
): Promise<BackendFingerprint | null> {
  if (!backendConfig) {
    return null;
  }
  const path = pythonPosixAbspath(backendConfig, process.cwd());
  const present = isFile(path);
  if (present) {
    return {
      key: backendKey ?? null,
      present,
      sha256: await fileSha256(path, budget),
    };
  }
  return { key: backendKey ?? null, present };
}

export async function varFileFingerprints(
  varFiles: readonly string[],
  budget = new ReadBudget(),
): Promise<FileFingerprint[]> {
  const sorted = [...varFiles].sort((left, right) => {
    return pythonStringCompare(path.basename(left), path.basename(right));
  });
  const out: FileFingerprint[] = [];
  for (const filePath of sorted) {
    if (isFile(filePath)) {
      out.push([path.basename(filePath), await fileSha256(filePath, budget)]);
    }
  }
  return out;
}

export async function capturePlanSourcesPayload(
  input: PlanFingerprintInput,
  budget = new ReadBudget(),
): Promise<PlanSourcesPayload> {
  return {
    backend: await backendFingerprint(
      input.backendConfig,
      input.backendKey,
      budget,
    ),
    member_types: sortedStrings(input.memberTypes),
    modules: await moduleFingerprints(input.envDir, input.memberTypes, budget),
    root_tf: await rootTfFingerprints(input.envDir, budget),
    var_files: await varFileFingerprints(input.varFiles, budget),
  };
}

export async function captureInitSourcesPayload(
  input: InitFingerprintInput,
  budget = new ReadBudget(),
): Promise<InitSourcesPayload> {
  return {
    backend: await backendFingerprint(
      input.backendConfig,
      input.backendKey,
      budget,
    ),
    modules: await moduleFingerprints(input.envDir, input.memberTypes, budget),
    root_config: await rootConfigFingerprints(input.envDir, budget),
  };
}

function encodePythonJsonString(value: string): string {
  return JSON.stringify(value).replace(/[\u007f-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function canonicalJson(value: CanonicalJson): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || Object.is(value, -0)) {
      throw new TypeError("fingerprint canonical JSON accepts safe integers only");
    }
    return String(value);
  }
  if (typeof value === "string") {
    return encodePythonJsonString(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJson(item)).join(",")}]`;
  }
  const objectValue = value as { readonly [key: string]: CanonicalJson | undefined };
  const fields = sortedStrings(Object.keys(objectValue)).map((key) => {
    const child = objectValue[key];
    if (child === undefined) {
      throw new TypeError("undefined is not a JSON value");
    }
    return `${encodePythonJsonString(key)}:${canonicalJson(child)}`;
  });
  return `{${fields.join(",")}}`;
}

/** Match json.dumps(payload, sort_keys=True, separators=(",", ":")). */
export function canonicalPlanSourcesJson(payload: PlanSourcesPayload): string {
  return canonicalJson(payload as unknown as CanonicalJson);
}

export function planSourcesSha256(payload: PlanSourcesPayload): string {
  return createHash("sha256")
    .update(canonicalPlanSourcesJson(payload), "utf8")
    .digest("hex");
}

export function initSourcesSha256(payload: InitSourcesPayload): string {
  return createHash("sha256")
    .update(canonicalJson(payload as unknown as CanonicalJson), "utf8")
    .digest("hex");
}

export async function planFingerprintV2(
  input: PlanFingerprintInput,
  budget = new ReadBudget(),
): Promise<PlanFingerprintV2> {
  return {
    version: PLAN_FINGERPRINT_VERSION,
    sha256: planSourcesSha256(await capturePlanSourcesPayload(input, budget)),
  };
}
