import { lstat } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import {
  runTerraformCommand,
  snapshotTerraformCommandEnvironment,
  snapshotTerraformCommandLimits,
} from "./terraform-command.js";

export interface TerraformShowLimits {
  readonly timeoutMs: number;
  readonly maxStdoutBytes: number;
  readonly maxStderrBytes: number;
}

export const DEFAULT_TERRAFORM_SHOW_LIMITS: TerraformShowLimits = {
  timeoutMs: 120_000,
  maxStdoutBytes: 8 * 1024 * 1024,
  maxStderrBytes: 1024 * 1024,
};

const DEFAULT_TERRAFORM_SHOW_ENVIRONMENT = Object.freeze({
  CHECKPOINT_DISABLE: "1",
  LANG: "C",
  LC_ALL: "C",
});
const MAX_TERRAFORM_JSON_STRUCTURE_TOKENS = 100_000;
const MAX_TERRAFORM_JSON_STRING_CHARACTERS = 4 * 1024 * 1024;
const MAX_TERRAFORM_JSON_SCALAR_CHARACTERS = 1024 * 1024;

export interface TerraformShowOptions {
  readonly terraformExecutable: string;
  readonly envDir: string;
  readonly snapshotPath: string;
  /** Complete child environment; never merged with process.env. */
  readonly environment?: Readonly<Record<string, string>>;
  readonly limits?: TerraformShowLimits;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function snapshotShowLimits(value: TerraformShowLimits): TerraformShowLimits {
  try {
    return snapshotTerraformCommandLimits(value);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_LIMIT",
      "Terraform show limits must be positive",
    );
  }
}

function snapshotShowEnvironment(
  value: Readonly<Record<string, string>>,
): Readonly<Record<string, string>> {
  try {
    return snapshotTerraformCommandEnvironment(value);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_ENVIRONMENT",
      "Terraform show environment is not allowed",
    );
  }
}

function deadlineFailure(): never {
  return fail(
    "TERRAFORM_SHOW_TIMEOUT",
    "Terraform show exceeded its execution deadline",
    "io",
  );
}

function checkDeadline(deadline: number): void {
  if (Date.now() > deadline) {
    deadlineFailure();
  }
}

/** Bound lossless-parser object growth before constructing the JSON graph. */
function preflightTerraformJson(text: string, deadline: number): void {
  let escaped = false;
  let inString = false;
  let scalarCharacters = 0;
  let stringCharacters = 0;
  let structureTokens = 0;
  for (let index = 0; index < text.length; index += 1) {
    if ((index & 0xfff) === 0) {
      checkDeadline(deadline);
    }
    const character = text[index] ?? "";
    if (inString) {
      stringCharacters += 1;
      if (stringCharacters > MAX_TERRAFORM_JSON_STRING_CHARACTERS) {
        fail(
          "TERRAFORM_SHOW_COMPLEXITY_LIMIT",
          "Terraform show JSON exceeds its string-content limit",
        );
      }
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
      scalarCharacters = 0;
      continue;
    }
    if ("{}[],:".includes(character)) {
      structureTokens += 1;
      scalarCharacters = 0;
      if (structureTokens > MAX_TERRAFORM_JSON_STRUCTURE_TOKENS) {
        fail(
          "TERRAFORM_SHOW_COMPLEXITY_LIMIT",
          "Terraform show JSON exceeds its structural limit",
        );
      }
      continue;
    }
    if (/\s/.test(character)) {
      scalarCharacters = 0;
      continue;
    }
    scalarCharacters += 1;
    if (scalarCharacters > MAX_TERRAFORM_JSON_SCALAR_CHARACTERS) {
      fail(
        "TERRAFORM_SHOW_COMPLEXITY_LIMIT",
        "Terraform show JSON exceeds its scalar-token limit",
      );
    }
  }
  checkDeadline(deadline);
}

async function requireRegularFile(
  filePath: string,
  code: string,
  executable: boolean,
): Promise<void> {
  try {
    const metadata = await lstat(filePath);
    if (
      !metadata.isFile()
      || metadata.isSymbolicLink()
      || (executable && (metadata.mode & 0o111) === 0)
    ) {
      fail(code, "trusted Terraform input is not an allowed regular file", "io");
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    fail(code, "unable to inspect trusted Terraform input", "io");
  }
}

function mapTerraformCommandFailure(error: unknown): never {
  const code = error instanceof ProcessFailure ? error.code : "";
  switch (code) {
    case "TERRAFORM_COMMAND_TIMEOUT":
      return fail(
        "TERRAFORM_SHOW_TIMEOUT",
        "Terraform show exceeded its execution deadline",
        "io",
      );
    case "TERRAFORM_COMMAND_STDOUT_LIMIT":
      return fail(
        "TERRAFORM_SHOW_STDOUT_LIMIT",
        "Terraform show exceeded its output limit",
        "io",
      );
    case "TERRAFORM_COMMAND_STDERR_LIMIT":
      return fail(
        "TERRAFORM_SHOW_STDERR_LIMIT",
        "Terraform show exceeded its diagnostic-output limit",
        "io",
      );
    case "TERRAFORM_COMMAND_STDOUT_FAILED":
      return fail(
        "TERRAFORM_SHOW_STDOUT_FAILED",
        "unable to read Terraform show output",
        "io",
      );
    case "TERRAFORM_COMMAND_STDERR_FAILED":
      return fail(
        "TERRAFORM_SHOW_STDERR_FAILED",
        "unable to read Terraform show diagnostic output",
        "io",
      );
    case "UNTRUSTED_TERRAFORM_EXECUTABLE":
      return fail(
        "UNTRUSTED_TERRAFORM_EXECUTABLE",
        "trusted Terraform input is not an allowed regular file",
        "io",
      );
    case "UNRESOLVED_TERRAFORM_COMMAND_PATH":
      return fail(
        "UNRESOLVED_TERRAFORM_SHOW_PATH",
        "Terraform show requires resolved absolute paths",
      );
    case "INVALID_TERRAFORM_COMMAND_LIMIT":
      return fail(
        "INVALID_TERRAFORM_SHOW_LIMIT",
        "Terraform show limits must be positive",
      );
    case "INVALID_TERRAFORM_COMMAND_ENVIRONMENT":
      return fail(
        "INVALID_TERRAFORM_SHOW_ENVIRONMENT",
        "Terraform show environment is not allowed",
      );
    case "TERRAFORM_COMMAND_FAILED":
      return fail(
        "TERRAFORM_SHOW_FAILED",
        "Terraform could not render the saved plan",
      );
    case "TERRAFORM_COMMAND_SPAWN_FAILED":
    case "INVALID_TERRAFORM_COMMAND_ARGUMENTS":
    case "INVALID_TERRAFORM_COMMAND_OUTPUT":
    default:
      return fail(
        "TERRAFORM_SHOW_SPAWN_FAILED",
        "unable to start Terraform show",
        "io",
      );
  }
}

/**
 * Render a private saved-plan snapshot with a fixed, non-shell Terraform call.
 * Child output is never copied into a diagnostic.
 */
export async function terraformShowPlan(
  options: TerraformShowOptions,
): Promise<unknown> {
  const terraformExecutable = options.terraformExecutable;
  const envDir = options.envDir;
  const snapshotPath = options.snapshotPath;
  if (
    terraformExecutable.includes("\0")
    || envDir.includes("\0")
    || snapshotPath.includes("\0")
    ||
    !path.isAbsolute(terraformExecutable)
    || !path.isAbsolute(envDir)
    || !path.isAbsolute(snapshotPath)
  ) {
    return fail(
      "UNRESOLVED_TERRAFORM_SHOW_PATH",
      "Terraform show requires resolved absolute paths",
    );
  }
  const limits = snapshotShowLimits(
    options.limits ?? DEFAULT_TERRAFORM_SHOW_LIMITS,
  );
  const environment = snapshotShowEnvironment(
    options.environment ?? DEFAULT_TERRAFORM_SHOW_ENVIRONMENT,
  );
  const commandCwd = envDir;
  const deadline = Date.now() + limits.timeoutMs;
  await requireRegularFile(
    terraformExecutable,
    "UNTRUSTED_TERRAFORM_EXECUTABLE",
    true,
  );
  await requireRegularFile(snapshotPath, "INVALID_PLAN_SNAPSHOT", false);

  checkDeadline(deadline);
  const remainingTimeoutMs = deadline - Date.now();
  if (remainingTimeoutMs <= 0) {
    deadlineFailure();
  }
  let stdout: Buffer;
  try {
    const result = await runTerraformCommand({
      terraformExecutable,
      argv: [
        `-chdir=${envDir}`,
        "show",
        "-json",
        snapshotPath,
      ],
      cwd: commandCwd,
      environment,
      limits: {
        timeoutMs: remainingTimeoutMs,
        maxStdoutBytes: limits.maxStdoutBytes,
        maxStderrBytes: limits.maxStderrBytes,
      },
      output: "capture",
    });
    stdout = result.stdout;
  } catch (error: unknown) {
    return mapTerraformCommandFailure(error);
  }

  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(stdout);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_UTF8",
      "Terraform show did not emit valid UTF-8 plan JSON",
    );
  }
  stdout = Buffer.alloc(0);
  checkDeadline(deadline);
  preflightTerraformJson(text, deadline);
  let plan: unknown;
  try {
    plan = parseDataJsonLosslessly(text);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_JSON",
      "Terraform show did not emit valid plan JSON",
    );
  }
  checkDeadline(deadline);
  return plan;
}
