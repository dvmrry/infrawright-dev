import { access, readFile, realpath } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

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
import { fetchResources, selectFetchResources } from "../collectors/rest.js";
import { fetchProducts } from "../collectors/selection.js";
import { probeRestHost } from "../collectors/rest-diagnostics.js";
import {
  collectorAuthMode,
  collectorContext,
  createZscalerCollectorAdapters,
  diagnosticHosts,
  fetchDebugLines,
  maskCollectorIdentifiers,
} from "../collectors/zscaler-adapters.js";
import { createRestHttpTransport } from "../io/rest-http-transport.js";
import {
  defaultAdoptionStateLoader,
  loadAdoptionPolicy,
  runAdoptBatch,
  type AdoptionStateLoader,
} from "../domain/adopt-runner.js";
import { resolveTerraformExecutable } from "../io/terraform-command.js";
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
  "  infrawright fetch --tenant <name> [--resource <selector>] [--out <dir>] [--root <packs>] [--profile <file>] [--catalog <file>]",
  "  infrawright fetch-diag [--root <packs>] [--profile <file>] [--catalog <file>]",
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

function takeOption(
  arguments_: string[],
  index: number,
  option: string,
  allowEmpty = false,
): { readonly value: string; readonly next: number } {
  const value = arguments_[index + 1];
  if (value === undefined || (!allowEmpty && value.length === 0)) {
    return usageError(`${option} requires a value`);
  }
  return { value, next: index + 2 };
}

async function readStandardInput(): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk)));
  }
  return Buffer.concat(chunks).toString("utf8");
}

async function checkPack(arguments_: string[]): Promise<number> {
  let selectedPack: string | undefined;
  let selectedRoot: string | undefined;
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--pack") {
      const option = takeOption(arguments_, index, "--pack");
      selectedPack = option.value;
      index = option.next;
    } else if (argument === "--root") {
      const option = takeOption(arguments_, index, "--root");
      selectedRoot = option.value;
      index = option.next;
    } else if (argument?.startsWith("PACK=")) {
      selectedPack = argument.slice("PACK=".length);
      if (selectedPack.length === 0) usageError("PACK= requires a value");
      index += 1;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 2);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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
  let requirements: string | undefined;
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--requirements"
    ) {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--requirements") requirements = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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
  let selectedPath: string | undefined;
  const positional: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--deployment") {
      const option = takeOption(arguments_, index, "--deployment");
      selectedPath = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 2);
    } else {
      if (argument === undefined) usageError("missing deployment argument");
      positional.push(argument);
      index += 1;
    }
  }
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
  const verb = arguments_[0];
  if (verb !== "generate" && verb !== "validate") {
    usageError("modules requires the generate or validate verb");
  }
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  let selectedDeployment = deploymentPath();
  let output: string | undefined;
  let terraform: string | undefined;
  const resources: string[] = [];
  for (let index = 1; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--out"
      || argument === "--terraform"
      || argument === "--resource"
    ) {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--out") output = option.value;
      if (argument === "--terraform") terraform = option.value;
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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
  const root = await loadPackRoot({
    packsRoot: options.root,
    profilePath: options.profile,
    catalogPath: options.catalog,
  });
  const outputRoot = options.output
    ?? deploymentModuleDir(await loadDeployment(options.deployment));
  const active = activeGeneratedResourceTypes(root);
  const activeSet = new Set(active);
  for (const resourceType of options.resources) {
    if (!activeSet.has(resourceType)) {
      throw new Error(`unknown active generated resource type ${JSON.stringify(resourceType)}`);
    }
  }
  const resources = options.resources.length === 0
    ? active
    : options.resources;
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
  let selectedDeployment = deploymentPath();
  let input: string | undefined;
  let tenant: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--in"
      || argument === "--tenant"
      || argument === "--resource"
    ) {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--in") input = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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

async function adopt(arguments_: string[]): Promise<number> {
  const rootDirectory = await packageRoot();
  let root = process.env.INFRAWRIGHT_PACKS || path.join(rootDirectory, "packs");
  let profile = process.env.INFRAWRIGHT_PACK_PROFILE
    || path.join(rootDirectory, "packsets", "full.json");
  let catalog = path.join(rootDirectory, "packsets", "full.json");
  let selectedDeployment = deploymentPath();
  let input: string | undefined;
  let tenant: string | undefined;
  let policyPath: string | undefined;
  let terraform: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--in"
      || argument === "--tenant"
      || argument === "--resource"
      || argument === "--policy"
      || argument === "--terraform"
    ) {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--in") input = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      if (argument === "--policy") policyPath = option.value;
      if (argument === "--terraform") terraform = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
  if (input === undefined || tenant === undefined) usageError("adopt requires --in and --tenant");
  const [packRoot, loadedDeployment] = await Promise.all([
    loadPackRoot({ packsRoot: root, profilePath: profile, catalogPath: catalog }),
    loadDeployment(selectedDeployment),
  ]);
  const policy = await loadAdoptionPolicy({
    ...(policyPath === undefined ? {} : { path: policyPath }),
    root: packRoot,
  });
  let loadedState: AdoptionStateLoader | null = null;
  const stateLoader: AdoptionStateLoader = async (request) => {
    if (loadedState === null) {
      const executable = await resolveTerraformExecutable(
        terraform ?? process.env.TF,
        process.env,
      );
      loadedState = await defaultAdoptionStateLoader({
        environment: process.env,
        onDiagnostic: (message) => process.stderr.write(`${message}\n`),
        root: packRoot,
        terraformExecutable: executable,
      });
    }
    return loadedState(request);
  };
  const result = await runAdoptBatch({
    deployment: loadedDeployment,
    inputDirectory: input,
    onDiagnostic: (message) => process.stderr.write(`${message}\n`),
    policy,
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
  let selectedDeployment = deploymentPath();
  let backend: string | undefined;
  let tenant: string | undefined;
  let terraform: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--backend"
      || argument === "--tenant"
      || argument === "--terraform"
      || argument === "--resource"
    ) {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--backend") backend = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--terraform") terraform = option.value;
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let backendConfig: string | undefined;
  let tenant: string | undefined;
  let terraform: string | undefined;
  let stateAware = false;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--state-aware") {
      if (command === "unstage-imports") {
        usageError("unstage-imports does not accept --state-aware");
      }
      stateAware = true;
      index += 1;
    } else if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--tenant"
      || argument === "--resource"
      || argument === "--backend-config"
      || argument === "--terraform"
    ) {
      if (
        command === "unstage-imports"
        && (argument === "--backend-config" || argument === "--terraform")
      ) {
        usageError(`unstage-imports does not accept ${argument}`);
      }
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      if (argument === "--backend-config") backendConfig = option.value;
      if (argument === "--terraform") terraform = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let tenant: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--tenant"
      || argument === "--resource"
    ) {
      const option = takeOption(arguments_, index, argument, argument === "--tenant");
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--tenant") {
        if (tenant !== undefined) usageError("--tenant may be specified only once");
        tenant = option.value;
      }
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`${command} does not accept ${String(argument)}`);
    }
  }
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
  let order: "sorted" | "references" = "sorted";
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--order=references") {
      order = "references";
      index += 1;
    } else if (argument === "--root" || argument === "--profile" || argument === "--catalog" || argument === "--resource") {
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`resources does not accept ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let pathsJson: string | undefined;
  const paths: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--paths-json"
      || argument === "--path"
    ) {
      const option = takeOption(arguments_, index, argument, argument === "--path");
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--paths-json") {
        if (pathsJson !== undefined) usageError("--paths-json may be specified only once");
        pathsJson = option.value;
      }
      if (argument === "--path") paths.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`scope-paths does not accept ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let tenant: string | undefined;
  let backendConfig: string | undefined;
  let terraform: string | undefined;
  let importsOnly = false;
  let save = false;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (argument === "--imports-only" || argument === "--save") {
      if (argument === "--imports-only") importsOnly = true;
      else save = true;
      index += 1;
    } else if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--tenant"
      || argument === "--resource"
      || argument === "--backend-config"
      || argument === "--terraform"
    ) {
      const option = takeOption(arguments_, index, argument, argument === "--tenant");
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      if (argument === "--backend-config") backendConfig = option.value;
      if (argument === "--terraform") terraform = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`plan does not accept ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let tenant: string | undefined;
  let backendConfig: string | undefined;
  let policy: string | undefined;
  let report: string | undefined;
  let terraform: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--tenant"
      || argument === "--resource"
      || argument === "--backend-config"
      || argument === "--policy"
      || argument === "--report"
      || argument === "--terraform"
    ) {
      if (command === "assert-clean" && argument === "--policy") {
        usageError("assert-clean does not accept --policy");
      }
      const option = takeOption(arguments_, index, argument, argument === "--tenant");
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--tenant") {
        if (tenant !== undefined) usageError("--tenant may be specified only once");
        tenant = option.value;
      }
      if (argument === "--resource") resources.push(option.value);
      if (argument === "--backend-config") backendConfig = option.value;
      if (argument === "--policy") policy = option.value;
      if (argument === "--report") {
        if (report !== undefined) usageError("--report may be specified only once");
        report = option.value;
      }
      if (argument === "--terraform") terraform = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`${command} does not accept ${String(argument)}`);
    }
  }
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
  let selectedDeployment = deploymentPath();
  let tenant: string | undefined;
  let backendConfig: string | undefined;
  let policy: string | undefined;
  let terraform: string | undefined;
  let mainBranch: string | undefined;
  let allowDestroy = false;
  let allowNonMain = false;
  let allowPlanChanges = false;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--allow-destroy"
      || argument === "--allow-non-main"
      || argument === "--allow-plan-changes"
    ) {
      if (argument === "--allow-destroy") allowDestroy = true;
      if (argument === "--allow-non-main") allowNonMain = true;
      if (argument === "--allow-plan-changes") allowPlanChanges = true;
      index += 1;
    } else if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
      || argument === "--tenant"
      || argument === "--resource"
      || argument === "--backend-config"
      || argument === "--policy"
      || argument === "--terraform"
      || argument === "--main-branch"
    ) {
      const option = takeOption(arguments_, index, argument, argument === "--tenant");
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--deployment") selectedDeployment = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      if (argument === "--backend-config") backendConfig = option.value;
      if (argument === "--policy") policy = option.value;
      if (argument === "--terraform") terraform = option.value;
      if (argument === "--main-branch") mainBranch = option.value;
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`apply does not accept ${String(argument)}`);
    }
  }
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
  readonly output?: string;
  readonly profile: string;
  readonly resources: readonly string[];
  readonly root: string;
  readonly tenant?: string;
}

async function requireBuiltInCollectorAuthority(
  root: Awaited<ReturnType<typeof loadPackRoot>>,
  products: ReadonlySet<string>,
): Promise<void> {
  if (products.size === 0) return;
  const installed = await realpath(path.join(await packageRoot(), "packs"));
  const selected = await realpath(root.root);
  if (installed !== selected) {
    usageError(
      "the fetch CLI can use built-in product adapters only with the installed pack root; external collector roots require a library caller that supplies matching adapters",
    );
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
  let output: string | undefined;
  let tenant: string | undefined;
  const resources: string[] = [];
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--out"
      || argument === "--tenant"
      || argument === "--resource"
    ) {
      if (!requireTenant && (argument === "--out" || argument === "--tenant" || argument === "--resource")) {
        usageError(`fetch-diag does not accept ${argument}`);
      }
      const option = takeOption(arguments_, index, argument);
      if (argument === "--root") root = option.value;
      if (argument === "--profile") profile = option.value;
      if (argument === "--catalog") catalog = option.value;
      if (argument === "--out") output = option.value;
      if (argument === "--tenant") tenant = option.value;
      if (argument === "--resource") resources.push(option.value);
      index = option.next;
    } else if (argument === "-h" || argument === "--help") {
      throw new CliExit(USAGE, 0, true);
    } else {
      usageError(`unknown argument ${String(argument)}`);
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
    ...(output === undefined ? {} : { output }),
    profile,
    resources,
    root,
    ...(tenant === undefined ? {} : { tenant }),
  };
}

async function fetchCommand(arguments_: string[]): Promise<number> {
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
  await requireBuiltInCollectorAuthority(root, products);
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
  const transport = await createRestHttpTransport(process.env);
  let result;
  let primary: unknown;
  try {
    result = await fetchResources({
      adapters: createZscalerCollectorAdapters(),
      context,
      environment: process.env,
      mode,
      onDiagnostic: (message) => process.stderr.write(`${message}\n`),
      outputDirectory: options.output ?? path.join("pulls", tenant),
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
  const products = new Set(fetchProducts(root));
  await requireBuiltInCollectorAuthority(root, products);
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

async function main(arguments_: string[]): Promise<number> {
  const command = arguments_[0];
  if (command === "check-pack") return checkPack(arguments_.slice(1));
  if (command === "check-pack-set") return checkPackSet(arguments_.slice(1));
  if (command === "deployment") return deployment(arguments_.slice(1));
  if (command === "modules") return modules(arguments_.slice(1));
  if (command === "transform") return transform(arguments_.slice(1));
  if (command === "adopt") return adopt(arguments_.slice(1));
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
  if (command === "fetch") return fetchCommand(arguments_.slice(1));
  if (command === "fetch-diag") return fetchDiag(arguments_.slice(1));
  if (command === "-h" || command === "--help") {
    process.stdout.write(`${USAGE}\n`);
    return 0;
  }
  usageError(command === undefined ? USAGE : `unknown command ${command}\n${USAGE}`);
}

try {
  process.exitCode = await main(process.argv.slice(2));
} catch (error: unknown) {
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
