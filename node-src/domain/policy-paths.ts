export const POLICY_LIST_MARKER = "[]";
export const POLICY_WILDCARD = "*";

export type PolicyPathSegment = string | number | bigint;
export type ConcretePathSegment = string | number;

const NAME = /^[A-Za-z_][A-Za-z0-9_]*/;
const ASCII_DIGITS = /^[0-9]+$/;

export class PolicyPathError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PolicyPathError";
  }
}

function invalidSegment(
  raw: string,
  fullPath: string,
  what: string,
): never {
  throw new PolicyPathError(
    `invalid ${what} segment ${JSON.stringify(raw)} in ${JSON.stringify(fullPath)}`,
  );
}

function splitDotted(text: string, what: string): string[] {
  const parts: string[] = [];
  let buffer = "";
  let inQuote = false;
  let escaped = false;
  for (const character of text) {
    if (escaped) {
      buffer += character;
      escaped = false;
      continue;
    }
    if (character === "\\" && inQuote) {
      buffer += character;
      escaped = true;
      continue;
    }
    if (character === "\"") {
      inQuote = !inQuote;
      buffer += character;
      continue;
    }
    if (character === "." && !inQuote) {
      parts.push(buffer);
      buffer = "";
      continue;
    }
    buffer += character;
  }
  if (inQuote) {
    throw new PolicyPathError(
      `unterminated quoted ${what} selector in ${JSON.stringify(text)}`,
    );
  }
  parts.push(buffer);
  return parts;
}

function selectorEnd(
  raw: string,
  start: number,
  fullPath: string,
  what: string,
): number {
  let inQuote = false;
  let escaped = false;
  for (let index = start + 1; index < raw.length; index += 1) {
    const character = raw[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (character === "\\" && inQuote) {
      escaped = true;
      continue;
    }
    if (character === "\"") {
      inQuote = !inQuote;
      continue;
    }
    if (character === "]" && !inQuote) {
      return index;
    }
  }
  throw new PolicyPathError(
    `unterminated ${what} selector in ${JSON.stringify(fullPath)}`,
  );
}

function unquoteSelector(text: string): string {
  return text.replaceAll('\\"', '"').replaceAll("\\\\", "\\");
}

function parseIndex(text: string): number | bigint {
  const value = BigInt(text);
  return value <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(value) : value;
}

function parseSegment(
  raw: string,
  fullPath: string,
  what: string,
): PolicyPathSegment[] {
  const match = NAME.exec(raw);
  if (match === null) {
    return invalidSegment(raw, fullPath, what);
  }
  const output: PolicyPathSegment[] = [match[0]];
  let position = match[0].length;
  while (position < raw.length) {
    if (raw[position] !== "[") {
      return invalidSegment(raw, fullPath, what);
    }
    const end = selectorEnd(raw, position, fullPath, what);
    const selector = raw.slice(position + 1, end);
    if (selector === "" || selector === POLICY_WILDCARD) {
      output.push(POLICY_WILDCARD);
    } else if (ASCII_DIGITS.test(selector)) {
      output.push(parseIndex(selector));
    } else if (
      selector.length >= 2
      && selector.startsWith('"')
      && selector.endsWith('"')
    ) {
      output.push(unquoteSelector(selector.slice(1, -1)));
    } else {
      throw new PolicyPathError(
        `invalid ${what} selector ${JSON.stringify(selector)} in ${JSON.stringify(fullPath)}`,
      );
    }
    position = end + 1;
  }
  return output;
}

/** Parse the strict path dialect accepted by drift-policy entries. */
export function parsePolicyPath(
  text: unknown,
  what = "policy path",
): PolicyPathSegment[] {
  if (typeof text !== "string" || text.length === 0) {
    throw new PolicyPathError(`${what} must be a non-empty string`);
  }
  const output: PolicyPathSegment[] = [];
  for (const raw of splitDotted(text, what)) {
    output.push(...parseSegment(raw, text, what));
  }
  return output;
}

function isCollectionSelector(segment: PolicyPathSegment): boolean {
  return typeof segment === "number"
    || typeof segment === "bigint"
    || segment === POLICY_WILDCARD;
}

/** Match policy selectors; [] and [*] match numeric list indexes only. */
export function policySelectorMatches(
  selector: readonly PolicyPathSegment[],
  actual: readonly ConcretePathSegment[],
): boolean {
  if (selector.length !== actual.length) {
    return false;
  }
  return selector.every((segment, index) => {
    const candidate = actual[index];
    if (segment === POLICY_WILDCARD) {
      return typeof candidate === "number" && Number.isInteger(candidate);
    }
    return segment === candidate;
  });
}

export function policyPathHasWildcardOrIndex(
  path: readonly PolicyPathSegment[],
): boolean {
  return path.some(isCollectionSelector);
}

export function policyPathsEqual(
  left: readonly PolicyPathSegment[],
  right: readonly PolicyPathSegment[],
): boolean {
  return left.length === right.length
    && left.every((segment, index) => segment === right[index]);
}

/** Collapse exact indexes and wildcard selectors to the report-style [] marker. */
export function normalizePolicyPath(
  path: readonly PolicyPathSegment[],
): string[] {
  return path.map((segment) => (
    isCollectionSelector(segment) ? POLICY_LIST_MARKER : segment
  ) as string);
}

/** Faithful display used by diagnostics; exact indexes remain concrete. */
export function formatPolicyPath(
  path: readonly PolicyPathSegment[],
): string {
  if (path.length === 0) {
    return "<root>";
  }
  const parts: string[] = [];
  for (const segment of path) {
    if (segment === POLICY_WILDCARD || segment === POLICY_LIST_MARKER) {
      if (parts.length === 0) {
        parts.push(POLICY_LIST_MARKER);
      } else {
        const previous = parts[parts.length - 1];
        parts[parts.length - 1] = `${previous}${POLICY_LIST_MARKER}`;
      }
    } else if (typeof segment === "number" || typeof segment === "bigint") {
      const rendered = `[${String(segment)}]`;
      if (parts.length === 0) {
        parts.push(rendered);
      } else {
        const previous = parts[parts.length - 1];
        parts[parts.length - 1] = `${previous}${rendered}`;
      }
    } else {
      parts.push(segment);
    }
  }
  return parts.join(".");
}
