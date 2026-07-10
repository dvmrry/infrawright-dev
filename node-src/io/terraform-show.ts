import { spawn } from "node:child_process";
import { lstat } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import { parseDataJsonLosslessly } from "../json/control.js";

export interface TerraformShowLimits {
  readonly timeoutMs: number;
  readonly maxStdoutBytes: number;
  readonly maxStderrBytes: number;
}

export const DEFAULT_TERRAFORM_SHOW_LIMITS: TerraformShowLimits = {
  timeoutMs: 120_000,
  maxStdoutBytes: 256 * 1024 * 1024,
  maxStderrBytes: 1024 * 1024,
};

const MAX_TERRAFORM_SHOW_TIMEOUT_MS = 10 * 60 * 1000;
const MAX_TERRAFORM_SHOW_STDOUT_BYTES = 512 * 1024 * 1024;
const MAX_TERRAFORM_SHOW_STDERR_BYTES = 16 * 1024 * 1024;

export interface TerraformShowOptions {
  readonly terraformExecutable: string;
  readonly envDir: string;
  readonly snapshotPath: string;
  readonly limits?: TerraformShowLimits;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function validateLimits(limits: TerraformShowLimits): void {
  if (
    !Number.isSafeInteger(limits.timeoutMs)
    || limits.timeoutMs <= 0
    || limits.timeoutMs > MAX_TERRAFORM_SHOW_TIMEOUT_MS
    || !Number.isSafeInteger(limits.maxStdoutBytes)
    || limits.maxStdoutBytes <= 0
    || limits.maxStdoutBytes > MAX_TERRAFORM_SHOW_STDOUT_BYTES
    || !Number.isSafeInteger(limits.maxStderrBytes)
    || limits.maxStderrBytes <= 0
    || limits.maxStderrBytes > MAX_TERRAFORM_SHOW_STDERR_BYTES
  ) {
    fail("INVALID_TERRAFORM_SHOW_LIMIT", "Terraform show limits must be positive");
  }
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

/**
 * Render a private saved-plan snapshot with a fixed, non-shell Terraform call.
 * Child output is never copied into a diagnostic.
 */
export async function terraformShowPlan(
  options: TerraformShowOptions,
): Promise<unknown> {
  if (
    !path.isAbsolute(options.terraformExecutable)
    || !path.isAbsolute(options.envDir)
    || !path.isAbsolute(options.snapshotPath)
  ) {
    return fail(
      "UNRESOLVED_TERRAFORM_SHOW_PATH",
      "Terraform show requires resolved absolute paths",
    );
  }
  const limits = options.limits ?? DEFAULT_TERRAFORM_SHOW_LIMITS;
  validateLimits(limits);
  await requireRegularFile(
    options.terraformExecutable,
    "UNTRUSTED_TERRAFORM_EXECUTABLE",
    true,
  );
  await requireRegularFile(options.snapshotPath, "INVALID_PLAN_SNAPSHOT", false);

  const stdout = await new Promise<Buffer>((resolve, reject) => {
    const child = spawn(
      options.terraformExecutable,
      [
        `-chdir=${options.envDir}`,
        "show",
        "-json",
        options.snapshotPath,
      ],
      {
        env: {
          CHECKPOINT_DISABLE: "1",
          LANG: "C",
          LC_ALL: "C",
        },
        shell: false,
        stdio: ["ignore", "pipe", "pipe"],
        windowsHide: true,
      },
    );
    const chunks: Buffer[] = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let terminalFailure: ProcessFailure | null = null;
    let closed = false;

    const terminate = (failure: ProcessFailure): void => {
      if (terminalFailure === null) {
        terminalFailure = failure;
        child.kill("SIGKILL");
      }
    };
    const timer = setTimeout(() => {
      terminate(new ProcessFailure({
        code: "TERRAFORM_SHOW_TIMEOUT",
        category: "io",
        message: "Terraform show exceeded its execution deadline",
      }));
    }, limits.timeoutMs);

    child.stdout.on("data", (chunk: Buffer) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > limits.maxStdoutBytes) {
        terminate(new ProcessFailure({
          code: "TERRAFORM_SHOW_STDOUT_LIMIT",
          category: "io",
          message: "Terraform show exceeded its output limit",
        }));
        return;
      }
      chunks.push(Buffer.from(chunk));
    });
    child.stderr.on("data", (chunk: Buffer) => {
      stderrBytes += chunk.length;
      if (stderrBytes > limits.maxStderrBytes) {
        terminate(new ProcessFailure({
          code: "TERRAFORM_SHOW_STDERR_LIMIT",
          category: "io",
          message: "Terraform show exceeded its diagnostic-output limit",
        }));
      }
    });
    child.on("error", () => {
      terminate(new ProcessFailure({
        code: "TERRAFORM_SHOW_SPAWN_FAILED",
        category: "io",
        message: "unable to start Terraform show",
      }));
    });
    child.on("close", (code) => {
      if (closed) {
        return;
      }
      closed = true;
      clearTimeout(timer);
      if (terminalFailure !== null) {
        reject(terminalFailure);
      } else if (code !== 0) {
        reject(new ProcessFailure({
          code: "TERRAFORM_SHOW_FAILED",
          category: "domain",
          message: "Terraform could not render the saved plan",
        }));
      } else {
        resolve(Buffer.concat(chunks, stdoutBytes));
      }
    });
  });

  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(stdout);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_UTF8",
      "Terraform show did not emit valid UTF-8 plan JSON",
    );
  }
  try {
    return parseDataJsonLosslessly(text);
  } catch {
    return fail(
      "INVALID_TERRAFORM_SHOW_JSON",
      "Terraform show did not emit valid plan JSON",
    );
  }
}
