import { spawn, type ChildProcessByStdio } from "node:child_process";
import { constants } from "node:fs";
import { access, lstat, realpath } from "node:fs/promises";
import path from "node:path";
import type { Readable } from "node:stream";
import { types as utilTypes } from "node:util";

import { ProcessFailure } from "../domain/errors.js";

export interface TerraformCommandLimits {
  readonly timeoutMs: number | null;
  readonly maxStdoutBytes: number;
  readonly maxStderrBytes: number;
}

export const DEFAULT_TERRAFORM_COMMAND_LIMITS: TerraformCommandLimits = Object.freeze({
  timeoutMs: null,
  maxStdoutBytes: 8 * 1024 * 1024,
  maxStderrBytes: 1024 * 1024,
});

const MAX_TERRAFORM_COMMAND_STDOUT_BYTES = 8 * 1024 * 1024;
const MAX_TERRAFORM_COMMAND_STDERR_BYTES = 16 * 1024 * 1024;
const MAX_TERRAFORM_COMMAND_ARGUMENTS = 128;
const MAX_TERRAFORM_COMMAND_ARGUMENT_BYTES = 256 * 1024;
const MAX_TERRAFORM_COMMAND_ENVIRONMENT_ENTRIES = 256;
const MAX_TERRAFORM_COMMAND_ENVIRONMENT_BYTES = 256 * 1024;

function monotonicMilliseconds(): number {
  return Number(process.hrtime.bigint() / 1_000_000n);
}

export interface TerraformCommandBaseOptions {
  /** Trusted absolute Terraform binary selected by the process host. */
  readonly terraformExecutable: string;
  /** Fixed non-secret argv assembled by a typed operation, never shell text. */
  readonly argv: readonly string[];
  /** Resolved absolute working directory selected by the process host. */
  readonly cwd: string;
  /**
   * The complete child environment allowlist. It is never merged with
   * process.env, so inherited TF_* and credential variables cannot leak in.
   */
  readonly environment: Readonly<Record<string, string>>;
  readonly limits?: TerraformCommandLimits;
}

export interface TerraformCommandCaptureOptions
  extends TerraformCommandBaseOptions {
  readonly output: "capture";
}

export interface TerraformCommandDiscardOptions
  extends TerraformCommandBaseOptions {
  readonly output: "discard";
}

export interface TerraformCommandInheritOptions
  extends TerraformCommandBaseOptions {
  readonly output: "inherit";
}

export interface TerraformCommandInheritStderrOptions
  extends TerraformCommandBaseOptions {
  readonly output: "inherit-stderr";
}

export type TerraformCommandOptions =
  | TerraformCommandCaptureOptions
  | TerraformCommandDiscardOptions
  | TerraformCommandInheritOptions
  | TerraformCommandInheritStderrOptions;

export interface TerraformCommandCaptureResult {
  readonly kind: "captured";
  readonly stdout: Buffer;
}

export interface TerraformCommandDiscardResult {
  readonly kind: "discarded";
}

export interface TerraformCommandInheritResult {
  readonly kind: "inherited";
}

export type TerraformCommandResult =
  | TerraformCommandCaptureResult
  | TerraformCommandDiscardResult
  | TerraformCommandInheritResult;

const TERMINATION_SIGNALS = ["SIGTERM", "SIGINT", "SIGHUP"] as const;
type TerminationSignal = (typeof TERMINATION_SIGNALS)[number];
const activeTerraformProcessGroups = new Set<number>();
const terminationSignalHandlers = new Map<TerminationSignal, () => void>();
let exitHandlerInstalled = false;

function killPosixProcessGroup(pid: number): void {
  try {
    process.kill(-pid, "SIGKILL");
  } catch {
    // The isolated process group may already be empty.
  }
}

function killActiveTerraformProcessGroups(): void {
  for (const pid of activeTerraformProcessGroups) {
    killPosixProcessGroup(pid);
  }
}

function removeTerminationHandlers(): void {
  for (const [signal, handler] of terminationSignalHandlers) {
    process.removeListener(signal, handler);
  }
  terminationSignalHandlers.clear();
  if (exitHandlerInstalled) {
    process.removeListener("exit", killActiveTerraformProcessGroups);
    exitHandlerInstalled = false;
  }
}

function installTerminationHandlers(): void {
  if (process.platform === "win32" || terminationSignalHandlers.size > 0) return;
  for (const signal of TERMINATION_SIGNALS) {
    const handler = (): void => {
      killActiveTerraformProcessGroups();
      activeTerraformProcessGroups.clear();
      removeTerminationHandlers();
      process.kill(process.pid, signal);
    };
    terminationSignalHandlers.set(signal, handler);
    process.once(signal, handler);
  }
  process.once("exit", killActiveTerraformProcessGroups);
  exitHandlerInstalled = true;
}

function registerTerraformProcessGroup(pid: number | undefined): () => void {
  if (process.platform === "win32" || pid === undefined) return () => undefined;
  activeTerraformProcessGroups.add(pid);
  installTerminationHandlers();
  let registered = true;
  return () => {
    if (!registered) return;
    registered = false;
    activeTerraformProcessGroups.delete(pid);
    if (activeTerraformProcessGroups.size === 0) removeTerminationHandlers();
  };
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

/** Validate and detach command bounds before any asynchronous inspection. */
export function snapshotTerraformCommandLimits(
  value: TerraformCommandLimits,
): TerraformCommandLimits {
  if (
    value === null
    || typeof value !== "object"
    || utilTypes.isProxy(value)
  ) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_LIMIT",
      "Terraform command limits are outside the allowed range",
    );
  }
  const timeoutDescriptor = Object.getOwnPropertyDescriptor(
    value,
    "timeoutMs",
  );
  const stdoutDescriptor = Object.getOwnPropertyDescriptor(
    value,
    "maxStdoutBytes",
  );
  const stderrDescriptor = Object.getOwnPropertyDescriptor(
    value,
    "maxStderrBytes",
  );
  if (
    timeoutDescriptor === undefined
    || !("value" in timeoutDescriptor)
    || stdoutDescriptor === undefined
    || !("value" in stdoutDescriptor)
    || stderrDescriptor === undefined
    || !("value" in stderrDescriptor)
  ) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_LIMIT",
      "Terraform command limits are outside the allowed range",
    );
  }
  const limits: TerraformCommandLimits = {
    timeoutMs: timeoutDescriptor.value as number | null,
    maxStdoutBytes: stdoutDescriptor.value as number,
    maxStderrBytes: stderrDescriptor.value as number,
  };
  if (
    (limits.timeoutMs !== null
      && (!Number.isSafeInteger(limits.timeoutMs) || limits.timeoutMs <= 0))
    || !Number.isSafeInteger(limits.maxStdoutBytes)
    || limits.maxStdoutBytes <= 0
    || limits.maxStdoutBytes > MAX_TERRAFORM_COMMAND_STDOUT_BYTES
    || !Number.isSafeInteger(limits.maxStderrBytes)
    || limits.maxStderrBytes <= 0
    || limits.maxStderrBytes > MAX_TERRAFORM_COMMAND_STDERR_BYTES
  ) {
    fail(
      "INVALID_TERRAFORM_COMMAND_LIMIT",
      "Terraform command limits are outside the allowed range",
    );
  }
  return Object.freeze(limits);
}

/**
 * Resolve CLI/TF overrides using host path semantics without mistaking a
 * Windows absolute path for a PATH lookup merely because the host separator
 * differs. Exported for focused portability tests and other typed adapters.
 */
export function terraformExecutableCandidates(
  selected: string | undefined,
  environment: NodeJS.ProcessEnv,
  options?: { readonly cwd?: string; readonly platform?: NodeJS.Platform },
): string[] {
  const requested = selected && selected.length > 0 ? selected : "terraform";
  if (requested.includes("\0")) {
    return fail(
      "UNRESOLVED_TERRAFORM_COMMAND_PATH",
      "Terraform executable path contains an embedded null character",
    );
  }
  const platform = options?.platform ?? process.platform;
  const pathModule = platform === "win32" ? path.win32 : path.posix;
  const cwd = options?.cwd ?? process.cwd();
  const explicit = path.posix.isAbsolute(requested)
    || path.win32.isAbsolute(requested)
    || requested.includes("/")
    || (platform === "win32" && requested.includes("\\"));
  if (explicit) {
    if (path.win32.isAbsolute(requested) && platform !== "win32") {
      return [requested];
    }
    return [pathModule.resolve(cwd, requested)];
  }

  const pathValue = environment.PATH ?? environment.Path ?? environment.path ?? "";
  const delimiter = platform === "win32" ? ";" : ":";
  const directories = pathValue.split(delimiter).filter((entry) => entry.length > 0);
  const names = platform === "win32" && path.win32.extname(requested) === ""
    ? (environment.PATHEXT ?? ".COM;.EXE;.BAT;.CMD")
      .split(";")
      .filter((extension) => extension.length > 0)
      .map((extension) => `${requested}${extension.toLowerCase()}`)
    : [requested];
  return directories.flatMap((directory) => {
    return names.map((name) => pathModule.resolve(directory, name));
  });
}

/** Resolve and validate the Terraform executable selected by CLI then TF. */
export async function resolveTerraformExecutable(
  selected: string | undefined,
  environment: NodeJS.ProcessEnv,
): Promise<string> {
  const requested = selected && selected.length > 0 ? selected : "terraform";
  for (const candidate of terraformExecutableCandidates(selected, environment)) {
    try {
      await access(candidate, constants.X_OK);
      const resolved = await realpath(candidate);
      const metadata = await lstat(resolved);
      if (metadata.isFile() && (process.platform === "win32" || (metadata.mode & 0o111) !== 0)) {
        return resolved;
      }
    } catch {
      // Continue searching PATH candidates.
    }
  }
  throw new Error(`unable to resolve Terraform executable ${JSON.stringify(requested)}`);
}

function snapshotArgv(value: readonly string[]): string[] {
  if (
    !Array.isArray(value)
    || utilTypes.isProxy(value)
    || value.length > MAX_TERRAFORM_COMMAND_ARGUMENTS
  ) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
      "Terraform command arguments are not allowed",
    );
  }
  const result: string[] = [];
  let totalBytes = 0;
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || descriptor.value.includes("\0")
    ) {
      return fail(
        "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
        "Terraform command arguments are not allowed",
      );
    }
    totalBytes += Buffer.byteLength(descriptor.value);
    if (totalBytes > MAX_TERRAFORM_COMMAND_ARGUMENT_BYTES) {
      return fail(
        "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
        "Terraform command arguments are not allowed",
      );
    }
    result.push(descriptor.value);
  }
  return result;
}

/** Validate and detach the exact child environment before asynchronous work. */
export function snapshotTerraformCommandEnvironment(
  value: Readonly<Record<string, string>>,
): Readonly<Record<string, string>> {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || utilTypes.isProxy(value)
  ) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
      "Terraform command environment is not allowed",
    );
  }
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
      "Terraform command environment is not allowed",
    );
  }
  const keys = Reflect.ownKeys(value);
  if (keys.length > MAX_TERRAFORM_COMMAND_ENVIRONMENT_ENTRIES) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
      "Terraform command environment is not allowed",
    );
  }
  const result = Object.create(null) as Record<string, string>;
  let totalBytes = 0;
  for (const key of keys) {
    if (
      typeof key !== "string"
      || key.length === 0
      || key.includes("=")
      || key.includes("\0")
    ) {
      return fail(
        "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
        "Terraform command environment is not allowed",
      );
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || descriptor.value.includes("\0")
    ) {
      return fail(
        "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
        "Terraform command environment is not allowed",
      );
    }
    totalBytes += Buffer.byteLength(key) + Buffer.byteLength(descriptor.value);
    if (totalBytes > MAX_TERRAFORM_COMMAND_ENVIRONMENT_BYTES) {
      return fail(
        "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
        "Terraform command environment is not allowed",
      );
    }
    result[key] = descriptor.value;
  }
  return Object.freeze(result);
}

async function requireTrustedExecutable(filePath: string): Promise<void> {
  try {
    const metadata = await lstat(filePath);
    if (
      !metadata.isFile()
      || metadata.isSymbolicLink()
      || (process.platform !== "win32" && (metadata.mode & 0o111) === 0)
    ) {
      fail(
        "UNTRUSTED_TERRAFORM_EXECUTABLE",
        "trusted Terraform executable is not an allowed regular file",
        "io",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    fail(
      "UNTRUSTED_TERRAFORM_EXECUTABLE",
      "unable to inspect trusted Terraform executable",
      "io",
    );
  }
}

export function runTerraformCommand(
  options: TerraformCommandCaptureOptions,
): Promise<TerraformCommandCaptureResult>;
export function runTerraformCommand(
  options: TerraformCommandDiscardOptions,
): Promise<TerraformCommandDiscardResult>;
export function runTerraformCommand(
  options: TerraformCommandInheritOptions,
): Promise<TerraformCommandInheritResult>;
export function runTerraformCommand(
  options: TerraformCommandInheritStderrOptions,
): Promise<TerraformCommandInheritResult>;
export function runTerraformCommand(
  options: TerraformCommandOptions,
): Promise<TerraformCommandResult>;
/**
 * Run one bounded Terraform process without a shell or inherited environment.
 * Child output is counted and either captured, discarded, or streamed; it
 * never enters a structured failure.
 * Terraform and its provider executables are trusted: on POSIX the runner
 * kills their isolated process group, but a deliberately daemonized descendant
 * that creates a new session is outside this in-process containment boundary.
 */
export async function runTerraformCommand(
  options: TerraformCommandOptions,
): Promise<TerraformCommandResult> {
  const terraformExecutable = options.terraformExecutable;
  const cwd = options.cwd;
  const outputMode = options.output;
  if (
    typeof terraformExecutable !== "string"
    || typeof cwd !== "string"
    || terraformExecutable.includes("\0")
    || cwd.includes("\0")
    || !path.isAbsolute(terraformExecutable)
    || !path.isAbsolute(cwd)
  ) {
    return fail(
      "UNRESOLVED_TERRAFORM_COMMAND_PATH",
      "Terraform command requires resolved absolute paths",
    );
  }
  if (
    outputMode !== "capture"
    && outputMode !== "discard"
    && outputMode !== "inherit"
    && outputMode !== "inherit-stderr"
  ) {
    return fail(
      "INVALID_TERRAFORM_COMMAND_OUTPUT",
      "Terraform command output mode is not allowed",
    );
  }
  const limits = snapshotTerraformCommandLimits(
    options.limits ?? DEFAULT_TERRAFORM_COMMAND_LIMITS,
  );
  const argv = snapshotArgv(options.argv);
  const environment = snapshotTerraformCommandEnvironment(options.environment);
  const startedAt = monotonicMilliseconds();
  await requireTrustedExecutable(terraformExecutable);
  if (
    limits.timeoutMs !== null
    && monotonicMilliseconds() - startedAt >= limits.timeoutMs
  ) {
    return fail(
      "TERRAFORM_COMMAND_TIMEOUT",
      "Terraform command exceeded its execution deadline",
      "io",
    );
  }

  return new Promise<TerraformCommandResult>((resolve, reject) => {
    const detached = process.platform !== "win32";
    let child: ChildProcessByStdio<null, Readable, Readable>;
    try {
      child = spawn(terraformExecutable, argv, {
        cwd,
        detached,
        env: environment,
        shell: false,
        stdio: ["ignore", "pipe", "pipe"],
        windowsHide: true,
      });
    } catch {
      reject(new ProcessFailure({
        code: "TERRAFORM_COMMAND_SPAWN_FAILED",
        category: "io",
        message: "unable to start Terraform command",
      }));
      return;
    }
    const unregisterProcessGroup = registerTerraformProcessGroup(child.pid);

    const output = outputMode === "capture"
      ? Buffer.allocUnsafe(limits.maxStdoutBytes)
      : null;
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let terminalFailure: ProcessFailure | null = null;
    let closed = false;

    const killProcessTree = (): void => {
      if (detached && child.pid !== undefined) {
        try {
          process.kill(-child.pid, "SIGKILL");
          return;
        } catch {
          // The isolated process group may already be empty.
        }
      }
      try {
        child.kill("SIGKILL");
      } catch {
        // The direct child may already have exited.
      }
    };
    const terminate = (failure: ProcessFailure): void => {
      if (terminalFailure === null) {
        terminalFailure = failure;
        killProcessTree();
      }
    };
    let timer: NodeJS.Timeout | null = null;
    const armTimeout = (): void => {
      if (limits.timeoutMs === null || terminalFailure !== null || closed) {
        return;
      }
      const remaining = limits.timeoutMs - (monotonicMilliseconds() - startedAt);
      if (remaining <= 0) {
        terminate(new ProcessFailure({
          code: "TERRAFORM_COMMAND_TIMEOUT",
          category: "io",
          message: "Terraform command exceeded its execution deadline",
        }));
        return;
      }
      // Node clamps larger delays to 1 ms. Chunk long practical deadlines so
      // they cannot accidentally fire immediately through timer overflow.
      timer = setTimeout(armTimeout, Math.max(1, Math.min(remaining, 2_147_483_647)));
    };
    armTimeout();

    const streamOutput = (
      source: Readable,
      destination: NodeJS.WriteStream,
      chunk: Buffer,
      enabled: boolean,
    ): void => {
      if (!enabled) return;
      if (!destination.write(chunk)) {
        source.pause();
        destination.once("drain", () => source.resume());
      }
    };

    child.stdout.on("data", (chunk: Buffer) => {
      if (chunk.length > limits.maxStdoutBytes - stdoutBytes) {
        terminate(new ProcessFailure({
          code: "TERRAFORM_COMMAND_STDOUT_LIMIT",
          category: "io",
          message: "Terraform command exceeded its output limit",
        }));
        return;
      }
      if (output !== null) {
        chunk.copy(output, stdoutBytes);
      }
      stdoutBytes += chunk.length;
      streamOutput(child.stdout, process.stdout, chunk, outputMode === "inherit");
    });
    child.stderr.on("data", (chunk: Buffer) => {
      if (chunk.length > limits.maxStderrBytes - stderrBytes) {
        terminate(new ProcessFailure({
          code: "TERRAFORM_COMMAND_STDERR_LIMIT",
          category: "io",
          message: "Terraform command exceeded its diagnostic-output limit",
        }));
        return;
      }
      stderrBytes += chunk.length;
      streamOutput(
        child.stderr,
        process.stderr,
        chunk,
        outputMode === "inherit" || outputMode === "inherit-stderr",
      );
    });
    child.stdout.on("error", () => {
      terminate(new ProcessFailure({
        code: "TERRAFORM_COMMAND_STDOUT_FAILED",
        category: "io",
        message: "unable to read Terraform command output",
      }));
    });
    child.stderr.on("error", () => {
      terminate(new ProcessFailure({
        code: "TERRAFORM_COMMAND_STDERR_FAILED",
        category: "io",
        message: "unable to read Terraform command diagnostic output",
      }));
    });
    child.on("error", () => {
      terminate(new ProcessFailure({
        code: "TERRAFORM_COMMAND_SPAWN_FAILED",
        category: "io",
        message: "unable to start Terraform command",
      }));
    });
    // `close` waits for inherited pipes. Reap the isolated group as soon as
    // the direct process exits so a successful descendant cannot hold them.
    child.on("exit", () => {
      killProcessTree();
    });
    child.on("close", (code) => {
      if (closed) {
        return;
      }
      closed = true;
      if (timer !== null) clearTimeout(timer);
      killProcessTree();
      unregisterProcessGroup();
      if (terminalFailure !== null) {
        output?.fill(0);
        reject(terminalFailure);
      } else if (code !== 0) {
        output?.fill(0);
        reject(new ProcessFailure({
          code: "TERRAFORM_COMMAND_FAILED",
          category: "domain",
          message: "Terraform command did not complete successfully",
        }));
      } else if (outputMode === "inherit" || outputMode === "inherit-stderr") {
        resolve({ kind: "inherited" });
      } else if (output === null) {
        resolve({ kind: "discarded" });
      } else {
        const stdout = Buffer.from(output.subarray(0, stdoutBytes));
        output.fill(0);
        resolve({ kind: "captured", stdout });
      }
    });
  });
}
