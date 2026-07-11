type JsonObject = { readonly [key: string]: JsonValue };
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | readonly JsonValue[]
  | JsonObject;

function compareCodePoints(
  left: string,
  right: string,
): number {
  let leftIndex = 0;
  let rightIndex = 0;
  while (leftIndex < left.length && rightIndex < right.length) {
    const leftPoint = left.codePointAt(leftIndex) ?? 0;
    const rightPoint = right.codePointAt(rightIndex) ?? 0;
    const delta = leftPoint - rightPoint;
    if (delta !== 0) {
      return delta;
    }
    leftIndex += leftPoint > 0xffff ? 2 : 1;
    rightIndex += rightPoint > 0xffff ? 2 : 1;
  }
  return (leftIndex < left.length ? 1 : 0) - (rightIndex < right.length ? 1 : 0);
}

export function sortedStrings(values: Iterable<string>): string[] {
  return Array.from(values).sort(compareCodePoints);
}

function encodeString(value: string): string {
  return JSON.stringify(value).replace(/[\u0080-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function encode(value: JsonValue, level: number): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || Object.is(value, -0)) {
      throw new TypeError(
        "the initial Python-compatible renderer accepts safe integers only",
      );
    }
    return String(value);
  }
  if (typeof value === "string") {
    return encodeString(value);
  }
  const currentIndent = "  ".repeat(level);
  const childIndent = "  ".repeat(level + 1);
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return "[]";
    }
    return [
      "[",
      value.map((item) => `${childIndent}${encode(item, level + 1)}`).join(",\n"),
      `${currentIndent}]`,
    ].join("\n");
  }
  const objectValue = value as JsonObject;
  const entries = sortedStrings(Object.keys(objectValue)).map((key) => {
    const child = objectValue[key];
    if (child === undefined) {
      throw new TypeError("undefined is not a JSON value");
    }
    return `${childIndent}${encodeString(key)}: ${encode(child, level + 1)}`;
  });
  if (entries.length === 0) {
    return "{}";
  }
  return ["{", entries.join(",\n"), `${currentIndent}}`].join("\n");
}

/** Match json.dumps(..., indent=2, sort_keys=True) for integer-only contracts. */
export function renderPythonCompatibleJson(value: JsonValue): string {
  return `${encode(value, 0)}\n`;
}
