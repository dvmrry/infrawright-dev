import { chmod, readFile, stat, unlink, writeFile } from "node:fs/promises";
import path from "node:path";

import type { LoadedPackRoot } from "../metadata/loader.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../io/terraform-command.js";
import type { Deployment, WholeRootDiagnostic } from "./types.js";
import { ProcessFailure } from "./errors.js";
import { loadedPlanRoots } from "./plan-roots.js";
import {
  captureInitSourcesPayload,
  initSourcesSha256,
  planFingerprintV2,
  type PlanFingerprintV2,
} from "./plan-fingerprint.js";
import { transformArtifactPaths } from "./transform-artifacts.js";
import { validateTenant } from "./roots.js";

export interface PlanTerraformRequest {
  readonly backendConfig?: string;
  readonly backendKey?: string;
  readonly directory: string;
  readonly save: boolean;
  readonly varFiles: readonly string[];
}

export interface PlanTerraform {
  initialize(request: PlanTerraformRequest): Promise<void>;
  plan(request: PlanTerraformRequest): Promise<void>;
}

export interface PlanRunResult {
  readonly planned: number;
}

export interface CleanPlansResult {
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

/** Adapt the shell-free Terraform runner to ordinary deployment init/plan. */
export function createPlanTerraform(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly limits?: TerraformCommandLimits;
  readonly terraformExecutable: string;
}): PlanTerraform {
  const environment = definedEnvironment(options.environment);
  const common = {
    environment,
    terraformExecutable: options.terraformExecutable,
    ...(options.limits === undefined ? {} : { limits: options.limits }),
  };
  return {
    initialize: async (request) => {
      const argv = ["init", "-input=false"];
      if (request.backendConfig !== undefined) {
        argv.push(
          "-reconfigure",
          `-backend-config=${request.backendConfig}`,
          `-backend-config=key=${request.backendKey ?? ""}`,
        );
      }
      await runTerraformCommand({
        ...common,
        argv,
        cwd: request.directory,
        output: "inherit-stderr",
      });
    },
    plan: async (request) => {
      const argv = [
        "plan",
        "-input=false",
        ...request.varFiles.map((file) => `-var-file=${file}`),
        ...(request.save ? ["-out=tfplan"] : []),
      ];
      await runTerraformCommand({
        ...common,
        argv,
        cwd: request.directory,
        output: "inherit",
      });
    },
  };
}

async function exists(candidate: string): Promise<boolean> {
  try {
    await stat(candidate);
    return true;
  } catch {
    return false;
  }
}

async function isFile(candidate: string): Promise<boolean> {
  try {
    return (await stat(candidate)).isFile();
  } catch {
    return false;
  }
}

async function removeIfPresent(candidate: string): Promise<boolean> {
  try {
    await unlink(candidate);
    return true;
  } catch (error: unknown) {
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) {
      return false;
    }
    throw error;
  }
}

function workspacePath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate) ? candidate : path.resolve(workspace, candidate);
}

export async function requireBackendConfiguration(options: {
  readonly backendConfig?: string;
  readonly directory: string;
  readonly label: string;
}): Promise<void> {
  if (options.backendConfig !== undefined) return;
  const main = path.join(options.directory, "main.tf");
  if (!(await exists(main))) return;
  let bytes: Buffer;
  try {
    bytes = await readFile(main);
  } catch {
    return fail("READ_FAILED", `unable to read ${options.label} environment root`, "io");
  }
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  } catch {
    return fail("INVALID_UTF8", `${options.label} environment root is not valid UTF-8`);
  }
  if (text.replaceAll(/\r\n?/gu, "\n").split("\n").some((line) => {
    return line.startsWith('  backend "');
  })) {
    fail(
      "BACKEND_CONFIG_REQUIRED",
      `${options.label} declares a remote backend; run with BACKEND_CONFIG=<file>`,
    );
  }
}

function isDerived(root: LoadedPackRoot, resourceType: string): boolean {
  const derive = root.resources.get(resourceType)?.registry.derive;
  return typeof derive === "object" && derive !== null && !Array.isArray(derive);
}

function sameFingerprint(left: PlanFingerprintV2, right: PlanFingerprintV2): boolean {
  return left.version === right.version && left.sha256 === right.sha256;
}

function fingerprintText(fingerprint: PlanFingerprintV2): string {
  return `{"sha256": "${fingerprint.sha256}", "version": ${fingerprint.version}}\n`;
}

function savedPaths(directory: string): {
  readonly plan: string;
  readonly sources: string;
} {
  return {
    plan: path.join(directory, "tfplan"),
    sources: path.join(directory, "tfplan.sources"),
  };
}

export async function removeSavedPlanArtifacts(directory: string): Promise<boolean> {
  const saved = savedPaths(directory);
  const plan = await removeIfPresent(saved.plan);
  const sources = await removeIfPresent(saved.sources);
  return plan || sources;
}

function noteWholeRoot(
  diagnostic: WholeRootDiagnostic,
  onDiagnostic: (message: string) => void,
): void {
  onDiagnostic(`NOTE: ${diagnostic.message}`);
}

/** Plan every selected complete materialized root with Python's file lifecycle. */
export async function planEnvironmentRoots(options: {
  readonly backendConfig?: string;
  readonly deployment: Deployment;
  readonly importsOnly: boolean;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly save: boolean;
  readonly selectors: readonly string[];
  readonly tenant: string;
  readonly terraform: PlanTerraform;
  readonly workspace: string;
}): Promise<PlanRunResult> {
  validateTenant(options.tenant);
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  const selected = await loadedPlanRoots({
    deployment: options.deployment,
    root: options.root,
    selectors: options.selectors,
    tenant: options.tenant,
    workspace: options.workspace,
  });
  const diagnostics = new Map(selected.diagnostics.map((item) => [item.root, item]));
  const backendConfig = options.backendConfig === undefined || options.backendConfig.length === 0
    ? undefined
    : workspacePath(options.workspace, options.backendConfig);
  let planned = 0;
  for (const selectedRoot of selected.result.roots) {
    const wholeRoot = diagnostics.get(selectedRoot.label);
    if (wholeRoot !== undefined) noteWholeRoot(wholeRoot, onDiagnostic);
    const derived = selectedRoot.members.filter((member) => isDerived(options.root, member)).sort();
    if (options.importsOnly && derived.length > 0) {
      onDiagnostic(
        `skip ${selectedRoot.label} (IMPORTS_ONLY: derived/non-importable member ${derived.join(", ")})`,
      );
      continue;
    }
    const directory = workspacePath(options.workspace, selectedRoot.env_dir);
    if (options.save) await removeSavedPlanArtifacts(directory);

    const varFiles: string[] = [];
    const missing: string[] = [];
    for (const resourceType of selectedRoot.members) {
      const logical = transformArtifactPaths({
        deployment: options.deployment,
        resourceType,
        tenant: options.tenant,
      }).config;
      const resolved = workspacePath(options.workspace, logical);
      if (await isFile(resolved)) varFiles.push(resolved);
      else missing.push(logical);
    }
    if (varFiles.length === 0) {
      for (const file of missing) onDiagnostic(`skip ${selectedRoot.label} (no ${file})`);
      continue;
    }
    if (missing.length > 0) {
      fail(
        "MISSING_GROUP_CONFIG",
        `root ${selectedRoot.label} is missing member config(s): ${missing.join(", ")} - run `
          + "make transform or make adopt for every group member first",
      );
    }
    await requireBackendConfiguration({
      ...(backendConfig === undefined ? {} : { backendConfig }),
      directory,
      label: selectedRoot.label,
    });
    onDiagnostic(`== plan ${selectedRoot.label}`);
    const backend = backendConfig === undefined
      ? {}
      : {
          backendConfig,
          backendKey: `${options.tenant}/${selectedRoot.label}.tfstate`,
        };
    const request: PlanTerraformRequest = {
      ...backend,
      directory,
      save: options.save,
      varFiles,
    };

    let initIdentity: string | undefined;
    if (options.save) {
      initIdentity = initSourcesSha256(await captureInitSourcesPayload({
        ...backend,
        envDir: directory,
        memberTypes: selectedRoot.members,
      }));
    }
    try {
      await options.terraform.initialize(request);
      if (options.save) {
        const current = initSourcesSha256(await captureInitSourcesPayload({
          ...backend,
          envDir: directory,
          memberTypes: selectedRoot.members,
        }));
        if (current !== initIdentity) {
          return fail(
            "INIT_INPUTS_CHANGED",
            `${selectedRoot.env_dir}: init inputs changed while init was running - re-run make plan SAVE=1`,
          );
        }
      }
      const fingerprint = options.save
        ? await planFingerprintV2({
            ...backend,
            envDir: directory,
            memberTypes: selectedRoot.members,
            varFiles,
          })
        : undefined;
      await options.terraform.plan(request);
      if (options.save) {
        const current = await planFingerprintV2({
          ...backend,
          envDir: directory,
          memberTypes: selectedRoot.members,
          varFiles,
        });
        if (fingerprint === undefined || !sameFingerprint(current, fingerprint)) {
          return fail(
            "PLAN_INPUTS_CHANGED",
            `${selectedRoot.env_dir}: saved plan is stale relative to the current root configuration - `
              + "re-run make plan SAVE=1 (plan inputs changed while the plan was running)",
          );
        }
        const saved = savedPaths(directory);
        if (!(await isFile(saved.plan))) {
          return fail("MISSING_SAVED_PLAN", `${selectedRoot.env_dir}: Terraform did not write tfplan`);
        }
        if (process.platform !== "win32") await chmod(saved.plan, 0o600);
        await writeFile(saved.sources, fingerprintText(fingerprint), { encoding: "utf8", mode: 0o600 });
      }
      planned += 1;
    } catch (error: unknown) {
      if (options.save) await removeSavedPlanArtifacts(directory);
      throw error;
    }
  }
  if (planned === 0) {
    fail(
      "NO_ROOTS_PLANNED",
      `no roots planned for TENANT=${options.tenant} (missing env roots or config?)`,
    );
  }
  return Object.freeze({ planned });
}

/** Remove only saved-plan pairs from selected materialized roots. */
export async function cleanPlans(options: {
  readonly deployment: Deployment;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly tenant: string | null;
  readonly workspace: string;
}): Promise<CleanPlansResult> {
  if (options.tenant !== null) validateTenant(options.tenant);
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  const selected = await loadedPlanRoots({
    deployment: options.deployment,
    root: options.root,
    selectors: options.selectors,
    tenant: options.tenant,
    workspace: options.workspace,
  });
  const diagnostics = new Map(selected.diagnostics.map((item) => [item.root, item]));
  let removed = 0;
  for (const selectedRoot of selected.result.roots) {
    const wholeRoot = diagnostics.get(selectedRoot.label);
    if (wholeRoot !== undefined) noteWholeRoot(wholeRoot, onDiagnostic);
    let removedAny = false;
    for (const name of ["tfplan", "tfplan.sources"] as const) {
      const logical = path.join(selectedRoot.env_dir, name);
      if (await removeIfPresent(workspacePath(options.workspace, logical))) {
        onDiagnostic(`removed ${logical}`);
        removedAny = true;
      }
    }
    if (removedAny) removed += 1;
  }
  onDiagnostic(`${removed} stale plan(s) removed`);
  return Object.freeze({ removed });
}
