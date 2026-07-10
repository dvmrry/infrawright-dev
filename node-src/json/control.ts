import {
  LosslessNumber,
  parse as parseLossless,
} from "lossless-json";

const INTEGER_TOKEN = /^-?(?:0|[1-9][0-9]*)$/;
const MAX_JSON_DEPTH = 128;

function assertBoundedDepth(text: string): void {
  let depth = 0;
  let escaped = false;
  let inString = false;
  for (const character of text) {
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === '"') {
        inString = false;
      }
      continue;
    }
    if (character === '"') {
      inString = true;
    } else if (character === "{" || character === "[") {
      depth += 1;
      if (depth > MAX_JSON_DEPTH) {
        throw new SyntaxError(`JSON nesting exceeds ${MAX_JSON_DEPTH}`);
      }
    } else if (character === "}" || character === "]") {
      depth -= 1;
    }
  }
}

function parseControlNumber(token: string): number {
  const value = Number(token);
  if (!Number.isFinite(value)) {
    throw new SyntaxError("non-finite JSON numbers are not accepted");
  }
  if (INTEGER_TOKEN.test(token) && !Number.isSafeInteger(value)) {
    throw new SyntaxError(
      "control JSON integers must be exactly representable",
    );
  }
  return value;
}

/** Parse protocol/config JSON without silent numeric truncation or duplicate keys. */
export function parseControlJson(text: string): unknown {
  // lossless-json supplies duplicate-key and numeric-safety validation. Native
  // JSON.parse constructs special property names such as "__proto__" as own
  // data properties instead of invoking an object prototype setter.
  assertBoundedDepth(text);
  parseLossless(text, undefined, { parseNumber: parseControlNumber });
  return JSON.parse(text) as unknown;
}

/** Parse provider/Terraform JSON while preserving every numeric token. */
export function parseDataJsonLosslessly(text: string): unknown {
  // Validate duplicate keys first, then use Node 24's source context so native
  // object construction and exact numeric lexemes are both preserved.
  assertBoundedDepth(text);
  parseLossless(text);
  const parseWithSource = JSON.parse as unknown as (
    source: string,
    reviver: (
      key: string,
      value: unknown,
      context: { source?: string },
    ) => unknown,
  ) => unknown;
  return parseWithSource(
    text,
    (_key: string, value: unknown, context: { source?: string }) => {
      return typeof value === "number" && context.source !== undefined
        ? new LosslessNumber(context.source)
        : value;
    },
  ) as unknown;
}
