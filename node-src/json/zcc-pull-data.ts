import { ProcessFailure } from "../domain/errors.js";
import { parseDataJsonLosslessly } from "./control.js";

const MAX_PULL_JSON_DEPTH = 128;
const MAX_PULL_JSON_TOKENS = 250_000;
const MAX_PULL_ITEMS = 50_000;
const MAX_PULL_STRING_TOKEN_CHARACTERS = 1024 * 1024;
const MAX_PULL_NUMBER_TOKEN_CHARACTERS = 1024;

function complexityFailure(): never {
  throw new ProcessFailure({
    code: "PULL_DATA_COMPLEXITY_LIMIT",
    category: "domain",
    message: "pull data JSON exceeds a structural complexity limit",
  });
}

function invalidPullData(): never {
  throw new ProcessFailure({
    code: "INVALID_PULL_DATA_JSON",
    category: "domain",
    message: "pull data must be a valid JSON array",
  });
}

function isJsonWhitespace(character: string): boolean {
  return character === " "
    || character === "\n"
    || character === "\r"
    || character === "\t";
}

function isStructural(character: string): boolean {
  return character === "{"
    || character === "}"
    || character === "["
    || character === "]"
    || character === ":"
    || character === ",";
}

/** Bound parser allocation and recursion before constructing the pull graph. */
function preflightPullDataJson(text: string): void {
  let depth = 0;
  let index = 0;
  let tokens = 0;
  let topLevelItems = 0;
  let rootIsArray = false;

  const addToken = (): void => {
    tokens += 1;
    if (tokens > MAX_PULL_JSON_TOKENS) {
      complexityFailure();
    }
  };
  const addTopLevelItem = (): void => {
    if (!rootIsArray || depth !== 1) {
      return;
    }
    topLevelItems += 1;
    if (topLevelItems > MAX_PULL_ITEMS) {
      complexityFailure();
    }
  };

  while (index < text.length) {
    const character = text[index] ?? "";
    if (isJsonWhitespace(character)) {
      index += 1;
      continue;
    }
    if (character === '"') {
      addTopLevelItem();
      addToken();
      index += 1;
      let escaped = false;
      let tokenCharacters = 0;
      while (index < text.length) {
        const current = text[index] ?? "";
        if (!escaped && current === '"') {
          index += 1;
          break;
        }
        tokenCharacters += 1;
        if (tokenCharacters > MAX_PULL_STRING_TOKEN_CHARACTERS) {
          complexityFailure();
        }
        if (escaped) {
          escaped = false;
        } else if (current === "\\") {
          escaped = true;
        }
        index += 1;
      }
      continue;
    }
    if (isStructural(character)) {
      if (character === "{" || character === "[") {
        addTopLevelItem();
        if (depth === 0 && character === "[") {
          rootIsArray = true;
        }
        depth += 1;
        if (depth > MAX_PULL_JSON_DEPTH) {
          complexityFailure();
        }
      } else if (character === "}" || character === "]") {
        depth -= 1;
      }
      addToken();
      index += 1;
      continue;
    }

    addTopLevelItem();
    addToken();
    let tokenCharacters = 0;
    while (index < text.length) {
      const current = text[index] ?? "";
      if (isJsonWhitespace(current) || isStructural(current) || current === '"') {
        break;
      }
      tokenCharacters += 1;
      if (tokenCharacters > MAX_PULL_NUMBER_TOKEN_CHARACTERS) {
        complexityFailure();
      }
      index += 1;
    }
  }
}

/** Parse a bounded raw pull array without truncating provider numeric tokens. */
export function parseZccPullDataJson(text: string): readonly unknown[] {
  if (typeof text !== "string") {
    return invalidPullData();
  }
  try {
    preflightPullDataJson(text);
    const parsed = parseDataJsonLosslessly(text);
    if (!Array.isArray(parsed) || parsed.length > MAX_PULL_ITEMS) {
      return invalidPullData();
    }
    return parsed;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return invalidPullData();
  }
}
