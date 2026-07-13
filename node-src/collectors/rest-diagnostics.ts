import type { HttpTransport } from "./types.js";
import {
  createRestHttpTransport,
  REST_HTTP_TIMEOUT_MS,
  type RestHttpTransportOptions,
} from "../io/rest-http-transport.js";

export interface RestHostProbeResult {
  readonly detail: string;
  readonly host: string;
  readonly ok: boolean;
}

export interface RestHostProbeOptions {
  readonly environment?: Readonly<Record<string, string | undefined>>;
  readonly includeCustomCa?: boolean;
  readonly timeoutMs?: number;
  /** Test seam; callers that supply a transport retain ownership of it. */
  readonly transport?: HttpTransport;
  /** Additional production-transport test seams. */
  readonly transportOptions?: Omit<
    RestHttpTransportOptions,
    "includeCustomCa" | "requestTimeoutMs"
  >;
}

function hostUrl(host: string): URL {
  if (
    typeof host !== "string"
    || host.length === 0
    || host.includes("/")
    || host.includes("@")
    || host.includes("?")
    || host.includes("#")
  ) {
    throw new TypeError("diagnostic host must be a hostname with an optional port");
  }
  let url: URL;
  try {
    url = new URL(`https://${host}/`);
  } catch {
    throw new TypeError("diagnostic host must be a hostname with an optional port");
  }
  if (url.hostname === "" || url.username !== "" || url.password !== "") {
    throw new TypeError("diagnostic host must be a hostname with an optional port");
  }
  return url;
}

/** Probe one collector host; any HTTP response proves DNS/TCP/TLS success. */
export async function probeRestHost(
  host: string,
  options: RestHostProbeOptions = {},
): Promise<RestHostProbeResult> {
  const url = hostUrl(host);
  const owned = options.transport === undefined;
  const transport = options.transport ?? await createRestHttpTransport(
    options.environment ?? process.env,
    {
      ...options.transportOptions,
      includeCustomCa: options.includeCustomCa !== false,
      requestTimeoutMs: options.timeoutMs ?? Math.min(15_000, REST_HTTP_TIMEOUT_MS),
    },
  );
  let result: RestHostProbeResult;
  try {
    const response = await transport.request({
      headers: { accept: "*/*" },
      method: "GET",
      timeoutMs: options.timeoutMs ?? Math.min(15_000, REST_HTTP_TIMEOUT_MS),
      url,
    });
    result = Object.freeze({
      detail: `HTTP ${response.status}`,
      host,
      ok: true,
    });
  } catch (error: unknown) {
    result = Object.freeze({
      detail: error instanceof Error ? error.message : "connection failed",
      host,
      ok: false,
    });
  } finally {
    if (owned) {
      try {
        await transport.close?.();
      } catch {
        // The connectivity result remains useful if best-effort cleanup fails.
      }
    }
  }
  return result;
}

/** Probe a deterministic host list without sharing cookies or connections. */
export async function probeRestHosts(
  hosts: readonly string[],
  options: RestHostProbeOptions = {},
): Promise<readonly RestHostProbeResult[]> {
  const results: RestHostProbeResult[] = [];
  for (const host of [...new Set(hosts)].sort()) {
    results.push(await probeRestHost(host, options));
  }
  return Object.freeze(results);
}
