import { createHash } from "node:crypto";
import {
  constants,
  lstat,
  mkdir,
  mkdtemp,
  open,
  realpath,
  rm,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";
import { performance } from "node:perf_hooks";
import { types as utilTypes } from "node:util";

import {
  type ZccAdoptionOracleAdapters,
  type ZccAdoptionOracleCommandRequest,
  type ZccAdoptionOracleShowRequest,
  type ZccAdoptionOracleWriteRequest,
} from "../domain/zcc-adoption-oracle.js";
import { ProcessFailure } from "../domain/errors.js";
import {
  DEFAULT_TERRAFORM_COMMAND_LIMITS,
  runTerraformCommand,
} from "./terraform-command.js";
import {
  DEFAULT_TERRAFORM_SHOW_LIMITS,
  terraformShowPlan,
} from "./terraform-show.js";
import { sameStringSequence } from "../json/python-compatible.js";

const SCRATCH_PREFIX = "infrawright-zcc-oracle-";
const MAX_SCRATCH_FILE_BYTES = 256 * 1024 * 1024;
const MAX_TEXT_FILE_BYTES = 4 * 1024 * 1024;
const HASH_CHUNK_BYTES = 64 * 1024;

/** Fixed host policy; neither the private oracle request nor factory selects it. */
export const ZCC_ADOPTION_ORACLE_TRANSACTION_TIMEOUT_MS = 300_000;
/** Cleanup is outside the transaction deadline and gets one bounded final window. */
export const ZCC_ADOPTION_ORACLE_CLEANUP_TIMEOUT_MS = 30_000;

export const ZCC_ADOPTION_ORACLE_HOST_ENVIRONMENT_NAMES = Object.freeze([
  "ZSCALER_CLIENT_ID",
  "ZSCALER_CLIENT_SECRET",
  "ZSCALER_PRIVATE_KEY",
  "ZSCALER_VANITY_DOMAIN",
  "ZSCALER_CLOUD",
  "ZSCALER_HTTP_PROXY",
  "HTTP_PROXY",
  "HTTPS_PROXY",
  "NO_PROXY",
  "SSL_CERT_FILE",
  "SSL_CERT_DIR",
] as const);

const SUPPLIED_ENVIRONMENT_NAMES = new Set<string>(
  ZCC_ADOPTION_ORACLE_HOST_ENVIRONMENT_NAMES,
);

const FACTORY_OPTION_NAMES = new Set([
  "terraformExecutable",
  "tempRoot",
  "environment",
]);

type AdapterPhase = "fresh" | "creating" | "active" | "spent";

export interface ZccAdoptionOracleAdapterFactoryOptions {
  /** Trusted canonical absolute Terraform binary selected by the process host. */
  readonly terraformExecutable: string;
  /** Existing canonical absolute private directory owned by this process user. */
  readonly tempRoot: string;
  /** Complete caller-supplied provider/proxy/certificate environment allowlist. */
  readonly environment: Readonly<Record<string, string>>;
}

interface FrozenFactoryOptions {
  readonly terraformExecutable: string;
  readonly tempRoot: string;
  readonly environment: Readonly<Record<string, string>>;
}

interface DirectoryBinding {
  readonly absolutePath: string;
  readonly dev: bigint;
  readonly ino: bigint;
  readonly uid: bigint;
  readonly mode: bigint;
}

interface FileMetadata {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly uid: bigint;
  readonly mode: bigint;
  readonly size: bigint;
  readonly nlink: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}

interface FileBinding {
  readonly absolutePath: string;
  readonly metadata: FileMetadata;
  readonly sha256: string;
}

type ProtectionFailureCode =
  | "ZCC_ORACLE_COMMAND_PROTECTION_FAILED"
  | "ZCC_ORACLE_SHOW_PROTECTION_FAILED"
  | "ZCC_ORACLE_FINAL_PROTECTION_FAILED";

interface ProtectedWorkOutcome {
  readonly error: unknown;
  readonly failed: boolean;
  readonly timeout: ProcessFailure | null;
}

interface TransactionPaths {
  readonly directory: string;
  readonly generatedConfig: string;
  readonly home: string;
  readonly imports: string;
  readonly lock: string;
  readonly plan: string;
  readonly root: string;
  readonly state: string;
  readonly terraformData: string;
  readonly temporary: string;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "io",
): never {
  throw new ProcessFailure({ code, category, message });
}

function errorCode(error: unknown): string | undefined {
  if (error === null || typeof error !== "object") {
    return undefined;
  }
  const descriptor = Object.getOwnPropertyDescriptor(error, "code");
  return descriptor !== undefined && "value" in descriptor
    && typeof descriptor.value === "string"
    ? descriptor.value
    : undefined;
}

function dataProperty(
  value: object,
  name: string,
  required: boolean,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, name);
  if (descriptor === undefined) {
    if (!required) {
      return undefined;
    }
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_OPTIONS",
      "ZCC oracle adapter options are incomplete",
      "domain",
    );
  }
  if (!descriptor.enumerable || !("value" in descriptor)) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_OPTIONS",
      "ZCC oracle adapter options are not plain data",
      "domain",
    );
  }
  return descriptor.value;
}

function plainRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || utilTypes.isProxy(value)
  ) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function validString(value: unknown): value is string {
  return typeof value === "string"
    && value.length > 0
    && !value.includes("\0")
    && value.isWellFormed();
}

function snapshotEnvironment(value: unknown): Readonly<Record<string, string>> {
  if (!plainRecord(value)) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_ENVIRONMENT",
      "ZCC oracle adapter environment is not an allowed plain record",
      "domain",
    );
  }
  const keys = Reflect.ownKeys(value);
  const result = Object.create(null) as Record<string, string>;
  let totalBytes = 0;
  for (const key of keys) {
    if (typeof key !== "string" || !SUPPLIED_ENVIRONMENT_NAMES.has(key)) {
      return fail(
        "INVALID_ZCC_ORACLE_ADAPTER_ENVIRONMENT",
        "ZCC oracle adapter environment contains an unsupported name",
        "domain",
      );
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || descriptor.value.includes("\0")
      || !descriptor.value.isWellFormed()
    ) {
      return fail(
        "INVALID_ZCC_ORACLE_ADAPTER_ENVIRONMENT",
        "ZCC oracle adapter environment contains an unsupported value",
        "domain",
      );
    }
    totalBytes += Buffer.byteLength(key) + Buffer.byteLength(descriptor.value);
    if (totalBytes > 128 * 1024) {
      return fail(
        "INVALID_ZCC_ORACLE_ADAPTER_ENVIRONMENT",
        "ZCC oracle adapter environment exceeds its private input limit",
        "domain",
      );
    }
    result[key] = descriptor.value;
  }
  return Object.freeze(result);
}

function snapshotOptions(
  value: ZccAdoptionOracleAdapterFactoryOptions,
): FrozenFactoryOptions {
  if (!plainRecord(value)) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_OPTIONS",
      "ZCC oracle adapter options are not an allowed plain record",
      "domain",
    );
  }
  if (
    Reflect.ownKeys(value).some(
      (key) => typeof key !== "string" || !FACTORY_OPTION_NAMES.has(key),
    )
  ) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_OPTIONS",
      "ZCC oracle adapter options contain an unsupported field",
      "domain",
    );
  }
  const terraformExecutable = dataProperty(value, "terraformExecutable", true);
  const tempRoot = dataProperty(value, "tempRoot", true);
  const environment = snapshotEnvironment(dataProperty(value, "environment", true));
  if (
    !validString(terraformExecutable)
    || !path.isAbsolute(terraformExecutable)
    || path.resolve(terraformExecutable) !== terraformExecutable
    || !validString(tempRoot)
    || !path.isAbsolute(tempRoot)
    || path.resolve(tempRoot) !== tempRoot
    || path.parse(tempRoot).root === tempRoot
  ) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_OPTIONS",
      "ZCC oracle adapter paths must be canonical absolute paths",
      "domain",
    );
  }
  return Object.freeze({
    terraformExecutable,
    tempRoot,
    environment,
  });
}

function directoryBinding(
  absolutePath: string,
  metadata: Awaited<ReturnType<typeof lstat>>,
): DirectoryBinding {
  const bigint = metadata as unknown as {
    dev: bigint;
    ino: bigint;
    uid: bigint;
    mode: bigint;
  };
  return {
    absolutePath,
    dev: bigint.dev,
    ino: bigint.ino,
    uid: bigint.uid,
    mode: bigint.mode,
  };
}

function fileMetadata(
  metadata: Awaited<ReturnType<FileHandle["stat"]>>,
): FileMetadata {
  const bigint = metadata as unknown as {
    dev: bigint;
    ino: bigint;
    uid: bigint;
    mode: bigint;
    size: bigint;
    nlink: bigint;
    mtimeNs: bigint;
    ctimeNs: bigint;
  };
  return {
    dev: bigint.dev,
    ino: bigint.ino,
    uid: bigint.uid,
    mode: bigint.mode,
    size: bigint.size,
    nlink: bigint.nlink,
    mtimeNs: bigint.mtimeNs,
    ctimeNs: bigint.ctimeNs,
  };
}

function sameDirectory(left: DirectoryBinding, right: DirectoryBinding): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.uid === right.uid;
}

function sameFileMetadata(left: FileMetadata, right: FileMetadata): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.uid === right.uid
    && left.mode === right.mode
    && left.size === right.size
    && left.nlink === right.nlink
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

function processUid(): bigint | null {
  return typeof process.getuid === "function" ? BigInt(process.getuid()) : null;
}

async function bindDirectory(
  absolutePath: string,
  privateMode: "root" | "exact-0700" | "trusted",
): Promise<DirectoryBinding> {
  try {
    const [canonical, metadata] = await Promise.all([
      realpath(absolutePath),
      lstat(absolutePath, { bigint: true }),
    ]);
    const uid = processUid();
    if (
      canonical !== absolutePath
      || !metadata.isDirectory()
      || metadata.isSymbolicLink()
      || (uid !== null && privateMode !== "trusted" && metadata.uid !== uid)
      || (
        privateMode === "root"
        && (metadata.mode & 0o077n) !== 0n
      )
      || (
        privateMode === "exact-0700"
        && (metadata.mode & 0o777n) !== 0o700n
      )
    ) {
      return fail(
        "UNSAFE_ZCC_ORACLE_DIRECTORY",
        "ZCC oracle directory authority is not safe",
      );
    }
    return directoryBinding(absolutePath, metadata);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "UNSAFE_ZCC_ORACLE_DIRECTORY",
      "ZCC oracle directory authority could not be verified",
    );
  }
}

async function recheckDirectory(
  binding: DirectoryBinding,
  exact0700: boolean,
): Promise<void> {
  try {
    const metadata = await lstat(binding.absolutePath, { bigint: true });
    const current = directoryBinding(binding.absolutePath, metadata);
    if (
      !metadata.isDirectory()
      || metadata.isSymbolicLink()
      || !sameDirectory(binding, current)
      || (exact0700 && (metadata.mode & 0o777n) !== 0o700n)
      || (!exact0700 && (metadata.mode & 0o077n) !== 0n)
    ) {
      return fail(
        "ZCC_ORACLE_DIRECTORY_CHANGED",
        "ZCC oracle directory authority changed during the transaction",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "ZCC_ORACLE_DIRECTORY_CHANGED",
      "ZCC oracle directory authority could not be rechecked",
    );
  }
}

async function forceDirectoryMode(absolutePath: string): Promise<void> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      absolutePath,
      constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_DIRECTORY,
    );
    await handle.chmod(0o700);
    await handle.sync();
  } catch {
    return fail(
      "ZCC_ORACLE_DIRECTORY_CREATE_FAILED",
      "ZCC oracle private directory could not be secured",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function validateExecutable(absolutePath: string): Promise<void> {
  try {
    const [canonical, metadata] = await Promise.all([
      realpath(absolutePath),
      lstat(absolutePath),
    ]);
    if (
      canonical !== absolutePath
      || !metadata.isFile()
      || metadata.isSymbolicLink()
      || (metadata.mode & 0o111) === 0
    ) {
      return fail(
        "UNTRUSTED_ZCC_ORACLE_EXECUTABLE",
        "ZCC oracle Terraform executable is not trusted",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "UNTRUSTED_ZCC_ORACLE_EXECUTABLE",
      "ZCC oracle Terraform executable could not be verified",
    );
  }
}

async function hashHandle(handle: FileHandle, size: number): Promise<string> {
  const digest = createHash("sha256");
  let offset = 0;
  while (offset < size) {
    const length = Math.min(HASH_CHUNK_BYTES, size - offset);
    const chunk = Buffer.allocUnsafe(length);
    const result = await handle.read(chunk, 0, length, offset);
    if (result.bytesRead !== length) {
      return fail(
        "ZCC_ORACLE_FILE_CHANGED",
        "a protected ZCC oracle file changed while it was verified",
      );
    }
    digest.update(chunk);
    chunk.fill(0);
    offset += length;
  }
  const tail = Buffer.allocUnsafe(1);
  const result = await handle.read(tail, 0, 1, size);
  tail.fill(0);
  if (result.bytesRead !== 0) {
    return fail(
      "ZCC_ORACLE_FILE_CHANGED",
      "a protected ZCC oracle file changed while it was verified",
    );
  }
  return digest.digest("hex");
}

async function inspectFile(
  absolutePath: string,
  forcePrivateMode: boolean,
): Promise<FileBinding> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      absolutePath,
      constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW,
    );
    if (forcePrivateMode) {
      await handle.chmod(0o600);
      await handle.sync();
    }
    const beforeStat = await handle.stat({ bigint: true });
    const before = fileMetadata(beforeStat);
    if (
      !beforeStat.isFile()
      || beforeStat.isSymbolicLink()
      || (before.mode & 0o777n) !== 0o600n
      || before.nlink !== 1n
      || before.size < 0n
      || before.size > BigInt(MAX_SCRATCH_FILE_BYTES)
      || before.size > BigInt(Number.MAX_SAFE_INTEGER)
      || (processUid() !== null && before.uid !== processUid())
    ) {
      return fail(
        "UNSAFE_ZCC_ORACLE_FILE",
        "a protected ZCC oracle file is not safe",
      );
    }
    const sha256 = await hashHandle(handle, Number(before.size));
    const [afterStat, pathStat] = await Promise.all([
      handle.stat({ bigint: true }),
      lstat(absolutePath, { bigint: true }),
    ]);
    const after = fileMetadata(afterStat);
    const atPath = fileMetadata(pathStat);
    if (
      !pathStat.isFile()
      || pathStat.isSymbolicLink()
      || !sameFileMetadata(before, after)
      || !sameFileMetadata(before, atPath)
    ) {
      return fail(
        "ZCC_ORACLE_FILE_CHANGED",
        "a protected ZCC oracle file changed while it was verified",
      );
    }
    return { absolutePath, metadata: before, sha256 };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "UNSAFE_ZCC_ORACLE_FILE",
      "a protected ZCC oracle file could not be verified",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function recheckFile(binding: FileBinding): Promise<void> {
  const current = await inspectFile(binding.absolutePath, false);
  if (
    !sameFileMetadata(binding.metadata, current.metadata)
    || binding.sha256 !== current.sha256
  ) {
    return fail(
      "ZCC_ORACLE_FILE_CHANGED",
      "a protected ZCC oracle file changed during the transaction",
    );
  }
}

function transactionPaths(directory: string): TransactionPaths {
  return Object.freeze({
    directory,
    generatedConfig: path.join(directory, "generated.tf"),
    home: path.join(directory, ".home"),
    imports: path.join(directory, "imports.tf"),
    lock: path.join(directory, ".terraform.lock.hcl"),
    plan: path.join(directory, "oracle.tfplan"),
    root: path.join(directory, "main.tf"),
    state: path.join(directory, "terraform.tfstate"),
    terraformData: path.join(directory, ".terraform-data"),
    temporary: path.join(directory, ".tmp"),
  });
}

function snapshotStringArray(
  value: unknown,
  maxEntries = 32,
  maxBytes = 256 * 1024,
): readonly string[] {
  if (
    !Array.isArray(value)
    || utilTypes.isProxy(value)
    || value.length > maxEntries
  ) {
    return fail(
      "INVALID_ZCC_ORACLE_ADAPTER_REQUEST",
      "ZCC oracle adapter request contains an unsupported list",
      "domain",
    );
  }
  const result: string[] = [];
  let bytes = 0;
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || descriptor.value.includes("\0")
      || !descriptor.value.isWellFormed()
    ) {
      return fail(
        "INVALID_ZCC_ORACLE_ADAPTER_REQUEST",
        "ZCC oracle adapter request contains an unsupported list value",
        "domain",
      );
    }
    bytes += Buffer.byteLength(descriptor.value);
    if (bytes > maxBytes) {
      return fail(
        "INVALID_ZCC_ORACLE_ADAPTER_REQUEST",
        "ZCC oracle adapter request exceeds its private input limit",
        "domain",
      );
    }
    result.push(descriptor.value);
  }
  return Object.freeze(result);
}

function expectedCommandArgv(
  stage: ZccAdoptionOracleCommandRequest["stage"],
  paths: TransactionPaths,
): readonly string[] {
  if (stage === "init") {
    return [
      "init",
      "-backend=false",
      "-input=false",
      "-no-color",
      "-lockfile=readonly",
    ];
  }
  if (stage === "plan") {
    return [
      "plan",
      "-input=false",
      "-no-color",
      "-lock=false",
      `-generate-config-out=${paths.generatedConfig}`,
      `-out=${paths.plan}`,
    ];
  }
  return [
    "apply",
    "-input=false",
    "-no-color",
    "-lock=false",
    paths.plan,
  ];
}

function expectedProtectedPaths(
  stage: ZccAdoptionOracleCommandRequest["stage"]
    | ZccAdoptionOracleShowRequest["stage"],
  paths: TransactionPaths,
): readonly string[] {
  if (stage === "init" || stage === "plan") {
    return [paths.root, paths.imports, paths.lock];
  }
  if (stage === "show-plan" || stage === "apply") {
    return [
      paths.root,
      paths.imports,
      paths.lock,
      paths.generatedConfig,
      paths.plan,
    ];
  }
  return [
    paths.root,
    paths.imports,
    paths.lock,
    paths.generatedConfig,
    paths.plan,
    paths.state,
  ];
}

function containedDirectFile(candidate: string, paths: TransactionPaths): boolean {
  return path.isAbsolute(candidate)
    && path.resolve(candidate) === candidate
    && path.dirname(candidate) === paths.directory;
}

async function writeAll(handle: FileHandle, bytes: Buffer): Promise<void> {
  let offset = 0;
  while (offset < bytes.length) {
    const result = await handle.write(bytes, offset, bytes.length - offset, offset);
    if (result.bytesWritten <= 0) {
      return fail(
        "ZCC_ORACLE_WRITE_FAILED",
        "ZCC oracle scratch input could not be written",
      );
    }
    offset += result.bytesWritten;
  }
}

/**
 * Build a single-use private effect boundary for one ZCC adoption transaction.
 * No caller or inherited environment is merged into Terraform's child process.
 */
export function createZccAdoptionOracleAdapters(
  unsafeOptions: ZccAdoptionOracleAdapterFactoryOptions,
): ZccAdoptionOracleAdapters {
  const options = snapshotOptions(unsafeOptions);
  let phase: AdapterPhase = "fresh";
  let tempRootBinding: DirectoryBinding | null = null;
  let transactionBinding: DirectoryBinding | null = null;
  let transactionDeadline: number | null = null;
  let paths: TransactionPaths | null = null;
  let environment: Readonly<Record<string, string>> | null = null;
  const privateDirectories: DirectoryBinding[] = [];
  const files = new Map<string, FileBinding>();

  const requireActive = (): {
    readonly paths: TransactionPaths;
    readonly environment: Readonly<Record<string, string>>;
  } => {
    if (phase !== "active" || paths === null || environment === null) {
      return fail(
        "ZCC_ORACLE_ADAPTER_ALREADY_USED",
        "ZCC oracle adapter factory is single-use",
        "domain",
      );
    }
    return { paths, environment };
  };

  const recheckAuthorities = async (): Promise<void> => {
    if (tempRootBinding === null || transactionBinding === null) {
      return fail(
        "ZCC_ORACLE_ADAPTER_NOT_ACTIVE",
        "ZCC oracle adapter transaction is not active",
        "domain",
      );
    }
    await recheckDirectory(tempRootBinding, false);
    await recheckDirectory(transactionBinding, true);
    for (const directory of privateDirectories) {
      await recheckDirectory(directory, true);
    }
  };

  const transactionTimeout = (): ProcessFailure => new ProcessFailure({
    code: "ZCC_ADOPTION_ORACLE_TIMEOUT",
    category: "io",
    message: "ZCC adoption oracle transaction exceeded its execution deadline",
  });

  const withProtectionFailure = (
    primary: ProcessFailure,
    code: ProtectionFailureCode,
    message: string,
  ): ProcessFailure => new ProcessFailure({
    code: primary.code,
    category: primary.category,
    message: primary.message,
    retryable: primary.retryable,
    details: [{
      path: "protection",
      code,
      message,
    }],
  });

  const transactionRemaining = (): number => {
    if (transactionDeadline === null) {
      return fail(
        "ZCC_ORACLE_ADAPTER_NOT_ACTIVE",
        "ZCC oracle adapter transaction is not active",
        "domain",
      );
    }
    return Math.floor(transactionDeadline - performance.now());
  };

  const observedTransactionTimeout = (): ProcessFailure | null => {
    return transactionRemaining() <= 0 ? transactionTimeout() : null;
  };

  const remainingTransactionBudget = (): number => {
    const remaining = transactionRemaining();
    if (remaining <= 0) {
      throw transactionTimeout();
    }
    return remaining;
  };

  const attemptProtectedWork = async (
    work: () => Promise<void>,
    priorTimeout: ProcessFailure | null = null,
  ): Promise<ProtectedWorkOutcome> => {
    const before = observedTransactionTimeout();
    let failed = false;
    let error: unknown = null;
    try {
      await work();
    } catch (caught: unknown) {
      failed = true;
      error = caught;
    }
    const after = observedTransactionTimeout();
    return {
      error,
      failed,
      timeout: priorTimeout ?? before ?? after,
    };
  };

  const boundedCleanup = async (work: () => Promise<void>): Promise<void> => {
    let timer: NodeJS.Timeout | null = null;
    const deadline = new Promise<never>((_resolve, reject) => {
      timer = setTimeout(() => {
        reject(new ProcessFailure({
          code: "ZCC_ORACLE_CLEANUP_FAILED",
          category: "io",
          message: "ZCC oracle private transaction cleanup exceeded its deadline",
        }));
      }, ZCC_ADOPTION_ORACLE_CLEANUP_TIMEOUT_MS);
    });
    try {
      await Promise.race([Promise.resolve().then(work), deadline]);
    } finally {
      if (timer !== null) {
        clearTimeout(timer);
      }
    }
  };

  const bindOrRecheck = async (
    protectedPaths: readonly string[],
    stage: ZccAdoptionOracleCommandRequest["stage"]
      | ZccAdoptionOracleShowRequest["stage"],
    activePaths: TransactionPaths,
  ): Promise<void> => {
    const expected = expectedProtectedPaths(stage, activePaths);
    if (!sameStringSequence(protectedPaths, expected)) {
      return fail(
        "INVALID_ZCC_ORACLE_PROTECTED_PATHS",
        "ZCC oracle protected path set is not exact",
        "domain",
      );
    }
    await recheckAuthorities();
    for (const absolutePath of protectedPaths) {
      if (!containedDirectFile(absolutePath, activePaths)) {
        return fail(
          "INVALID_ZCC_ORACLE_PROTECTED_PATHS",
          "ZCC oracle protected path escaped the transaction",
          "domain",
        );
      }
      const prior = files.get(absolutePath);
      if (prior !== undefined) {
        await recheckFile(prior);
        continue;
      }
      return fail(
        "UNBOUND_ZCC_ORACLE_FILE",
        "ZCC oracle encountered an unbound protected file",
      );
    }
    await recheckAuthorities();
  };

  const bindProducedFiles = async (
    stage: ZccAdoptionOracleCommandRequest["stage"],
    activePaths: TransactionPaths,
  ): Promise<void> => {
    const produced = stage === "plan"
      ? [activePaths.generatedConfig, activePaths.plan]
      : stage === "apply"
        ? [activePaths.state]
        : [];
    await recheckAuthorities();
    for (const absolutePath of produced) {
      if (files.has(absolutePath)) {
        return fail(
          "ZCC_ORACLE_FILE_CHANGED",
          "a protected ZCC oracle output was produced more than once",
        );
      }
      files.set(absolutePath, await inspectFile(absolutePath, true));
    }
    await recheckAuthorities();
  };

  const requireProducedFilesAbsent = async (
    stage: ZccAdoptionOracleCommandRequest["stage"],
    activePaths: TransactionPaths,
  ): Promise<void> => {
    const produced = stage === "plan"
      ? [activePaths.generatedConfig, activePaths.plan]
      : stage === "apply"
        ? [activePaths.state]
        : [];
    await recheckAuthorities();
    for (const absolutePath of produced) {
      try {
        await lstat(absolutePath);
        return fail(
          "ZCC_ORACLE_OUTPUT_PREEXISTS",
          "a protected ZCC oracle output existed before its producing stage",
        );
      } catch (error: unknown) {
        if (error instanceof ProcessFailure) {
          throw error;
        }
        if (errorCode(error) !== "ENOENT") {
          return fail(
            "ZCC_ORACLE_OUTPUT_PRECHECK_FAILED",
            "a protected ZCC oracle output could not be checked before production",
          );
        }
      }
    }
    await recheckAuthorities();
  };

  const createTemporary = async (prefix: string): Promise<string> => {
    if (phase !== "fresh") {
      return fail(
        "ZCC_ORACLE_ADAPTER_ALREADY_USED",
        "ZCC oracle adapter factory is single-use",
        "domain",
      );
    }
    phase = "creating";
    let created: string | null = null;
    try {
      if (prefix !== SCRATCH_PREFIX) {
        return fail(
          "INVALID_ZCC_ORACLE_TEMP_PREFIX",
          "ZCC oracle scratch prefix is not allowed",
          "domain",
        );
      }
      tempRootBinding = await bindDirectory(options.tempRoot, "root");
      await validateExecutable(options.terraformExecutable);
      await recheckDirectory(tempRootBinding, false);
      created = await mkdtemp(path.join(options.tempRoot, prefix));
      await forceDirectoryMode(created);
      transactionBinding = await bindDirectory(created, "exact-0700");
      paths = transactionPaths(created);
      for (const directory of [paths.home, paths.temporary, paths.terraformData]) {
        await mkdir(directory, { mode: 0o700 });
        await forceDirectoryMode(directory);
        privateDirectories.push(await bindDirectory(directory, "exact-0700"));
      }
      await recheckDirectory(tempRootBinding, false);
      const childEnvironment = Object.create(null) as Record<string, string>;
      for (const [name, value] of Object.entries(options.environment)) {
        childEnvironment[name] = value;
      }
      Object.assign(childEnvironment, {
        CHECKPOINT_DISABLE: "1",
        LANG: "C",
        LC_ALL: "C",
        TF_IN_AUTOMATION: "1",
        HOME: paths.home,
        TMPDIR: paths.temporary,
        TF_DATA_DIR: paths.terraformData,
      });
      environment = Object.freeze(childEnvironment);
      transactionDeadline = performance.now()
        + ZCC_ADOPTION_ORACLE_TRANSACTION_TIMEOUT_MS;
      phase = "active";
      return created;
    } catch (error: unknown) {
      phase = "spent";
      if (created !== null) {
        const cleanupPath = created;
        await boundedCleanup(() => {
          return rm(cleanupPath, { force: true, recursive: true });
        }).catch(() => undefined);
      }
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "ZCC_ORACLE_TEMP_CREATE_FAILED",
        "ZCC oracle private transaction directory could not be created",
      );
    }
  };

  const removeTemporary = async (directory: string): Promise<void> => {
    const active = requireActive();
    phase = "spent";
    transactionDeadline = null;
    let changed = directory !== active.paths.directory;
    const cleanup = async (): Promise<void> => {
      try {
        if (transactionBinding === null || tempRootBinding === null) {
          changed = true;
        } else {
          await recheckDirectory(transactionBinding, true).catch(() => {
            changed = true;
          });
        }
        await rm(active.paths.directory, { force: true, recursive: true });
        try {
          await lstat(active.paths.directory);
          changed = true;
        } catch (error: unknown) {
          if (errorCode(error) !== "ENOENT") {
            changed = true;
          }
        }
        if (tempRootBinding !== null) {
          await recheckDirectory(tempRootBinding, false).catch(() => {
            changed = true;
          });
        }
      } catch {
        changed = true;
      }
    };
    try {
      await boundedCleanup(cleanup);
    } catch {
      changed = true;
    }
    files.clear();
    environment = null;
    paths = null;
    privateDirectories.length = 0;
    if (changed) {
      return fail(
        "ZCC_ORACLE_CLEANUP_FAILED",
        "ZCC oracle private transaction cleanup could not be verified",
      );
    }
  };

  const writeText = async (
    request: ZccAdoptionOracleWriteRequest,
  ): Promise<void> => {
    const active = requireActive();
    const requestedPath = request.path;
    const content = request.content;
    if (
      request.mode !== 0o600
      || typeof requestedPath !== "string"
      || typeof content !== "string"
      || content.includes("\0")
      || !content.isWellFormed()
      || !containedDirectFile(requestedPath, active.paths)
      || (
        requestedPath !== active.paths.root
        && requestedPath !== active.paths.imports
        && requestedPath !== active.paths.lock
      )
      || files.has(requestedPath)
    ) {
      return fail(
        "INVALID_ZCC_ORACLE_WRITE",
        "ZCC oracle scratch write is not allowed",
        "domain",
      );
    }
    const bytes = Buffer.from(content, "utf8");
    if (bytes.length > MAX_TEXT_FILE_BYTES) {
      bytes.fill(0);
      return fail(
        "INVALID_ZCC_ORACLE_WRITE",
        "ZCC oracle scratch write exceeds its limit",
        "domain",
      );
    }
    let handle: FileHandle | null = null;
    try {
      await recheckAuthorities();
      handle = await open(
        requestedPath,
        constants.O_RDWR
          | constants.O_CREAT
          | constants.O_EXCL
          | constants.O_NOFOLLOW,
        0o600,
      );
      await handle.chmod(0o600);
      await writeAll(handle, bytes);
      await handle.sync();
      await handle.close();
      handle = null;
      files.set(requestedPath, await inspectFile(requestedPath, false));
      await recheckAuthorities();
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "ZCC_ORACLE_WRITE_FAILED",
        "ZCC oracle scratch input could not be created safely",
      );
    } finally {
      bytes.fill(0);
      await handle?.close().catch(() => undefined);
    }
  };

  const runCommand = async (
    request: ZccAdoptionOracleCommandRequest,
  ): Promise<void> => {
    const active = requireActive();
    const argv = snapshotStringArray(request.argv);
    const protectedPaths = snapshotStringArray(request.protectedPaths);
    snapshotStringArray(request.sensitiveTokens, 10_000, 4 * 1024 * 1024);
    const stage = request.stage;
    if (
      (stage !== "init" && stage !== "plan" && stage !== "apply")
      || request.executable !== options.terraformExecutable
      || request.cwd !== active.paths.directory
      || !sameStringSequence(argv, expectedCommandArgv(stage, active.paths))
    ) {
      return fail(
        "INVALID_ZCC_ORACLE_COMMAND",
        "ZCC oracle command request is not exact",
        "domain",
      );
    }
    const preflight = await attemptProtectedWork(async () => {
      await bindOrRecheck(protectedPaths, stage, active.paths);
      await requireProducedFilesAbsent(stage, active.paths);
    });
    if (preflight.timeout !== null) {
      throw preflight.failed
        ? withProtectionFailure(
            preflight.timeout,
            "ZCC_ORACLE_COMMAND_PROTECTION_FAILED",
            "protected files also changed before the timed-out Terraform command",
          )
        : preflight.timeout;
    }
    if (preflight.failed) {
      throw preflight.error;
    }
    const remainingTimeoutMs = remainingTransactionBudget();
    let primary: unknown = null;
    try {
      await runTerraformCommand({
        terraformExecutable: options.terraformExecutable,
        argv,
        cwd: active.paths.directory,
        environment: active.environment,
        limits: {
          timeoutMs: remainingTimeoutMs,
          maxStdoutBytes: DEFAULT_TERRAFORM_COMMAND_LIMITS.maxStdoutBytes,
          maxStderrBytes: DEFAULT_TERRAFORM_COMMAND_LIMITS.maxStderrBytes,
        },
        output: "discard",
      });
    } catch (error: unknown) {
      primary = error instanceof ProcessFailure
        && error.code === "TERRAFORM_COMMAND_TIMEOUT"
        ? transactionTimeout()
        : error;
    }
    let deadlinePrimary = primary instanceof ProcessFailure
        && primary.code === "ZCC_ADOPTION_ORACLE_TIMEOUT"
      ? primary
      : null;
    let produced: ProtectedWorkOutcome | null = null;
    if (primary === null) {
      produced = await attemptProtectedWork(
        () => bindProducedFiles(stage, active.paths),
        deadlinePrimary,
      );
      deadlinePrimary = produced.timeout;
    }
    const postflight = await attemptProtectedWork(
      () => bindOrRecheck(protectedPaths, stage, active.paths),
      deadlinePrimary,
    );
    deadlinePrimary = postflight.timeout;
    if (deadlinePrimary !== null) {
      throw (produced?.failed ?? false) || postflight.failed
        ? withProtectionFailure(
            deadlinePrimary,
            "ZCC_ORACLE_COMMAND_PROTECTION_FAILED",
            "protected files also changed around the timed-out Terraform command",
          )
        : deadlinePrimary;
    }
    if (produced?.failed ?? false) {
      return fail(
        "ZCC_ORACLE_COMMAND_PROTECTION_FAILED",
        "ZCC oracle could not bind Terraform command outputs",
      );
    }
    if (postflight.failed) {
      return fail(
        "ZCC_ORACLE_COMMAND_PROTECTION_FAILED",
        "ZCC oracle protected files changed around a Terraform command",
      );
    }
    if (primary !== null) {
      throw primary;
    }
  };

  const readJson = async (
    request: ZccAdoptionOracleShowRequest,
  ): Promise<unknown> => {
    const active = requireActive();
    const argv = snapshotStringArray(request.argv);
    const protectedPaths = snapshotStringArray(request.protectedPaths);
    snapshotStringArray(request.sensitiveTokens, 10_000, 4 * 1024 * 1024);
    const snapshotPath = request.stage === "show-plan"
      ? active.paths.plan
      : active.paths.state;
    if (
      (request.stage !== "show-plan" && request.stage !== "show-state")
      || request.executable !== options.terraformExecutable
      || request.cwd !== active.paths.directory
      || request.snapshotPath !== snapshotPath
      || !sameStringSequence(argv, ["show", "-json", snapshotPath])
    ) {
      return fail(
        "INVALID_ZCC_ORACLE_SHOW",
        "ZCC oracle show request is not exact",
        "domain",
      );
    }
    const preflight = await attemptProtectedWork(
      () => bindOrRecheck(protectedPaths, request.stage, active.paths),
    );
    if (preflight.timeout !== null) {
      throw preflight.failed
        ? withProtectionFailure(
            preflight.timeout,
            "ZCC_ORACLE_SHOW_PROTECTION_FAILED",
            "protected files also changed before the timed-out Terraform show",
          )
        : preflight.timeout;
    }
    if (preflight.failed) {
      throw preflight.error;
    }
    const remainingTimeoutMs = remainingTransactionBudget();
    let result: unknown;
    let primary: unknown = null;
    try {
      result = await terraformShowPlan({
        terraformExecutable: options.terraformExecutable,
        envDir: active.paths.directory,
        snapshotPath,
        environment: active.environment,
        limits: {
          timeoutMs: remainingTimeoutMs,
          maxStdoutBytes: DEFAULT_TERRAFORM_SHOW_LIMITS.maxStdoutBytes,
          maxStderrBytes: DEFAULT_TERRAFORM_SHOW_LIMITS.maxStderrBytes,
        },
      });
    } catch (error: unknown) {
      primary = error instanceof ProcessFailure
        && error.code === "TERRAFORM_SHOW_TIMEOUT"
        ? transactionTimeout()
        : error;
    }
    const deadlinePrimary = primary instanceof ProcessFailure
        && primary.code === "ZCC_ADOPTION_ORACLE_TIMEOUT"
      ? primary
      : null;
    const postflight = await attemptProtectedWork(
      () => bindOrRecheck(protectedPaths, request.stage, active.paths),
      deadlinePrimary,
    );
    if (postflight.timeout !== null) {
      throw postflight.failed
        ? withProtectionFailure(
            postflight.timeout,
            "ZCC_ORACLE_SHOW_PROTECTION_FAILED",
            "protected files also changed around the timed-out Terraform show",
          )
        : postflight.timeout;
    }
    if (postflight.failed) {
      return fail(
        "ZCC_ORACLE_SHOW_PROTECTION_FAILED",
        "ZCC oracle protected files changed around Terraform show",
      );
    }
    if (primary !== null) {
      throw primary;
    }
    return result;
  };

  const checkpointTransaction = async (): Promise<void> => {
    const active = requireActive();
    const protectedPaths = expectedProtectedPaths("show-state", active.paths);
    const checkpoint = await attemptProtectedWork(
      () => bindOrRecheck(protectedPaths, "show-state", active.paths),
    );
    if (checkpoint.timeout !== null) {
      throw checkpoint.failed
        ? withProtectionFailure(
            checkpoint.timeout,
            "ZCC_ORACLE_FINAL_PROTECTION_FAILED",
            "protected files also changed before timed-out result acceptance",
          )
        : checkpoint.timeout;
    }
    if (checkpoint.failed) {
      return fail(
        "ZCC_ORACLE_FINAL_PROTECTION_FAILED",
        "ZCC oracle protected files changed before result acceptance",
      );
    }
  };

  return Object.freeze({
    transaction: Object.freeze({ checkpoint: checkpointTransaction }),
    temporary: Object.freeze({
      create: createTemporary,
      remove: removeTemporary,
    }),
    files: Object.freeze({ writeText }),
    command: Object.freeze({ run: runCommand }),
    show: Object.freeze({ readJson }),
  });
}
