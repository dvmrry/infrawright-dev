import { LosslessNumber } from "lossless-json";

const INTEGER_TOKEN = /^-?(?:0|[1-9][0-9]*)$/;
const NUMBER_TOKEN = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/;
const MAX_JSON_DEPTH = 128;
const UNPAIRED_UTF16_SURROGATE = "Unpaired UTF-16 surrogate";

/** SyntaxError whose message matches CPython's JSONDecodeError text. */
export class PythonJsonDecodeError extends SyntaxError {
  readonly position: number;

  constructor(reason: string, source: string, position: number) {
    const bounded = Math.max(0, Math.min(position, source.length));
    const before = source.slice(0, bounded);
    const line = 1 + (before.match(/\n/g)?.length ?? 0);
    const lastNewline = before.lastIndexOf("\n");
    const column = bounded - lastNewline;
    super(`${reason}: line ${line} column ${column} (char ${bounded})`);
    this.name = "PythonJsonDecodeError";
    this.position = bounded;
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

class JsonContractScanner {
  private index = 0;
  private firstUnpairedSurrogateOffset: number | undefined;
  private lastStringHasUnpairedSurrogate = false;

  constructor(
    private readonly text: string,
    private readonly validateNumbers: boolean,
  ) {}

  scan(): void {
    this.skipWhitespace();
    this.scanValue(0);
    this.skipWhitespace();
    if (this.index !== this.text.length) {
      throw new PythonJsonDecodeError("Extra data", this.text, this.index);
    }
    if (this.firstUnpairedSurrogateOffset !== undefined) {
      throw new PythonJsonDecodeError(
        UNPAIRED_UTF16_SURROGATE,
        this.text,
        this.firstUnpairedSurrogateOffset,
      );
    }
  }

  private scanValue(depth: number): void {
    this.skipWhitespace();
    const character = this.text[this.index];
    if (character === "{") {
      this.scanObject(depth + 1);
    } else if (character === "[") {
      this.scanArray(depth + 1);
    } else if (character === '"') {
      this.scanString();
    } else if (character === "t") {
      this.scanLiteral("true");
    } else if (character === "f") {
      this.scanLiteral("false");
    } else if (character === "n") {
      this.scanLiteral("null");
    } else {
      this.scanNumber();
    }
  }

  private checkDepth(depth: number): void {
    if (depth > MAX_JSON_DEPTH) {
      throw new SyntaxError(`JSON nesting exceeds ${MAX_JSON_DEPTH}`);
    }
  }

  private scanObject(depth: number): void {
    this.checkDepth(depth);
    this.index += 1;
    this.skipWhitespace();
    if (this.text[this.index] === "}") {
      this.index += 1;
      return;
    }
    const keys = new Set<string>();
    while (true) {
      if (this.text[this.index] !== '"') {
        throw new PythonJsonDecodeError(
          "Expecting property name enclosed in double quotes",
          this.text,
          this.index,
        );
      }
      const key = this.scanString();
      if (!this.lastStringHasUnpairedSurrogate) {
        if (keys.has(key)) {
          throw new SyntaxError(`duplicate JSON key ${JSON.stringify(key)}`);
        }
        keys.add(key);
      }
      this.skipWhitespace();
      this.expect(":", "Expecting ':' delimiter");
      this.scanValue(depth);
      this.skipWhitespace();
      const separator = this.text[this.index];
      if (separator === "}") {
        this.index += 1;
        return;
      }
      this.expect(",", "Expecting ',' delimiter");
      this.skipWhitespace();
    }
  }

  private scanArray(depth: number): void {
    this.checkDepth(depth);
    this.index += 1;
    this.skipWhitespace();
    if (this.text[this.index] === "]") {
      this.index += 1;
      return;
    }
    while (true) {
      this.scanValue(depth);
      this.skipWhitespace();
      const separator = this.text[this.index];
      if (separator === "]") {
        this.index += 1;
        return;
      }
      this.expect(",", "Expecting ',' delimiter");
      this.skipWhitespace();
    }
  }

  private scanString(): string {
    const start = this.index;
    this.lastStringHasUnpairedSurrogate = false;
    this.index += 1;
    while (this.index < this.text.length) {
      const character = this.text[this.index];
      if (character === '"') {
        this.index += 1;
        // Keep JSON.parse first: malformed escapes remain native syntax
        // errors, rather than being mistaken for the surrogate contract.
        const decoded = JSON.parse(this.text.slice(start, this.index)) as string;
        const invalidOffset = firstUnpairedJsonStringSurrogateOffset(
          this.text,
          start,
          this.index,
        );
        if (invalidOffset !== undefined && this.firstUnpairedSurrogateOffset === undefined) {
          this.firstUnpairedSurrogateOffset = invalidOffset;
        }
        this.lastStringHasUnpairedSurrogate = invalidOffset !== undefined;
        return decoded;
      }
      if (character === "\\") {
        this.index += 2;
      } else {
        this.index += 1;
      }
    }
    throw new PythonJsonDecodeError(
      "Unterminated string starting at",
      this.text,
      start,
    );
  }

  private scanLiteral(literal: "true" | "false" | "null"): void {
    if (this.text.slice(this.index, this.index + literal.length) !== literal) {
      throw new PythonJsonDecodeError("Expecting value", this.text, this.index);
    }
    this.index += literal.length;
  }

  private scanNumber(): void {
    const match = NUMBER_TOKEN.exec(this.text.slice(this.index));
    if (match === null) {
      throw new PythonJsonDecodeError("Expecting value", this.text, this.index);
    }
    const token = match[0];
    if (this.validateNumbers) {
      parseControlNumber(token);
    }
    this.index += token.length;
  }

  private expect(character: ":" | ",", reason: string): void {
    if (this.text[this.index] !== character) {
      throw new PythonJsonDecodeError(reason, this.text, this.index);
    }
    this.index += 1;
  }

  private skipWhitespace(): void {
    while (/\s/.test(this.text[this.index] ?? "") && this.index < this.text.length) {
      const character = this.text[this.index];
      if (character !== " " && character !== "\n" && character !== "\r" && character !== "\t") {
        throw new SyntaxError(`invalid JSON whitespace at offset ${this.index}`);
      }
      this.index += 1;
    }
  }
}

function isHighSurrogate(unit: number): boolean {
  return unit >= 0xd800 && unit <= 0xdbff;
}

function isLowSurrogate(unit: number): boolean {
  return unit >= 0xdc00 && unit <= 0xdfff;
}

function hexUnit(text: string): number {
  return Number.parseInt(text, 16);
}

function standardEscapeUnit(escape: string): number {
  switch (escape) {
    case '"':
    case "\\":
    case "/":
      return escape.charCodeAt(0);
    case "b": return 0x08;
    case "f": return 0x0c;
    case "n": return 0x0a;
    case "r": return 0x0d;
    case "t": return 0x09;
    default: return 0;
  }
}

// Returns the first raw source offset for an unpaired decoded UTF-16 unit in
// one already-JSON.parse-validated string literal. A \uXXXX unit points at
// its backslash, while a literal unit points at that source UTF-16 code unit.
function firstUnpairedJsonStringSurrogateOffset(
  text: string,
  start: number,
  end: number,
): number | undefined {
  let pendingHighOffset: number | undefined;
  const acceptUnit = (unit: number, offset: number): number | undefined => {
    if (pendingHighOffset !== undefined) {
      if (isLowSurrogate(unit)) {
        pendingHighOffset = undefined;
        return undefined;
      }
      return pendingHighOffset;
    }
    if (isHighSurrogate(unit)) {
      pendingHighOffset = offset;
    } else if (isLowSurrogate(unit)) {
      return offset;
    }
    return undefined;
  };

  for (let index = start + 1; index < end - 1;) {
    const offset = index;
    if (text[index] === "\\") {
      const escape = text[index + 1] ?? "";
      if (escape === "u") {
        const invalidOffset = acceptUnit(
          hexUnit(text.slice(index + 2, index + 6)),
          offset,
        );
        index += 6;
        if (invalidOffset !== undefined) return invalidOffset;
      } else {
        const invalidOffset = acceptUnit(standardEscapeUnit(escape), offset);
        index += 2;
        if (invalidOffset !== undefined) return invalidOffset;
      }
    } else {
      const invalidOffset = acceptUnit(text.charCodeAt(index), offset);
      index += 1;
      if (invalidOffset !== undefined) return invalidOffset;
    }
  }
  return pendingHighOffset;
}

/**
 * Reject unpaired UTF-16 surrogate units in every JSON string token.
 *
 * Call only after a document-level JSON.parse has succeeded. It intentionally
 * does not enforce the control dialect's duplicate-key, depth, or number
 * rules, so metadata can use the same string invariant without inheriting
 * those unrelated restrictions.
 */
export function validateJsonStringSurrogates(text: string): void {
  for (let index = 0; index < text.length;) {
    if (text[index] !== '"') {
      index += 1;
      continue;
    }
    const start = index;
    index += 1;
    while (index < text.length) {
      if (text[index] === '"') {
        index += 1;
        const invalidOffset = firstUnpairedJsonStringSurrogateOffset(text, start, index);
        if (invalidOffset !== undefined) {
          throw new PythonJsonDecodeError(UNPAIRED_UTF16_SURROGATE, text, invalidOffset);
        }
        break;
      }
      index += text[index] === "\\" ? 2 : 1;
    }
  }
}

function validateJsonContract(text: string, validateNumbers: boolean): void {
  new JsonContractScanner(text, validateNumbers).scan();
}

/** Parse protocol/config JSON without silent numeric truncation or duplicate keys. */
export function parseControlJson(text: string): unknown {
  validateJsonContract(text, true);
  return JSON.parse(text) as unknown;
}

/** Parse provider/Terraform JSON while preserving every numeric token. */
export function parseDataJsonLosslessly(text: string): unknown {
  validateJsonContract(text, false);
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
