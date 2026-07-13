import { createHash } from "node:crypto";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { manifestForProvider } from "../metadata/packs.js";
import { isObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { readOptionalUtf8 } from "../io/files.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../io/terraform-command.js";
import { renderHclQuotedString } from "./import-moves.js";
import { applyGeneratedConfigPolicy } from "./generated-config-policy.js";
import { DriftPolicy } from "./drift-policy.js";

type JsonRecord = Record<string, unknown>;

export class OracleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OracleError";
  }
}

export interface OracleStateObject {
  readonly address: string;
  readonly sensitiveValues: unknown;
  readonly values: Readonly<Record<string, unknown>>;
}

export interface OracleCommandRequest {
  readonly argv: readonly string[];
  readonly cwd: string;
  readonly debugName: string;
  readonly environment: Readonly<Record<string, string>>;
  readonly output: "capture" | "discard";
  readonly sensitiveTokens: readonly string[];
}

export interface OracleCommandResult {
  readonly stdout: string;
}

export interface OracleCommandRunner {
  run(request: OracleCommandRequest): Promise<OracleCommandResult>;
}

const BACKEND_BLOCK = /\bbackend\s+"[^"]+"\s*\{/u;
const CLOUD_BLOCK = /\bcloud\s*\{/u;
const DEFAULT_TIMEOUT_MS = 300_000;

function environmentRecord(environment: NodeJS.ProcessEnv): Readonly<Record<string, string>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, value] of Object.entries(environment)) {
    if (value !== undefined) output[key] = value;
  }
  return output;
}

export function oracleTimeoutMs(environment: NodeJS.ProcessEnv): number {
  const raw = environment.INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS;
  if (raw === undefined || raw.trim().length === 0) return DEFAULT_TIMEOUT_MS;
  const seconds = Number(raw);
  if (!Number.isFinite(seconds) || seconds <= 0) {
    throw new OracleError("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS must be a positive number");
  }
  const milliseconds = Math.ceil(seconds * 1000);
  if (!Number.isSafeInteger(milliseconds) || milliseconds > 600_000) {
    throw new OracleError("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS exceeds the supported 600 second bound");
  }
  return milliseconds;
}

export function createOracleCommandRunner(options: {
  readonly limits?: TerraformCommandLimits;
  readonly terraformExecutable: string;
}): OracleCommandRunner {
  return {
    run: async (request) => {
      try {
        const common = {
          argv: request.argv,
          cwd: request.cwd,
          environment: request.environment,
          terraformExecutable: options.terraformExecutable,
          ...(options.limits === undefined ? {} : { limits: options.limits }),
        };
        if (request.output === "capture") {
          const result = await runTerraformCommand({ ...common, output: "capture" });
          return { stdout: result.stdout.toString("utf8") };
        }
        await runTerraformCommand({ ...common, output: "discard" });
        return { stdout: "" };
      } catch (error: unknown) {
        const code = error instanceof ProcessFailure ? error.code : "TERRAFORM_COMMAND_FAILED";
        throw new ProcessFailure({
          code,
          category: "domain",
          message: `terraform ${request.debugName} failed; provider diagnostics and import IDs were redacted`,
        });
      }
    },
  };
}

function instanceName(key: string): string {
  return `iw_${createHash("sha1").update(key, "utf8").digest("hex").slice(0, 16)}`;
}

export function oracleAddress(resourceType: string, key: string): string {
  return `${resourceType}.${instanceName(key)}`;
}

function checkAddressCollisions(resourceType: string, keys: Iterable<string>): void {
  const seen = new Map<string, string>();
  for (const key of [...keys].sort()) {
    const name = instanceName(key);
    const prior = seen.get(name);
    if (prior !== undefined) {
      throw new OracleError(
        `${resourceType} oracle instance name collision: ${JSON.stringify(prior)} and ${JSON.stringify(key)} both map to ${name}`,
      );
    }
    seen.set(name, key);
  }
}

async function providerBlock(root: LoadedPackRoot, provider: string): Promise<string> {
  const manifest = manifestForProvider(root.packs, provider);
  const source = path.join(manifest.directory, "oracle", `${provider}.tf`);
  return await readOptionalUtf8(source, `${provider} oracle provider configuration`)
    ?? `provider "${provider}" {\n  # credentials via provider environment variables\n}\n`;
}

export async function renderOracleRoot(options: {
  readonly provider: string;
  readonly root: LoadedPackRoot;
}): Promise<string> {
  const source = options.root.packs.providerSources[options.provider];
  if (source === undefined) {
    throw new OracleError(`no provider source declared for ${options.provider}`);
  }
  const manifest = manifestForProvider(options.root.packs, options.provider);
  const pin = typeof manifest.data.pin === "string"
    ? `      version = ${renderHclQuotedString(manifest.data.pin)}\n`
    : "";
  return `terraform {\n`
    + `  required_version = ">= 1.5"\n`
    + `  required_providers {\n`
    + `    ${options.provider} = {\n`
    + `      source = ${renderHclQuotedString(source)}\n`
    + pin
    + `    }\n`
    + `  }\n`
    + `}\n\n`
    + await providerBlock(options.root, options.provider);
}

export function renderOracleImports(
  resourceType: string,
  keyToImportId: ReadonlyMap<string, string>,
): string {
  return [...keyToImportId.keys()].sort().map((key) => {
    const importId = keyToImportId.get(key) ?? "";
    return `import {\n  to = ${oracleAddress(resourceType, key)}\n  id = ${renderHclQuotedString(importId)}\n}\n`;
  }).join("\n");
}

function assertLocalScratchRoot(text: string): void {
  if (BACKEND_BLOCK.test(text)) {
    throw new OracleError("oracle scratch root must not declare a Terraform backend; oracle state is intentionally ephemeral and local");
  }
  if (CLOUD_BLOCK.test(text)) {
    throw new OracleError("oracle scratch root must not declare Terraform cloud; oracle state is intentionally ephemeral and local");
  }
}

function truthy(value: string | undefined): boolean {
  return value !== undefined && ["1", "true", "yes", "on"].includes(value.trim().toLowerCase());
}

function jsonRecord(value: unknown, message: string): JsonRecord {
  if (!isObject(value)) throw new OracleError(message);
  return value;
}

export function assertImportOnlyPlan(options: {
  readonly expectedAddresses: ReadonlySet<string>;
  readonly plan: unknown;
  readonly resourceType: string;
}): void {
  const plan = jsonRecord(options.plan, `${options.resourceType} terraform show -json plan returned a non-object`);
  const drift = Array.isArray(plan.resource_drift) ? plan.resource_drift : [];
  if (drift.length > 0) {
    throw new OracleError(`${options.resourceType} oracle import plan reported resource drift; refusing to apply the scratch plan`);
  }
  const changes = Array.isArray(plan.resource_changes) ? plan.resource_changes : [];
  if (changes.length !== options.expectedAddresses.size) {
    throw new OracleError(
      `${options.resourceType} oracle import plan reported ${changes.length} resource change(s), expected ${options.expectedAddresses.size} import(s); refusing to apply the scratch plan`,
    );
  }
  const addresses = new Set<string>();
  for (const raw of changes) {
    const change = jsonRecord(raw, `${options.resourceType} oracle import plan contained a malformed change`);
    const address = typeof change.address === "string" ? change.address : "<unknown>";
    const details = isObject(change.change) ? change.change : {};
    const actions = Array.isArray(details.actions) ? details.actions : [];
    const importing = isObject(details.importing) && Object.keys(details.importing).length > 0;
    if (actions.length !== 1 || actions[0] !== "no-op" || !importing) {
      throw new OracleError(
        `${options.resourceType} oracle import plan was not import-only for ${address} (actions=${JSON.stringify(actions)} importing=${String(importing)}); refusing to apply the scratch plan`,
      );
    }
    addresses.add(address);
  }
  const missing = [...options.expectedAddresses].filter((address) => !addresses.has(address)).sort();
  const unexpected = [...addresses].filter((address) => !options.expectedAddresses.has(address)).sort();
  if (missing.length > 0 || unexpected.length > 0) {
    throw new OracleError(
      `${options.resourceType} oracle import plan addresses did not match expected scratch addresses (missing=${missing.join(", ") || "<none>"} unexpected=${unexpected.join(", ") || "<none>"}); refusing to apply the scratch plan`,
    );
  }
}

function stateResources(state: unknown): readonly JsonRecord[] {
  const rootValue = isObject(state) && isObject(state.values) && isObject(state.values.root_module)
    ? state.values.root_module
    : {};
  const output: JsonRecord[] = [];
  const stack: unknown[] = [rootValue];
  while (stack.length > 0) {
    const module = stack.pop();
    if (!isObject(module)) continue;
    if (Array.isArray(module.resources)) {
      for (const resource of module.resources) if (isObject(resource)) output.push(resource);
    }
    if (Array.isArray(module.child_modules)) stack.push(...module.child_modules);
  }
  return output;
}

async function fileExists(file: string): Promise<boolean> {
  try {
    await access(file);
    return true;
  } catch {
    return false;
  }
}

function normalPlanFailure(error: unknown): boolean {
  return error instanceof ProcessFailure && error.code === "TERRAFORM_COMMAND_FAILED";
}

/** Execute the generic local-state import/read Oracle transaction. */
export async function importProviderState(options: {
  readonly environment?: NodeJS.ProcessEnv;
  readonly keepWorkdir?: boolean;
  readonly keyToImportId: ReadonlyMap<string, string>;
  readonly onDiagnostic?: (message: string) => void;
  readonly policy?: DriftPolicy;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
  readonly runner: OracleCommandRunner;
}): Promise<ReadonlyMap<string, OracleStateObject>> {
  if (options.keyToImportId.size === 0) return new Map();
  if (options.policy?.entries(options.resourceType, "projection_fill").length && options.rawItems === undefined) {
    throw new OracleError(`${options.resourceType} projection_fill requires raw_items`);
  }
  const importIds = new Map<string, string>();
  for (const [key, importId] of [...options.keyToImportId].sort(([left], [right]) => left.localeCompare(right))) {
    const prior = importIds.get(importId);
    if (prior !== undefined) {
      throw new OracleError(`${options.resourceType} duplicate import_id for keys ${JSON.stringify(prior)} and ${JSON.stringify(key)}`);
    }
    importIds.set(importId, key);
  }
  checkAddressCollisions(options.resourceType, options.keyToImportId.keys());
  const environment = options.environment ?? process.env;
  const keep = options.keepWorkdir === true || truthy(environment.INFRAWRIGHT_KEEP_ORACLE);
  const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-oracle-"));
  let primary: unknown;
  try {
    const resource = options.root.resources.get(options.resourceType);
    if (resource === undefined) throw new OracleError(`unknown active resource ${options.resourceType}`);
    const main = await renderOracleRoot({ provider: resource.provider, root: options.root });
    assertLocalScratchRoot(main);
    await writeFile(path.join(temporary, "main.tf"), main, "utf8");
    await writeFile(
      path.join(temporary, "imports.tf"),
      renderOracleImports(options.resourceType, options.keyToImportId),
      "utf8",
    );
    const childEnvironment = {
      ...environmentRecord(environment),
      TF_DATA_DIR: path.join(temporary, ".terraform"),
    };
    const addresses = new Map<string, string>();
    for (const key of [...options.keyToImportId.keys()].sort()) {
      addresses.set(oracleAddress(options.resourceType, key), key);
    }
    const sensitiveTokens = [...options.keyToImportId.values()].sort();
    const run = async (
      argv: readonly string[],
      debugName: string,
      output: "capture" | "discard" = "discard",
    ): Promise<string> => {
      const result = await options.runner.run({
        argv,
        cwd: temporary,
        debugName,
        environment: childEnvironment,
        output,
        sensitiveTokens,
      });
      return result.stdout;
    };
    await run(["init", "-input=false", "-no-color"], "init");
    const plan = path.join(temporary, "oracle.tfplan");
    const generated = path.join(temporary, "generated.tf");
    let generateFailure: unknown;
    try {
      await run([
        "plan", "-input=false", "-no-color", "-lock=false",
        `-generate-config-out=${generated}`,
        `-out=${plan}`,
      ], "plan-generate-config");
    } catch (error: unknown) {
      if (!normalPlanFailure(error) || !(await fileExists(generated))) throw error;
      generateFailure = error;
    }
    const original = await readOptionalUtf8(generated, `${options.resourceType} generated import config`) ?? "";
    const applied = await applyGeneratedConfigPolicy({
      addressToKey: addresses,
      generatedConfig: original,
      policy: options.policy ?? null,
      resourceType: options.resourceType,
      root: options.root,
      ...(options.rawItems === undefined ? {} : { rawItems: options.rawItems }),
    });
    if (applied.edits > 0) {
      if (keep) await writeFile(path.join(temporary, "generated.tf.before-policy"), original, "utf8");
      await writeFile(generated, applied.text, "utf8");
    }
    if (generateFailure !== undefined && applied.edits === 0) throw generateFailure;
    if (generateFailure !== undefined || applied.edits > 0) {
      await run([
        "plan", "-input=false", "-no-color", "-lock=false", `-out=${plan}`,
      ], "plan-imports");
    }
    const planJson = parseDataJsonLosslessly(await run(["show", "-json", plan], "show-plan", "capture"));
    assertImportOnlyPlan({
      expectedAddresses: new Set(addresses.keys()),
      plan: planJson,
      resourceType: options.resourceType,
    });
    await run(["apply", "-input=false", "-no-color", "-lock=false", plan], "apply-imports");
    const stateJson = parseDataJsonLosslessly(
      await run(["show", "-json", "terraform.tfstate"], "show-state", "capture"),
    );
    const output = new Map<string, OracleStateObject>();
    for (const state of stateResources(stateJson)) {
      if (state.type !== options.resourceType || typeof state.address !== "string") continue;
      const key = addresses.get(state.address);
      if (key === undefined) continue;
      output.set(key, {
        address: state.address,
        values: isObject(state.values) ? state.values : {},
        sensitiveValues: state.sensitive_values ?? {},
      });
    }
    const missing = [...options.keyToImportId.keys()].filter((key) => !output.has(key)).sort();
    if (missing.length > 0) {
      throw new OracleError(`${options.resourceType} import oracle did not return state for key(s): ${missing.join(", ")}`);
    }
    return output;
  } catch (error: unknown) {
    primary = error;
    throw error;
  } finally {
    if (keep) {
      options.onDiagnostic?.(
        `WARNING: kept oracle workdir ${temporary}; it may contain unencrypted provider state, generated configuration, raw API pull values, credentials, import IDs, and provider diagnostics. Remove it when debugging is complete.`,
      );
    } else {
      try {
        await rm(temporary, { force: true, recursive: true });
      } catch (error: unknown) {
        if (primary !== undefined) {
          options.onDiagnostic?.(`WARNING: failed to remove oracle workdir ${temporary} after an error`);
        } else {
          throw new OracleError(`failed to remove oracle workdir ${temporary}`);
        }
      }
    }
  }
}
