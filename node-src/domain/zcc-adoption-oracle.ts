import { createHash } from "node:crypto";
import path from "node:path";

import { isJsonRecord } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
  type ZccAdoptionArtifactSet,
} from "./zcc-adoption-artifacts.js";
import {
  requireSupportedZccAdoptionCatalog,
  type ZccAdoptionCatalog,
  type ZccAdoptionCatalogResource,
} from "./zcc-adoption-catalog.js";
import {
  deriveZccAdoptionIdentities,
  type ZccAdoptionIdentities,
  type ZccAdoptionStateObservation,
} from "./zcc-adoption-projection.js";
import {
  ProcessFailure,
  type ErrorCategory,
  type ErrorDetail,
} from "./errors.js";
import {
  validateZccBootstrapArtifactMetadata,
  type ZccArtifactTarget,
  type ZccPullSource,
} from "./zcc-pull-artifacts.js";

const MAX_ORACLE_GRAPH_DEPTH = 128;
const SCRATCH_PREFIX = "infrawright-zcc-oracle-";
const PROVIDER_ADDRESS_PREFIX = "registry.terraform.io/";

export type ZccAdoptionOracleCommandStage = "init" | "plan" | "apply";
export type ZccAdoptionOracleShowStage = "show-plan" | "show-state";

export interface ZccAdoptionOracleRequest {
  readonly catalog: ZccAdoptionCatalog;
  readonly catalogSha256: string;
  readonly rawItems: readonly unknown[];
  readonly source: ZccPullSource;
  readonly target: ZccArtifactTarget;
  readonly terraformExecutable: string;
}

export interface ZccAdoptionOracleCommandRequest {
  readonly stage: ZccAdoptionOracleCommandStage;
  readonly executable: string;
  readonly cwd: string;
  readonly argv: readonly string[];
  /** Import identifiers that an executor must never include in diagnostics. */
  readonly sensitiveTokens: readonly string[];
  /** Files the executor must recheck immediately before starting this stage. */
  readonly protectedPaths: readonly string[];
}

export interface ZccAdoptionOracleShowRequest {
  readonly stage: ZccAdoptionOracleShowStage;
  readonly executable: string;
  readonly cwd: string;
  readonly argv: readonly string[];
  readonly snapshotPath: string;
  /** Import identifiers that a show adapter must never include in diagnostics. */
  readonly sensitiveTokens: readonly string[];
  /** Files the adapter must recheck and bind while reading this snapshot. */
  readonly protectedPaths: readonly string[];
}

export interface ZccAdoptionOracleWriteRequest {
  readonly path: string;
  readonly content: string;
  readonly mode: 0o600;
}

export interface ZccAdoptionOracleAdapters {
  readonly temporary: {
    readonly create: (prefix: string) => Promise<string>;
    readonly remove: (directory: string) => Promise<void>;
  };
  readonly files: {
    readonly writeText: (
      request: ZccAdoptionOracleWriteRequest,
    ) => Promise<void>;
  };
  readonly command: {
    readonly run: (
      request: ZccAdoptionOracleCommandRequest,
    ) => Promise<void>;
  };
  readonly show: {
    readonly readJson: (
      request: ZccAdoptionOracleShowRequest,
    ) => Promise<unknown>;
  };
}

interface FrozenRequest extends ZccAdoptionOracleRequest {
  readonly rawItems: readonly unknown[];
}

interface ScratchPaths {
  readonly directory: string;
  readonly generatedConfig: string;
  readonly imports: string;
  readonly plan: string;
  readonly root: string;
  readonly state: string;
}

interface ExpectedImport {
  readonly address: string;
  readonly importId: string;
  readonly key: string;
}

function failure(
  code: string,
  message: string,
  category: ErrorCategory = "domain",
  details: readonly ErrorDetail[] = [],
): ProcessFailure {
  return new ProcessFailure({ code, category, message, details });
}

function throwFailure(
  code: string,
  message: string,
  category: "domain" | "io" = "domain",
): never {
  throw failure(code, message, category);
}

function freezeRequest(request: ZccAdoptionOracleRequest): FrozenRequest {
  let snapshot: unknown;
  try {
    const result = snapshotPlainJsonGraph(request, {
      maxDepth: MAX_ORACLE_GRAPH_DEPTH,
    });
    if (!result.ok) {
      return throwFailure(
        "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
        "ZCC adoption oracle input does not satisfy the private JSON contract",
      );
    }
    snapshot = result.value;
  } catch {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
      "ZCC adoption oracle input does not satisfy the private JSON contract",
    );
  }
  if (!isJsonRecord(snapshot)) {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
      "ZCC adoption oracle input does not satisfy the private JSON contract",
    );
  }
  return snapshot as unknown as FrozenRequest;
}

function catalogResource(
  catalog: ZccAdoptionCatalog,
  resourceType: string,
): ZccAdoptionCatalogResource {
  const resource = catalog.resources.find((entry) => entry.type === resourceType);
  if (resource === undefined) {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
      "ZCC adoption oracle resource is not in the supported catalog",
    );
  }
  return resource;
}

function validateRequest(request: FrozenRequest): {
  readonly catalog: ZccAdoptionCatalog;
  readonly identities: ZccAdoptionIdentities;
  readonly resource: ZccAdoptionCatalogResource;
} {
  try {
    if (
      typeof request.terraformExecutable !== "string"
      || request.terraformExecutable.length === 0
      || request.terraformExecutable.includes("\0")
      || !request.terraformExecutable.isWellFormed()
      || request.catalogSha256 !== ZCC_ADOPTION_CATALOG_SHA256
    ) {
      return throwFailure(
        "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
        "ZCC adoption oracle requires the supported executable and catalog binding",
      );
    }
    const catalog = requireSupportedZccAdoptionCatalog(request.catalog);
    const resource = catalogResource(catalog, request.target.resourceType);
    validateZccBootstrapArtifactMetadata({
      lookupSource: resource.lookup_source,
      resourceType: resource.type,
      source: request.source,
      target: request.target,
    });
    const identities = deriveZccAdoptionIdentities({
      catalog,
      rawItems: request.rawItems,
      resourceType: resource.type,
    });
    return { catalog, identities, resource };
  } catch {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
      "ZCC adoption oracle input did not produce a supported adoption identity set",
    );
  }
}

function hclStringLiteral(value: string): string {
  if (value.includes("\0") || !value.isWellFormed()) {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
      "ZCC adoption oracle import identity contains an unsupported character",
    );
  }
  const escaped = value
    .replaceAll("\\", "\\\\")
    .replaceAll('"', '\\"')
    .replaceAll("\n", "\\n")
    .replaceAll("\r", "\\r")
    .replaceAll("\t", "\\t")
    .replaceAll("${", () => "$${")
    .replaceAll("%{", "%%{");
  return `"${escaped}"`;
}

function scratchAddress(resourceType: string, key: string): string {
  const digest = createHash("sha1")
    .update(key, "utf8")
    .digest("hex")
    .slice(0, 16);
  return `${resourceType}.iw_${digest}`;
}

function expectedImports(
  resourceType: string,
  identities: ZccAdoptionIdentities,
): readonly ExpectedImport[] {
  const seenAddresses = new Set<string>();
  return Object.freeze(sortedStrings(Object.keys(identities.import_ids)).map((key) => {
    const importId = identities.import_ids[key];
    if (importId === undefined) {
      return throwFailure(
        "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
        "ZCC adoption oracle identity disappeared before rendering",
      );
    }
    const address = scratchAddress(resourceType, key);
    if (seenAddresses.has(address)) {
      return throwFailure(
        "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
        "ZCC adoption oracle scratch address derivation collided",
      );
    }
    seenAddresses.add(address);
    return Object.freeze({
      address,
      importId,
      key,
    });
  }));
}

function renderRoot(catalog: ZccAdoptionCatalog): string {
  const provider = catalog.provider;
  // The strict plan gate depends on complete, errored, and applyable, which
  // Terraform's JSON contract exposes together from 1.8 onward. This floor is
  // intentionally tighter than the legacy Python oracle's >= 1.5 root.
  return "terraform {\n"
    + '  required_version = ">= 1.8"\n'
    + "  required_providers {\n"
    + `    ${provider.name} = {\n`
    + `      source = ${hclStringLiteral(provider.source)}\n`
    + `      version = ${hclStringLiteral(provider.version)}\n`
    + "    }\n"
    + "  }\n"
    + "}\n\n"
    + `provider "${provider.name}" {\n`
    + "  # credentials via provider environment variables\n"
    + "}\n";
}

function renderImports(imports: readonly ExpectedImport[]): string {
  return imports.map((entry) => {
    return "import {\n"
      + `  to = ${entry.address}\n`
      + `  id = ${hclStringLiteral(entry.importId)}\n`
      + "}\n";
  }).join("\n");
}

function scratchPaths(directory: string): ScratchPaths {
  if (
    !path.isAbsolute(directory)
    || directory.includes("\0")
    || !directory.isWellFormed()
  ) {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_TEMP_FAILED",
      "ZCC adoption oracle temporary authority returned an invalid directory",
      "io",
    );
  }
  return {
    directory,
    generatedConfig: path.join(directory, "generated.tf"),
    imports: path.join(directory, "imports.tf"),
    plan: path.join(directory, "oracle.tfplan"),
    root: path.join(directory, "main.tf"),
    state: path.join(directory, "terraform.tfstate"),
  };
}

async function writeScratchRoot(
  adapters: ZccAdoptionOracleAdapters,
  paths: ScratchPaths,
  catalog: ZccAdoptionCatalog,
  imports: readonly ExpectedImport[],
): Promise<void> {
  try {
    await adapters.files.writeText({
      path: paths.root,
      content: renderRoot(catalog),
      mode: 0o600,
    });
    await adapters.files.writeText({
      path: paths.imports,
      content: renderImports(imports),
      mode: 0o600,
    });
  } catch {
    throw failure(
      "ZCC_ADOPTION_ORACLE_WRITE_FAILED",
      "ZCC adoption oracle could not create its private scratch configuration",
      "io",
    );
  }
}

async function runCommand(
  adapters: ZccAdoptionOracleAdapters,
  request: ZccAdoptionOracleCommandRequest,
): Promise<void> {
  try {
    await adapters.command.run(request);
  } catch {
    const code = request.stage === "init"
      ? "ZCC_ADOPTION_ORACLE_INIT_FAILED"
      : request.stage === "plan"
        ? "ZCC_ADOPTION_ORACLE_PLAN_FAILED"
        : "ZCC_ADOPTION_ORACLE_APPLY_FAILED";
    throw failure(
      code,
      `ZCC adoption oracle ${request.stage} stage failed`,
      "io",
    );
  }
}

async function showJson(
  adapters: ZccAdoptionOracleAdapters,
  request: ZccAdoptionOracleShowRequest,
): Promise<unknown> {
  try {
    const raw = await adapters.show.readJson(request);
    const snapshot = snapshotPlainJsonGraph(raw, {
      maxDepth: MAX_ORACLE_GRAPH_DEPTH,
    });
    if (!snapshot.ok) {
      throw new TypeError("unsupported Terraform JSON graph");
    }
    return snapshot.value;
  } catch {
    throw failure(
      request.stage === "show-plan"
        ? "ZCC_ADOPTION_ORACLE_PLAN_SHOW_FAILED"
        : "ZCC_ADOPTION_ORACLE_STATE_SHOW_FAILED",
      request.stage === "show-plan"
        ? "ZCC adoption oracle could not read the saved plan"
        : "ZCC adoption oracle could not read local state",
      "io",
    );
  }
}

function optionalEmptyArray(value: unknown): boolean {
  return value === undefined || (Array.isArray(value) && value.length === 0);
}

function optionalEmptyRecord(value: unknown): boolean {
  return value === undefined
    || (isJsonRecord(value) && Object.keys(value).length === 0);
}

function optionalEmptyCollection(value: unknown): boolean {
  return optionalEmptyArray(value) || optionalEmptyRecord(value);
}

function hasOwn(record: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function assertImportOnlyPlan(
  candidate: unknown,
  imports: readonly ExpectedImport[],
  resourceType: string,
  providerName: string,
): void {
  const reject = (): never => throwFailure(
    "ZCC_ADOPTION_ORACLE_PLAN_REJECTED",
    "ZCC adoption oracle rejected a non-exact import-only plan",
  );
  if (
    !isJsonRecord(candidate)
    || typeof candidate.format_version !== "string"
    || !/^1\.[0-9]+$/.test(candidate.format_version)
    || candidate.complete !== true
    || candidate.errored !== false
    || candidate.applyable !== true
    || !optionalEmptyCollection(candidate.errors)
    || !optionalEmptyCollection(candidate.diagnostics)
    || !optionalEmptyArray(candidate.checks)
    || !optionalEmptyArray(candidate.deferred_changes)
    || !optionalEmptyArray(candidate.action_invocations)
    || !optionalEmptyArray(candidate.deferred_action_invocations)
    || !optionalEmptyArray(candidate.resource_drift)
    || !optionalEmptyRecord(candidate.output_changes)
    || !Array.isArray(candidate.resource_changes)
    || candidate.resource_changes.length !== imports.length
  ) {
    return reject();
  }
  const expected = new Map(imports.map((entry) => [entry.address, entry] as const));
  const seen = new Set<string>();
  for (const resource of candidate.resource_changes) {
    if (!isJsonRecord(resource) || typeof resource.address !== "string") {
      return reject();
    }
    const expectedImport = expected.get(resource.address);
    if (
      expectedImport === undefined
      || seen.has(resource.address)
      || resource.mode !== "managed"
      || resource.type !== resourceType
      || resource.provider_name !== providerName
      || !isJsonRecord(resource.change)
      || !Array.isArray(resource.change.actions)
      || resource.change.actions.length !== 1
      || resource.change.actions[0] !== "no-op"
      || !isJsonRecord(resource.change.importing)
      || resource.change.importing.id !== expectedImport.importId
    ) {
      return reject();
    }
    seen.add(resource.address);
  }
  if (seen.size !== expected.size) {
    return reject();
  }
}

function exactRootStateObservations(
  candidate: unknown,
  imports: readonly ExpectedImport[],
  resourceType: string,
  providerName: string,
): readonly ZccAdoptionStateObservation[] {
  const reject = (): never => throwFailure(
    "ZCC_ADOPTION_ORACLE_STATE_REJECTED",
    "ZCC adoption oracle rejected non-exact root managed state",
  );
  if (
    !isJsonRecord(candidate)
    || typeof candidate.format_version !== "string"
    || !/^1\.[0-9]+$/.test(candidate.format_version)
    || !isJsonRecord(candidate.values)
    || !optionalEmptyRecord(candidate.values.outputs)
    || !optionalEmptyArray(candidate.checks)
    || !isJsonRecord(candidate.values.root_module)
  ) {
    return reject();
  }
  const root = candidate.values.root_module;
  if (
    !Array.isArray(root.resources)
    || root.resources.length !== imports.length
    || !optionalEmptyArray(root.child_modules)
  ) {
    return reject();
  }
  const expected = new Map(imports.map((entry) => [entry.address, entry] as const));
  const seen = new Set<string>();
  const observations: ZccAdoptionStateObservation[] = [];
  for (const resource of root.resources) {
    if (!isJsonRecord(resource) || typeof resource.address !== "string") {
      return reject();
    }
    const expectedImport = expected.get(resource.address);
    if (
      expectedImport === undefined
      || seen.has(resource.address)
      || resource.mode !== "managed"
      || resource.type !== resourceType
      || resource.provider_name !== providerName
      || hasOwn(resource, "deposed_key")
      || (resource.tainted !== undefined && resource.tainted !== false)
      || !isJsonRecord(resource.values)
      || !hasOwn(resource, "sensitive_values")
      || (
        !isJsonRecord(resource.sensitive_values)
        && resource.sensitive_values !== true
      )
    ) {
      return reject();
    }
    seen.add(resource.address);
    observations.push(Object.freeze({
      address: expectedImport.address,
      import_id: expectedImport.importId,
      key: expectedImport.key,
      provider_name: providerName,
      resource_type: resourceType,
      sensitive_values: resource.sensitive_values,
      values: resource.values,
    }));
  }
  if (seen.size !== expected.size) {
    return reject();
  }
  return Object.freeze(observations);
}

function compileArtifacts(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly observations: readonly ZccAdoptionStateObservation[];
  readonly request: FrozenRequest;
}): ZccAdoptionArtifactSet {
  try {
    return compileZccAdoptionArtifactSet({
      catalog: options.catalog,
      catalogSha256: options.request.catalogSha256,
      observedStates: options.observations,
      rawItems: options.request.rawItems,
      source: options.request.source,
      target: options.request.target,
    });
  } catch {
    throw failure(
      "ZCC_ADOPTION_ORACLE_ARTIFACT_FAILED",
      "ZCC adoption oracle could not compile provider-observed bootstrap artifacts",
    );
  }
}

function withCleanupFailure(primary: ProcessFailure): ProcessFailure {
  return failure(primary.code, primary.message, primary.category, [
    ...primary.details,
    {
      path: "cleanup",
      code: "ZCC_ADOPTION_ORACLE_CLEANUP_FAILED",
      message: "private oracle cleanup also failed",
    },
  ]);
}

/**
 * Run the private ZCC Terraform adoption transaction through injected effects.
 *
 * The core is credential-free and writes no caller-owned paths. Executors own
 * process isolation, environment stripping, path rechecks, output limits, and
 * JSON decoding. This function owns transaction order and the plan/state gates.
 */
export async function runZccAdoptionOracle(
  unsafeRequest: ZccAdoptionOracleRequest,
  adapters: ZccAdoptionOracleAdapters,
): Promise<ZccAdoptionArtifactSet> {
  const request = freezeRequest(unsafeRequest);
  const { catalog, identities, resource } = validateRequest(request);
  const imports = expectedImports(resource.type, identities);
  if (imports.length === 0) {
    return compileArtifacts({
      catalog,
      observations: [],
      request,
    });
  }

  let paths: ScratchPaths | null = null;
  let temporaryDirectory: string | null = null;
  let primary: ProcessFailure | null = null;
  let result: ZccAdoptionArtifactSet | null = null;
  try {
    let directory: string;
    try {
      directory = await adapters.temporary.create(SCRATCH_PREFIX);
      temporaryDirectory = directory;
    } catch {
      throw failure(
        "ZCC_ADOPTION_ORACLE_TEMP_FAILED",
        "ZCC adoption oracle could not create a private temporary directory",
        "io",
      );
    }
    paths = scratchPaths(directory);
    await writeScratchRoot(adapters, paths, catalog, imports);

    const sensitiveTokens = Object.freeze(imports.map((entry) => entry.importId));
    const rootInputs = Object.freeze([paths.root, paths.imports]);
    await runCommand(adapters, {
      stage: "init",
      executable: request.terraformExecutable,
      cwd: paths.directory,
      argv: Object.freeze(["init", "-input=false", "-no-color"]),
      sensitiveTokens: Object.freeze([]),
      protectedPaths: rootInputs,
    });
    await runCommand(adapters, {
      stage: "plan",
      executable: request.terraformExecutable,
      cwd: paths.directory,
      argv: Object.freeze([
        "plan",
        "-input=false",
        "-no-color",
        "-lock=false",
        `-generate-config-out=${paths.generatedConfig}`,
        `-out=${paths.plan}`,
      ]),
      sensitiveTokens,
      protectedPaths: rootInputs,
    });

    const plannedFiles = Object.freeze([
      paths.root,
      paths.imports,
      paths.generatedConfig,
      paths.plan,
    ]);
    const planJson = await showJson(adapters, {
      stage: "show-plan",
      executable: request.terraformExecutable,
      cwd: paths.directory,
      argv: Object.freeze(["show", "-json", paths.plan]),
      snapshotPath: paths.plan,
      sensitiveTokens,
      protectedPaths: plannedFiles,
    });
    const providerName = `${PROVIDER_ADDRESS_PREFIX}${catalog.provider.source}`;
    assertImportOnlyPlan(planJson, imports, resource.type, providerName);

    await runCommand(adapters, {
      stage: "apply",
      executable: request.terraformExecutable,
      cwd: paths.directory,
      argv: Object.freeze([
        "apply",
        "-input=false",
        "-no-color",
        "-lock=false",
        paths.plan,
      ]),
      sensitiveTokens,
      protectedPaths: plannedFiles,
    });
    const stateJson = await showJson(adapters, {
      stage: "show-state",
      executable: request.terraformExecutable,
      cwd: paths.directory,
      argv: Object.freeze(["show", "-json", paths.state]),
      snapshotPath: paths.state,
      sensitiveTokens,
      protectedPaths: Object.freeze([...plannedFiles, paths.state]),
    });
    const observations = exactRootStateObservations(
      stateJson,
      imports,
      resource.type,
      providerName,
    );
    result = compileArtifacts({
      catalog,
      observations,
      request,
    });
  } catch (error: unknown) {
    primary = error instanceof ProcessFailure
      ? error
      : failure(
          "ZCC_ADOPTION_ORACLE_FAILED",
          "ZCC adoption oracle transaction failed",
        );
  } finally {
    if (temporaryDirectory !== null) {
      try {
        await adapters.temporary.remove(temporaryDirectory);
      } catch {
        primary = primary === null
          ? failure(
              "ZCC_ADOPTION_ORACLE_CLEANUP_FAILED",
              "ZCC adoption oracle could not remove its private temporary directory",
              "io",
            )
          : withCleanupFailure(primary);
      }
    }
  }
  if (primary !== null) {
    throw primary;
  }
  if (result === null) {
    return throwFailure(
      "ZCC_ADOPTION_ORACLE_FAILED",
      "ZCC adoption oracle transaction did not produce an artifact set",
    );
  }
  return result;
}
