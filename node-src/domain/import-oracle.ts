import { createHash } from "node:crypto";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { manifestForProvider } from "../metadata/packs.js";
import { isObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { terraformJsonEqual } from "../json/python-equality.js";
import { readOptionalUtf8 } from "../io/files.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../io/terraform-command.js";
import { renderHclQuotedString } from "./import-moves.js";
import { applyGeneratedConfigPolicy } from "./generated-config-policy.js";
import { DriftPolicy } from "./drift-policy.js";
import type { PerformanceRecorder } from "../performance/recorder.js";

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

export type OracleStateSource = "accepted-plan" | "applied-state";

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
  if (!Number.isSafeInteger(milliseconds) || milliseconds <= 0) {
    throw new OracleError("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS is outside the supported numeric range");
  }
  return milliseconds;
}

export function oracleStateSource(environment: NodeJS.ProcessEnv): OracleStateSource {
  const raw = environment.INFRAWRIGHT_ORACLE_STATE_SOURCE?.trim();
  if (raw === undefined || raw.length === 0 || raw === "applied-state") {
    return "applied-state";
  }
  if (raw === "accepted-plan") return "accepted-plan";
  throw new OracleError(
    "INFRAWRIGHT_ORACLE_STATE_SOURCE must be applied-state or accepted-plan",
  );
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
        if (
          error instanceof ProcessFailure
          && error.code === "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM"
        ) {
          throw error;
        }
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

function optionalEmptyArray(value: unknown): boolean {
  return value === undefined || (Array.isArray(value) && value.length === 0);
}

function optionalEmptyRecord(value: unknown): boolean {
  return value === undefined || (isObject(value) && Object.keys(value).length === 0);
}

function optionalEmptyCollection(value: unknown): boolean {
  return optionalEmptyArray(value) || optionalEmptyRecord(value);
}

function providerName(source: string): string {
  return source.split("/").length === 2
    ? `registry.terraform.io/${source}`
    : source;
}

export function assertImportOnlyPlan(options: {
  readonly expectedImports: ReadonlyMap<string, string>;
  readonly plan: unknown;
  readonly providerName: string;
  readonly resourceType: string;
}): void {
  const plan = jsonRecord(options.plan, `${options.resourceType} terraform show -json plan returned a non-object`);
  if (
    typeof plan.format_version !== "string"
    || !/^1\./u.test(plan.format_version)
    || typeof plan.terraform_version !== "string"
    || plan.terraform_version.length === 0
    || plan.complete !== true
    || plan.errored !== false
    || plan.applyable !== true
    || !optionalEmptyCollection(plan.errors)
    || !optionalEmptyCollection(plan.diagnostics)
    || !optionalEmptyArray(plan.checks)
    || !optionalEmptyArray(plan.deferred_changes)
    || !optionalEmptyArray(plan.action_invocations)
    || !optionalEmptyArray(plan.deferred_action_invocations)
    || !optionalEmptyArray(plan.resource_drift)
    || !optionalEmptyRecord(plan.output_changes)
  ) {
    throw new OracleError(`${options.resourceType} oracle import plan was incomplete, errored, non-applyable, deferred, or contained non-import effects; refusing to apply the scratch plan`);
  }
  const changes = Array.isArray(plan.resource_changes) ? plan.resource_changes : null;
  if (changes === null || changes.length !== options.expectedImports.size) {
    throw new OracleError(
      `${options.resourceType} oracle import plan reported ${changes?.length ?? "malformed"} resource change(s), expected ${options.expectedImports.size} import(s); refusing to apply the scratch plan`,
    );
  }
  const addresses = new Set<string>();
  for (const raw of changes) {
    const change = jsonRecord(raw, `${options.resourceType} oracle import plan contained a malformed change`);
    const address = typeof change.address === "string" ? change.address : "<unknown>";
    const expectedImportId = options.expectedImports.get(address);
    const details = isObject(change.change) ? change.change : {};
    const actions = Array.isArray(details.actions) ? details.actions : [];
    const importing = isObject(details.importing) ? details.importing : null;
    if (
      expectedImportId === undefined
      || addresses.has(address)
      || change.mode !== "managed"
      || change.type !== options.resourceType
      || change.provider_name !== options.providerName
      || actions.length !== 1
      || actions[0] !== "no-op"
      || importing === null
      || importing.id !== expectedImportId
    ) {
      throw new OracleError(
        `${options.resourceType} oracle import plan was not the exact requested import for ${address}; refusing to apply the scratch plan`,
      );
    }
    addresses.add(address);
  }
  const missing = [...options.expectedImports.keys()].filter((address) => !addresses.has(address)).sort();
  const unexpected = [...addresses].filter((address) => !options.expectedImports.has(address)).sort();
  if (missing.length > 0 || unexpected.length > 0) {
    throw new OracleError(
      `${options.resourceType} oracle import plan addresses did not match expected scratch addresses (missing=${missing.join(", ") || "<none>"} unexpected=${unexpected.join(", ") || "<none>"}); refusing to apply the scratch plan`,
    );
  }
}

function exactStateObjects(options: {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly providerName: string;
  readonly resourceType: string;
  readonly state: unknown;
}): ReadonlyMap<string, OracleStateObject> {
  const state = jsonRecord(options.state, `${options.resourceType} terraform show -json state returned a non-object`);
  if (
    typeof state.format_version !== "string"
    || !/^1\./u.test(state.format_version)
    || typeof state.terraform_version !== "string"
    || state.terraform_version.length === 0
    || !isObject(state.values)
    || !optionalEmptyRecord(state.values.outputs)
    || !optionalEmptyArray(state.checks)
    || !isObject(state.values.root_module)
  ) {
    throw new OracleError(`${options.resourceType} import oracle returned malformed Terraform state`);
  }
  const rootModule = state.values.root_module;
  if (
    !Array.isArray(rootModule.resources)
    || rootModule.resources.length !== options.addressToKey.size
    || !optionalEmptyArray(rootModule.child_modules)
  ) {
    throw new OracleError(`${options.resourceType} import oracle returned non-exact root state`);
  }
  const output = new Map<string, OracleStateObject>();
  const seen = new Set<string>();
  for (const raw of rootModule.resources) {
    if (!isObject(raw) || typeof raw.address !== "string") {
      throw new OracleError(`${options.resourceType} import oracle returned a malformed root resource`);
    }
    const key = options.addressToKey.get(raw.address);
    if (
      key === undefined
      || seen.has(raw.address)
      || raw.mode !== "managed"
      || raw.type !== options.resourceType
      || raw.provider_name !== options.providerName
      || Object.hasOwn(raw, "deposed_key")
      || (raw.tainted !== undefined && raw.tainted !== false)
      || !isObject(raw.values)
      || !Object.hasOwn(raw, "sensitive_values")
      || (!isObject(raw.sensitive_values) && raw.sensitive_values !== true)
    ) {
      throw new OracleError(`${options.resourceType} import oracle returned non-exact managed state for ${raw.address}`);
    }
    seen.add(raw.address);
    output.set(key, {
      address: raw.address,
      values: raw.values,
      sensitiveValues: raw.sensitive_values,
    });
  }
  if (seen.size !== options.addressToKey.size) {
    throw new OracleError(`${options.resourceType} import oracle did not return exact expected-address coverage`);
  }
  return output;
}

function assertNoUnknownValues(value: unknown, resourceType: string, address: string): void {
  if (value === false) return;
  if (value === true) {
    throw new OracleError(
      `${resourceType} accepted import plan left provider-observed values unknown for ${address}`,
    );
  }
  if (Array.isArray(value)) {
    for (const child of value) assertNoUnknownValues(child, resourceType, address);
    return;
  }
  if (isObject(value)) {
    for (const child of Object.values(value)) {
      assertNoUnknownValues(child, resourceType, address);
    }
    return;
  }
  throw new OracleError(
    `${resourceType} accepted import plan returned a malformed unknown-value mask for ${address}`,
  );
}

/**
 * Extract provider-observed state from a fully known, exact import-only plan.
 *
 * This remains experimental until live provider evidence proves that the
 * accepted plan and post-Apply local state are equivalent for the selected
 * resource cohort.
 */
export function extractAcceptedPlanState(options: {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly expectedImports: ReadonlyMap<string, string>;
  readonly plan: unknown;
  readonly providerName: string;
  readonly resourceType: string;
}): ReadonlyMap<string, OracleStateObject> {
  assertImportOnlyPlan({
    expectedImports: options.expectedImports,
    plan: options.plan,
    providerName: options.providerName,
    resourceType: options.resourceType,
  });
  const plan = jsonRecord(
    options.plan,
    `${options.resourceType} terraform show -json plan returned a non-object`,
  );
  if (!isObject(plan.planned_values) || !isObject(plan.prior_state)) {
    throw new OracleError(
      `${options.resourceType} accepted import plan did not contain complete planned and prior state`,
    );
  }
  const planned = exactStateObjects({
    addressToKey: options.addressToKey,
    providerName: options.providerName,
    resourceType: options.resourceType,
    state: {
      format_version: plan.format_version,
      terraform_version: plan.terraform_version,
      values: plan.planned_values,
    },
  });
  const prior = exactStateObjects({
    addressToKey: options.addressToKey,
    providerName: options.providerName,
    resourceType: options.resourceType,
    state: plan.prior_state,
  });
  const changes = Array.isArray(plan.resource_changes) ? plan.resource_changes : [];
  const changeByAddress = new Map<string, JsonRecord>();
  for (const raw of changes) {
    const change = jsonRecord(
      raw,
      `${options.resourceType} accepted import plan contained a malformed change`,
    );
    if (typeof change.address !== "string" || changeByAddress.has(change.address)) {
      throw new OracleError(
        `${options.resourceType} accepted import plan contained duplicate or malformed change addresses`,
      );
    }
    changeByAddress.set(change.address, change);
  }
  for (const [address, key] of options.addressToKey) {
    const plannedObject = planned.get(key);
    const priorObject = prior.get(key);
    const rawChange = changeByAddress.get(address);
    const change = isObject(rawChange?.change) ? rawChange.change : null;
    if (
      plannedObject === undefined
      || priorObject === undefined
      || rawChange === undefined
      || change === null
      || Object.hasOwn(rawChange, "deposed")
      || !isObject(change.before)
      || !isObject(change.after)
      || !Object.hasOwn(change, "after_unknown")
      || !Object.hasOwn(change, "before_sensitive")
      || !Object.hasOwn(change, "after_sensitive")
      || (!isObject(change.before_sensitive) && change.before_sensitive !== true)
      || (!isObject(change.after_sensitive) && change.after_sensitive !== true)
    ) {
      throw new OracleError(
        `${options.resourceType} accepted import plan did not contain exact provider-observed evidence for ${address}`,
      );
    }
    assertNoUnknownValues(change.after_unknown, options.resourceType, address);
    if (
      !terraformJsonEqual(change.before, change.after)
      || !terraformJsonEqual(change.after, plannedObject.values)
      || !terraformJsonEqual(plannedObject.values, priorObject.values)
      || !terraformJsonEqual(change.before_sensitive, change.after_sensitive)
      || !terraformJsonEqual(change.after_sensitive, plannedObject.sensitiveValues)
      || !terraformJsonEqual(plannedObject.sensitiveValues, priorObject.sensitiveValues)
    ) {
      throw new OracleError(
        `${options.resourceType} accepted import plan provider observations were inconsistent for ${address}`,
      );
    }
  }
  return planned;
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
  readonly performance?: PerformanceRecorder;
  readonly policy?: DriftPolicy;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
  readonly runner: OracleCommandRunner;
}): Promise<ReadonlyMap<string, OracleStateObject>> {
  if (options.keyToImportId.size === 0) return new Map();
  const environment = options.environment ?? process.env;
  const stateSource = oracleStateSource(environment);
  options.performance?.recordSpan({
    durationMs: 0,
    oracleStateSource: stateSource,
    phase: "oracle.state_source",
    resourceFamily: options.resourceType,
    status: "success",
    terraformCommands: 0,
  });
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
    const expectedImports = new Map<string, string>();
    for (const key of [...options.keyToImportId.keys()].sort()) {
      const address = oracleAddress(options.resourceType, key);
      addresses.set(address, key);
      expectedImports.set(address, options.keyToImportId.get(key) ?? "");
    }
    const expectedProviderName = providerName(
      options.root.packs.providerSources[resource.provider] ?? "",
    );
    const sensitiveTokens = [...options.keyToImportId.values()].sort();
    const run = async (
      argv: readonly string[],
      debugName: string,
      output: "capture" | "discard" = "discard",
    ): Promise<string> => {
      const phase = debugName === "init"
        ? "oracle.init"
        : debugName === "plan-generate-config"
          ? "oracle.generated_config_plan"
          : debugName === "plan-imports"
            ? "oracle.corrected_plan"
            : debugName === "show-plan"
              ? "oracle.plan_show"
              : debugName === "apply-imports"
                ? "oracle.scratch_apply"
                : "oracle.state_show";
      const started = options.performance?.now() ?? 0;
      let status: "failed" | "success" = "success";
      try {
        const result = await options.runner.run({
          argv,
          cwd: temporary,
          debugName,
          environment: childEnvironment,
          output,
          sensitiveTokens,
        });
        return result.stdout;
      } catch (error: unknown) {
        status = "failed";
        throw error;
      } finally {
        options.performance?.recordSpan({
          ...(phase === "oracle.corrected_plan" ? { correctedPlan: true } : {}),
          durationMs: options.performance.durationSince(started),
          phase,
          resourceFamily: options.resourceType,
          status,
          terraformCommands: 1,
        });
      }
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
    const policyStarted = options.performance?.now() ?? 0;
    let applied: Awaited<ReturnType<typeof applyGeneratedConfigPolicy>>;
    let policyStatus: "failed" | "success" = "success";
    try {
      applied = await applyGeneratedConfigPolicy({
        addressToKey: addresses,
        generatedConfig: original,
        policy: options.policy ?? null,
        resourceType: options.resourceType,
        root: options.root,
        ...(options.rawItems === undefined ? {} : { rawItems: options.rawItems }),
      });
    } catch (error: unknown) {
      policyStatus = "failed";
      throw error;
    } finally {
      options.performance?.recordSpan({
        durationMs: options.performance.durationSince(policyStarted),
        phase: "oracle.generated_config_policy",
        resourceFamily: options.resourceType,
        status: policyStatus,
      });
    }
    if (applied.edits > 0) {
      if (keep) await writeFile(path.join(temporary, "generated.tf.before-policy"), original, "utf8");
      await writeFile(generated, applied.text, "utf8");
    }
    if (generateFailure !== undefined && applied.edits === 0) throw generateFailure;
    if (generateFailure !== undefined || applied.edits > 0) {
      await run([
        "plan", "-input=false", "-no-color", "-lock=false", `-out=${plan}`,
      ], "plan-imports");
    } else {
      options.performance?.recordSpan({
        correctedPlan: false,
        durationMs: 0,
        phase: "oracle.corrected_plan",
        resourceFamily: options.resourceType,
        status: "skipped",
        terraformCommands: 0,
      });
    }
    const planJson = parseDataJsonLosslessly(await run(["show", "-json", plan], "show-plan", "capture"));
    if (stateSource === "accepted-plan") {
      const output = extractAcceptedPlanState({
        addressToKey: addresses,
        expectedImports,
        plan: planJson,
        providerName: expectedProviderName,
        resourceType: options.resourceType,
      });
      for (const phase of ["oracle.scratch_apply", "oracle.state_show"] as const) {
        options.performance?.recordSpan({
          durationMs: 0,
          phase,
          resourceFamily: options.resourceType,
          status: "skipped",
          terraformCommands: 0,
        });
      }
      return output;
    }
    assertImportOnlyPlan({
      expectedImports,
      plan: planJson,
      providerName: expectedProviderName,
      resourceType: options.resourceType,
    });
    await run(["apply", "-input=false", "-no-color", "-lock=false", plan], "apply-imports");
    const stateJson = parseDataJsonLosslessly(
      await run(["show", "-json", "terraform.tfstate"], "show-state", "capture"),
    );
    return exactStateObjects({
      addressToKey: addresses,
      providerName: expectedProviderName,
      resourceType: options.resourceType,
      state: stateJson,
    });
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
