import { createHash } from "node:crypto";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { manifestForProvider } from "../metadata/packs.js";
import { isObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { terraformJsonExactlyEqual } from "../json/python-equality.js";
import { readOptionalUtf8 } from "../io/files.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../io/terraform-command.js";
import { renderHclQuotedString } from "./import-moves.js";
import { applyGeneratedConfigPolicies } from "./generated-config-policy.js";
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

export interface OracleBatchResourceRequest {
  readonly keyToImportId: ReadonlyMap<string, string>;
  readonly policy?: DriftPolicy;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
}

export type OracleBatchState = ReadonlyMap<
  string,
  ReadonlyMap<string, OracleStateObject>
>;

export function oracleBatchResourceFamily(resourceTypes: Iterable<string>): string {
  const sorted = [...new Set(resourceTypes)].sort();
  const joined = sorted.join(",");
  if (sorted.length === 1) return sorted[0] ?? "oracle_batch_1_empty";
  const readable = `oracle_batch.${sorted.join(".")}`;
  if (readable.length <= 256) return readable;
  const digest = createHash("sha1").update(joined, "utf8").digest("hex").slice(0, 16);
  return `oracle_batch_${sorted.length}_${digest}`;
}

interface ExpectedOracleInstance {
  readonly importId: string;
  readonly key: string;
  readonly providerName: string;
  readonly resourceType: string;
}

function expectedResourceContext(
  expected: ReadonlyMap<string, ExpectedOracleInstance>,
): string {
  const resourceTypes = new Set([...expected.values()].map((instance) => instance.resourceType));
  return resourceTypes.size === 1 ? `${[...resourceTypes][0]} ` : "";
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
const ORACLE_PLAN_DEBUG_HINT = "rerun with INFRAWRIGHT_KEEP_ORACLE=1 to retain the sensitive scratch workdir for local inspection";

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

function renderOracleBatchImports(resources: readonly OracleBatchResourceRequest[]): string {
  return [...resources]
    .sort((left, right) => left.resourceType.localeCompare(right.resourceType))
    .map((resource) => renderOracleImports(resource.resourceType, resource.keyToImportId))
    .join("\n");
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

function oraclePlanRefusal(message: string): OracleError {
  return new OracleError(`${message}; ${ORACLE_PLAN_DEBUG_HINT}`);
}

function assertImportOnlyBatchPlan(options: {
  readonly expected: ReadonlyMap<string, ExpectedOracleInstance>;
  readonly plan: unknown;
}): void {
  const context = expectedResourceContext(options.expected);
  const plan = jsonRecord(options.plan, `${context}terraform show -json plan returned a non-object`);
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
    throw oraclePlanRefusal(`${context}oracle import plan was incomplete, errored, non-applyable, deferred, or contained non-import effects; refusing to apply the scratch plan`);
  }
  const changes = Array.isArray(plan.resource_changes) ? plan.resource_changes : null;
  if (changes === null || changes.length !== options.expected.size) {
    throw oraclePlanRefusal(
      `${context}oracle import plan reported ${changes?.length ?? "malformed"} resource change(s), expected ${options.expected.size} import(s); refusing to apply the scratch plan`,
    );
  }
  const addresses = new Set<string>();
  for (const raw of changes) {
    const change = jsonRecord(raw, `${context}oracle import plan contained a malformed change`);
    const address = typeof change.address === "string" ? change.address : "<unknown>";
    const expected = options.expected.get(address);
    const details = isObject(change.change) ? change.change : {};
    const actions = Array.isArray(details.actions) ? details.actions : [];
    const importing = isObject(details.importing) ? details.importing : null;
    if (
      expected === undefined
      || addresses.has(address)
      || change.mode !== "managed"
      || change.type !== expected.resourceType
      || change.provider_name !== expected.providerName
      || actions.length !== 1
      || actions[0] !== "no-op"
      || importing === null
      || importing.id !== expected.importId
    ) {
      const safeAddress = expected === undefined ? "<unexpected address>" : address;
      throw oraclePlanRefusal(
        `${context}oracle import plan was not the exact requested import for ${safeAddress}; refusing to apply the scratch plan`,
      );
    }
    addresses.add(address);
  }
  const missing = [...options.expected.keys()].filter((address) => !addresses.has(address)).sort();
  const unexpected = [...addresses].filter((address) => !options.expected.has(address)).sort();
  if (missing.length > 0 || unexpected.length > 0) {
    throw oraclePlanRefusal(
      `${context}oracle import plan addresses did not match expected scratch addresses (missing=${missing.join(", ") || "<none>"} unexpected=${unexpected.join(", ") || "<none>"}); refusing to apply the scratch plan`,
    );
  }
}

export function assertImportOnlyPlan(options: {
  readonly expectedImports: ReadonlyMap<string, string>;
  readonly plan: unknown;
  readonly providerName: string;
  readonly resourceType: string;
}): void {
  const expected = new Map<string, ExpectedOracleInstance>();
  for (const [address, importId] of options.expectedImports) {
    expected.set(address, {
      importId,
      key: address,
      providerName: options.providerName,
      resourceType: options.resourceType,
    });
  }
  assertImportOnlyBatchPlan({ expected, plan: options.plan });
}

function exactBatchStateObjects(options: {
  readonly expected: ReadonlyMap<string, ExpectedOracleInstance>;
  readonly state: unknown;
}): OracleBatchState {
  const context = expectedResourceContext(options.expected);
  const state = jsonRecord(options.state, `${context}terraform show -json state returned a non-object`);
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
    throw new OracleError(`${context}import oracle returned malformed Terraform state`);
  }
  const rootModule = state.values.root_module;
  if (
    !Array.isArray(rootModule.resources)
    || rootModule.resources.length !== options.expected.size
    || !optionalEmptyArray(rootModule.child_modules)
  ) {
    throw new OracleError(`${context}import oracle returned non-exact root state`);
  }
  const output = new Map<string, Map<string, OracleStateObject>>();
  const seen = new Set<string>();
  for (const raw of rootModule.resources) {
    if (!isObject(raw) || typeof raw.address !== "string") {
      throw new OracleError(`${context}import oracle returned a malformed root resource`);
    }
    const expected = options.expected.get(raw.address);
    if (
      expected === undefined
      || seen.has(raw.address)
      || raw.mode !== "managed"
      || raw.type !== expected.resourceType
      || raw.provider_name !== expected.providerName
      || Object.hasOwn(raw, "deposed_key")
      || (raw.tainted !== undefined && raw.tainted !== false)
      || !isObject(raw.values)
      || !Object.hasOwn(raw, "sensitive_values")
      || (!isObject(raw.sensitive_values) && raw.sensitive_values !== true)
    ) {
      throw new OracleError(`${context}import oracle returned non-exact managed state for ${raw.address}`);
    }
    seen.add(raw.address);
    let resourceOutput = output.get(expected.resourceType);
    if (resourceOutput === undefined) {
      resourceOutput = new Map();
      output.set(expected.resourceType, resourceOutput);
    }
    resourceOutput.set(expected.key, {
      address: raw.address,
      values: raw.values,
      sensitiveValues: raw.sensitive_values,
    });
  }
  if (seen.size !== options.expected.size) {
    throw new OracleError(`${context}import oracle did not return exact expected-address coverage`);
  }
  return output;
}

function exactStateObjects(options: {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly providerName: string;
  readonly resourceType: string;
  readonly state: unknown;
}): ReadonlyMap<string, OracleStateObject> {
  const expected = new Map<string, ExpectedOracleInstance>();
  for (const [address, key] of options.addressToKey) {
    expected.set(address, {
      importId: "",
      key,
      providerName: options.providerName,
      resourceType: options.resourceType,
    });
  }
  return exactBatchStateObjects({ expected, state: options.state }).get(options.resourceType)
    ?? new Map();
}

function assertNoUnknownValues(value: unknown, resourceType: string): void {
  if (value === false) return;
  if (value === true) {
    throw new OracleError(
      `${resourceType} accepted import plan left provider-observed values unknown`,
    );
  }
  if (Array.isArray(value)) {
    for (const child of value) assertNoUnknownValues(child, resourceType);
    return;
  }
  if (isObject(value)) {
    for (const child of Object.values(value)) {
      assertNoUnknownValues(child, resourceType);
    }
    return;
  }
  throw new OracleError(
    `${resourceType} accepted import plan returned a malformed unknown-value mask`,
  );
}

/**
 * Extract provider-observed state from a fully known, exact import-only plan.
 *
 * This remains experimental until live provider evidence proves that the
 * accepted plan and post-Apply local state are equivalent for the selected
 * resource cohort.
 */
function extractAcceptedBatchPlanState(options: {
  readonly expected: ReadonlyMap<string, ExpectedOracleInstance>;
  readonly plan: unknown;
}): OracleBatchState {
  assertImportOnlyBatchPlan({ expected: options.expected, plan: options.plan });
  const context = expectedResourceContext(options.expected);
  const plan = jsonRecord(options.plan, `${context}terraform show -json plan returned a non-object`);
  if (!isObject(plan.planned_values) || !isObject(plan.prior_state)) {
    throw new OracleError(`${context}accepted import plan did not contain complete planned and prior state`);
  }
  const planned = exactBatchStateObjects({
    expected: options.expected,
    state: {
      format_version: plan.format_version,
      terraform_version: plan.terraform_version,
      values: plan.planned_values,
    },
  });
  const prior = exactBatchStateObjects({
    expected: options.expected,
    state: plan.prior_state,
  });
  const changes = Array.isArray(plan.resource_changes) ? plan.resource_changes : [];
  const changeByAddress = new Map<string, JsonRecord>();
  for (const raw of changes) {
    const change = jsonRecord(
      raw,
      `${context}accepted import plan contained a malformed change`,
    );
    if (typeof change.address !== "string" || changeByAddress.has(change.address)) {
      throw new OracleError(
        `${context}accepted import plan contained duplicate or malformed change addresses`,
      );
    }
    changeByAddress.set(change.address, change);
  }
  for (const [address, expected] of options.expected) {
    const plannedObject = planned.get(expected.resourceType)?.get(expected.key);
    const priorObject = prior.get(expected.resourceType)?.get(expected.key);
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
        `${expected.resourceType} accepted import plan did not contain exact provider-observed evidence`,
      );
    }
    assertNoUnknownValues(change.after_unknown, expected.resourceType);
    if (
      !terraformJsonExactlyEqual(change.before, change.after)
      || !terraformJsonExactlyEqual(change.after, plannedObject.values)
      || !terraformJsonExactlyEqual(plannedObject.values, priorObject.values)
      || !terraformJsonExactlyEqual(change.before_sensitive, change.after_sensitive)
      || !terraformJsonExactlyEqual(change.after_sensitive, plannedObject.sensitiveValues)
      || !terraformJsonExactlyEqual(plannedObject.sensitiveValues, priorObject.sensitiveValues)
    ) {
      throw new OracleError(
        `${expected.resourceType} accepted import plan provider observations were inconsistent`,
      );
    }
  }
  return planned;
}

export function extractAcceptedPlanState(options: {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly expectedImports: ReadonlyMap<string, string>;
  readonly plan: unknown;
  readonly providerName: string;
  readonly resourceType: string;
}): ReadonlyMap<string, OracleStateObject> {
  const missingImports = [...options.addressToKey.keys()]
    .filter((address) => !options.expectedImports.has(address))
    .sort();
  const unexpectedImports = [...options.expectedImports.keys()]
    .filter((address) => !options.addressToKey.has(address))
    .sort();
  if (missingImports.length > 0 || unexpectedImports.length > 0) {
    throw new OracleError(
      `${options.resourceType} accepted import plan address maps did not match (missing=${missingImports.join(", ") || "<none>"} unexpected=${unexpectedImports.join(", ") || "<none>"})`,
    );
  }
  const expected = new Map<string, ExpectedOracleInstance>();
  for (const [address, key] of options.addressToKey) {
    const importId = options.expectedImports.get(address);
    if (importId === undefined) {
      throw new OracleError(`${options.resourceType} accepted import plan missing expected import ${address}`);
    }
    expected.set(address, {
      importId,
      key,
      providerName: options.providerName,
      resourceType: options.resourceType,
    });
  }
  return extractAcceptedBatchPlanState({ expected, plan: options.plan }).get(options.resourceType)
    ?? new Map();
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

/** Execute one generic local-state import/read Oracle transaction for one provider batch. */
export async function importProviderStates(options: {
  readonly environment?: NodeJS.ProcessEnv;
  readonly keepWorkdir?: boolean;
  readonly onDiagnostic?: (message: string) => void;
  readonly performance?: PerformanceRecorder;
  readonly resources: readonly OracleBatchResourceRequest[];
  readonly root: LoadedPackRoot;
  readonly runner: OracleCommandRunner;
}): Promise<OracleBatchState> {
  const resources = [...options.resources].sort((left, right) => {
    return left.resourceType.localeCompare(right.resourceType);
  });
  const resourceTypes = new Set<string>();
  for (const resource of resources) {
    if (resourceTypes.has(resource.resourceType)) {
      throw new OracleError(`duplicate oracle batch resource type ${resource.resourceType}`);
    }
    resourceTypes.add(resource.resourceType);
  }
  const active = resources.filter((resource) => resource.keyToImportId.size > 0);
  if (active.length === 0) {
    return new Map(resources.map((resource) => [resource.resourceType, new Map()]));
  }
  const resourceFamily = oracleBatchResourceFamily(
    active.map((resource) => resource.resourceType),
  );
  const environment = options.environment ?? process.env;
  const stateSource = oracleStateSource(environment);
  options.performance?.recordSpan({
    durationMs: 0,
    oracleStateSource: stateSource,
    phase: "oracle.state_source",
    resourceFamily,
    status: "success",
    terraformCommands: 0,
  });
  for (const resource of active) {
    if (resource.policy?.entries(resource.resourceType, "projection_fill").length && resource.rawItems === undefined) {
      throw new OracleError(`${resource.resourceType} projection_fill requires raw_items`);
    }
    const importIds = new Map<string, string>();
    for (const [key, importId] of [...resource.keyToImportId].sort(([left], [right]) => left.localeCompare(right))) {
      const prior = importIds.get(importId);
      if (prior !== undefined) {
        throw new OracleError(`${resource.resourceType} duplicate import_id for keys ${JSON.stringify(prior)} and ${JSON.stringify(key)}`);
      }
      importIds.set(importId, key);
    }
    checkAddressCollisions(resource.resourceType, resource.keyToImportId.keys());
  }
  const providerResources = active.map((request) => {
    const resource = options.root.resources.get(request.resourceType);
    if (resource === undefined) throw new OracleError(`unknown active resource ${request.resourceType}`);
    return { request, resource };
  });
  const providers = new Set(providerResources.map(({ resource }) => resource.provider));
  if (providers.size !== 1) {
    throw new OracleError(`oracle batch must contain exactly one provider, found ${[...providers].sort().join(", ")}`);
  }
  const provider = providerResources[0]?.resource.provider ?? "";
  const expectedProviderName = providerName(options.root.packs.providerSources[provider] ?? "");
  const expected = new Map<string, ExpectedOracleInstance>();
  for (const { request } of providerResources) {
    for (const [key, importId] of [...request.keyToImportId].sort(([left], [right]) => left.localeCompare(right))) {
      const address = oracleAddress(request.resourceType, key);
      if (expected.has(address)) throw new OracleError(`oracle batch address collision at ${address}`);
      expected.set(address, {
        importId,
        key,
        providerName: expectedProviderName,
        resourceType: request.resourceType,
      });
    }
  }
  const keep = options.keepWorkdir === true || truthy(environment.INFRAWRIGHT_KEEP_ORACLE);
  const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-oracle-"));
  let primary: unknown;
  try {
    const main = await renderOracleRoot({ provider, root: options.root });
    assertLocalScratchRoot(main);
    await writeFile(path.join(temporary, "main.tf"), main, "utf8");
    await writeFile(
      path.join(temporary, "imports.tf"),
      renderOracleBatchImports(active),
      "utf8",
    );
    const childEnvironment = {
      ...environmentRecord(environment),
      TF_DATA_DIR: path.join(temporary, ".terraform"),
    };
    const sensitiveTokens = active.flatMap((resource) => [...resource.keyToImportId.values()]).sort();
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
          resourceFamily,
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
    const original = await readOptionalUtf8(generated, `${resourceFamily} generated import config`) ?? "";
    const policyStarted = options.performance?.now() ?? 0;
    let applied: Awaited<ReturnType<typeof applyGeneratedConfigPolicies>>;
    let policyStatus: "failed" | "success" = "success";
    try {
      applied = await applyGeneratedConfigPolicies({
        generatedConfig: original,
        resources: active.map((resource) => ({
          addressToKey: new Map([...expected].filter(([, instance]) => {
            return instance.resourceType === resource.resourceType;
          }).map(([address, instance]) => [address, instance.key])),
          policy: resource.policy ?? null,
          resourceType: resource.resourceType,
          ...(resource.rawItems === undefined ? {} : { rawItems: resource.rawItems }),
        })),
        root: options.root,
      });
    } catch (error: unknown) {
      policyStatus = "failed";
      throw error;
    } finally {
      options.performance?.recordSpan({
        durationMs: options.performance.durationSince(policyStarted),
        phase: "oracle.generated_config_policy",
        resourceFamily,
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
        resourceFamily,
        status: "skipped",
        terraformCommands: 0,
      });
    }
    const planJson = parseDataJsonLosslessly(await run(["show", "-json", plan], "show-plan", "capture"));
    if (stateSource === "accepted-plan") {
      const output = extractAcceptedBatchPlanState({ expected, plan: planJson });
      for (const phase of ["oracle.scratch_apply", "oracle.state_show"] as const) {
        options.performance?.recordSpan({
          durationMs: 0,
          phase,
          resourceFamily,
          status: "skipped",
          terraformCommands: 0,
        });
      }
      return new Map(resources.map((resource) => [
        resource.resourceType,
        output.get(resource.resourceType) ?? new Map(),
      ]));
    }
    assertImportOnlyBatchPlan({ expected, plan: planJson });
    await run(["apply", "-input=false", "-no-color", "-lock=false", plan], "apply-imports");
    const stateJson = parseDataJsonLosslessly(
      await run(["show", "-json", "terraform.tfstate"], "show-state", "capture"),
    );
    const output = exactBatchStateObjects({ expected, state: stateJson });
    return new Map(resources.map((resource) => [
      resource.resourceType,
      output.get(resource.resourceType) ?? new Map(),
    ]));
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

/** Execute the generic local-state import/read Oracle transaction for one resource type. */
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
  const output = await importProviderStates({
    ...(options.environment === undefined ? {} : { environment: options.environment }),
    ...(options.keepWorkdir === undefined ? {} : { keepWorkdir: options.keepWorkdir }),
    ...(options.onDiagnostic === undefined ? {} : { onDiagnostic: options.onDiagnostic }),
    ...(options.performance === undefined ? {} : { performance: options.performance }),
    resources: [{
      keyToImportId: options.keyToImportId,
      ...(options.policy === undefined ? {} : { policy: options.policy }),
      ...(options.rawItems === undefined ? {} : { rawItems: options.rawItems }),
      resourceType: options.resourceType,
    }],
    root: options.root,
    runner: options.runner,
  });
  return output.get(options.resourceType) ?? new Map();
}
