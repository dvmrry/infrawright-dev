import { types as utilTypes } from "node:util";

import { ProcessFailure } from "./errors.js";
import { parseControlJson } from "../json/control.js";

export const ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES = 64 * 1024;
const MAX_CREDENTIAL_BYTES = 16 * 1024;
const MIN_TOKEN_LIFETIME_SECONDS = 60;
const MAX_TOKEN_LIFETIME_SECONDS = 24 * 60 * 60;
const TOKEN_REFRESH_SKEW_MS = 30_000;
const DECIMAL_INTEGER = /^(?:0|[1-9][0-9]*)$/;
const HEADER_CONTROL = /[\u0000-\u001f\u007f]/;

export interface ZccOneApiCredentials {
  readonly clientId: string;
  readonly clientSecret: string;
}

export interface ZccOneApiTokenResponse {
  readonly accessToken: string;
  readonly expiresInSeconds: number;
}

export interface ZccOneApiTokenLease extends ZccOneApiTokenResponse {
  readonly expiresAtMs: number;
  readonly refreshAtMs: number;
}

function fail(
  code: "INVALID_ZCC_ONEAPI_CREDENTIALS" | "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
  message: string,
  category: "domain" | "io",
): never {
  throw new ProcessFailure({ category, code, message });
}

function plainRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  if (
    value === null
    || typeof value !== "object"
    || utilTypes.isProxy(value)
    || Array.isArray(value)
  ) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function exactDataKeys(
  value: Readonly<Record<string, unknown>>,
  expected: readonly string[],
): boolean {
  const keys = Reflect.ownKeys(value);
  if (
    keys.length !== expected.length
    || keys.some((key) => typeof key !== "string")
  ) {
    return false;
  }
  const actual = (keys as string[]).sort();
  const sortedExpected = [...expected].sort();
  for (let index = 0; index < actual.length; index += 1) {
    if (actual[index] !== sortedExpected[index]) {
      return false;
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, actual[index] ?? "");
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
    ) {
      return false;
    }
  }
  return true;
}

function credentialValue(value: unknown): string {
  if (
    typeof value !== "string"
    || value.length === 0
    || value.includes("\0")
    || !value.isWellFormed()
    || Buffer.byteLength(value, "utf8") > MAX_CREDENTIAL_BYTES
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_CREDENTIALS",
      "ZCC OneAPI credentials are missing or invalid",
      "domain",
    );
  }
  return value;
}

/** Validate and detach the two opaque OAuth credential values. */
export function snapshotZccOneApiCredentials(
  value: ZccOneApiCredentials,
): ZccOneApiCredentials {
  if (!plainRecord(value) || !exactDataKeys(value, ["clientId", "clientSecret"])) {
    return fail(
      "INVALID_ZCC_ONEAPI_CREDENTIALS",
      "ZCC OneAPI credentials are missing or invalid",
      "domain",
    );
  }
  const clientId = Object.getOwnPropertyDescriptor(value, "clientId")?.value;
  const clientSecret = Object.getOwnPropertyDescriptor(value, "clientSecret")?.value;
  return Object.freeze({
    clientId: credentialValue(clientId),
    clientSecret: credentialValue(clientSecret),
  });
}

/** Render the exact OneAPI client-credentials form in a deterministic order. */
export function renderZccOneApiTokenForm(
  credentials: ZccOneApiCredentials,
  audience: "https://api.zscaler.com",
): string {
  const snapshot = snapshotZccOneApiCredentials(credentials);
  if (audience !== "https://api.zscaler.com") {
    return fail(
      "INVALID_ZCC_ONEAPI_CREDENTIALS",
      "ZCC OneAPI credentials are missing or invalid",
      "domain",
    );
  }
  return new URLSearchParams([
    ["grant_type", "client_credentials"],
    ["client_id", snapshot.clientId],
    ["client_secret", snapshot.clientSecret],
    ["audience", audience],
  ]).toString();
}

function tokenResponseFailure(): never {
  return fail(
    "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
    "ZCC OneAPI authentication returned an invalid response",
    "io",
  );
}

function accessToken(value: unknown): string {
  if (
    typeof value !== "string"
    || value.length === 0
    || HEADER_CONTROL.test(value)
    || !value.isWellFormed()
    || Buffer.byteLength(value, "utf8")
      > ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES
  ) {
    return tokenResponseFailure();
  }
  return value;
}

function expiresInSeconds(value: unknown): number {
  let parsed: number;
  if (typeof value === "number") {
    parsed = value;
  } else if (
    typeof value === "string"
    && value.isWellFormed()
    && DECIMAL_INTEGER.test(value)
  ) {
    parsed = Number(value);
  } else {
    return tokenResponseFailure();
  }
  if (
    !Number.isSafeInteger(parsed)
    || parsed < MIN_TOKEN_LIFETIME_SECONDS
    || parsed > MAX_TOKEN_LIFETIME_SECONDS
  ) {
    return tokenResponseFailure();
  }
  return parsed;
}

/** Parse the bounded, fatal-UTF-8 token JSON after transport framing. */
export function parseZccOneApiTokenResponseJson(
  text: string,
): ZccOneApiTokenResponse {
  if (
    typeof text !== "string"
    || !text.isWellFormed()
    || Buffer.byteLength(text, "utf8") > ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES
  ) {
    return tokenResponseFailure();
  }
  let parsed: unknown;
  try {
    parsed = parseControlJson(text);
  } catch {
    return tokenResponseFailure();
  }
  if (!plainRecord(parsed)) {
    return tokenResponseFailure();
  }
  const parsedAccessToken = parsed.access_token;
  const tokenType = parsed.token_type;
  if (
    (
      tokenType !== undefined
      && (
        typeof tokenType !== "string"
        || tokenType.toLowerCase() !== "bearer"
      )
    )
  ) {
    return tokenResponseFailure();
  }
  return Object.freeze({
    accessToken: accessToken(parsedAccessToken),
    expiresInSeconds: expiresInSeconds(parsed.expires_in),
  });
}

/** Bind a parsed token to a monotonic acquisition instant. */
export function zccOneApiTokenLease(
  token: ZccOneApiTokenResponse,
  issuedAtMs: number,
): ZccOneApiTokenLease {
  if (
    !plainRecord(token)
    || !exactDataKeys(token, ["accessToken", "expiresInSeconds"])
  ) {
    return tokenResponseFailure();
  }
  const tokenAccess = Object.getOwnPropertyDescriptor(token, "accessToken");
  const tokenExpires = Object.getOwnPropertyDescriptor(token, "expiresInSeconds");
  if (
    tokenAccess === undefined
    || !("value" in tokenAccess)
    || tokenExpires === undefined
    || !("value" in tokenExpires)
  ) {
    return tokenResponseFailure();
  }
  const snapshot = Object.freeze({
    accessToken: accessToken(tokenAccess.value),
    expiresInSeconds: expiresInSeconds(tokenExpires.value),
  });
  if (!Number.isFinite(issuedAtMs) || issuedAtMs < 0) {
    return tokenResponseFailure();
  }
  const expiresAtMs = issuedAtMs + snapshot.expiresInSeconds * 1000;
  if (!Number.isFinite(expiresAtMs) || expiresAtMs > Number.MAX_SAFE_INTEGER) {
    return tokenResponseFailure();
  }
  return Object.freeze({
    ...snapshot,
    expiresAtMs,
    refreshAtMs: expiresAtMs - TOKEN_REFRESH_SKEW_MS,
  });
}
