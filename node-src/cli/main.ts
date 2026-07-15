import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  CliArgumentParseError,
  lastOption,
  parseCommandArguments,
  type ParseCommandArgumentsOptions,
  type ParsedCommandArguments,
} from "./arguments.js";
import {
  deploymentConfigDir,
  deploymentEnvsDir,
  deploymentImportsDir,
  deploymentModuleDir,
  deploymentOverlay,
  deploymentPath,
  deploymentTenantRoot,
  deploymentTfvarsFormat,
  loadBoundAssessmentDeployment,
  loadDeployment,
} from "../domain/deployment.js";
import {
  checkPackRequirements,
  validateActivePackSet,
  validatePackAuthoring,
} from "../metadata/packs.js";
import { loadPackRoot } from "../metadata/loader.js";
import { validatePackResources } from "../metadata/resources.js";
import {
  activeGeneratedResourceTypes,
  generateActiveModules,
  generateModule,
  terraformHclFormatter,
  validateGeneratedModuleTree,
} from "../modules/generator.js";
import { runTransformBatch } from "../domain/transform-runner.js";
import { expandLoadedResources, loadedRootTopology, validateTenant } from "../domain/roots.js";
import {
  fetchResources,
  MAX_FETCH_CONCURRENCY,
  selectFetchResources,
  type CollectorAdapter,
} from "../collectors/rest.js";
import { resolveCollectorAdapters } from "../collectors/authority.js";
import { probeRestHost } from "../collectors/rest-diagnostics.js";
import {
  collectorAuthMode,
  collectorContext,
  createZscalerCollectorAdaptersByProviderSource,
  diagnosticHosts,
  fetchDebugLines,
  maskCollectorIdentifiers,
} from "../collectors/zscaler-adapters.js";
import { createRestHttpTransport } from "../io/rest-http-transport.js";
import {
  defaultAdoptionBatchStateLoader,
  defaultAdoptionStateLoader,
  loadAdoptionPolicy,
  runAdoptBatch,
  type AdoptionBatchStateLoader,
  type AdoptionStateLoader,
} from "../domain/adopt-runner.js";
import {
  assertSupportedTerraformExecutionPlatform,
  resolveTerraformExecutable,
} from "../io/terraform-command.js";
import { ProcessFailure } from "../domain/errors.js";
import { generateEnvironmentRoots } from "../domain/environment-generator.js";
import {
  createImportStagingTerraform,
  stageImports,
  unstageImports,
  type ImportStagingTerraform,
} from "../domain/import-staging.js";
import { referenceOrder } from "../domain/transform-selection.js";
import { changedPathScopeLoaded } from "../domain/scope-paths.js";
import { loadedPlanRoots } from "../domain/plan-roots.js";
import {
  cleanPlans,
  createPlanTerraform,
  planEnvironmentRoots,
  type PlanTerraform,
} from "../domain/plan-lifecycle.js";
import { runSavedPlanAssertion } from "../domain/plan-assessment-runner.js";
import {
  applyExactSavedPlans,
  createExactPlanApplyTerraform,
  currentApplyBranch,
  type ExactPlanApplyTerraform,
} from "../domain/exact-plan-apply.js";
import {
  renderLegacyChangedPathScope,
  renderLegacyPlanRoots,
  renderLegacyRootDiagnostics,
  renderLegacyRootTopology,
} from "../process/legacy.js";
import { sortedStrings } from "../json/python-compatible.js";
import { writePerformanceReport } from "../io/performance-report.js";
import {
  PerformanceRecorder,
  type PerformanceStatus,
} from "../performance/recorder.js";
import {
  AUTHORING_COMMANDS,
  AuthoringCliUsageError,
  runAuthoringCommand,
} from "../authoring/cli.js";

const USAGE = [
  "usage:",
  "  infrawright check-pack [--pack <name>|PACK=<name>] [--root <packs>]",
  "  infrawright check-pack-set [--profile <file>] [--catalog <file>] [--requirements <file>] [--root <packs>]",
  "  infrawright deployment [--deployment <file>] <overlay|tfvars-format|module-dir|tenant-root|config-dir|imports-dir|envs-dir> [tenant]",
  "  infrawright modules <generate|validate> [--resource <type>] [--out <dir>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>] [--terraform <path>]",
  "  infrawright transform --in <dir> --tenant <name> [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright adopt --in <dir> --tenant <name> [--resource <selector>] [--policy <file>] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright gen-env --tenant <name> [--backend <backend>] [--resource <selector>] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright stage-imports --tenant <name> [--resource <selector>] [--state-aware] [--backend-config <file>] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright unstage-imports --tenant <name> [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright resources [--order=references] [--resource <selector>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright roots [--tenant <name>] [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright scope-paths --paths-json <file|-> [--path <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright plan-roots [--tenant <name>] [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright plan --tenant <name> [--resource <selector>] [--imports-only] [--save] [--backend-config <file>] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright clean-plans [--tenant <name>] [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright assert-clean [--tenant <name>] [--resource <selector>] [--backend-config <file>] [--report <file|->] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright assert-adoptable [--tenant <name>] [--resource <selector>] [--policy <file>] [--backend-config <file>] [--report <file|->] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright apply [--tenant <name>] [--resource <selector>] [--policy <file>] [--backend-config <file>] [--allow-destroy] [--allow-non-main] [--allow-plan-changes] [--main-branch <name>] [--terraform <path>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright fetch --tenant <name> [--resource <selector>] [--out <dir>] [--concurrency <count>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright fetch-diag [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright reconcile <resource-type> --api <file> [--api <file>] [--schema <file>] [--provider-source <source>] [--api-options <file>] [--openapi <file>] [--openapi-read <METHOD:/path>] [--openapi-write <METHOD:/path>] [--override <file>] [--out <file>] [--fail-on-unknown]",
  "  infrawright openapi-map --schema <file> --openapi <file> [--provider-source <source>] [--resource-prefix <prefix>] [--api-prefix <prefix>] [--registry <file>] [--out <file>]",
  "  infrawright source-operation-map --schema <file> --openapi <file> --source-root <dir> [--provider-source <source>] [--resource-prefix <prefix>] [--resources <a,b>] [--source-facts <file>] [--source-facts-compare <file>] [--sdk-root <dir>] [--out <file>] [--diagnostics <file>]",
  "  infrawright source-evidence-eval --schema <file> --openapi <file> --source-root <dir> --out-dir <dir> [--provider-source <source>] [--resource-prefix <prefix>] [--resources <a,b>] [--source-facts <file>] [--ast-tool-dir <dir>] [--fail-on-regression]",
  "  infrawright provider-probe <recipe.json> [--work-dir <dir>] [--out <summary.json>] [--markdown <summary.md>] [--debug-traceback]",
  "  infrawright audit-vendor-boundary [--root <repository>] [--allowlist <file>]",
].join("\n");

class CliExit extends Error {
  readonly status: number;
  readonly stdout: boolean;

  constructor(message: string, status: number, stdout = false) {
    super(message);
    this.name = "CliExit";
    this.status = status;
    this.stdout = stdout;
  }
}

function usageError(message: string): never {
  throw new CliExit(message, 2);
}

function commandArguments(
  arguments_: readonly string[],
  configuration: ParseCommandArgumentsOptions,
  behavior: {
    readonly command?: string;
    readonly status?: number;
    readonly stdout?: boolean;
  } = {},
): ParsedCommandArguments {
  let parsed: ParsedCommandArguments;
  try {
    parsed = parseCommandArguments(arguments_, configuration);
  } catch (error: unknown) {
    if (error instanceof CliArgumentParseError) {
      const unknown = /^unknown argument (?<argument>.+)$/u.exec(error.message);
      if (behavior.command !== undefined && unknown?.groups?.argument !== undefined) {
        usageError(`${behavior.command} does not accept ${unknown.groups.argument}`);
      }
      usageError(error.message);
    }
    throw error;
  }
  if (parsed.flags.has("--help")) {
    throw new CliExit(USAGE, behavior.status ?? 0, behavior.stdout ?? true);
  }
  return parsed;
}

const LEGACY_USAGE_FAILURE_CODES = new Set([
  "INVALID_CHANGED_PATHS",
  "INVALID_ASSESSMENT_INPUT",
  "INVALID_DEPLOYMENT",
  "INVALID_ROOT_CONFIGURATION",
  "INVALID_TENANT",
  "INVALID_DRIFT_POLICY",
  "INVALID_TERRAFORM_SHOW_JSON",
  "INVALID_TERRAFORM_SHOW_UTF8",
  "UNKNOWN_RESOURCE_SELECTOR",
]);

async function legacyPlanLifecycleCommand(
  operation: () => Promise<number>,
): Promise<number> {
  try {
    return await operation();
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && LEGACY_USAGE_FAILURE_CODES.has(error.code)
    ) {
      throw new CliExit(error.message, 2);
    }
    throw error;
  }
}

async function packageRoot(): Promise<string> {
  let current = path.dirname(fileURLToPath(import.meta.url));
  while (true) {
    try {
      await access(path.join(current, "package.json"));
      return current;
    } catch {
      const parent = path.dirname(current);
      if (parent === current) {
        throw new Error("unable to locate the Infrawright package root");
      }
      current = parent;
    }
  }
}

async function readStandardInput(): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk)));
  }
  return Buffer.concat(chunks).toString("utf8");
}

async function checkPack(arguments_: string[]): Promise<number> {
  const parsed = commandArguments(arguments_, {
    allowPositionals: true,
    values: { "--pack": {}, "--root": {} },
  }, { status: 2, stdout: false });
  let selectedPack: string | undefined;
  for (const occurrence of parsed.occurrences) {
    if (occurrence.kind === "option") {
      if (occurrence.name === "--pack") selectedPack = occurrence.value;
      continue;
    }
    const argument = occurrence.value;
    if (!argument.startsWith("PACK=")) usageError(`unknown argument ${argument}`);
    selectedPack = argument.slice("PACK=".length);
    if (selectedPack.length === 0) usageError("PACK= requires a value");
  }
  const selectedRoot = lastOption(parsed, "--root");
  const root = path.resolve(
    selectedRoot
      ?? (process.env.INFRAWRIGHT_PACKS || path.join(await packageRoot(), "packs")),
  );
  const result = await validatePackAuthoring({
    root,
    ...(selectedPack === undefined ? {} : { pack: selectedPack }),
  });
  await validatePackResources(result.metadata, result.names);
  process.stdout.write(
    `validated packs: ${result.names.length === 0 ? "none" : result.names.join(", ")}\n`,
  );
  return 0;
}

async function checkPackSet(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--profile": {},
    "--requirements": {},
    "--root": {},
  } });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const requirements = lastOption(parsed, "--requirements");
  if (requirements !== undefined) {
    const result = await checkPackRequirements({
      requirementsPath: requirements,
      root,
      catalogPath: catalog,
    });
    if (!result.available) {
      const pieces: string[] = [];
      if (result.missing.packs.length > 0) {
        pieces.push(`packs=${result.missing.packs.join(",")}`);
      }
      if (result.missing.shared.length > 0) {
        pieces.push(`shared=${result.missing.shared.join(",")}`);
      }
      process.stdout.write(`requirements unavailable: ${pieces.join(" ")}\n`);
      return 3;
    }
    process.stdout.write(
      `requirements satisfied: packs=[${result.active.packs.join(",")}] shared=[${result.active.shared.join(",")}]\n`,
    );
    return 0;
  }
  const result = await validateActivePackSet({
    profilePath: profile,
    root,
    catalogPath: catalog,
  });
  process.stdout.write(
    `validated pack set: packs=[${result.active.packs.join(",")}] shared=[${result.active.shared.join(",")}]\n`,
  );
  return 0;
}

async function deployment(arguments_: string[]): Promise<number> {
  const parsed = commandArguments(arguments_, {
    allowPositionals: true,
    values: { "--deployment": {} },
  }, { status: 2, stdout: false });
  const selectedPath = lastOption(parsed, "--deployment");
  const positional = parsed.positionals;
  const verb = positional[0];
  if (verb === undefined) usageError("deployment requires a verb");
  const source = deploymentPath(
    selectedPath === undefined ? undefined : { explicit: selectedPath },
  );
  const loaded = await loadDeployment(source);
  if (verb === "overlay") process.stdout.write(`${deploymentOverlay(loaded)}\n`);
  else if (verb === "tfvars-format") {
    process.stdout.write(`${deploymentTfvarsFormat(loaded)}\n`);
  } else if (verb === "module-dir") {
    process.stdout.write(`${deploymentModuleDir(loaded)}\n`);
  } else if (
    verb === "tenant-root"
    || verb === "config-dir"
    || verb === "imports-dir"
    || verb === "envs-dir"
  ) {
    const tenant = positional[1];
    if (tenant === undefined) usageError(`${verb} requires a tenant`);
    const value = verb === "tenant-root"
      ? deploymentTenantRoot(loaded, tenant)
      : verb === "config-dir"
        ? deploymentConfigDir(loaded, tenant)
        : verb === "imports-dir"
          ? deploymentImportsDir(loaded, tenant)
          : deploymentEnvsDir(loaded, tenant);
    process.stdout.write(`${value}\n`);
  } else {
    usageError(`unknown deployment verb ${JSON.stringify(verb)}`);
  }
  return 0;
}

interface ModuleOptions {
  readonly verb: "generate" | "validate";
  readonly root: string;
  readonly profile: string;
  readonly catalog: string;
  readonly deployment: string;
  readonly output?: string;
  readonly terraform?: string;
  readonly resources: readonly string[];
}

async function moduleOptions(arguments_: string[]): Promise<ModuleOptions> {
  const rootDirectory = await packageRoot();
  const parsed = commandArguments(arguments_, {
    allowPositionals: true,
    values: {
      "--catalog": {},
      "--deployment": {},
      "--out": {},
      "--profile": {},
      "--resource": { multiple: true },
      "--root": {},
      "--terraform": {},
    },
  }, { command });
  const verb = parsed.positionals[0];
  if (verb !== "generate" && verb !== "validate") {
    usageError("modules requires the generate or validate verb");
  }
  if (parsed.positionals.length !== 1) usageError("modules accepts exactly one verb");
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const output = lastOption(parsed, "--out");
  const terraform = lastOption(parsed, "--terraform");
  const resources = parsed.options["--resource"] ?? [];
  const duplicates = resources.filter((item, index) => resources.indexOf(item) !== index);
  if (duplicates.length > 0) {
    usageError(`duplicate --resource ${JSON.stringify(duplicates[0])}`);
  }
  return {
    verb,
    root,
    profile,
    catalog,
    deployment: selectedDeployment,
    ...(output === undefined ? {} : { output }),
    ...(terraform === undefined ? {} : { terraform }),
    resources,
  };
}

async function modules(arguments_: string[]): Promise<number> {
  const options = await moduleOptions(arguments_);
  const [root, deployment] = await Promise.all([
    loadPackRoot({
      packsRoot: options.root,
      profilePath: options.profile,
      catalogPath: options.catalog,
    }),
    loadDeployment(options.deployment),
  ]);
  const outputRoot = options.output ?? deploymentModuleDir(deployment);
  const active = activeGeneratedResourceTypes(root);
  let resources = active;
  if (options.resources.length > 0) {
    const selected = loadedRootTopology({
      deployment,
      root,
      selectors: options.resources,
      tenant: null,
    });
    process.stderr.write(renderLegacyRootDiagnostics(selected.diagnostics));
    resources = sortedStrings(new Set(
      selected.topology.roots.flatMap((root_) => root_.members),
    ));
  }
  if (options.verb === "validate") {
    await validateGeneratedModuleTree(outputRoot, resources);
    process.stdout.write(
      `validated generated module tree ${outputRoot}: ${resources.length} module(s)\n`,
    );
    return 0;
  }
  const formatter = terraformHclFormatter({
    ...(options.terraform === undefined ? {} : { executable: options.terraform }),
  });
  const onWrite = (destination: string): void => {
    process.stderr.write(`wrote ${destination}\n`);
  };
  let generated;
  if (options.resources.length === 0) {
    generated = await generateActiveModules(root, {
      outputRoot,
      formatHcl: formatter,
      onWrite,
    });
  } else {
    generated = [];
    for (const resourceType of resources) {
      generated.push(await generateModule(root, resourceType, {
        outputRoot,
        formatHcl: formatter,
        onWrite,
      }));
    }
  }
  const files = generated.reduce((total, item) => total + item.files.length, 0);
  process.stdout.write(
    `generated ${generated.length} module(s), ${files} file(s), in ${outputRoot}\n`,
  );
  return 0;
}

async function transform(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--deployment": {},
    "--in": {},
    "--profile": {},
    "--resource": { multiple: true },
    "--root": {},
    "--tenant": {},
  } }, { command: "transform" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const input = lastOption(parsed, "--in");
  const tenant = lastOption(parsed, "--tenant");
  const resources = parsed.options["--resource"] ?? [];
  if (input === undefined || tenant === undefined) {
    usageError("transform requires --in and --tenant");
  }
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog }),
    loadDeployment(selectedDeployment),
  ]);
  const result = await runTransformBatch({
    deployment: loadedDeployment,
    environment: process.env,
    inputDirectory: input,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    selectors: resources,
    tenant,
  });
  return result.failed.length === 0 ? 0 : 1;
}

async function adopt(
  arguments_: string[],
  performance?: PerformanceRecorder,
): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--deployment": {},
    "--in": {},
    "--policy": {},
    "--profile": {},
    "--resource": { multiple: true },
    "--root": {},
    "--tenant": {},
    "--terraform": {},
  } }, { command });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const input = lastOption(parsed, "--in");
  const tenant = lastOption(parsed, "--tenant");
  const policyPath = lastOption(parsed, "--policy");
  const terraform = lastOption(parsed, "--terraform");
  const resources = parsed.options["--resource"] ?? [];
  if (input === undefined || tenant === undefined) usageError("adopt requires --in and --tenant");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog }),
    loadDeployment(selectedDeployment),
  ]);
  const policy = await loadAdoptionPolicy({
    ...(policyPath === undefined ? {} : { path: policyPath }),
    root: packRoot,
  });
  let loadedTerraformExecutable: Promise<string> | null = null;
  const terraformExecutable = (): Promise<string> => {
    loadedTerraformExecutable ??= resolveTerraformExecutable(
      terraform ?? process.env.TF,
      process.env,
    );
    return loadedTerraformExecutable;
  };
  let loadedState: AdoptionStateLoader | null = null;
  const stateLoader: AdoptionStateLoader = async (request) => {
    if (loadedState === null) {
      loadedState = await defaultAdoptionStateLoader({
        environment: process.env,
        onDiagnostic: (message) => process.stderr.write(`${message}\n`),
        ...(performance === undefined ? {} : { performance }),
        root: packRoot,
        terraformExecutable: await terraformExecutable(),
      });
    }
    return loadedState(request);
  };
  let loadedBatchState: AdoptionBatchStateLoader | null = null;
  const batchStateLoader: AdoptionBatchStateLoader = async (request) => {
    if (loadedBatchState === null) {
      loadedBatchState = await defaultAdoptionBatchStateLoader({
        environment: process.env,
        onDiagnostic: (message) => process.stderr.write(`${message}\n`),
        ...(performance === undefined ? {} : { performance }),
        root: packRoot,
        terraformExecutable: await terraformExecutable(),
      });
    }
    return loadedBatchState(request);
  };
  const result = await runAdoptBatch({
    batchStateLoader,
    deployment: loadedDeployment,
    environment: process.env,
    inputDirectory: input,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    policy,
    ...(performance === undefined ? {} : { performance }),
    root: packRoot,
    selectors: resources,
    stateLoader,
    tenant,
  });
  return result.failed.length === 0 ? 0 : 1;
}

async function genEnv(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--backend": {},
    "--catalog": {},
    "--deployment": {},
    "--profile": {},
    "--resource": { multiple: true },
    "--root": {},
    "--tenant": {},
    "--terraform": {},
  } }, { command: "gen-env" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const backend = lastOption(parsed, "--backend");
  const tenant = lastOption(parsed, "--tenant");
  const terraform = lastOption(parsed, "--terraform");
  const resources = parsed.options["--resource"] ?? [];
  if (tenant === undefined) usageError("gen-env requires --tenant");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog }),
    loadDeployment(selectedDeployment),
  ]);
  await generateEnvironmentRoots({
    ...(backend === undefined ? {} : { backend }),
    deployment: loadedDeployment,
    formatHcl: terraformHclFormatter({
      ...(terraform === undefined ? {} : { executable: terraform }),
    }),
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    selectors: resources,
    tenant,
  });
  return 0;
}

interface ImportStagingCliOptions {
  readonly backendConfig?: string;
  readonly catalog: string;
  readonly deployment: string;
  readonly profile: string;
  readonly resources: readonly string[];
  readonly root: string;
  readonly stateAware: boolean;
  readonly tenant: string;
  readonly terraform?: string;
}

async function importStagingCliOptions(
  arguments_: string[],
  command: "stage-imports" | "unstage-imports",
): Promise<ImportStagingCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const stage = command === "stage-imports";
  const parsed = commandArguments(arguments_, {
    flags: stage ? ["--state-aware"] : [],
    values: {
      ...(stage ? { "--backend-config": {}, "--terraform": {} } : {}),
      "--catalog": {},
      "--deployment": {},
      "--profile": {},
      "--resource": { multiple: true },
      "--root": {},
      "--tenant": {},
    },
  }, { command });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const backendConfig = lastOption(parsed, "--backend-config");
  const tenant = lastOption(parsed, "--tenant");
  const terraform = lastOption(parsed, "--terraform");
  const stateAware = parsed.flags.has("--state-aware");
  const resources = parsed.options["--resource"] ?? [];
  if (tenant === undefined) usageError(`${command} requires --tenant`);
  return {
    ...(backendConfig === undefined ? {} : { backendConfig }),
    catalog,
    deployment: selectedDeployment,
    profile,
    resources,
    root,
    stateAware,
    tenant,
    ...(terraform === undefined ? {} : { terraform }),
  };
}

async function stageImportsCommand(arguments_: string[]): Promise<number> {
  const options = await importStagingCliOptions(arguments_, "stage-imports");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({
      packsRoot: options.root,
      profilePath: options.profile,
      catalogPath: options.catalog,
    }),
    loadDeployment(options.deployment),
  ]);
  let adapterPromise: Promise<ImportStagingTerraform> | undefined;
  const adapter = options.stateAware ? {
    initialize: async (request: Parameters<ImportStagingTerraform["initialize"]>[0]) => {
      adapterPromise ??= resolveTerraformExecutable(
        options.terraform ?? process.env.TF,
        process.env,
      ).then((terraformExecutable) => createImportStagingTerraform({
        environment: process.env,
        terraformExecutable,
      }));
      await (await adapterPromise).initialize(request);
    },
    listState: async (request: Parameters<ImportStagingTerraform["listState"]>[0]) => {
      adapterPromise ??= resolveTerraformExecutable(
        options.terraform ?? process.env.TF,
        process.env,
      ).then((terraformExecutable) => createImportStagingTerraform({
        environment: process.env,
        terraformExecutable,
      }));
      return (await adapterPromise).listState(request);
    },
  } satisfies ImportStagingTerraform : undefined;
  await stageImports({
    ...(options.backendConfig === undefined ? {} : { backendConfig: options.backendConfig }),
    deployment: loadedDeployment,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    selectors: options.resources,
    stateAware: options.stateAware,
    tenant: options.tenant,
    ...(adapter === undefined ? {} : { terraform: adapter }),
    workspace: process.cwd(),
  });
  return 0;
}

async function unstageImportsCommand(arguments_: string[]): Promise<number> {
  const options = await importStagingCliOptions(arguments_, "unstage-imports");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({
      packsRoot: options.root,
      profilePath: options.profile,
      catalogPath: options.catalog,
    }),
    loadDeployment(options.deployment),
  ]);
  await unstageImports({
    deployment: loadedDeployment,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    selectors: options.resources,
    tenant: options.tenant,
    workspace: process.cwd(),
  });
  return 0;
}

interface RootQueryCliOptions {
  readonly catalog: string;
  readonly deployment: string;
  readonly profile: string;
  readonly resources: readonly string[];
  readonly root: string;
  readonly tenant?: string;
}

async function rootQueryCliOptions(
  arguments_: string[],
  command: "roots" | "plan-roots" | "clean-plans",
): Promise<RootQueryCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--deployment": {},
    "--profile": {},
    "--resource": { multiple: true },
    "--root": {},
    "--tenant": { allowEmpty: true, multiple: false },
  } }, { command });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const tenant = lastOption(parsed, "--tenant");
  const resources = parsed.options["--resource"] ?? [];
  return {
    catalog,
    deployment: selectedDeployment,
    profile,
    resources,
    root,
    ...(tenant === undefined ? {} : { tenant }),
  };
}

async function resourcesCommand(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--order": {},
    "--profile": {},
    "--resource": { multiple: true },
    "--root": {},
  } }, { command: "resources" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const orderValue = lastOption(parsed, "--order");
  if (orderValue !== undefined && orderValue !== "references") {
    usageError("--order only accepts references");
  }
  const order: "sorted" | "references" = orderValue === "references" ? "references" : "sorted";
  const resources = parsed.options["--resource"] ?? [];
  const packRoot = await loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog });
  const selected = expandLoadedResources(packRoot, resources);
  const ordered = order === "references"
    ? referenceOrder({ root: packRoot, resourceTypes: selected })
    : { resourceTypes: selected, notes: [] as readonly string[] };
  for (const note of ordered.notes) process.stderr.write(note);
  for (const resourceType of ordered.resourceTypes) process.stdout.write(`${resourceType}\n`);
  return 0;
}

async function rootsCommand(arguments_: string[]): Promise<number> {
  const options = await rootQueryCliOptions(arguments_, "roots");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: options.root, profilePath: options.profile, catalogPath: options.catalog }),
    loadDeployment(options.deployment),
  ]);
  const result = loadedRootTopology({
    deployment: loadedDeployment,
    root: packRoot,
    selectors: options.resources,
    tenant: options.tenant ?? null,
  });
  process.stderr.write(renderLegacyRootDiagnostics(result.diagnostics));
  process.stdout.write(renderLegacyRootTopology(result.topology));
  return 0;
}

async function scopePathsCommand(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsedArguments = commandArguments(arguments_, { values: {
    "--catalog": {},
    "--deployment": {},
    "--path": { allowEmpty: true, multiple: true },
    "--paths-json": { multiple: false },
    "--profile": {},
    "--root": {},
  } }, { command: "scope-paths" });
  root = lastOption(parsedArguments, "--root") ?? root;
  profile = lastOption(parsedArguments, "--profile") ?? profile;
  catalog = lastOption(parsedArguments, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsedArguments, "--deployment") ?? deploymentPath();
  const pathsJson = lastOption(parsedArguments, "--paths-json");
  const paths = [...(parsedArguments.options["--path"] ?? [])];
  if (pathsJson !== undefined) {
    let parsed: unknown;
    const text = pathsJson === "-"
      ? await readStandardInput()
      : await readFile(pathsJson, "utf8");
    try {
      parsed = JSON.parse(text) as unknown;
    } catch {
      usageError(`${pathsJson} must contain a JSON array of changed paths`);
    }
    if (!Array.isArray(parsed)) usageError(`${pathsJson} must contain a JSON array of changed paths`);
    paths.push(...parsed as string[]);
  }
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog }),
    loadDeployment(selectedDeployment),
  ]);
  process.stdout.write(renderLegacyChangedPathScope(changedPathScopeLoaded({
    deployment: loadedDeployment,
    deploymentPath: selectedDeployment,
    paths,
    root: packRoot,
    workspace: process.cwd(),
  })));
  return 0;
}

async function planRootsCommand(arguments_: string[]): Promise<number> {
  const options = await rootQueryCliOptions(arguments_, "plan-roots");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: options.root, profilePath: options.profile, catalogPath: options.catalog }),
    loadDeployment(options.deployment),
  ]);
  const result = await loadedPlanRoots({
    deployment: loadedDeployment,
    root: packRoot,
    selectors: options.resources,
    tenant: options.tenant ?? null,
    workspace: process.cwd(),
  });
  process.stderr.write(renderLegacyRootDiagnostics(result.diagnostics));
  process.stdout.write(renderLegacyPlanRoots(result.result));
  return 0;
}

interface PlanCliOptions extends RootQueryCliOptions {
  readonly backendConfig?: string;
  readonly importsOnly: boolean;
  readonly save: boolean;
  readonly terraform?: string;
  readonly tenant: string;
}

async function planCliOptions(arguments_: string[]): Promise<PlanCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, {
    flags: ["--imports-only", "--save"],
    values: {
      "--backend-config": {},
      "--catalog": {},
      "--deployment": {},
      "--profile": {},
      "--resource": { multiple: true },
      "--root": {},
      "--tenant": { allowEmpty: true },
      "--terraform": {},
    },
  }, { command: "plan" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const tenant = lastOption(parsed, "--tenant");
  const backendConfig = lastOption(parsed, "--backend-config");
  const terraform = lastOption(parsed, "--terraform");
  const importsOnly = parsed.flags.has("--imports-only");
  const save = parsed.flags.has("--save");
  const resources = parsed.options["--resource"] ?? [];
  if (tenant === undefined) usageError("plan requires --tenant");
  return {
    ...(backendConfig === undefined ? {} : { backendConfig }),
    catalog,
    deployment: selectedDeployment,
    importsOnly,
    profile,
    resources,
    root,
    save,
    tenant,
    ...(terraform === undefined ? {} : { terraform }),
  };
}

async function planCommand(arguments_: string[]): Promise<number> {
  const options = await planCliOptions(arguments_);
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: options.root, profilePath: options.profile, catalogPath: options.catalog }),
    loadDeployment(options.deployment),
  ]);
  let adapterPromise: Promise<PlanTerraform> | undefined;
  const adapter: PlanTerraform = {
    initialize: async (request) => {
      adapterPromise ??= resolveTerraformExecutable(
        options.terraform ?? process.env.TF,
        process.env,
      ).then((terraformExecutable) => createPlanTerraform({
        environment: process.env,
        terraformExecutable,
      }));
      await (await adapterPromise).initialize(request);
    },
    plan: async (request) => {
      if (adapterPromise === undefined) {
        throw new Error("Terraform plan adapter was used before initialization");
      }
      await (await adapterPromise).plan(request);
    },
  };
  await planEnvironmentRoots({
    ...(options.backendConfig === undefined ? {} : { backendConfig: options.backendConfig }),
    deployment: loadedDeployment,
    importsOnly: options.importsOnly,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    save: options.save,
    selectors: options.resources,
    tenant: options.tenant,
    terraform: adapter,
    workspace: process.cwd(),
  });
  return 0;
}

async function cleanPlansCommand(arguments_: string[]): Promise<number> {
  const options = await rootQueryCliOptions(arguments_, "clean-plans");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: options.root, profilePath: options.profile, catalogPath: options.catalog }),
    loadDeployment(options.deployment),
  ]);
  await cleanPlans({
    deployment: loadedDeployment,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    root: packRoot,
    selectors: options.resources,
    tenant: options.tenant ?? null,
    workspace: process.cwd(),
  });
  return 0;
}

interface AssessmentCliOptions extends RootQueryCliOptions {
  readonly backendConfig?: string;
  readonly policy?: string;
  readonly report?: string;
  readonly terraform?: string;
}

async function assessmentCliOptions(
  arguments_: string[],
  command: "assert-clean" | "assert-adoptable",
): Promise<AssessmentCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--backend-config": {},
    "--catalog": {},
    "--deployment": {},
    ...(command === "assert-adoptable" ? { "--policy": {} } : {}),
    "--profile": {},
    "--report": { multiple: false },
    "--resource": { multiple: true },
    "--root": {},
    "--tenant": { allowEmpty: true, multiple: false },
    "--terraform": {},
  } }, { command });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const tenant = lastOption(parsed, "--tenant");
  const backendConfig = lastOption(parsed, "--backend-config");
  const policy = lastOption(parsed, "--policy");
  const report = lastOption(parsed, "--report");
  const terraform = lastOption(parsed, "--terraform");
  const resources = parsed.options["--resource"] ?? [];
  return {
    ...(backendConfig === undefined ? {} : { backendConfig }),
    catalog,
    deployment: selectedDeployment,
    ...(policy === undefined ? {} : { policy }),
    profile,
    ...(report === undefined ? {} : { report }),
    resources,
    root,
    ...(tenant === undefined ? {} : { tenant }),
    ...(terraform === undefined ? {} : { terraform }),
  };
}

async function assessmentCommand(
  arguments_: string[],
  command: "assert-clean" | "assert-adoptable",
): Promise<number> {
  const options = await assessmentCliOptions(arguments_, command);
  await runSavedPlanAssertion({
    backendConfig: options.backendConfig ?? null,
    loadInputs: async () => {
      const [packRoot, boundDeployment] = await Promise.all([
        loadPackRoot({
          packsRoot: options.root,
          profilePath: options.profile,
          catalogPath: options.catalog,
        }),
        loadBoundAssessmentDeployment(path.resolve(options.deployment)),
      ]);
      return {
        deployment: boundDeployment.deployment,
        root: packRoot,
        controlFiles: [boundDeployment.file],
      };
    },
    mode: command,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    policyPath: options.policy ?? null,
    reportPath: options.report ?? null,
    selectors: options.resources,
    stdout: (text) => process.stdout.write(text),
    tenant: options.tenant ?? null,
    terraformExecutable: () => resolveTerraformExecutable(
      options.terraform ?? process.env.TF,
      process.env,
    ),
    workspace: process.cwd(),
  });
  return 0;
}

interface ApplyCliOptions extends RootQueryCliOptions {
  readonly allowDestroy: boolean;
  readonly allowNonMain: boolean;
  readonly allowPlanChanges: boolean;
  readonly backendConfig?: string;
  readonly mainBranch?: string;
  readonly policy?: string;
  readonly terraform?: string;
}

async function applyCliOptions(arguments_: string[]): Promise<ApplyCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, {
    flags: ["--allow-destroy", "--allow-non-main", "--allow-plan-changes"],
    values: {
      "--backend-config": {},
      "--catalog": {},
      "--deployment": {},
      "--main-branch": {},
      "--policy": {},
      "--profile": {},
      "--resource": { multiple: true },
      "--root": {},
      "--tenant": { allowEmpty: true },
      "--terraform": {},
    },
  }, { command: "apply" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const selectedDeployment = lastOption(parsed, "--deployment") ?? deploymentPath();
  const tenant = lastOption(parsed, "--tenant");
  const backendConfig = lastOption(parsed, "--backend-config");
  const policy = lastOption(parsed, "--policy");
  const terraform = lastOption(parsed, "--terraform");
  const mainBranch = lastOption(parsed, "--main-branch");
  const allowDestroy = parsed.flags.has("--allow-destroy");
  const allowNonMain = parsed.flags.has("--allow-non-main");
  const allowPlanChanges = parsed.flags.has("--allow-plan-changes");
  const resources = parsed.options["--resource"] ?? [];
  if (tenant !== undefined) validateTenant(tenant);
  return {
    allowDestroy,
    allowNonMain,
    allowPlanChanges,
    ...(backendConfig === undefined ? {} : { backendConfig }),
    catalog,
    deployment: selectedDeployment,
    ...(mainBranch === undefined ? {} : { mainBranch }),
    ...(policy === undefined ? {} : { policy }),
    profile,
    resources,
    root,
    ...(tenant === undefined ? {} : { tenant }),
    ...(terraform === undefined ? {} : { terraform }),
  };
}

async function applyCommand(arguments_: string[]): Promise<number> {
  const options = await applyCliOptions(arguments_);
  const workspace = process.cwd();
  let adapterPromise: Promise<ExactPlanApplyTerraform> | undefined;
  const adapter = async (): Promise<ExactPlanApplyTerraform> => {
    adapterPromise ??= resolveTerraformExecutable(
      options.terraform ?? process.env.TF,
      process.env,
    ).then((terraformExecutable) => createExactPlanApplyTerraform({
      environment: process.env,
      terraformExecutable,
    }));
    return adapterPromise;
  };
  await applyExactSavedPlans({
    allowDestroy: options.allowDestroy,
    allowNonMain: options.allowNonMain,
    allowPlanChanges: options.allowPlanChanges,
    backendConfig: options.backendConfig === undefined
      ? null
      : path.resolve(workspace, options.backendConfig),
    currentBranch: () => currentApplyBranch({
      cwd: workspace,
      environment: process.env,
    }),
    loadInputs: async () => {
      const [packRoot, boundDeployment] = await Promise.all([
        loadPackRoot({
          packsRoot: options.root,
          profilePath: options.profile,
          catalogPath: options.catalog,
        }),
        loadBoundAssessmentDeployment(path.resolve(options.deployment)),
      ]);
      return {
        controlFiles: [boundDeployment.file],
        deployment: boundDeployment.deployment,
        root: packRoot,
      };
    },
    mainBranch: options.mainBranch ?? null,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    policyPath: options.policy === undefined
      ? null
      : path.resolve(workspace, options.policy),
    selectors: options.resources,
    tenant: options.tenant ?? null,
    terraform: {
      initialize: async (request) => (await adapter()).initialize(request),
      show: async (request) => (await adapter()).show(request),
      apply: async (request) => (await adapter()).apply(request),
    },
    workspace,
  });
  return 0;
}

interface FetchCliOptions {
  readonly catalog: string;
  readonly concurrency: number;
  readonly output?: string;
  readonly profile: string;
  readonly resources: readonly string[];
  readonly root: string;
  readonly tenant?: string;
}

function requireBuiltInCollectorAuthority(
  root: Awaited<ReturnType<typeof loadPackRoot>>,
  resourceTypes: readonly string[],
): ReadonlyMap<string, CollectorAdapter> {
  try {
    return resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      resourceTypes,
      root,
    });
  } catch (error: unknown) {
    usageError(error instanceof Error ? error.message : "invalid collector adapter authority");
  }
}

async function fetchCliOptions(
  arguments_: string[],
  requireTenant: boolean,
): Promise<FetchCliOptions> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  const parsed = commandArguments(arguments_, { values: {
    "--catalog": {},
    ...(requireTenant ? {
      "--concurrency": { multiple: false },
      "--out": {},
      "--resource": { multiple: true },
      "--tenant": {},
    } : {}),
    "--profile": {},
    "--root": {},
  } }, requireTenant ? {} : { command: "fetch-diag" });
  root = lastOption(parsed, "--root") ?? root;
  profile = lastOption(parsed, "--profile") ?? profile;
  catalog = lastOption(parsed, "--catalog") ?? catalog;
  const output = lastOption(parsed, "--out");
  const tenant = lastOption(parsed, "--tenant");
  const resources = parsed.options["--resource"] ?? [];
  const concurrencyValue = lastOption(parsed, "--concurrency");
  let concurrency = 1;
  if (concurrencyValue !== undefined) {
    if (!/^[1-9][0-9]*$/u.test(concurrencyValue)) {
      usageError("--concurrency must be a positive integer");
    }
    concurrency = Number(concurrencyValue);
    if (!Number.isSafeInteger(concurrency) || concurrency > MAX_FETCH_CONCURRENCY) {
      usageError(`--concurrency must not exceed ${MAX_FETCH_CONCURRENCY}`);
    }
  }
  if (requireTenant && tenant === undefined) usageError("fetch requires --tenant");
  if (tenant !== undefined) {
    try {
      validateTenant(tenant);
    } catch (error: unknown) {
      usageError(error instanceof Error ? error.message : "invalid tenant");
    }
  }
  return {
    catalog,
    concurrency,
    ...(output === undefined ? {} : { output }),
    profile,
    resources,
    root,
    ...(tenant === undefined ? {} : { tenant }),
  };
}

async function fetchCommand(
  arguments_: string[],
  performance?: PerformanceRecorder,
): Promise<number> {
  const options = await fetchCliOptions(arguments_, true);
  const tenant = options.tenant;
  if (tenant === undefined) usageError("fetch requires --tenant");
  const root = await loadPackRoot({
    packsRoot: options.root,
    profilePath: options.profile,
    catalogPath: options.catalog,
  });
  let selected: readonly string[];
  try {
    selected = selectFetchResources({ root, selectors: options.resources });
  } catch (error: unknown) {
    usageError(error instanceof Error ? error.message : "invalid fetch selector");
  }
  const products = new Set(selected.map((resourceType) => {
    const resource = root.resources.get(resourceType);
    if (resource === undefined) throw new Error(`unknown active resource ${resourceType}`);
    return resource.product;
  }));
  const adapters = requireBuiltInCollectorAuthority(root, selected);
  const mode = collectorAuthMode(process.env);
  const context = collectorContext({
    environment: process.env,
    mode,
    neededProducts: products,
  });
  for (const line of fetchDebugLines({
    context,
    environment: process.env,
    mode,
    products,
  })) {
    process.stderr.write(`${line}\n`);
  }
  const transport = await createRestHttpTransport(
    process.env,
    performance === undefined ? {} : { performance },
  );
  let result;
  let primary: unknown;
  try {
    result = await fetchResources({
      adapters,
      concurrency: options.concurrency,
      context,
      environment: process.env,
      mode,
      onDiagnostic: (message) => process.stderr.write(`${message}\n`),
      outputDirectory: options.output ?? path.join("pulls", tenant),
      ...(performance === undefined ? {} : { performance }),
      root,
      selectors: options.resources,
      transport,
    });
  } catch (error: unknown) {
    primary = error;
  } finally {
    try {
      await transport.close?.();
    } catch (error: unknown) {
      if (primary === undefined) primary = error;
    }
  }
  if (primary !== undefined) throw primary;
  if (result === undefined) throw new Error("fetch did not produce a result");
  return Object.keys(result.failed).length === 0 ? 0 : 1;
}

async function fetchDiag(arguments_: string[]): Promise<number> {
  const options = await fetchCliOptions(arguments_, false);
  const root = await loadPackRoot({
    packsRoot: options.root,
    profilePath: options.profile,
    catalogPath: options.catalog,
  });
  const selected = selectFetchResources({ root, selectors: [] });
  const products = new Set(selected.map((resourceType) => {
    const resource = root.resources.get(resourceType);
    if (resource === undefined) throw new Error(`unknown active resource ${resourceType}`);
    return resource.product;
  }));
  requireBuiltInCollectorAuthority(root, selected);
  const bundle = process.env.REQUESTS_CA_BUNDLE || process.env.SSL_CERT_FILE;
  for (const host of diagnosticHosts(process.env, products)) {
    if (host.includes("<")) {
      process.stderr.write(`${maskCollectorIdentifiers(host)}: skipped (env vars not set)\n`);
      continue;
    }
    const system = await probeRestHost(host, {
      environment: process.env,
      includeCustomCa: false,
    });
    let line = `${maskCollectorIdentifiers(host)}: system-trust ${system.ok ? "OK" : "FAIL"} (${maskCollectorIdentifiers(system.detail)})`;
    if (bundle === undefined || bundle === "") {
      line += "; no CA bundle configured (set REQUESTS_CA_BUNDLE)";
    } else {
      const custom = await probeRestHost(host, {
        environment: process.env,
        includeCustomCa: true,
      });
      line += `; +bundle ${custom.ok ? "OK" : "FAIL"} (${maskCollectorIdentifiers(custom.detail)})`;
    }
    process.stderr.write(`${line}\n`);
  }
  return 0;
}

const TERRAFORM_COMMAND_VALUE_OPTIONS = new Set([
  "--backend",
  "--backend-config",
  "--catalog",
  "--deployment",
  "--in",
  "--main-branch",
  "--out",
  "--policy",
  "--profile",
  "--report",
  "--resource",
  "--root",
  "--tenant",
  "--terraform",
]);
const TERRAFORM_COMMAND_FLAGS = new Set([
  "--allow-destroy",
  "--allow-non-main",
  "--allow-plan-changes",
  "--imports-only",
  "--save",
  "--state-aware",
]);

function hasStandaloneTerraformHelp(arguments_: readonly string[]): boolean {
  for (let index = 1; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "-h" || argument === "--help") return true;
    if (argument !== undefined && TERRAFORM_COMMAND_VALUE_OPTIONS.has(argument)) {
      if (arguments_[index + 1] === undefined) return false;
      index += 2;
      continue;
    }
    if (
      argument !== undefined
      && (
        TERRAFORM_COMMAND_FLAGS.has(argument)
        || (index === 1 && arguments_[0] === "modules" && argument === "generate")
      )
    ) {
      index += 1;
      continue;
    }
    return false;
  }
  return false;
}

function requiresTerraformExecution(arguments_: readonly string[]): boolean {
  if (hasStandaloneTerraformHelp(arguments_)) return false;
  const command = arguments_[0];
  return command === "adopt"
    || command === "gen-env"
    || command === "plan"
    || command === "assert-clean"
    || command === "assert-adoptable"
    || command === "apply"
    || (command === "modules" && arguments_[1] === "generate")
    || (command === "stage-imports" && arguments_.includes("--state-aware"));
}

async function main(
  arguments_: string[],
  performance?: PerformanceRecorder,
): Promise<number> {
  const command = arguments_[0];
  if (requiresTerraformExecution(arguments_)) {
    assertSupportedTerraformExecutionPlatform();
  }
  if (command === "check-pack") return checkPack(arguments_.slice(1));
  if (command === "check-pack-set") return checkPackSet(arguments_.slice(1));
  if (command === "deployment") return deployment(arguments_.slice(1));
  if (command === "modules") return modules(arguments_.slice(1));
  if (command === "transform") return transform(arguments_.slice(1));
  if (command === "adopt") return adopt(arguments_.slice(1), performance);
  if (command === "gen-env") return genEnv(arguments_.slice(1));
  if (command === "stage-imports") return stageImportsCommand(arguments_.slice(1));
  if (command === "unstage-imports") return unstageImportsCommand(arguments_.slice(1));
  if (command === "resources") {
    return legacyPlanLifecycleCommand(() => resourcesCommand(arguments_.slice(1)));
  }
  if (command === "roots") {
    return legacyPlanLifecycleCommand(() => rootsCommand(arguments_.slice(1)));
  }
  if (command === "scope-paths") {
    return legacyPlanLifecycleCommand(() => scopePathsCommand(arguments_.slice(1)));
  }
  if (command === "plan-roots") {
    return legacyPlanLifecycleCommand(() => planRootsCommand(arguments_.slice(1)));
  }
  if (command === "plan") {
    return legacyPlanLifecycleCommand(() => planCommand(arguments_.slice(1)));
  }
  if (command === "clean-plans") {
    return legacyPlanLifecycleCommand(() => cleanPlansCommand(arguments_.slice(1)));
  }
  if (command === "assert-clean" || command === "assert-adoptable") {
    return legacyPlanLifecycleCommand(() => assessmentCommand(
      arguments_.slice(1),
      command,
    ));
  }
  if (command === "apply") {
    return legacyPlanLifecycleCommand(() => applyCommand(arguments_.slice(1)));
  }
  if (command === "fetch") return fetchCommand(arguments_.slice(1), performance);
  if (command === "fetch-diag") return fetchDiag(arguments_.slice(1));
  if (command !== undefined && AUTHORING_COMMANDS.has(command)) {
    if (arguments_[1] === "-h" || arguments_[1] === "--help") {
      process.stdout.write(`${USAGE}\n`);
      return 0;
    }
    try {
      return await runAuthoringCommand({
        arguments: arguments_.slice(1),
        command,
        repositoryRoot: await packageRoot(),
      });
    } catch (error: unknown) {
      if (error instanceof AuthoringCliUsageError) {
        throw new CliExit(error.message, 2);
      }
      throw error;
    }
  }
  if (command === "-h" || command === "--help") {
    process.stdout.write(`${USAGE}\n`);
    return 0;
  }
  usageError(command === undefined ? USAGE : `unknown command ${command}\n${USAGE}`);
}

const PERFORMANCE_COMMANDS = new Set([
  "adopt",
  "apply",
  "assert-adoptable",
  "assert-clean",
  "fetch",
  "gen-env",
  "modules",
  "plan",
  "stage-imports",
  "transform",
  "unstage-imports",
]);

function performanceCommand(arguments_: readonly string[]): string {
  const command = arguments_[0];
  if (command === undefined || !PERFORMANCE_COMMANDS.has(command)) return "unknown";
  return command === "modules" && arguments_[1] === "generate"
    ? "modules.generate"
    : command;
}

function performancePhase(command: string): string {
  if (command === "modules.generate") return "module_generation.total";
  if (command === "gen-env") return "root_generation.total";
  if (command === "stage-imports" || command === "unstage-imports") return "staging.total";
  if (command === "plan") return "deployment_plan.total";
  if (command === "assert-clean" || command === "assert-adoptable") return "assessment.total";
  if (command === "apply") return "exact_plan_apply.total";
  return `${command}.command`;
}

const cliArguments = process.argv.slice(2);
const performancePath = (process.env.INFRAWRIGHT_PERFORMANCE_REPORT ?? "").trim();
const performance = performancePath === "" ? undefined : new PerformanceRecorder();
const command = performanceCommand(cliArguments);
const commandStarted = performance?.now() ?? 0;
let commandResult: number | undefined;
let primary: unknown;
try {
  commandResult = await main(cliArguments, performance);
} catch (error: unknown) {
  primary = error;
}

if (performance !== undefined) {
  const status: PerformanceStatus = primary === undefined && commandResult === 0
    ? "success"
    : "failed";
  const commandDurationMs = performance.durationSince(commandStarted);
  performance.recordSpan({
    durationMs: commandDurationMs,
    phase: performancePhase(command),
    status,
  });
  try {
    await writePerformanceReport({
      path: performancePath,
      report: performance.report({
        command,
        commandDurationMs,
        commandStatus: status,
      }),
    });
  } catch (error: unknown) {
    if (primary === undefined && commandResult === 0) primary = error;
    else process.stderr.write("WARNING: unable to write performance report after command failure\n");
  }
}

if (primary === undefined) {
  process.exitCode = commandResult ?? 1;
} else {
  const error = primary;
  const message = error instanceof Error ? error.message : String(error);
  if (error instanceof CliExit) {
    const stream = error.stdout ? process.stdout : process.stderr;
    stream.write(`${error.stdout ? "" : "error: "}${message}\n`);
    process.exitCode = error.status;
  } else {
    process.stderr.write(`error: ${message}\n`);
    process.exitCode = 1;
  }
}
