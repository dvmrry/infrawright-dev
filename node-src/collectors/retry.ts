const MAX_RETRIES = 5;
const RETRY_CAP_MS = 30_000;
const PYTHON_FLOAT = /^[+-]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?|inf(?:inity)?|nan)$/i;

/** Match Python float parsing and the collector's bounded 429 schedule. */
export function retryDelayMs(
  attempt: number,
  retryAfter: string | null | undefined,
): number {
  const token = retryAfter?.trim();
  if (token !== undefined && token !== "" && PYTHON_FLOAT.test(token)) {
    const normalized = token.toLowerCase().replace(/^[+-]/, "");
    if (normalized === "nan") return 0;
    const sign = token.startsWith("-") ? -1 : 1;
    const seconds = normalized === "inf" || normalized === "infinity"
      ? sign * Number.POSITIVE_INFINITY
      : Number(token);
    return Math.max(0, Math.min(seconds * 1_000, RETRY_CAP_MS));
  }
  return Math.min(1_000 * (2 ** attempt), RETRY_CAP_MS);
}

export function collectorMaxRetries(): number {
  return MAX_RETRIES;
}
