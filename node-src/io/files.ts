import { readFile } from "node:fs/promises";

import { ProcessFailure } from "../domain/errors.js";

function decodeUtf8(content: Uint8Array, label: string): string {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(content);
  } catch {
    throw new ProcessFailure({
      code: "INVALID_UTF8",
      category: "domain",
      message: `${label} is not valid UTF-8`,
    });
  }
}

export async function readRequiredUtf8(
  path: string,
  label: string,
): Promise<string> {
  try {
    return decodeUtf8(await readFile(path), label);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw new ProcessFailure({
      code: "READ_FAILED",
      category: "io",
      message: `unable to read ${label}`,
    });
  }
}

export async function readOptionalUtf8(
  path: string,
  label: string,
): Promise<string | null> {
  try {
    return decodeUtf8(await readFile(path), label);
  } catch (error: unknown) {
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) {
      return null;
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw new ProcessFailure({
      code: "READ_FAILED",
      category: "io",
      message: `unable to read ${label}`,
    });
  }
}
