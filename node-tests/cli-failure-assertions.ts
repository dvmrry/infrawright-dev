import assert from "node:assert/strict";

interface ExpectedCliFailureMetadata {
  readonly category: "domain" | "internal" | "transient" | "usage";
  readonly code: string;
  readonly retryable: boolean;
}

/**
 * Preserve the Python diagnostic as the first, byte-identical CLI line while
 * explicitly qualifying Node's additive structured operator metadata.
 */
export function assertCliFailureExtendsLegacy(
  actual: string,
  legacy: string,
  expected: ExpectedCliFailureMetadata,
  message?: string,
): void {
  const suffix = [
    `  code: ${expected.code}`,
    `  category: ${expected.category}`,
    `  retryable: ${expected.retryable ? "yes" : "no"}`,
    "",
  ].join("\n");
  assert.equal(actual, `${legacy}${suffix}`, message);
}
