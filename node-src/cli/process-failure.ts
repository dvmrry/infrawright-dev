import { ProcessFailure } from "../domain/errors.js";

function indent(value: string): string {
  return value.replace(/\r?\n/gu, "\n  ");
}

/** Render the domain failure fields without turning the CLI into a wire protocol. */
export function renderCliProcessFailure(failure: ProcessFailure): string {
  const lines = [
    `error: ${indent(failure.message)}`,
    `  code: ${failure.code}`,
    `  category: ${failure.category}`,
    `  retryable: ${failure.retryable ? "yes" : "no"}`,
  ];
  for (const detail of failure.details) {
    lines.push(
      `  detail: ${detail.path} [${detail.code}] ${indent(detail.message)}`,
    );
  }
  return `${lines.join("\n")}\n`;
}
