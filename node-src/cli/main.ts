import { access, realpath } from "node:fs/promises";
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
import { validateTenant } from "../domain/roots.js";
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

const USAGE = [
  "usage:",
  "  infrawright check-pack [--pack <name>|PACK=<name>] [--root <packs>]",
  "  infrawright check-pack-set [--profile <file>] [--catalog <file>] [--requirements <file>] [--root <packs>]",
  "  infrawright deployment [--deployment <file>] <overlay|tfvars-format|module-dir|tenant-root|config-dir|imports-dir|envs-dir> [tenant]",
  "  infrawright modules <generate|validate> [--resource <type>] [--out <dir>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>] [--terraform <path>]",
  "  infrawright transform --in <dir> --tenant <name> [--resource <selector>] [--deployment <file>] [--root <packs>] [--profile <file>] [--catalog <file>]",
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
): { readonly value: string; readonly next: number } {
  const value = arguments_[index + 1];
  if (value === undefined || value.length === 0) {
    return usageError(`${option} requires a value`);
  }
  return { value, next: index + 2 };
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
