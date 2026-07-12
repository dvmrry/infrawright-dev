import { mkdir, mkdtemp, rm, rmdir, writeFile } from "node:fs/promises";
import path from "node:path";

import {
  deriveZiaUrlCategoryIdentities,
  ZIA_PROVIDER_NAME,
  ZIA_PROVIDER_SOURCE,
  ZIA_PROVIDER_VERSION,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  type ZiaUrlCategoryIdentity,
  type ZiaUrlCategoryStateObservation,
} from "../domain/zia-url-categories.js";
import { ProcessFailure } from "../domain/errors.js";
import { renderHclQuotedString } from "../domain/import-moves.js";
import { runTerraformCommand } from "./terraform-command.js";
import { terraformShowPlan } from "./terraform-show.js";

const COMMAND_TIMEOUT_MS = 300_000;
const TERRAFORM_VERSION = "1.15.4";
const PLAN_FORMAT_VERSION = "1.2";
const STATE_FORMAT_VERSION = "1.0";
const TERRAFORM_ENVIRONMENT_NAMES = [
  "HTTP_PROXY",
  "HTTPS_PROXY",
  "NO_PROXY",
  "SSL_CERT_DIR",
  "SSL_CERT_FILE",
  "ZSCALER_CLIENT_ID",
  "ZSCALER_CLIENT_SECRET",
  "ZSCALER_CLOUD",
  "ZSCALER_HTTP_PROXY",
  "ZSCALER_PRIVATE_KEY",
  "ZSCALER_VANITY_DOMAIN",
  "http_proxy",
  "https_proxy",
  "no_proxy",
] as const;

export type ZiaUrlCategoryOracleStage =
  | "init"
  | "plan"
  | "show-plan"
  | "apply"
  | "show-state";

export interface ZiaUrlCategoriesOracleDependencies {
  readonly command?: (options: {
    readonly argv: readonly string[];
    readonly cwd: string;
    readonly environment: Readonly<Record<string, string>>;
    readonly stage: "init" | "plan" | "apply";
    readonly terraformExecutable: string;
  }) => Promise<void>;
  readonly show?: (options: {
    readonly cwd: string;
    readonly environment: Readonly<Record<string, string>>;
    readonly snapshotPath: string;
    readonly stage: "show-plan" | "show-state";
    readonly terraformExecutable: string;
  }) => Promise<unknown>;
}

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function renderRoot(): string {
  return "terraform {\n"
    + `  required_version = "= ${TERRAFORM_VERSION}"\n`
    + "  required_providers {\n"
    + `    ${ZIA_PROVIDER_NAME} = {\n`
    + `      source = ${renderHclQuotedString(ZIA_PROVIDER_SOURCE)}\n`
    + `      version = ${renderHclQuotedString(ZIA_PROVIDER_VERSION)}\n`
    + "    }\n"
    + "  }\n"
    + "}\n\n"
    + `provider ${renderHclQuotedString(ZIA_PROVIDER_NAME)} {\n`
    + "  # credentials via provider environment variables\n"
    + "}\n";
}

function renderScratchImports(identities: readonly ZiaUrlCategoryIdentity[]): string {
  return identities.map((identity) => {
    return "import {\n"
      + `  to = ${identity.address}\n`
      + `  id = ${renderHclQuotedString(identity.importId)}\n`
      + "}\n";
  }).join("\n");
}

/** Build the exact allowlisted Terraform environment used by ZIA workflows. */
export function ziaUrlCategoryTerraformEnvironment(
  source: NodeJS.ProcessEnv,
  scratch: string,
  terraformDataDir: string | null = path.join(scratch, ".terraform-data"),
): Readonly<Record<string, string>> {
  const environment = Object.create(null) as Record<string, string>;
  for (const name of TERRAFORM_ENVIRONMENT_NAMES) {
    const value = source[name];
    if (value !== undefined) environment[name] = value;
  }
  Object.assign(environment, {
    CHECKPOINT_DISABLE: "1",
    HOME: path.join(scratch, "home"),
    LANG: "C",
    LC_ALL: "C",
    TF_IN_AUTOMATION: "1",
    TMPDIR: path.join(scratch, "tmp"),
  });
  if (terraformDataDir !== null) environment.TF_DATA_DIR = terraformDataDir;
  return Object.freeze(environment);
}

function emptyCollection(value: unknown): boolean {
  return value === undefined
    || (Array.isArray(value) && value.length === 0)
    || (isRecord(value) && Object.keys(value).length === 0);
}

function emptyArray(value: unknown): boolean {
  return value === undefined || (Array.isArray(value) && value.length === 0);
}

function emptyRecord(value: unknown): boolean {
  return value === undefined || (isRecord(value) && Object.keys(value).length === 0);
}

function hasOwn(record: object, name: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, name);
}

function assertImportOnlyPlan(
  plan: unknown,
  identities: readonly ZiaUrlCategoryIdentity[],
): void {
  if (
    !isRecord(plan)
    || plan.format_version !== PLAN_FORMAT_VERSION
    || plan.terraform_version !== TERRAFORM_VERSION
    || plan.complete !== true
    || plan.errored !== false
    || plan.applyable !== true
    || !emptyCollection(plan.errors)
    || !emptyCollection(plan.diagnostics)
    || !emptyArray(plan.checks)
    || !emptyArray(plan.deferred_changes)
    || !emptyArray(plan.action_invocations)
    || !emptyArray(plan.deferred_action_invocations)
    || !emptyArray(plan.resource_drift)
    || !emptyRecord(plan.output_changes)
    || !Array.isArray(plan.resource_changes)
    || plan.resource_changes.length !== identities.length
  ) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_PLAN_REJECTED",
      "ZIA URL-category Oracle plan is not an exact import-only plan",
    );
  }
  const expected = new Map(identities.map((identity) => [identity.address, identity]));
  const seen = new Set<string>();
  for (const resource of plan.resource_changes) {
    if (!isRecord(resource) || typeof resource.address !== "string") {
      return fail(
        "ZIA_URL_CATEGORY_ORACLE_PLAN_REJECTED",
        "ZIA URL-category Oracle plan has an invalid resource change",
      );
    }
    const identity = expected.get(resource.address);
    const change = resource.change;
    if (
      identity === undefined
      || seen.has(resource.address)
      || resource.mode !== "managed"
      || resource.type !== ZIA_URL_CATEGORIES_RESOURCE_TYPE
      || resource.provider_name !== `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`
      || !isRecord(change)
      || !Array.isArray(change.actions)
      || change.actions.length !== 1
      || change.actions[0] !== "no-op"
      || !isRecord(change.importing)
      || change.importing.id !== identity.importId
    ) {
      return fail(
        "ZIA_URL_CATEGORY_ORACLE_PLAN_REJECTED",
        "ZIA URL-category Oracle plan is not an exact import-only plan",
      );
    }
    seen.add(resource.address);
  }
  if (seen.size !== expected.size) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_PLAN_REJECTED",
      "ZIA URL-category Oracle plan did not cover every identity",
    );
  }
}

function stateObservations(
  state: unknown,
  identities: readonly ZiaUrlCategoryIdentity[],
): readonly ZiaUrlCategoryStateObservation[] {
  if (
    !isRecord(state)
    || state.format_version !== STATE_FORMAT_VERSION
    || state.terraform_version !== TERRAFORM_VERSION
    || !isRecord(state.values)
    || !emptyRecord(state.values.outputs)
    || !emptyArray(state.checks)
    || !isRecord(state.values.root_module)
  ) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_STATE_REJECTED",
      "ZIA URL-category Oracle state has an invalid root",
    );
  }
  const root = state.values.root_module;
  const resources = root.resources;
  if (
    !Array.isArray(resources)
    || resources.length !== identities.length
    || !emptyArray(root.child_modules)
  ) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_STATE_REJECTED",
      "ZIA URL-category Oracle state does not exactly cover the imports",
    );
  }
  const expected = new Map(identities.map((identity) => [identity.address, identity]));
  const observations: ZiaUrlCategoryStateObservation[] = [];
  const seen = new Set<string>();
  for (const resource of resources) {
    if (!isRecord(resource) || typeof resource.address !== "string") {
      return fail(
        "ZIA_URL_CATEGORY_ORACLE_STATE_REJECTED",
        "ZIA URL-category Oracle state contains an invalid resource",
      );
    }
    const identity = expected.get(resource.address);
    if (
      identity === undefined
      || seen.has(resource.address)
      || resource.mode !== "managed"
      || resource.type !== ZIA_URL_CATEGORIES_RESOURCE_TYPE
      || resource.provider_name !== `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`
      || hasOwn(resource, "deposed_key")
      || !isRecord(resource.values)
      || resource.values.category_id !== identity.importId
      || !hasOwn(resource, "sensitive_values")
      || (!isRecord(resource.sensitive_values) && resource.sensitive_values !== true)
      || (resource.tainted !== undefined && resource.tainted !== false)
    ) {
      return fail(
        "ZIA_URL_CATEGORY_ORACLE_STATE_REJECTED",
        "ZIA URL-category Oracle state does not match the expected imports",
      );
    }
    seen.add(resource.address);
    observations.push(Object.freeze({
      address: identity.address,
      importId: identity.importId,
      key: identity.key,
      providerName: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
      resourceType: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
      sensitiveValues: resource.sensitive_values ?? {},
      values: resource.values,
    }));
  }
  return Object.freeze(observations);
}

async function productionCommand(options: {
  readonly argv: readonly string[];
  readonly cwd: string;
  readonly environment: Readonly<Record<string, string>>;
  readonly terraformExecutable: string;
}): Promise<void> {
  await runTerraformCommand({
    terraformExecutable: options.terraformExecutable,
    argv: options.argv,
    cwd: options.cwd,
    environment: options.environment,
    limits: {
      maxStderrBytes: 16 * 1024 * 1024,
      maxStdoutBytes: 8 * 1024 * 1024,
      timeoutMs: COMMAND_TIMEOUT_MS,
    },
    output: "discard",
  });
}

async function productionShow(options: {
  readonly cwd: string;
  readonly environment: Readonly<Record<string, string>>;
  readonly snapshotPath: string;
  readonly terraformExecutable: string;
}): Promise<unknown> {
  return terraformShowPlan({
    terraformExecutable: options.terraformExecutable,
    envDir: options.cwd,
    snapshotPath: options.snapshotPath,
    environment: options.environment,
    limits: {
      maxStderrBytes: 16 * 1024 * 1024,
      maxStdoutBytes: 8 * 1024 * 1024,
      timeoutMs: COMMAND_TIMEOUT_MS,
    },
  });
}

/** Import ZIA URL categories into ephemeral local state and return provider Read values. */
export async function observeZiaUrlCategories(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly rawItems: readonly unknown[];
  readonly terraformExecutable: string;
  readonly workspace: string;
}, dependencies: ZiaUrlCategoriesOracleDependencies = {}): Promise<readonly ZiaUrlCategoryStateObservation[]> {
  const identities = deriveZiaUrlCategoryIdentities(options.rawItems);
  if (identities.length === 0) return Object.freeze([]);
  if (!path.isAbsolute(options.workspace) || !path.isAbsolute(options.terraformExecutable)) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_ORACLE_PATH",
      "ZIA URL-category Oracle paths must be absolute",
    );
  }
  const tempRoot = path.join(options.workspace, ".infrawright-oracle");
  await mkdir(tempRoot, { mode: 0o700, recursive: true });
  const scratch = await mkdtemp(path.join(tempRoot, "zia-url-categories-"));
  const environment = ziaUrlCategoryTerraformEnvironment(options.environment, scratch);
  const rootPath = path.join(scratch, "main.tf");
  const importsPath = path.join(scratch, "imports.tf");
  const generatedPath = path.join(scratch, "generated.tf");
  const planPath = path.join(scratch, "oracle.tfplan");
  const statePath = path.join(scratch, "terraform.tfstate");
  const command = dependencies.command ?? productionCommand;
  const show = dependencies.show ?? productionShow;
  let primary: ProcessFailure | null = null;
  let result: readonly ZiaUrlCategoryStateObservation[] | null = null;
  try {
    await Promise.all([
      mkdir(path.join(scratch, "home"), { mode: 0o700 }),
      mkdir(path.join(scratch, "tmp"), { mode: 0o700 }),
      mkdir(path.join(scratch, ".terraform-data"), { mode: 0o700 }),
    ]);
    await writeFile(rootPath, renderRoot(), { encoding: "utf8", mode: 0o600 });
    await writeFile(importsPath, renderScratchImports(identities), {
      encoding: "utf8",
      mode: 0o600,
    });
    await command({
      argv: ["init", "-backend=false", "-input=false", "-no-color"],
      cwd: scratch,
      environment,
      stage: "init",
      terraformExecutable: options.terraformExecutable,
    });
    await command({
      argv: [
        "plan",
        "-input=false",
        "-no-color",
        "-lock=false",
        `-generate-config-out=${generatedPath}`,
        `-out=${planPath}`,
      ],
      cwd: scratch,
      environment,
      stage: "plan",
      terraformExecutable: options.terraformExecutable,
    });
    const plan = await show({
      cwd: scratch,
      environment,
      snapshotPath: planPath,
      stage: "show-plan",
      terraformExecutable: options.terraformExecutable,
    });
    assertImportOnlyPlan(plan, identities);
    await command({
      argv: ["apply", "-input=false", "-no-color", "-lock=false", planPath],
      cwd: scratch,
      environment,
      stage: "apply",
      terraformExecutable: options.terraformExecutable,
    });
    const state = await show({
      cwd: scratch,
      environment,
      snapshotPath: statePath,
      stage: "show-state",
      terraformExecutable: options.terraformExecutable,
    });
    result = stateObservations(state, identities);
  } catch (error: unknown) {
    primary = error instanceof ProcessFailure
      ? error
      : new ProcessFailure({
          code: "ZIA_URL_CATEGORY_ORACLE_FAILED",
          category: "io",
          message: "ZIA URL-category provider Oracle failed",
        });
  }
  let cleanupFailed = false;
  try {
    await rm(scratch, { force: true, recursive: true });
  } catch {
    cleanupFailed = true;
  }
  if (!cleanupFailed) await rmdir(tempRoot).catch(() => undefined);
  if (primary !== null) {
    if (!cleanupFailed) throw primary;
    throw new ProcessFailure({
      code: primary.code,
      category: primary.category,
      message: primary.message,
      retryable: primary.retryable,
      details: [
        ...primary.details,
        {
          path: "cleanup",
          code: "ZIA_URL_CATEGORY_ORACLE_CLEANUP_FAILED",
          message: "private ZIA Oracle cleanup also failed",
        },
      ],
    });
  }
  if (cleanupFailed) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_CLEANUP_FAILED",
      "private ZIA Oracle cleanup failed",
      "io",
    );
  }
  if (result === null) {
    return fail(
      "ZIA_URL_CATEGORY_ORACLE_FAILED",
      "ZIA URL-category provider Oracle produced no state",
      "io",
    );
  }
  return result;
}
