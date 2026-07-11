import { LosslessNumber } from "lossless-json";

const INTEGER_TOKEN = /^-?(?:0|[1-9][0-9]*)$/;
const NUMBER_TOKEN = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/;
const MAX_JSON_DEPTH = 128;

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

  constructor(
    private readonly text: string,
    private readonly validateNumbers: boolean,
  ) {}

  scan(): void {
    this.skipWhitespace();
    this.scanValue(0);
    this.skipWhitespace();
    if (this.index !== this.text.length) {
      throw new SyntaxError("unexpected content after JSON value");
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
        throw new SyntaxError("object keys must be JSON strings");
      }
      const key = this.scanString();
      if (keys.has(key)) {
        throw new SyntaxError(`duplicate JSON key ${JSON.stringify(key)}`);
      }
      keys.add(key);
      this.skipWhitespace();
      this.expect(":");
      this.scanValue(depth);
      this.skipWhitespace();
      const separator = this.text[this.index];
      if (separator === "}") {
        this.index += 1;
        return;
      }
      this.expect(",");
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
      this.expect(",");
      this.skipWhitespace();
    }
  }

  private scanString(): string {
    const start = this.index;
    this.index += 1;
    while (this.index < this.text.length) {
      const character = this.text[this.index];
      if (character === '"') {
        this.index += 1;
        return JSON.parse(this.text.slice(start, this.index)) as string;
      }
      if (character === "\\") {
        this.index += 2;
      } else {
        this.index += 1;
      }
    }
    throw new SyntaxError("unterminated JSON string");
  }

  private scanLiteral(literal: "true" | "false" | "null"): void {
    if (this.text.slice(this.index, this.index + literal.length) !== literal) {
      throw new SyntaxError(`invalid JSON token at offset ${this.index}`);
    }
    this.index += literal.length;
  }

  private scanNumber(): void {
    const match = NUMBER_TOKEN.exec(this.text.slice(this.index));
    if (match === null) {
      throw new SyntaxError(`invalid JSON token at offset ${this.index}`);
    }
    const token = match[0];
    if (this.validateNumbers) {
      parseControlNumber(token);
    }
    this.index += token.length;
  }

  private expect(character: ":" | ","): void {
    if (this.text[this.index] !== character) {
      throw new SyntaxError(
        `expected ${JSON.stringify(character)} at offset ${this.index}`,
      );
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
