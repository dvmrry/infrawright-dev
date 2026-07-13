import { access } from "node:fs/promises";
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
import { validatePackResources } from "../metadata/resources.js";

const USAGE = [
  "usage:",
  "  infrawright check-pack [--pack <name>|PACK=<name>] [--root <packs>]",
  "  infrawright check-pack-set [--profile <file>] [--catalog <file>] [--requirements <file>] [--root <packs>]",
  "  infrawright deployment [--deployment <file>] <overlay|tfvars-format|module-dir|tenant-root|config-dir|imports-dir|envs-dir> [tenant]",
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

async function main(arguments_: string[]): Promise<number> {
  const command = arguments_[0];
  if (command === "check-pack") return checkPack(arguments_.slice(1));
  if (command === "check-pack-set") return checkPackSet(arguments_.slice(1));
  if (command === "deployment") return deployment(arguments_.slice(1));
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
