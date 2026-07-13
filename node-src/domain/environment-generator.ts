import { lstat, mkdir, readFile, unlink, writeFile } from "node:fs/promises";
import path from "node:path";

import { parseDataJsonLosslessly } from "../json/control.js";
import { sortedStrings } from "../json/python-compatible.js";
import type { HclFormatter } from "../modules/generator.js";
import type { LoadedPackRoot, LoadedResourceMetadata } from "../metadata/loader.js";
import {
  deploymentConfigDir,
  deploymentEnvsDir,
  deploymentModuleDir,
  deploymentTfvarsFormat,
} from "./deployment.js";
import {
  applyExpressionBindings,
  expressionModuleTargets,
  loadExpressionBindings,
  mergeExpressionBindingLayers,
  renderExpressionBindingsHcl,
  type ExpressionBinding,
} from "./expression-bindings.js";
import { loadedRootTopology, validateTenant } from "./roots.js";
import { transformArtifactPaths } from "./transform-artifacts.js";
import type { Deployment, RootTopology } from "./types.js";

const EXPRESSION_BINDINGS_TF = "expression_bindings.tf";
const STALE_DISABLED = "stale generated bindings ignored (bind_references disabled); rerun make transform to remove %s";
const STALE_NONMEMBER = "stale generated binding ignored (target %s not in root members); rerun make transform to remove %s";
const CYCLE_REMEDY = "resolve one direction via a literal ID or operator expression, or disable bind_references";

type JsonRecord = Readonly<Record<string, unknown>>;

export interface EnvironmentGenerationResult {
  readonly roots: readonly {
    readonly label: string;
    readonly members: readonly string[];
    readonly path: string;
  }[];
  readonly backend: string | null;
}

function record(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

async function exists(file: string): Promise<boolean> {
  try {
    await lstat(file);
    return true;
  } catch (error: unknown) {
    if (typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

async function removeIfPresent(file: string): Promise<boolean> {
  try {
    await unlink(file);
    return true;
  } catch (error: unknown) {
    if (typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

function resource(root: LoadedPackRoot, resourceType: string): LoadedResourceMetadata {
  const selected = root.resources.get(resourceType);
  if (selected === undefined) throw new TypeError(`unknown active resource ${resourceType}`);
  return selected;
}

function providerOf(root: LoadedPackRoot, resourceType: string): string {
  return resource(root, resourceType).provider;
}

function variableName(topology: RootTopology, resourceType: string): string {
  return topology.resource_roots[resourceType] === resourceType
    ? "items"
    : `${resourceType}_items`;
}

function tenantEnvironmentDirectory(
  deployment: Deployment,
  tenant: string,
  outputRoot?: string,
): string {
  return outputRoot === undefined
    ? deploymentEnvsDir(deployment, tenant)
    : path.join(outputRoot, tenant);
}

function environmentRootDirectory(
  deployment: Deployment,
  tenant: string,
  label: string,
  outputRoot?: string,
): string {
  return path.join(tenantEnvironmentDirectory(deployment, tenant, outputRoot), label);
}

function moduleSource(
  deployment: Deployment,
  resourceType: string,
  environmentDirectory: string,
): string {
  let source = path.relative(
    environmentDirectory,
    path.join(deploymentModuleDir(deployment), resourceType),
  );
  if (!source.startsWith("../") && !source.startsWith("./") && !path.isAbsolute(source)) {
    source = `./${source}`;
  }
  return source;
}

function expressionLocal(label: string, resourceType: string): string {
  return label === resourceType
    ? "infrawright_expression_bound_items"
    : `infrawright_${resourceType}_expression_bound_items`;
}

/** Render one complete deployment-selected root without touching state. */
export function renderEnvironmentMain(options: {
  readonly backend?: string;
  readonly deployment: Deployment;
  readonly environmentDirectory: string;
  readonly expressionBindingTypes?: readonly string[];
  readonly label: string;
  readonly members: readonly string[];
  readonly root: LoadedPackRoot;
  readonly tenant: string;
  readonly topology: RootTopology;
}): string {
  const members = sortedStrings(options.members);
  if (members.length === 0) throw new TypeError(`env root ${options.label} must contain at least one resource type`);
  const providers = sortedStrings(new Set(members.map((member) => providerOf(options.root, member))));
  if (providers.length !== 1) {
    throw new TypeError(`env root ${options.label} spans providers: ${providers.join(", ")}`);
  }
  const provider = providers[0] ?? "";
  const providerSource = options.root.packs.providerSources[provider];
  if (providerSource === undefined) throw new TypeError(`no provider source declared for ${provider}`);
  const backendLines = options.backend === undefined || options.backend.length === 0
    ? `  # local state — opt into remote state with\n  # make gen-env TENANT=${options.tenant} BACKEND=azurerm\n`
    : `  backend "${options.backend}" {\n`
      + "    # Partial configuration. Storage details come from a\n"
      + "    # work-side file at init: make plan BACKEND_CONFIG=<file>\n"
      + "    # (copy backend.conf.example). The state key is derived\n"
      + `    # per root: ${options.tenant}/${options.label}.tfstate\n`
      + "  }\n";
  const bound = new Set(options.expressionBindingTypes ?? []);
  const memberBlocks = members.map((resourceType) => {
    const name = variableName(options.topology, resourceType);
    const items = bound.has(resourceType)
      ? `local.${expressionLocal(options.label, resourceType)}`
      : `var.${name}`;
    return `variable "${name}" {\n`
      + "  # opaque at the root; the module enforces the strict type.\n"
      + "  type = any\n"
      + "}\n\n"
      + `module "${resourceType}" {\n`
      + `  source = "${moduleSource(options.deployment, resourceType, options.environmentDirectory)}"\n`
      + `  items = ${items}\n`
      + "}";
  });
  return `# GENERATED by engine.gen_env for tenant '${options.tenant}' — do not edit.\n`
    + `# Regenerate: make gen-env TENANT=${options.tenant}\n\n`
    + "terraform {\n"
    + '  required_version = ">= 1.5"\n'
    + "  required_providers {\n"
    + `    ${provider} = {\n`
    + `      source = "${providerSource}"\n`
    + "    }\n"
    + "  }\n"
    + backendLines
    + "}\n\n"
    + `provider "${provider}" {\n`
    + "  # credentials via provider environment variables\n"
    + `}\n\n${memberBlocks.join("\n\n")}\n`;
}

export function renderEnvironmentExpressionBindings(
  bindings: readonly ExpressionBinding[],
  options: { readonly label: string; readonly resourceType: string; readonly topology: RootTopology },
): string {
  return renderExpressionBindingsHcl(bindings, {
    itemsVariable: variableName(options.topology, options.resourceType),
    localName: expressionLocal(options.label, options.resourceType),
  });
}

function renderRootExpressionBindings(
  label: string,
  bindingsByType: ReadonlyMap<string, readonly ExpressionBinding[]>,
  topology: RootTopology,
): string {
  const sections: string[] = [];
  for (const resourceType of sortedStrings(bindingsByType.keys())) {
    const bindings = bindingsByType.get(resourceType) ?? [];
    const rendered = renderEnvironmentExpressionBindings(bindings, { label, resourceType, topology });
    if (rendered.length > 0) sections.push(rendered.trimEnd());
  }
  return sections.length === 0 ? "" : `${sections.join("\n\n")}\n`;
}

function configFile(
  deployment: Deployment,
  tenant: string,
  resourceType: string,
): string {
  return transformArtifactPaths({ deployment, resourceType, tenant }).config;
}

function configReference(
  deployment: Deployment,
  tenant: string,
  resourceType: string,
  environmentDirectory: string,
): string {
  return path.relative(
    environmentDirectory,
    configFile(deployment, tenant, resourceType),
  );
}

function operatorBindingsFile(deployment: Deployment, tenant: string, resourceType: string): string {
  return path.join(deploymentConfigDir(deployment, tenant), `${resourceType}.expressions.json`);
}

function generatedBindingsFile(deployment: Deployment, tenant: string, resourceType: string): string {
  return transformArtifactPaths({ deployment, resourceType, tenant }).generatedBindings;
}

async function validateBindingsAgainstConfig(options: {
  readonly bindings: readonly ExpressionBinding[];
  readonly config: string;
  readonly onDiagnostic: (message: string) => void;
  readonly variableName: string;
}): Promise<void> {
  if (!(await exists(options.config))) {
    throw new TypeError(`expression bindings require projected config at ${options.config}`);
  }
  if (!options.config.endsWith(".json")) {
    options.onDiagnostic(`skip expression binding validation for ${options.config} (hcl tfvars; validation reads json only)`);
    return;
  }
  const data = parseDataJsonLosslessly(await readFile(options.config, "utf8"));
  const items = record(data) ? data[options.variableName] : undefined;
  if (!record(items)) throw new TypeError(`${options.config} must contain a ${options.variableName} object`);
  applyExpressionBindings(items, options.bindings);
}

function filterGeneratedBindings(options: {
  readonly bindings: readonly ExpressionBinding[];
  readonly members: ReadonlySet<string>;
  readonly onDiagnostic: (message: string) => void;
  readonly path: string;
}): readonly ExpressionBinding[] {
  const kept: ExpressionBinding[] = [];
  for (const binding of options.bindings) {
    const nonmembers = expressionModuleTargets(binding.expression)
      .filter((target) => !options.members.has(target));
    if (nonmembers.length > 0) {
      options.onDiagnostic(`NOTE bindings: ${STALE_NONMEMBER.replace("%s", nonmembers.join(", ")).replace("%s", options.path)}`);
    } else {
      kept.push(binding);
    }
  }
  return kept;
}

async function loadBindingLayers(options: {
  readonly deployment: Deployment;
  readonly members: readonly string[];
  readonly onDiagnostic: (message: string) => void;
  readonly resource: LoadedResourceMetadata;
  readonly tenant: string;
}): Promise<readonly ExpressionBinding[]> {
  const layers: Array<readonly ExpressionBinding[]> = [];
  const generated = generatedBindingsFile(options.deployment, options.tenant, options.resource.type);
  if (await exists(generated)) {
    const enabled = options.deployment.roots[options.resource.provider]?.bind_references === true;
    if (enabled) {
      const filtered = filterGeneratedBindings({
        bindings: await loadExpressionBindings(generated, options.resource.type),
        members: new Set(options.members),
        onDiagnostic: options.onDiagnostic,
        path: generated,
      });
      if (filtered.length > 0) layers.push(filtered);
    } else {
      options.onDiagnostic(`NOTE bindings: ${STALE_DISABLED.replace("%s", generated)}`);
    }
  }
  const operator = operatorBindingsFile(options.deployment, options.tenant, options.resource.type);
  if (await exists(operator)) layers.push(await loadExpressionBindings(operator, options.resource.type));
  return mergeExpressionBindingLayers(layers);
}

function cyclePath(
  edges: ReadonlyMap<string, ReadonlySet<string>>,
  members: ReadonlySet<string>,
): readonly string[] | null {
  const states = new Map<string, "visiting" | "done">();
  const stack: string[] = [];
  const visit = (node: string): readonly string[] | null => {
    states.set(node, "visiting");
    stack.push(node);
    for (const target of sortedStrings(edges.get(node) ?? [])) {
      if (!members.has(target)) continue;
      if (states.get(target) === "visiting") {
        const start = stack.indexOf(target);
        return [...stack.slice(start), target];
      }
      if (states.get(target) === undefined) {
        const found = visit(target);
        if (found !== null) return found;
      }
    }
    stack.pop();
    states.set(node, "done");
    return null;
  };
  for (const member of sortedStrings(members)) {
    if (states.get(member) !== undefined) continue;
    const found = visit(member);
    if (found !== null) return found;
  }
  return null;
}

export function assertNoExpressionBindingCycles(options: {
  readonly bindingsByType: ReadonlyMap<string, readonly ExpressionBinding[]>;
  readonly label: string;
  readonly members: readonly string[];
}): void {
  const members = new Set(options.members);
  const edges = new Map<string, Set<string>>();
  for (const resourceType of sortedStrings(options.bindingsByType.keys())) {
    for (const binding of options.bindingsByType.get(resourceType) ?? []) {
      for (const target of expressionModuleTargets(binding.expression)) {
        if (members.has(target)) {
          const selected = edges.get(resourceType) ?? new Set<string>();
          selected.add(target);
          edges.set(resourceType, selected);
        }
      }
    }
  }
  const cycle = cyclePath(edges, members);
  if (cycle !== null) {
    throw new TypeError(
      `expression binding cycle detected in root ${options.label}: ${cycle.join(" -> ")}; ${CYCLE_REMEDY}`,
    );
  }
}

export function renderEnvironmentReadme(options: {
  readonly deployment: Deployment;
  readonly environmentDirectory: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly tenant: string;
  readonly topology: RootTopology;
}): string {
  const members = sortedStrings(options.members);
  if (members.length === 1 && options.topology.resource_roots[members[0] ?? ""] === members[0]) {
    const resourceType = members[0] ?? "";
    const config = configReference(
      options.deployment,
      options.tenant,
      resourceType,
      options.environmentDirectory,
    );
    return `# ${options.tenant} / ${resourceType} (generated env root)\n\n`
      + `Isolated Terraform root for \`${resourceType}\` on tenant \`${options.tenant}\`. GENERATED — do not\n`
      + `edit (AGENTS.md rule 6); regenerate with \`make gen-env TENANT=${options.tenant}\`.\n`
      + "Config is loaded at plan time from the tenant's config dir, relative to\n"
      + `this root: \`${config}\`.\n`;
  }
  const references = members.map((resourceType) => {
    return `${variableName(options.topology, resourceType)}=${configReference(
      options.deployment,
      options.tenant,
      resourceType,
      options.environmentDirectory,
    )}`;
  });
  return `# ${options.tenant} / ${options.label} (generated env root)\n\n`
    + `Grouped Terraform root for \`${members.join(", ")}\` on tenant \`${options.tenant}\`. GENERATED — do not\n`
    + `edit (AGENTS.md rule 6); regenerate with \`make gen-env TENANT=${options.tenant}\`.\n`
    + "Config is loaded at plan time from the tenant's config dir, relative to\n"
    + `this root: \`${references.join("`, `")}\`.\n`;
}

export function renderEnvironmentSmokeTest(options: {
  readonly configFormat: "json" | "hcl";
  readonly deployment: Deployment;
  readonly environmentDirectory: string;
  readonly hasConfig: ReadonlyMap<string, boolean>;
  readonly label: string;
  readonly members: readonly string[];
  readonly root: LoadedPackRoot;
  readonly tenant: string;
  readonly topology: RootTopology;
}): string {
  const members = sortedStrings(options.members);
  if (members.length === 0) throw new TypeError(`env root ${options.label} must contain at least one resource type`);
  const providers = sortedStrings(new Set(members.map((member) => providerOf(options.root, member))));
  if (providers.length !== 1) {
    throw new TypeError(`env root ${options.label} spans providers: ${providers.join(", ")}`);
  }
  const provider = providers[0] ?? "";
  const lines = [
    "# GENERATED smoke test — the root composes and plans against a",
    `# mocked provider; no credentials. Regenerate: make gen-env TENANT=${options.tenant}`,
    `mock_provider "${provider}" {}`,
    "",
    'run "empty_plan" {',
    "  command = plan",
    "",
    "  variables {",
  ];
  for (const resourceType of members) {
    lines.push(`    ${variableName(options.topology, resourceType)} = {}`);
  }
  lines.push("  }", "}");
  if (options.configFormat === "json") {
    const configured = members.filter((resourceType) => options.hasConfig.get(resourceType) === true);
    if (configured.length > 0) {
      lines.push("", 'run "config_plan" {', "  command = plan", "", "  variables {");
      for (const resourceType of configured) {
        const name = variableName(options.topology, resourceType);
        const reference = configReference(
          options.deployment,
          options.tenant,
          resourceType,
          options.environmentDirectory,
        );
        lines.push(`    ${name} = jsondecode(file("${reference}")).${name}`);
      }
      lines.push("  }", "}");
    }
  }
  return `${lines.join("\n")}\n`;
}

/** Generate deterministic Terraform roots and their expression overlays. */
export async function generateEnvironmentRoots(options: {
  readonly backend?: string;
  readonly deployment: Deployment;
  readonly formatHcl: HclFormatter;
  readonly onDiagnostic?: (message: string) => void;
  readonly outputRoot?: string;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly tenant: string;
}): Promise<EnvironmentGenerationResult> {
  validateTenant(options.tenant);
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  const topology = loadedRootTopology({
    deployment: options.deployment,
    root: options.root,
    selectors: options.selectors,
    tenant: options.tenant,
  }).topology;
  const tenantDirectory = tenantEnvironmentDirectory(
    options.deployment,
    options.tenant,
    options.outputRoot,
  );
  const marker = path.join(tenantDirectory, ".backend");
  let backend = options.backend;
  if (backend === undefined && await exists(marker)) {
    backend = (await readFile(marker, "utf8")).trim() || undefined;
  }
  await mkdir(tenantDirectory, { recursive: true });
  if (backend !== undefined && backend.length > 0) await writeFile(marker, `${backend}\n`, "utf8");
  const generated: Array<{ readonly label: string; readonly members: readonly string[]; readonly path: string }> = [];
  for (const selectedRoot of topology.roots) {
    const members = sortedStrings(selectedRoot.members);
    const directory = environmentRootDirectory(
      options.deployment,
      options.tenant,
      selectedRoot.label,
      options.outputRoot,
    );
    await mkdir(directory, { recursive: true });
    const bindingsByType = new Map<string, readonly ExpressionBinding[]>();
    for (const resourceType of members) {
      const bindings = await loadBindingLayers({
        deployment: options.deployment,
        members,
        onDiagnostic,
        resource: resource(options.root, resourceType),
        tenant: options.tenant,
      });
      if (bindings.length === 0) continue;
      await validateBindingsAgainstConfig({
        bindings,
        config: configFile(options.deployment, options.tenant, resourceType),
        onDiagnostic,
        variableName: variableName(topology, resourceType),
      });
      bindingsByType.set(resourceType, bindings);
    }
    assertNoExpressionBindingCycles({
      bindingsByType,
      label: selectedRoot.label,
      members,
    });
    const main = await options.formatHcl(renderEnvironmentMain({
      ...(backend === undefined || backend.length === 0 ? {} : { backend }),
      deployment: options.deployment,
      environmentDirectory: directory,
      expressionBindingTypes: sortedStrings(bindingsByType.keys()),
      label: selectedRoot.label,
      members,
      root: options.root,
      tenant: options.tenant,
      topology,
    }));
    const mainPath = path.join(directory, "main.tf");
    await writeFile(mainPath, main, "utf8");
    onDiagnostic(`wrote ${mainPath}`);
    const expressionPath = path.join(directory, EXPRESSION_BINDINGS_TF);
    if (bindingsByType.size > 0) {
      await writeFile(
        expressionPath,
        await options.formatHcl(renderRootExpressionBindings(selectedRoot.label, bindingsByType, topology)),
        "utf8",
      );
      onDiagnostic(`wrote ${expressionPath}`);
    } else if (await removeIfPresent(expressionPath)) {
      onDiagnostic(`removed stale ${expressionPath}`);
    }
    await writeFile(path.join(directory, "README.md"), renderEnvironmentReadme({
      deployment: options.deployment,
      environmentDirectory: directory,
      label: selectedRoot.label,
      members,
      tenant: options.tenant,
      topology,
    }), "utf8");
    const testsDirectory = path.join(directory, "tests");
    await mkdir(testsDirectory, { recursive: true });
    const hasConfig = new Map<string, boolean>();
    for (const resourceType of members) {
      hasConfig.set(resourceType, await exists(configFile(options.deployment, options.tenant, resourceType)));
    }
    const smokePath = path.join(testsDirectory, "smoke.tftest.hcl");
    await writeFile(smokePath, await options.formatHcl(renderEnvironmentSmokeTest({
      configFormat: deploymentTfvarsFormat(options.deployment),
      deployment: options.deployment,
      environmentDirectory: directory,
      hasConfig,
      label: selectedRoot.label,
      members,
      root: options.root,
      tenant: options.tenant,
      topology,
    })), "utf8");
    onDiagnostic(`wrote ${smokePath}`);
    for (const resourceType of members) {
      if (hasConfig.get(resourceType) === true) continue;
      const file = configFile(options.deployment, options.tenant, resourceType);
      onDiagnostic(
        `NOTE ${resourceType}: no config at ${file} — smoke test is STUB-only (composes + plans an empty root; does NOT exercise config). Materialize the config and re-run gen-env to upgrade it.`,
      );
    }
    generated.push({ label: selectedRoot.label, members, path: directory });
  }
  return { roots: generated, backend: backend === undefined || backend.length === 0 ? null : backend };
}
