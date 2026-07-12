import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { lstat, open, realpath } from "node:fs/promises";
import { spawn, type ChildProcess } from "node:child_process";
import { fileURLToPath } from "node:url";
import { performance } from "node:perf_hooks";
import { Readable, Writable } from "node:stream";

import { ProcessFailure } from "../domain/errors.js";
import {
  isZccCollectionResourceType,
  ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES,
  type ZccCollectionResourceType,
} from "../domain/zcc-collection-contract.js";
import type { JsonValue } from "../json/python-compatible.js";
import {
  decodeZccCollectionFrame,
  encodeZccCollectionFrame,
  validateZccCollectionChildResponse,
  ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
  ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
  type ZccCollectionChildFailureCode,
  type ZccCollectionChildResponse,
  type ZccCollectionChildSuccessResponse,
} from "./zcc-collection-protocol.js";

declare const __INFRAWRIGHT_ZCC_CHILD_SHA256__: string;
declare const __INFRAWRIGHT_ZCC_CHILD_SIZE__: number;

export const ZCC_COLLECTION_OUTER_TIMEOUT_MS = 310_000;
export const ZCC_COLLECTION_REAP_TIMEOUT_MS = 5_000;
const MAX_RESPONSE_FRAME_BYTES = ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES + 12;
const MAX_RESPONSE_CHUNKS = 32 * 1024;
const MAX_CHILD_BUNDLE_BYTES = 16 * 1024 * 1024;

export interface ZccCollectionChildIdentity {
  readonly path: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccCollectionChildRunnerOptions {
  readonly childIdentity?: ZccCollectionChildIdentity;
  readonly timeoutMs?: number;
  readonly reapTimeoutMs?: number;
  readonly spawnProcess?: typeof spawn;
}

const CHILD_FAILURES: Readonly<Record<ZccCollectionChildFailureCode, Readonly<{
  category: "domain" | "io" | "internal";
  message: string;
  retryable: boolean;
}>>> = Object.freeze({
  INVALID_ZCC_COLLECTION_CHILD_REQUEST: Object.freeze({
    category: "internal", message: "ZCC collection child rejected its private request", retryable: false,
  }),
  ZCC_ONEAPI_CA_BUNDLE_FAILED: Object.freeze({
    category: "io", message: "ZCC OneAPI CA bundle could not be loaded", retryable: false,
  }),
  ZCC_ONEAPI_CLEANUP_FAILED: Object.freeze({
    category: "io", message: "ZCC OneAPI transport cleanup failed", retryable: true,
  }),
  ZCC_ONEAPI_HOST_CONFIGURATION_INVALID: Object.freeze({
    category: "io", message: "ZCC OneAPI host configuration is invalid", retryable: false,
  }),
  INVALID_ZCC_COLLECTOR_RESPONSE: Object.freeze({
    category: "domain", message: "ZCC collector response is invalid", retryable: false,
  }),
  ZCC_COLLECTOR_HTTP_STATUS: Object.freeze({
    category: "io", message: "ZCC data request returned an unsupported HTTP status", retryable: false,
  }),
  ZCC_COLLECTOR_ITEM_LIMIT: Object.freeze({
    category: "domain", message: "ZCC collection exceeded its item bound", retryable: false,
  }),
  ZCC_COLLECTOR_RATE_LIMITED: Object.freeze({
    category: "io", message: "ZCC data request remained rate limited", retryable: true,
  }),
  ZCC_COLLECTOR_RESPONSE_LIMIT: Object.freeze({
    category: "domain", message: "ZCC collection exceeded its response bound", retryable: false,
  }),
  ZCC_COLLECTOR_RETRY_CLOCK_FAILURE: Object.freeze({
    category: "io", message: "ZCC collector retry clock failed", retryable: false,
  }),
  ZCC_COLLECTOR_TRANSPORT_FAILURE: Object.freeze({
    category: "io", message: "ZCC collector transport failed", retryable: true,
  }),
  ZCC_ONEAPI_AUTH_HTTP_STATUS: Object.freeze({
    category: "io", message: "ZCC OneAPI authentication returned an unsupported HTTP status", retryable: false,
  }),
  ZCC_ONEAPI_AUTH_RATE_LIMITED: Object.freeze({
    category: "io", message: "ZCC OneAPI authentication remained rate limited", retryable: true,
  }),
  ZCC_ONEAPI_AUTH_RESPONSE_INVALID: Object.freeze({
    category: "io", message: "ZCC OneAPI authentication returned an invalid response", retryable: false,
  }),
  ZCC_ONEAPI_AUTH_RESPONSE_LIMIT: Object.freeze({
    category: "io", message: "ZCC OneAPI authentication response exceeded its limit", retryable: false,
  }),
  ZCC_ONEAPI_AUTH_TRANSPORT_FAILED: Object.freeze({
    category: "io", message: "ZCC OneAPI authentication transport failed", retryable: true,
  }),
  ZCC_ONEAPI_DATA_RESPONSE_LIMIT: Object.freeze({
    category: "io", message: "ZCC OneAPI data response exceeded its limit", retryable: false,
  }),
  ZCC_ONEAPI_DATA_TRANSPORT_FAILED: Object.freeze({
    category: "io", message: "ZCC OneAPI data transport failed", retryable: true,
  }),
  ZCC_ONEAPI_DIAGNOSTICS_UNSAFE: Object.freeze({
    category: "io", message: "ZCC OneAPI child diagnostics isolation is unavailable", retryable: false,
  }),
  ZCC_ONEAPI_REDIRECT_REFUSED: Object.freeze({
    category: "io", message: "ZCC OneAPI redirect was refused", retryable: false,
  }),
  ZCC_ONEAPI_TRANSACTION_TIMEOUT: Object.freeze({
    category: "io", message: "ZCC OneAPI transaction exceeded its deadline", retryable: true,
  }),
  ZCC_ONEAPI_HOST_FAILED: Object.freeze({
    category: "internal", message: "ZCC OneAPI isolated host failed", retryable: false,
  }),
});

function failure(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "io",
  retryable = false,
): ProcessFailure {
  return new ProcessFailure({ code, message, category, retryable });
}

function defaultIdentity(): ZccCollectionChildIdentity {
  if (
    typeof __INFRAWRIGHT_ZCC_CHILD_SHA256__ !== "string"
    || !/^[0-9a-f]{64}$/.test(__INFRAWRIGHT_ZCC_CHILD_SHA256__)
    || typeof __INFRAWRIGHT_ZCC_CHILD_SIZE__ !== "number"
    || !Number.isSafeInteger(__INFRAWRIGHT_ZCC_CHILD_SIZE__)
    || __INFRAWRIGHT_ZCC_CHILD_SIZE__ <= 0
  ) {
    throw failure(
      "ZCC_COLLECTION_CHILD_IDENTITY_UNAVAILABLE",
      "ZCC collection child identity is unavailable",
      "internal",
    );
  }
  return {
    path: fileURLToPath(
      new URL("./infrawright-zcc-collector-child.mjs", import.meta.url),
    ),
    sha256: __INFRAWRIGHT_ZCC_CHILD_SHA256__,
    size_bytes: __INFRAWRIGHT_ZCC_CHILD_SIZE__,
  };
}

function snapshotPrivateInput(options: {
  readonly environment: Readonly<Record<string, string>>;
  readonly resourceType: ZccCollectionResourceType;
  readonly runner?: ZccCollectionChildRunnerOptions;
}): {
  readonly environment: Readonly<Record<string, string>>;
  readonly resourceType: ZccCollectionResourceType;
  readonly runner: ZccCollectionChildRunnerOptions;
} {
  if (!isZccCollectionResourceType(options.resourceType)) {
    throw failure(
      "ZCC_COLLECTION_CHILD_INPUT_INVALID",
      "ZCC collection child input is invalid",
      "internal",
    );
  }
  const allowed = new Set<string>(ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES);
  const environment = Object.create(null) as Record<string, string>;
  if (
    typeof options.environment !== "object"
    || options.environment === null
    || Array.isArray(options.environment)
  ) {
    throw failure(
      "ZCC_COLLECTION_CHILD_INPUT_INVALID",
      "ZCC collection child input is invalid",
      "internal",
    );
  }
  let totalEnvironmentBytes = 0;
  for (const key of Reflect.ownKeys(options.environment)) {
    const descriptor = typeof key === "string"
      ? Object.getOwnPropertyDescriptor(options.environment, key)
      : undefined;
    if (
      typeof key !== "string"
      || !allowed.has(key)
      || descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || !descriptor.value.isWellFormed()
      || descriptor.value.includes("\0")
    ) {
      throw failure(
        "ZCC_COLLECTION_CHILD_INPUT_INVALID",
        "ZCC collection child input is invalid",
        "internal",
      );
    }
    const valueBytes = Buffer.byteLength(descriptor.value, "utf8");
    totalEnvironmentBytes += Buffer.byteLength(key, "utf8") + valueBytes;
    if (valueBytes > 64 * 1024 || totalEnvironmentBytes > 128 * 1024) {
      throw failure(
        "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID",
        "ZCC OneAPI host configuration is invalid",
      );
    }
    environment[key] = descriptor.value;
  }
  const supplied = options.runner;
  const identity = supplied?.childIdentity;
  if (
    supplied?.timeoutMs !== undefined
    && (
      !Number.isSafeInteger(supplied.timeoutMs)
      || supplied.timeoutMs <= 0
      || supplied.timeoutMs > ZCC_COLLECTION_OUTER_TIMEOUT_MS
    )
  ) {
    throw failure(
      "ZCC_COLLECTION_CHILD_INPUT_INVALID",
      "ZCC collection child input is invalid",
      "internal",
    );
  }
  if (
    supplied?.reapTimeoutMs !== undefined
    && (
      !Number.isSafeInteger(supplied.reapTimeoutMs)
      || supplied.reapTimeoutMs <= 0
      || supplied.reapTimeoutMs > ZCC_COLLECTION_REAP_TIMEOUT_MS
    )
  ) {
    throw failure(
      "ZCC_COLLECTION_CHILD_INPUT_INVALID",
      "ZCC collection child input is invalid",
      "internal",
    );
  }
  const runner: ZccCollectionChildRunnerOptions = Object.freeze({
    ...(identity === undefined ? {} : {
      childIdentity: Object.freeze({
        path: identity.path,
        sha256: identity.sha256,
        size_bytes: identity.size_bytes,
      }),
    }),
    ...(supplied?.timeoutMs === undefined ? {} : { timeoutMs: supplied.timeoutMs }),
    ...(supplied?.reapTimeoutMs === undefined
      ? {}
      : { reapTimeoutMs: supplied.reapTimeoutMs }),
    ...(supplied?.spawnProcess === undefined
      ? {}
      : { spawnProcess: supplied.spawnProcess }),
  });
  return Object.freeze({
    environment: Object.freeze(environment),
    resourceType: options.resourceType,
    runner,
  });
}

async function verifyChildIdentity(
  expected: ZccCollectionChildIdentity,
): Promise<Buffer> {
  if (
    !/^[0-9a-f]{64}$/.test(expected.sha256)
    || !Number.isSafeInteger(expected.size_bytes)
    || expected.size_bytes <= 0
    || expected.size_bytes > MAX_CHILD_BUNDLE_BYTES
    || expected.path.includes("\0")
    || !expected.path.isWellFormed()
  ) {
    throw failure(
      "ZCC_COLLECTION_CHILD_IDENTITY_INVALID",
      "ZCC collection child identity is invalid",
      "internal",
    );
  }
  let handle = null;
  try {
    const before = await lstat(expected.path, { bigint: true });
    const canonical = await realpath(expected.path);
    if (
      canonical !== expected.path
      || before.isSymbolicLink()
      || !before.isFile()
      || before.size !== BigInt(expected.size_bytes)
    ) {
      throw new Error("untrusted child path");
    }
    handle = await open(expected.path, constants.O_RDONLY | constants.O_NOFOLLOW);
    const opened = await handle.stat({ bigint: true });
    if (
      !opened.isFile()
      || opened.dev !== before.dev
      || opened.ino !== before.ino
      || opened.size !== before.size
    ) {
      throw new Error("child identity changed");
    }
    const bytes = Buffer.allocUnsafe(expected.size_bytes);
    let accepted = false;
    try {
      let offset = 0;
      while (offset < bytes.length) {
        const { bytesRead } = await handle.read(
          bytes,
          offset,
          bytes.length - offset,
          offset,
        );
        if (bytesRead === 0) {
          throw new Error("child read truncated");
        }
        offset += bytesRead;
      }
      const after = await handle.stat({ bigint: true });
      const pathAfter = await lstat(expected.path, { bigint: true });
      if (
        after.dev !== opened.dev || after.ino !== opened.ino
        || after.size !== opened.size || after.mtimeNs !== opened.mtimeNs
        || pathAfter.dev !== opened.dev || pathAfter.ino !== opened.ino
        || createHash("sha256").update(bytes).digest("hex") !== expected.sha256
      ) {
        throw new Error("child bytes changed");
      }
      accepted = true;
      return bytes;
    } finally {
      if (!accepted) bytes.fill(0);
    }
  } catch {
    throw failure(
      "ZCC_COLLECTION_CHILD_INTEGRITY_FAILED",
      "ZCC collection child failed its embedded integrity check",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function readResponseFrame(stream: NodeJS.ReadableStream): Promise<Buffer> {
  const target = Buffer.allocUnsafe(MAX_RESPONSE_FRAME_BYTES);
  let length = 0;
  let chunks = 0;
  try {
    for await (const raw of stream) {
      chunks += 1;
      if (chunks > MAX_RESPONSE_CHUNKS || !(raw instanceof Uint8Array)) {
        throw new Error("invalid child response stream");
      }
      const chunk = Buffer.from(raw.buffer, raw.byteOffset, raw.byteLength);
      if (chunk.length > target.length - length) {
        throw new Error("child response exceeds its bound");
      }
      chunk.copy(target, length);
      length += chunk.length;
    }
    return Buffer.from(target.subarray(0, length));
  } finally {
    target.fill(0);
  }
}

async function writeRequestFrame(
  stream: NodeJS.WritableStream,
  frame: Buffer,
): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    stream.once("error", reject);
    stream.end(frame, (error?: Error | null) => {
      if (error) reject(error);
      else resolve();
    });
  });
}

function waitForExit(child: ChildProcess): Promise<{
  code: number | null;
  signal: NodeJS.Signals | null;
}> {
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve({ code, signal }));
  });
}

async function reap(
  child: ChildProcess,
  closed: Promise<void>,
  timeoutMs: number,
): Promise<void> {
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
  }
  let timer: NodeJS.Timeout | null = null;
  const reaped = await Promise.race([
    closed.then(() => true),
    new Promise<boolean>((resolve) => {
      timer = setTimeout(() => resolve(false), timeoutMs);
      timer.unref();
    }),
  ]);
  if (timer !== null) clearTimeout(timer);
  if (!reaped) {
    // Do not let an unresponsive, already-SIGKILLed direct child keep the
    // machine-only parent alive after the bounded cleanup result is known.
    child.unref();
    throw failure(
      "ZCC_COLLECTION_CHILD_REAP_FAILED",
      "ZCC collection child could not be reaped within its cleanup bound",
      "internal",
    );
  }
}

function waitForClose(child: ChildProcess): Promise<void> {
  return new Promise((resolve) => child.once("close", () => resolve()));
}

function privateError(response: ZccCollectionChildResponse): ProcessFailure {
  if (!("code" in response)) {
    return failure(
      "ZCC_COLLECTION_CHILD_PROTOCOL_FAILED",
      "ZCC collection child returned an invalid protocol result",
      "internal",
    );
  }
  const contract = CHILD_FAILURES[response.code];
  return failure(
    response.code,
    contract.message,
    contract.category,
    contract.retryable,
  );
}

/** Run the exact private collector child without exposing credentials in argv/env. */
export async function runZccCollectionChildProcess(options: {
  readonly environment: Readonly<Record<string, string>>;
  readonly resourceType: ZccCollectionResourceType;
  readonly runner?: ZccCollectionChildRunnerOptions;
}): Promise<ZccCollectionChildSuccessResponse> {
  const snapshot = snapshotPrivateInput(options);
  const identity = snapshot.runner.childIdentity ?? defaultIdentity();
  // This check deliberately precedes private frame construction. The exact
  // verified bytes, rather than the later pathname, become executable input.
  const childCode = await verifyChildIdentity(identity);
  let requestFrame: Buffer;
  try {
    requestFrame = encodeZccCollectionFrame({
      kind: "infrawright.zcc_collection_child_request",
      schema_version: 1,
      environment: { ...snapshot.environment },
      resource_type: snapshot.resourceType,
    } as unknown as JsonValue, ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES, "request");
  } catch {
    childCode.fill(0);
    throw failure(
      "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID",
      "ZCC OneAPI host configuration is invalid",
    );
  }
  const spawnProcess = snapshot.runner.spawnProcess ?? spawn;
  const timeoutMs = snapshot.runner.timeoutMs ?? ZCC_COLLECTION_OUTER_TIMEOUT_MS;
  const reapTimeoutMs = snapshot.runner.reapTimeoutMs ?? ZCC_COLLECTION_REAP_TIMEOUT_MS;
  let child: ChildProcess | null = null;
  let childClosed: Promise<void> | null = null;
  let timedOut = false;
  let responseFrame: Buffer | null = null;
  let requestWritten = false;
  let codeWritten = false;
  let response: ZccCollectionChildSuccessResponse | null = null;
  let primary: unknown = null;
  let cleanup: ProcessFailure | null = null;
  const signals = ["SIGTERM", "SIGINT", "SIGHUP"] as const;
  const signalHandlers = new Map<NodeJS.Signals, () => void>();
  const removeSignalHandlers = (): void => {
    for (const [signal, handler] of signalHandlers) {
      process.removeListener(signal, handler);
    }
    signalHandlers.clear();
  };
  const exitHook = (): void => { child?.kill("SIGKILL"); };
  try {
    for (const signal of signals) {
      const handler = (): void => {
        child?.kill("SIGKILL");
        removeSignalHandlers();
        process.removeListener("exit", exitHook);
        process.kill(process.pid, signal);
      };
      signalHandlers.set(signal, handler);
      process.once(signal, handler);
    }
    process.once("exit", exitHook);
    const started = performance.now();
    child = spawnProcess(
      process.execPath,
      ["--disable-sigusr1", "--input-type=module", "-"],
      {
        shell: false,
        env: { LANG: "C", LC_ALL: "C", TZ: "UTC" },
        stdio: ["pipe", "ignore", "ignore", "pipe", "pipe"],
        windowsHide: true,
      },
    );
    childClosed = waitForClose(child);
    const codePipe = child.stdin;
    const requestPipe = child.stdio[3];
    const responsePipe = child.stdio[4];
    if (
      !(codePipe instanceof Writable)
      || !(requestPipe instanceof Writable)
      || !(responsePipe instanceof Readable)
    ) {
      throw new Error("private child pipes unavailable");
    }
    let timer: NodeJS.Timeout | null = null;
    try {
      const remaining = Math.max(1, timeoutMs - (performance.now() - started));
      const timeout = new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          timedOut = true;
          reject(new Error("outer deadline"));
        }, remaining);
        timer.unref();
      });
      const write = writeRequestFrame(requestPipe, requestFrame).then(
        () => ({ ok: true as const }),
        () => ({ ok: false as const }),
      ).finally(() => {
        // Best-effort mutable-byte clearing. Source environment strings are
        // immutable JavaScript values and are not claimed to be erased.
        requestFrame.fill(0);
      });
      const writeCode = writeRequestFrame(codePipe, childCode).then(
        () => ({ ok: true as const }),
        () => ({ ok: false as const }),
      ).finally(() => {
        childCode.fill(0);
      });
      const result = await Promise.race([
        Promise.all([
          readResponseFrame(responsePipe),
          waitForExit(child),
          write,
          writeCode,
        ]),
        timeout,
      ]);
      responseFrame = result[0];
      const exit = result[1];
      requestWritten = result[2].ok;
      codeWritten = result[3].ok;
      if (exit.code !== 0 || exit.signal !== null) {
        throw new Error("child exit failed");
      }
    } finally {
      if (timer !== null) clearTimeout(timer);
    }
    const parsed = decodeZccCollectionFrame(
      responseFrame,
      ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
      "response",
    );
    if (!validateZccCollectionChildResponse(parsed)) {
      throw new Error("invalid child response");
    }
    if (!codeWritten) {
      throw new Error("child response was not produced by the complete verified code");
    }
    if ("code" in parsed) {
      throw privateError(parsed);
    }
    if (!requestWritten) {
      throw new Error("child succeeded without consuming the complete request");
    }
    response = parsed;
  } catch (error: unknown) {
    primary = timedOut
      ? failure(
        "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
        "ZCC OneAPI transaction exceeded its deadline",
        "io",
        true,
      )
      : error instanceof ProcessFailure
        ? error
        : failure(
            "ZCC_COLLECTION_CHILD_PROTOCOL_FAILED",
            "ZCC collection child process failed its closed protocol",
            "internal",
          );
  } finally {
    requestFrame.fill(0);
    childCode.fill(0);
    responseFrame?.fill(0);
    if (child !== null && childClosed !== null) {
      child.stdin?.destroy();
      child.stdio[3]?.destroy();
      child.stdio[4]?.destroy();
      try {
        await reap(child, childClosed, reapTimeoutMs);
      } catch (error: unknown) {
        cleanup = error instanceof ProcessFailure
          ? error
          : failure(
              "ZCC_COLLECTION_CHILD_REAP_FAILED",
              "ZCC collection child could not be reaped within its cleanup bound",
              "internal",
            );
      }
    }
    removeSignalHandlers();
    process.removeListener("exit", exitHook);
  }
  if (primary instanceof ProcessFailure) {
    if (cleanup === null) throw primary;
    throw new ProcessFailure({
      code: primary.code,
      category: primary.category,
      message: primary.message,
      retryable: primary.retryable,
      details: [
        ...primary.details,
        { path: "$", code: cleanup.code, message: cleanup.message },
      ],
    });
  }
  if (primary !== null) throw primary;
  if (cleanup !== null) throw cleanup;
  if (response === null) {
    throw failure(
      "ZCC_COLLECTION_CHILD_PROTOCOL_FAILED",
      "ZCC collection child process returned no result",
      "internal",
    );
  }
  return response;
}
