import { copyFile, readFile, stat, unlink, writeFile } from "node:fs/promises";
import path from "node:path";

import { runTerraformCommand } from "../io/terraform-command.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { deploymentEnvsDir } from "./deployment.js";
import { ProcessFailure } from "./errors.js";
import { filterGeneratedImports } from "./import-moves.js";
import { loadedRootTopology, validateTenant } from "./roots.js";
import { transformArtifactPaths } from "./transform-artifacts.js";
import type { Deployment, WholeRootDiagnostic } from "./types.js";

export interface ImportStagingTerraformRequest {
  readonly backendConfig?: string;
  readonly directory: string;
  readonly label: string;
  readonly tenant: string;
}

export interface ImportStagingTerraform {
  initialize(request: ImportStagingTerraformRequest): Promise<void>;
  listState(request: ImportStagingTerraformRequest): Promise<{
    readonly success: boolean;
    readonly stdout: string;
  }>;
}

export interface StageImportsResult {
  readonly sources: number;
  readonly staged: number;
}

export interface UnstageImportsResult {
  readonly removed: number;
}

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function definedEnvironment(environment: NodeJS.ProcessEnv): Readonly<Record<string, string>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, value] of Object.entries(environment)) {
    if (value !== undefined) output[key] = value;
  }
  return output;
}

function decodeUtf8(content: Uint8Array): string {
  try {
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(content);
  } catch {
    return fail(
      "INVALID_TERRAFORM_STATE_LIST",
      "terraform state list output is not valid UTF-8",
    );
  }
}

async function readPythonTextUtf8(file: string, label: string): Promise<string> {
  let content: Buffer;
  try {
    content = await readFile(file);
  } catch {
    return fail("READ_FAILED", `unable to read ${label}`, "io");
  }
  let text: string;
  try {
    // Python open(..., encoding="utf-8") retains the BOM as U+FEFF.
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(content);
  } catch {
    return fail("INVALID_UTF8", `${label} is not valid UTF-8`);
  }
  // Python's default text mode performs universal-newline translation.
  return text.replaceAll(/\r\n?/gu, "\n");
}

/** Adapt the bounded generic Terraform runner for staging-only init/state-list calls. */
export function createImportStagingTerraform(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly terraformExecutable: string;
}): ImportStagingTerraform {
  const environment = definedEnvironment(options.environment);
  return {
    initialize: async (request) => {
      const argv = ["init", "-input=false"];
      if (request.backendConfig !== undefined && request.backendConfig.length > 0) {
        argv.push(
          "-reconfigure",
          `-backend-config=${request.backendConfig}`,
          `-backend-config=key=${request.tenant}/${request.label}.tfstate`,
        );
      }
      await runTerraformCommand({
        argv,
        cwd: request.directory,
        environment,
        output: "discard",
        terraformExecutable: options.terraformExecutable,
      });
    },
    listState: async (request) => {
      try {
        const result = await runTerraformCommand({
          argv: ["state", "list"],
          cwd: request.directory,
          environment,
          output: "capture",
          terraformExecutable: options.terraformExecutable,
        });
        return Object.freeze({ success: true, stdout: decodeUtf8(result.stdout) });
      } catch (error: unknown) {
        if (error instanceof ProcessFailure && error.code === "TERRAFORM_COMMAND_FAILED") {
          return Object.freeze({ success: false, stdout: "" });
        }
        throw error;
      }
    },
  };
}

async function exists(file: string): Promise<boolean> {
  try {
    await stat(file);
    return true;
  } catch {
    return false;
  }
}

async function isDirectory(directory: string): Promise<boolean> {
  try {
    return (await stat(directory)).isDirectory();
  } catch {
    return false;
  }
}

async function removeIfPresent(file: string): Promise<boolean> {
  if (!(await exists(file))) return false;
  try {
    await unlink(file);
    return true;
  } catch (error: unknown) {
    if (typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

function workspacePath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate) ? candidate : path.resolve(workspace, candidate);
}

function environmentRootDirectory(
  workspace: string,
  deployment: Deployment,
  tenant: string,
  label: string,
): string {
  return workspacePath(workspace, path.join(deploymentEnvsDir(deployment, tenant), label));
}

function noteWholeRoot(
  diagnostic: WholeRootDiagnostic,
  onDiagnostic: (message: string) => void,
): void {
  onDiagnostic(`NOTE: ${diagnostic.message}`);
}

async function requireBackendConfiguration(options: {
  readonly backendConfig?: string;
  readonly directory: string;
  readonly label: string;
}): Promise<void> {
  if (options.backendConfig !== undefined && options.backendConfig.length > 0) return;
  const main = path.join(options.directory, "main.tf");
  if (!(await exists(main))) return;
  const text = await readPythonTextUtf8(main, `${options.label} environment root`);
  if (text.split("\n").some((line) => line.startsWith('  backend "'))) {
    fail(
      "BACKEND_CONFIG_REQUIRED",
      `${options.label} declares a remote backend; run with BACKEND_CONFIG=<file>`,
    );
  }
}

function stateAddresses(stdout: string): readonly string[] {
  if (stdout.length === 0) return [];
  const lines = stdout.split(/\r\n|[\n\v\f\r\x1c-\x1e\x85\u2028\u2029]/u);
  if (/(?:\r\n|[\n\v\f\r\x1c-\x1e\x85\u2028\u2029])$/u.test(stdout)) {
    lines.pop();
  }
  return lines;
}

/** Copy generated import/move artifacts into complete deployment-selected roots. */
export async function stageImports(options: {
  readonly backendConfig?: string;
  readonly deployment: Deployment;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly stateAware: boolean;
  readonly tenant: string;
  readonly terraform?: ImportStagingTerraform;
  readonly workspace: string;
}): Promise<StageImportsResult> {
  validateTenant(options.tenant);
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  const selected = loadedRootTopology({
    deployment: options.deployment,
    root: options.root,
    selectors: options.selectors,
    tenant: options.tenant,
  });
  for (const diagnostic of selected.diagnostics) noteWholeRoot(diagnostic, onDiagnostic);
  const backendConfig = options.backendConfig === undefined || options.backendConfig.length === 0
    ? undefined
    : workspacePath(options.workspace, options.backendConfig);
  let sources = 0;
  let staged = 0;
  for (const selectedRoot of selected.topology.roots) {
    const directory = environmentRootDirectory(
      options.workspace,
      options.deployment,
      options.tenant,
      selectedRoot.label,
    );
    for (const resourceType of selectedRoot.members) {
      const artifacts = transformArtifactPaths({
        deployment: options.deployment,
        resourceType,
        tenant: options.tenant,
      });
      for (const [kind, unresolvedSource] of [
        ["imports", artifacts.imports],
        ["moves", artifacts.moves],
      ] as const) {
        const source = workspacePath(options.workspace, unresolvedSource);
        if (!(await exists(source))) continue;
        sources += 1;
        const basename = path.basename(source);
        if (!(await isDirectory(directory))) {
          onDiagnostic(`skip ${basename} (no env root ${directory} - run make gen-env)`);
          continue;
        }
        const destination = path.join(directory, basename);
        if (kind === "imports" && options.stateAware) {
          const terraform = options.terraform;
          if (terraform === undefined) {
            fail("TERRAFORM_REQUIRED", "state-aware import staging requires Terraform");
          }
          await requireBackendConfiguration({
            ...(backendConfig === undefined ? {} : { backendConfig }),
            directory,
            label: selectedRoot.label,
          });
          const request: ImportStagingTerraformRequest = {
            ...(backendConfig === undefined ? {} : { backendConfig }),
            directory,
            label: selectedRoot.label,
            tenant: options.tenant,
          };
          await terraform.initialize(request);
          const state = await terraform.listState(request);
          const filtered = filterGeneratedImports(
            await readPythonTextUtf8(source, `${resourceType} imports`),
            state.success ? stateAddresses(state.stdout) : [],
          );
          if (filtered.text.length > 0) {
            await writeFile(destination, filtered.text, "utf8");
            onDiagnostic(`${filtered.kept} import(s) kept, ${filtered.skipped} already managed (skipped)`);
          } else {
            await removeIfPresent(destination);
            onDiagnostic(`skip ${basename} (every import already managed - delta is empty)`);
            continue;
          }
        } else {
          await copyFile(source, destination);
        }
        onDiagnostic(`staged ${destination}`);
        staged += 1;
      }
    }
  }
  if (sources === 0) {
    fail(
      "NO_IMPORT_ARTIFACTS",
      `nothing to stage for TENANT=${options.tenant} (run make transform or make adopt first)`,
    );
  }
  if (staged === 0) {
    onDiagnostic("NOTE: 0 staged - every import is already managed or no roots matched");
  }
  return Object.freeze({ sources, staged });
}

/** Remove only staged import/move copies from selected materialized roots. */
export async function unstageImports(options: {
  readonly deployment: Deployment;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly tenant: string;
  readonly workspace: string;
}): Promise<UnstageImportsResult> {
  validateTenant(options.tenant);
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  const selected = loadedRootTopology({
    deployment: options.deployment,
    root: options.root,
    selectors: options.selectors,
    tenant: options.tenant,
  });
  const diagnostics = new Map(selected.diagnostics.map((item) => [item.root, item]));
  let removed = 0;
  for (const selectedRoot of selected.topology.roots) {
    const directory = environmentRootDirectory(
      options.workspace,
      options.deployment,
      options.tenant,
      selectedRoot.label,
    );
    if (!(await isDirectory(directory))) continue;
    const diagnostic = diagnostics.get(selectedRoot.label);
    if (diagnostic !== undefined) noteWholeRoot(diagnostic, onDiagnostic);
    for (const resourceType of selectedRoot.members) {
      for (const suffix of ["_imports.tf", "_moves.tf"] as const) {
        const target = path.join(directory, `${resourceType}${suffix}`);
        if (await removeIfPresent(target)) {
          onDiagnostic(`removed ${target}`);
          removed += 1;
        }
      }
    }
  }
  onDiagnostic(`${removed} file(s) removed`);
  return Object.freeze({ removed });
}
