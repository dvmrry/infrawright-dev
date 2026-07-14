import {
  type CollectorAcquireInput,
  type CollectorAdapter,
  type CollectorAuthContext,
  type CollectorAuthMode,
  type CollectorComposeUrlInput,
  type CollectorContext,
  type HttpResponse,
} from "./types.js";

const ONEAPI_AUDIENCE = "https://api.zscaler.com";
const DNS_LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
const TRUTHY = new Set(["1", "true", "yes", "on"]);

const ZPA_LEGACY_BASES: Readonly<Record<string, string>> = Object.freeze({
  "": "https://config.private.zscaler.com",
  PRODUCTION: "https://config.private.zscaler.com",
  ZPATWO: "https://config.zpatwo.net",
  BETA: "https://config.zpabeta.net",
  GOV: "https://config.zpagov.net",
  GOVUS: "https://config.zpagov.us",
});

const ACCEPT_HEADERS = Object.freeze({ Accept: "application/json" });

function fail(message: string): never {
  throw new Error(message);
}

function requireEnvironment(
  environment: NodeJS.ProcessEnv,
  name: string,
): string {
  const value = environment[name];
  if (value === undefined || value === "") {
    return fail(`missing required env var ${name}`);
  }
  return value;
}

function normalizedLabel(
  value: string | undefined,
  label: string,
  allowPlaceholder = false,
): string {
  const text = (value ?? "").trim().toLowerCase();
  if (allowPlaceholder && text === "<vanity>") return text;
  if (text === "" || !DNS_LABEL.test(text)) {
    return fail(
      `${label} must be a DNS label (letters, digits, hyphen; no dots, slashes, or empty labels)`,
    );
  }
  return text;
}

function normalizedCloud(
  cloud: string | undefined,
  label = "ZSCALER_CLOUD",
): string {
  const text = (cloud ?? "").trim().toLowerCase();
  if (text === "" || text === "production") return "";
  return normalizedLabel(text, label);
}

/** Validate a legacy private/custom host override without imposing an allowlist. */
export function normalizeLegacyBaseUrl(
  name: "ZIA_LEGACY_BASE_URL" | "ZPA_LEGACY_BASE_URL",
  value: string | undefined,
): string {
  if (value === undefined || value === "") return "";
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return fail(`${name} must be an https:// host URL`);
  }
  if (parsed.protocol.toLowerCase() !== "https:" || parsed.hostname === "") {
    return fail(`${name} must be an https:// host URL`);
  }
  if (parsed.username !== "" || parsed.password !== "") {
    return fail(`${name} must not contain username or password`);
  }
  if (
    (parsed.pathname !== "" && parsed.pathname !== "/")
    || parsed.search !== ""
    || parsed.hash !== ""
  ) {
    return fail(`${name} must not contain path, query, or fragment`);
  }
  for (const segment of parsed.hostname.toLowerCase().split(".")) {
    normalizedLabel(segment, `${name} host segment`);
  }
  const port = parsed.port === "" ? "" : `:${parsed.port}`;
  return `https://${parsed.hostname.toLowerCase()}${port}`;
}

function oneApiGateway(cloud: string | undefined): string {
  const suffix = normalizedCloud(cloud);
  return suffix === ""
    ? "https://api.zsapi.net"
    : `https://api.${suffix}.zsapi.net`;
}

function oneApiTokenHost(
  vanityDomain: string | undefined,
  cloud: string | undefined,
  allowPlaceholder = false,
): string {
  const vanity = normalizedLabel(
    vanityDomain,
    "ZSCALER_VANITY_DOMAIN",
    allowPlaceholder,
  );
  const suffix = normalizedCloud(cloud);
  return `https://${vanity}.zslogin${suffix}.net`;
}

function ziaLegacyBase(context: CollectorContext): string {
  if ((context.ziaLegacyBase ?? "") !== "") {
    return normalizeLegacyBaseUrl(
      "ZIA_LEGACY_BASE_URL",
      context.ziaLegacyBase,
    );
  }
  if (context.cloud === "") {
    return fail(
      "ZIA_CLOUD is required in legacy mode (e.g. zscalertwo) — it selects the ZIA host https://zsapi.<cloud>.net",
    );
  }
  return `https://zsapi.${normalizedLabel(context.cloud, "ZIA_CLOUD")}.net`;
}

function zpaLegacyBaseOrUndefined(cloud: string | undefined): string | undefined {
  return ZPA_LEGACY_BASES[(cloud ?? "").trim().toUpperCase()];
}

function zpaLegacyBase(context: CollectorContext): string {
  if ((context.zpaLegacyBase ?? "") !== "") {
    return normalizeLegacyBaseUrl(
      "ZPA_LEGACY_BASE_URL",
      context.zpaLegacyBase,
    );
  }
  const base = zpaLegacyBaseOrUndefined(context.zpaCloud);
  if (base === undefined) {
    const known = Object.keys(ZPA_LEGACY_BASES)
      .filter((value) => value !== "")
      .join(", ");
    return fail(
      `unknown ZPA_CLOUD ${JSON.stringify(context.zpaCloud ?? "")} for the legacy config base — set ZPA_LEGACY_BASE_URL to the correct https://config.<cloud> host (known clouds: ${known})`,
    );
  }
  return base;
}

function responseText(response: HttpResponse): string {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(response.body);
  } catch {
    return fail("authentication response is not valid UTF-8");
  }
}

function tokenField(
  response: HttpResponse,
  key: string,
  label: string,
): string {
  let parsed: unknown;
  try {
    parsed = JSON.parse(responseText(response));
  } catch {
    return fail(
      `${label}: HTTP 200 but the response is not JSON (maintenance page? proxy interception?) — re-try, then check the auth endpoint with make fetch-diag`,
    );
  }
  if (
    parsed === null
    || typeof parsed !== "object"
    || Array.isArray(parsed)
    || !Object.hasOwn(parsed, key)
    || typeof (parsed as Record<string, unknown>)[key] !== "string"
  ) {
    return fail(
      `${label}: HTTP 200 but no ${JSON.stringify(key)} in the response — check the API client's permissions/credentials for this product`,
    );
  }
  return (parsed as Record<string, string>)[key] ?? "";
}

function bearerContext(token: string): CollectorAuthContext {
  return Object.freeze({
    headers: Object.freeze({
      Accept: "application/json",
      Authorization: `Bearer ${token}`,
    }),
  });
}

function authPerformance(
  input: CollectorAcquireInput,
  endpointFamily: string,
): Record<string, unknown> {
  return input.performanceContext === undefined
    ? {}
    : {
      performance: {
        ...input.performanceContext,
        classification: "authentication" as const,
        endpointFamily,
      },
    };
}

async function acquireOneApi(
  input: CollectorAcquireInput,
): Promise<CollectorAuthContext> {
  const tokenUrl = new URL(
    "/oauth2/v1/token",
    oneApiTokenHost(
      requireEnvironment(input.environment, "ZSCALER_VANITY_DOMAIN"),
      input.environment.ZSCALER_CLOUD,
    ),
  );
  const body = new URLSearchParams([
    ["grant_type", "client_credentials"],
    ["client_id", requireEnvironment(input.environment, "ZSCALER_CLIENT_ID")],
    ["client_secret", requireEnvironment(input.environment, "ZSCALER_CLIENT_SECRET")],
    ["audience", ONEAPI_AUDIENCE],
  ]).toString();
  const response = await input.transport.request({
    body,
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    method: "POST",
    ...authPerformance(input, "oauth2/v1/token"),
    url: tokenUrl,
  });
  if (response.status !== 200) {
    return fail(`OneAPI token request failed: HTTP ${response.status}`);
  }
  return bearerContext(tokenField(response, "access_token", "OneAPI token"));
}

/** Port of the legacy ZIA API-key obfuscation used by the public SDK. */
export function obfuscateZiaApiKey(apiKey: string, timestamp: string): string {
  const apiKeyCharacters = [...apiKey];
  if ([...timestamp].length < 6 || apiKeyCharacters.length < 12) {
    return fail("timestamp or api key below required length");
  }
  const high = timestamp.slice(-6);
  const parsedHigh = Number.parseInt(high, 10);
  if (!/^[0-9]{6}$/.test(high) || !Number.isSafeInteger(parsedHigh)) {
    return fail("timestamp or api key below required length");
  }
  const low = String(parsedHigh >> 1).padStart(6, "0");
  let obfuscated = "";
  for (const digit of high) {
    obfuscated += apiKeyCharacters[Number(digit)] ?? "";
  }
  for (const digit of low) {
    obfuscated += apiKeyCharacters[Number(digit) + 2] ?? "";
  }
  return obfuscated;
}

async function acquireZiaLegacy(
  input: CollectorAcquireInput,
): Promise<CollectorAuthContext> {
  const timestamp = String(input.nowMs ?? Date.now());
  const response = await input.transport.request({
    body: JSON.stringify({
      apiKey: obfuscateZiaApiKey(
        requireEnvironment(input.environment, "ZIA_API_KEY"),
        timestamp,
      ),
      username: requireEnvironment(input.environment, "ZIA_USERNAME"),
      password: requireEnvironment(input.environment, "ZIA_PASSWORD"),
      timestamp,
    }),
    headers: { "Content-Type": "application/json" },
    method: "POST",
    ...authPerformance(input, "api/v1/authenticatedSession"),
    url: new URL(`${ziaLegacyBase(input.context)}/api/v1/authenticatedSession`),
  });
  if (response.status !== 200) {
    return fail(`ZIA session auth failed: HTTP ${response.status}`);
  }
  // The injected transport owns and persists the authenticated session cookie.
  return Object.freeze({ headers: ACCEPT_HEADERS });
}

async function acquireZpaLegacy(
  input: CollectorAcquireInput,
): Promise<CollectorAuthContext> {
  const response = await input.transport.request({
    body: new URLSearchParams([
      ["client_id", requireEnvironment(input.environment, "ZPA_CLIENT_ID")],
      ["client_secret", requireEnvironment(input.environment, "ZPA_CLIENT_SECRET")],
    ]).toString(),
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    method: "POST",
    ...authPerformance(input, "signin"),
    url: new URL(`${zpaLegacyBase(input.context)}/signin`),
  });
  if (response.status !== 200) {
    return fail(`ZPA signin failed: HTTP ${response.status}`);
  }
  return bearerContext(tokenField(response, "access_token", "ZPA signin"));
}

function composeProductUrl(
  product: "zia" | "zpa" | "zcc" | "ztc",
  input: CollectorComposeUrlInput,
): URL {
  if (input.mode === "oneapi") {
    const gateway = oneApiGateway(input.context.cloud);
    switch (product) {
      case "zia":
        return new URL(`${gateway}/zia/api/v1/${input.path}`);
      case "zpa":
        return new URL(
          `${gateway}/zpa/mgmtconfig/v1/admin/customers/${input.context.customerId}/${input.path}`,
        );
      case "zcc":
        return new URL(`${gateway}/${input.path}`);
      case "ztc":
        return new URL(
          input.path.startsWith("/")
            ? `${gateway}${input.path}`
            : `${gateway}/${input.path}`,
        );
    }
  }
  if (product === "zia") {
    return new URL(`${ziaLegacyBase(input.context)}/api/v1/${input.path}`);
  }
  if (product === "zpa") {
    return new URL(
      `${zpaLegacyBase(input.context)}/mgmtconfig/v1/admin/customers/${input.context.customerId}/${input.path}`,
    );
  }
  return fail(
    product === "zcc"
      ? "unknown auth_mode/product: 'legacy'/'zcc'"
      : "ZTC legacy auth is not wired in the collector yet. Use OneAPI, or scope ZTC out of legacy runs with RESOURCE=\"zia zpa zcc\".",
  );
}

function adapter(product: "zia" | "zpa" | "zcc" | "ztc"): CollectorAdapter {
  return Object.freeze({
    product,
    async acquire(input: CollectorAcquireInput): Promise<CollectorAuthContext> {
      if (input.mode === "oneapi") return acquireOneApi(input);
      if (product === "zia") return acquireZiaLegacy(input);
      if (product === "zpa") return acquireZpaLegacy(input);
      if (product === "zcc") {
        return fail(
          "ZCC has no legacy auth path — it is OneAPI-only. Use OneAPI, or scope ZCC out of legacy runs with RESOURCE=\"zia zpa\".",
        );
      }
      return fail(
        "ZTC legacy auth is not wired in the collector yet. Use OneAPI, or scope ZTC out of legacy runs with RESOURCE=\"zia zpa zcc\".",
      );
    },
    composeUrl(input: CollectorComposeUrlInput): URL {
      return composeProductUrl(product, input);
    },
  });
}

/** Built-in product adapters; resource selection remains registry-driven. */
export function createZscalerCollectorAdapters(): ReadonlyMap<string, CollectorAdapter> {
  return new Map([
    ["zia", adapter("zia")],
    ["zpa", adapter("zpa")],
    ["zcc", adapter("zcc")],
    ["ztc", adapter("ztc")],
  ]);
}

export function collectorAuthMode(
  environment: NodeJS.ProcessEnv,
): CollectorAuthMode {
  const flag = (environment.ZSCALER_USE_LEGACY_CLIENT ?? "").trim().toLowerCase();
  return TRUTHY.has(flag) ? "legacy" : "oneapi";
}

export function collectorContext(input: {
  readonly environment: NodeJS.ProcessEnv;
  readonly neededProducts: ReadonlySet<string>;
  readonly mode?: CollectorAuthMode;
}): CollectorContext {
  const mode = input.mode ?? collectorAuthMode(input.environment);
  const customerId = input.neededProducts.has("zpa")
    ? requireEnvironment(input.environment, "ZPA_CUSTOMER_ID")
    : input.environment.ZPA_CUSTOMER_ID ?? "";
  if (mode === "oneapi") {
    return Object.freeze({
      cloud: input.environment.ZSCALER_CLOUD ?? "",
      customerId,
    });
  }
  return Object.freeze({
    cloud: input.environment.ZIA_CLOUD || input.environment.ZSCALER_CLOUD || "",
    customerId,
    ziaLegacyBase: normalizeLegacyBaseUrl(
      "ZIA_LEGACY_BASE_URL",
      input.environment.ZIA_LEGACY_BASE_URL,
    ),
    zpaCloud: input.environment.ZPA_CLOUD ?? "",
    zpaLegacyBase: normalizeLegacyBaseUrl(
      "ZPA_LEGACY_BASE_URL",
      input.environment.ZPA_LEGACY_BASE_URL,
    ),
  });
}

/** Remove tenant-identifying vanity/customer values from relay-safe diagnostics. */
export function maskCollectorIdentifiers(value: string): string {
  return value
    .replace(
      /(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi,
      (_match, prefix: string, _vanity: string, suffix: string) => {
        return `${prefix}<vanity>${suffix}`;
      },
    )
    .replace(/(\/customers\/)[^/]+/gi, "$1<customer-id>");
}

function debugVerbose(environment: NodeJS.ProcessEnv): boolean {
  return TRUTHY.has((environment.FETCH_DEBUG ?? "").trim().toLowerCase());
}

function configuredHttpsProxy(environment: NodeJS.ProcessEnv): string {
  return Object.hasOwn(environment, "https_proxy")
    ? environment.https_proxy ?? ""
    : environment.HTTPS_PROXY ?? "";
}

function safeLegacyBase(derive: () => string, override: string | undefined): string {
  if ((override ?? "") !== "") return `${override} (override)`;
  try {
    return derive();
  } catch (error: unknown) {
    return `<unresolved: ${error instanceof Error ? error.message : String(error)}>`;
  }
}

export function fetchDebugLines(input: {
  readonly environment: NodeJS.ProcessEnv;
  readonly context: CollectorContext;
  readonly mode: CollectorAuthMode;
  readonly products: ReadonlySet<string>;
}): readonly string[] {
  const verbose = debugVerbose(input.environment);
  let masked = false;
  const identity = (value: string | undefined): string => {
    if ((value ?? "") !== "" && !verbose) {
      masked = true;
      return "set";
    }
    return value === undefined || value === "" ? "<unset>" : value;
  };
  const lines = [
    `fetch: auth mode = ${input.mode}`,
    `fetch: proxy = ${configuredHttpsProxy(input.environment) ? "set" : "not set"}`,
  ];
  if (input.mode === "oneapi") {
    lines.push(
      `fetch: ZSCALER_CLOUD = ${input.environment.ZSCALER_CLOUD || "(production)"}`,
      `fetch: ZSCALER_VANITY_DOMAIN = ${identity(input.environment.ZSCALER_VANITY_DOMAIN)}`,
    );
    if (input.context.customerId !== "") {
      lines.push(`fetch: ZPA_CUSTOMER_ID = ${identity(input.context.customerId)}`);
    }
    const configuredVanity = input.environment.ZSCALER_VANITY_DOMAIN;
    let shownVanity = configuredVanity || "<vanity>";
    if (!verbose) {
      if (configuredVanity) masked = true;
      shownVanity = "<vanity>";
    }
    lines.push(
      `fetch: token host = ${oneApiTokenHost(shownVanity, input.environment.ZSCALER_CLOUD, true)}`,
      `fetch: gateway = ${oneApiGateway(input.context.cloud)}`,
    );
  } else {
    lines.push(`fetch: ZIA_CLOUD = ${input.environment.ZIA_CLOUD || "<unset>"}`);
    if (input.products.has("zpa")) {
      lines.push(`fetch: ZPA_CLOUD = ${input.environment.ZPA_CLOUD || "(production)"}`);
    }
    if (input.context.customerId !== "") {
      lines.push(`fetch: ZPA_CUSTOMER_ID = ${identity(input.context.customerId)}`);
    }
    if (input.products.has("zia")) {
      lines.push(
        `fetch: zia base = ${safeLegacyBase(
          () => ziaLegacyBase(input.context),
          input.context.ziaLegacyBase,
        )}`,
      );
    }
    if (input.products.has("zpa")) {
      lines.push(
        `fetch: zpa base = ${safeLegacyBase(
          () => zpaLegacyBase(input.context),
          input.context.zpaLegacyBase,
        )}`,
      );
    }
  }
  if (masked) {
    lines.push("fetch: (vanity/customer-id hidden; set FETCH_DEBUG=1 to show)");
  }
  return Object.freeze(lines);
}

function hostOf(value: string): string {
  return value.split("//", 2).at(-1)?.split("/", 1)[0] ?? value;
}

/** Unique HTTPS hosts contacted by the selected active Zscaler products. */
export function diagnosticHosts(
  environment: NodeJS.ProcessEnv,
  products: ReadonlySet<string>,
): readonly string[] {
  const mode = collectorAuthMode(environment);
  if (mode === "oneapi") {
    if (!["zcc", "zia", "zpa", "ztc"].some((product) => products.has(product))) {
      return Object.freeze([]);
    }
    const vanity = environment.ZSCALER_VANITY_DOMAIN || "<vanity>";
    return Object.freeze([
      hostOf(oneApiGateway(environment.ZSCALER_CLOUD)),
      hostOf(oneApiTokenHost(vanity, environment.ZSCALER_CLOUD, true)),
    ].sort());
  }
  const hosts = new Set<string>();
  const ziaOverride = normalizeLegacyBaseUrl(
    "ZIA_LEGACY_BASE_URL",
    environment.ZIA_LEGACY_BASE_URL,
  );
  const zpaOverride = normalizeLegacyBaseUrl(
    "ZPA_LEGACY_BASE_URL",
    environment.ZPA_LEGACY_BASE_URL,
  );
  if (products.has("zia")) {
    const cloud = environment.ZIA_CLOUD || environment.ZSCALER_CLOUD || "<cloud>";
    hosts.add(hostOf(ziaOverride || `https://zsapi.${cloud}.net`));
  }
  if (products.has("zpa")) {
    hosts.add(hostOf(
      zpaOverride
      || zpaLegacyBaseOrUndefined(environment.ZPA_CLOUD)
      || "https://config.<zpa-cloud>",
    ));
  }
  return Object.freeze([...hosts].sort());
}
