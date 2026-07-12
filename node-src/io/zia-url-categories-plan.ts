import {
  lstat,
  readFile,
  rm,
} from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import {
  parseGeneratedImports,
  renderGeneratedImports,
  renderHclQuotedString,
  type GeneratedImportPair,
} from "../domain/import-moves.js";
import {
  captureInitSourcesPayload,
  initSourcesSha256,
  planFingerprintV2,
} from "../domain/plan-fingerprint.js";
import {
  runSavedPlanAssessment,
  type SavedPlanAssessmentCore,
} from "../domain/plan-assessment.js";
import {
  ZIA_PROVIDER_SOURCE,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
} from "../domain/zia-url-categories.js";
import {
  runZiaUrlCategoryArtifactWorkflow,
  type ZiaUrlCategoryArtifactWorkflowDependencies,
  type ZiaUrlCategoryArtifactWorkflowResult,
} from "./zia-url-categories-artifacts.js";
import {
  writeZiaUrlCategoriesModule,
  type ZiaUrlCategoriesModulePaths,
} from "./zia-url-categories-module.js";
import { ziaUrlCategoryTerraformEnvironment } from "./zia-url-categories-oracle.js";
import { runTerraformCommand } from "./terraform-command.js";
import {
  assertZiaUrlCategoriesWorkspace,
  prepareZiaUrlCategoriesWorkspace,
  publishZiaUrlCategoryPlan,
  recheckZiaUrlCategoriesWorkspaceAuthorities,
  writeZiaUrlCategoryPrivateFile,
  type ZiaUrlCategoriesWorkspace,
} from "./zia-url-categories-workspace.js";

const COMMAND_LIMITS = Object.freeze({
  maxStderrBytes: 16 * 1024 * 1024,
  maxStdoutBytes: 8 * 1024 * 1024,
  timeoutMs: 10 * 60 * 1_000,
});

export interface ZiaUrlCategoryPlanPaths {
  readonly assessment: string;
  readonly envDir: string;
  readonly fingerprint: string;
  readonly pendingPlan: string;
  readonly plan: string;
  readonly stagedImports: string;
}

export interface ZiaUrlCategoryPlanWorkflowResult {
  readonly artifacts: ZiaUrlCategoryArtifactWorkflowResult;
  readonly assessment: SavedPlanAssessmentCore;
  readonly module: ZiaUrlCategoriesModulePaths;
  readonly paths: ZiaUrlCategoryPlanPaths;
  readonly staged: {
    readonly alreadyManaged: number;
    readonly imports: number;
  };
}

export interface ZiaUrlCategoryPlanWorkflowDependencies
  extends ZiaUrlCategoryArtifactWorkflowDependencies {
  /** Trusted test seams for proving mutation rejection. */
  readonly afterInit?: (paths: ZiaUrlCategoryPlanPaths) => void | Promise<void>;
  readonly afterPlan?: (paths: ZiaUrlCategoryPlanPaths) => void | Promise<void>;
  readonly beforeAssessment?: (paths: ZiaUrlCategoryPlanPaths) => void | Promise<void>;
}

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function pathsFor(layout: ZiaUrlCategoriesWorkspace): ZiaUrlCategoryPlanPaths {
  const envDir = layout.envDir;
  return Object.freeze({
    assessment: path.join(envDir, "tfplan.assessment.json"),
    envDir,
    fingerprint: path.join(envDir, "tfplan.sources"),
    pendingPlan: path.join(layout.planStagingDir, "tfplan.pending"),
    plan: path.join(envDir, "tfplan"),
    stagedImports: path.join(envDir, `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}_imports.tf`),
  });
}

async function removePlanOutputs(
  paths: ZiaUrlCategoryPlanPaths,
  layout: ZiaUrlCategoriesWorkspace,
): Promise<void> {
  await recheckZiaUrlCategoriesWorkspaceAuthorities(layout);
  const outcomes = await Promise.allSettled([
    rm(paths.assessment, { force: true }),
    rm(paths.fingerprint, { force: true }),
    rm(paths.pendingPlan, { force: true }),
    rm(paths.plan, { force: true }),
    rm(paths.stagedImports, { force: true }),
  ]);
  if (outcomes.some((outcome) => outcome.status === "rejected")) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_CLEANUP_FAILED",
      "prior ZIA URL-category plan outputs could not be removed",
      "io",
    );
  }
  await recheckZiaUrlCategoriesWorkspaceAuthorities(layout);
}

async function rejectAfterCleanup(
  paths: ZiaUrlCategoryPlanPaths,
  layout: ZiaUrlCategoriesWorkspace,
  primary: unknown,
): Promise<never> {
  try {
    await removePlanOutputs(paths, layout);
  } catch (cleanup: unknown) {
    if (primary instanceof ProcessFailure && cleanup instanceof ProcessFailure) {
      throw new ProcessFailure({
        code: primary.code,
        category: primary.category,
        message: primary.message,
        retryable: primary.retryable,
        details: [
          ...primary.details,
          {
            path: "cleanup",
            code: cleanup.code,
            message: "rejected ZIA URL-category plan cleanup also failed",
          },
        ],
      });
    }
  }
  throw primary;
}

async function command(options: {
  readonly argv: readonly string[];
  readonly cwd: string;
  readonly environment: Readonly<Record<string, string>>;
  readonly output: "capture" | "discard";
  readonly terraformExecutable: string;
}): Promise<Buffer | null> {
  if (options.output === "capture") {
    const result = await runTerraformCommand({
      ...options,
      limits: COMMAND_LIMITS,
      output: "capture",
    });
    return result.stdout;
  }
  await runTerraformCommand({
    ...options,
    limits: COMMAND_LIMITS,
    output: "discard",
  });
  return null;
}

async function hasLocalState(envDir: string): Promise<boolean> {
  const statePath = path.join(envDir, "terraform.tfstate");
  try {
    const metadata = await lstat(statePath);
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_LOCAL_STATE",
        "ZIA URL-category local state must be a regular file",
        "io",
      );
    }
    return true;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) {
      return false;
    }
    return fail(
      "ZIA_URL_CATEGORY_LOCAL_STATE_READ_FAILED",
      "ZIA URL-category local state could not be inspected",
      "io",
    );
  }
}

function stateAddresses(stdout: Buffer): ReadonlySet<string> {
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(stdout);
  } catch {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_STATE_LIST",
      "Terraform state list did not emit valid UTF-8",
    );
  }
  const addresses = new Set<string>();
  for (const line of text.split(/\r?\n/)) {
    if (line === "") continue;
    if (line.trim() !== line || line.includes("\0") || !line.isWellFormed()) {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_STATE_LIST",
        "Terraform state list emitted an invalid address",
      );
    }
    addresses.add(line);
  }
  return addresses;
}

function managedAddress(pair: GeneratedImportPair): string {
  return `module.${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.this[${
    renderHclQuotedString(pair.key)
  }]`;
}

async function stageImports(options: {
  readonly source: string;
  readonly destination: string;
  readonly managed: ReadonlySet<string>;
}): Promise<{
  readonly all: readonly GeneratedImportPair[];
  readonly alreadyManaged: number;
  readonly imports: number;
  readonly staged: readonly GeneratedImportPair[];
}> {
  let pairs: readonly GeneratedImportPair[];
  try {
    pairs = parseGeneratedImports(
      ZIA_URL_CATEGORIES_RESOURCE_TYPE,
      await readFile(options.source, "utf8"),
    );
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "ZIA_URL_CATEGORY_IMPORT_READ_FAILED",
      "generated ZIA URL-category imports could not be read",
      "io",
    );
  }
  const delta = pairs.filter((pair) => !options.managed.has(managedAddress(pair)));
  try {
    if (delta.length === 0) {
      await rm(options.destination, { force: true });
    } else {
      await writeZiaUrlCategoryPrivateFile(
        options.destination,
        renderGeneratedImports(ZIA_URL_CATEGORIES_RESOURCE_TYPE, delta),
      );
    }
  } catch {
    return fail(
      "ZIA_URL_CATEGORY_IMPORT_STAGE_FAILED",
      "state-aware ZIA URL-category imports could not be staged",
      "io",
    );
  }
  return Object.freeze({
    all: Object.freeze([...pairs]),
    alreadyManaged: pairs.length - delta.length,
    imports: delta.length,
    staged: Object.freeze([...delta]),
  });
}

async function writeFingerprint(
  target: string,
  value: Awaited<ReturnType<typeof planFingerprintV2>>,
): Promise<void> {
  try {
    await writeZiaUrlCategoryPrivateFile(target, `${JSON.stringify(value)}\n`);
  } catch {
    return fail(
      "ZIA_URL_CATEGORY_FINGERPRINT_WRITE_FAILED",
      "ZIA URL-category plan fingerprint could not be written",
      "io",
    );
  }
}

async function writeAssessment(
  target: string,
  value: SavedPlanAssessmentCore,
): Promise<void> {
  try {
    await writeZiaUrlCategoryPrivateFile(
      target,
      `${JSON.stringify(value, null, 2)}\n`,
    );
  } catch {
    return fail(
      "ZIA_URL_CATEGORY_ASSESSMENT_WRITE_FAILED",
      "ZIA URL-category assessment result could not be written",
      "io",
    );
  }
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function planRecords(
  plan: Readonly<Record<string, unknown>>,
  field: "resource_changes" | "resource_drift",
): readonly unknown[] {
  const value = plan[field];
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
      "ZIA URL-category plan contains an invalid change collection",
    );
  }
  return value;
}

function exactImportMarker(value: unknown, importId: string): boolean {
  return isRecord(value)
    && Object.keys(value).length === 1
    && value.id === importId;
}

function requireExactZiaUrlCategoryPlan(options: {
  readonly all: readonly GeneratedImportPair[];
  readonly plan: unknown;
  readonly staged: readonly GeneratedImportPair[];
}): void {
  if (!isRecord(options.plan)) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
      "ZIA URL-category assessment did not receive a plan object",
    );
  }
  const outputChanges = options.plan.output_changes;
  if (
    outputChanges !== undefined
    && outputChanges !== null
    && (!isRecord(outputChanges) || Object.keys(outputChanges).length !== 0)
  ) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
      "ZIA URL-category plan contains output changes",
    );
  }
  if (planRecords(options.plan, "resource_drift").length !== 0) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
      "ZIA URL-category plan contains resource drift",
    );
  }
  const expected = new Map(options.all.map((pair) => [managedAddress(pair), pair.importId]));
  const staged = new Map(options.staged.map((pair) => [managedAddress(pair), pair.importId]));
  const seen = new Set<string>();
  const imported = new Set<string>();
  for (const candidate of planRecords(options.plan, "resource_changes")) {
    if (!isRecord(candidate) || !isRecord(candidate.change)) {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
        "ZIA URL-category plan contains an invalid resource change",
      );
    }
    const address = candidate.address;
    if (
      typeof address !== "string"
      || seen.has(address)
      || !expected.has(address)
      || candidate.mode !== "managed"
      || candidate.type !== ZIA_URL_CATEGORIES_RESOURCE_TYPE
      || candidate.provider_name !== `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`
      || Object.hasOwn(candidate, "previous_address")
      || !Array.isArray(candidate.change.actions)
      || candidate.change.actions.length !== 1
      || candidate.change.actions[0] !== "no-op"
    ) {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
        "ZIA URL-category plan contains a foreign or non-import change",
      );
    }
    seen.add(address);
    const stagedId = staged.get(address);
    const marker = candidate.change.importing;
    if (stagedId === undefined) {
      if (marker !== undefined && marker !== null) {
        return fail(
          "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
          "ZIA URL-category plan imports an address outside the staged delta",
        );
      }
    } else if (!exactImportMarker(marker, stagedId)) {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
        "ZIA URL-category plan import does not match the staged import ID",
      );
    } else {
      imported.add(address);
    }
  }
  if (imported.size !== staged.size) {
    return fail(
      "ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED",
      "ZIA URL-category plan did not exactly cover the staged import delta",
    );
  }
}

/**
 * Continue the private PR-1 ZIA workflow through a real saved plan and the
 * existing Node assessment kernel. This deliberately remains one-resource
 * product code, not a public operation or generic orchestration protocol.
 */
export async function runZiaUrlCategoryPlanWorkflow(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly tenant: string;
  readonly terraformExecutable: string;
  readonly workspace: string;
}, dependencies: ZiaUrlCategoryPlanWorkflowDependencies = {}): Promise<ZiaUrlCategoryPlanWorkflowResult> {
  const layout = await prepareZiaUrlCategoriesWorkspace({
    tenant: options.tenant,
    workspace: options.workspace,
  });
  const paths = pathsFor(layout);
  // Invalidate prior plan evidence before collection or any other rerun phase
  // can fail. A rejected rerun must not leave an earlier pair looking current.
  await removePlanOutputs(paths, layout);
  try {
    await assertZiaUrlCategoriesWorkspace(layout);
    const artifacts = await runZiaUrlCategoryArtifactWorkflow(options, {
      ...(dependencies.collect === undefined ? {} : { collect: dependencies.collect }),
      ...(dependencies.observe === undefined ? {} : { observe: dependencies.observe }),
    });
    await assertZiaUrlCategoriesWorkspace(layout, Object.values(artifacts.paths));
    const module = await writeZiaUrlCategoriesModule({
      tenant: options.tenant,
      workspace: options.workspace,
    });
    const requiredInputs = [
      ...Object.values(artifacts.paths),
      ...Object.values(module),
    ];
    await assertZiaUrlCategoriesWorkspace(layout, requiredInputs);

    const environment = ziaUrlCategoryTerraformEnvironment(
      options.environment,
      layout.privateDir,
      null,
    );
    const fingerprintInput = Object.freeze({
      backendConfig: null,
      backendKey: null,
      envDir: paths.envDir,
      memberTypes: [ZIA_URL_CATEGORIES_RESOURCE_TYPE],
      varFiles: [artifacts.paths.tfvars],
    });
    const initInput = Object.freeze({
      backendConfig: null,
      backendKey: null,
      envDir: paths.envDir,
      memberTypes: [ZIA_URL_CATEGORIES_RESOURCE_TYPE],
    });
    let initBefore: string;
    try {
      initBefore = initSourcesSha256(await captureInitSourcesPayload(initInput));
    } catch {
      return fail(
        "ZIA_URL_CATEGORY_INIT_FINGERPRINT_FAILED",
        "ZIA URL-category init inputs could not be fingerprinted",
      );
    }
    await command({
      argv: ["init", "-input=false", "-no-color"],
      cwd: paths.envDir,
      environment,
      output: "discard",
      terraformExecutable: options.terraformExecutable,
    });
    await dependencies.afterInit?.(paths);
    let initAfter: string;
    try {
      initAfter = initSourcesSha256(await captureInitSourcesPayload(initInput));
    } catch {
      return fail(
        "ZIA_URL_CATEGORY_INIT_INPUTS_CHANGED",
        "ZIA URL-category init inputs changed while Terraform init was running",
      );
    }
    if (initAfter !== initBefore) {
      return fail(
        "ZIA_URL_CATEGORY_INIT_INPUTS_CHANGED",
        "ZIA URL-category init inputs changed while Terraform init was running",
      );
    }
    await assertZiaUrlCategoriesWorkspace(layout, requiredInputs);

    const state = await hasLocalState(paths.envDir)
      ? await command({
          argv: ["state", "list"],
          cwd: paths.envDir,
          environment,
          output: "capture",
          terraformExecutable: options.terraformExecutable,
        })
      : Buffer.alloc(0);
    const staged = await stageImports({
      source: artifacts.paths.imports,
      destination: paths.stagedImports,
      managed: stateAddresses(state ?? Buffer.alloc(0)),
    });
    await assertZiaUrlCategoriesWorkspace(layout, [
      ...requiredInputs,
      ...(staged.imports === 0 ? [] : [paths.stagedImports]),
    ]);
    let plannedFingerprint: Awaited<ReturnType<typeof planFingerprintV2>>;
    try {
      plannedFingerprint = await planFingerprintV2(fingerprintInput);
    } catch {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_FINGERPRINT_FAILED",
        "ZIA URL-category plan inputs could not be fingerprinted",
      );
    }
    await command({
      argv: [
        "plan",
        "-input=false",
        "-no-color",
        "-lock=false",
        `-var-file=${artifacts.paths.tfvars}`,
        `-out=${paths.pendingPlan}`,
      ],
      cwd: paths.envDir,
      environment,
      output: "discard",
      terraformExecutable: options.terraformExecutable,
    });
    await publishZiaUrlCategoryPlan(paths.pendingPlan, paths.plan);
    await dependencies.afterPlan?.(paths);
    await assertZiaUrlCategoriesWorkspace(layout, [...requiredInputs, paths.plan]);
    let currentFingerprint: Awaited<ReturnType<typeof planFingerprintV2>>;
    try {
      currentFingerprint = await planFingerprintV2(fingerprintInput);
    } catch {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_INPUTS_CHANGED",
        "ZIA URL-category inputs changed while Terraform plan was running",
      );
    }
    if (
      currentFingerprint.version !== plannedFingerprint.version
      || currentFingerprint.sha256 !== plannedFingerprint.sha256
    ) {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_INPUTS_CHANGED",
        "ZIA URL-category inputs changed while Terraform plan was running",
      );
    }
    await writeFingerprint(paths.fingerprint, plannedFingerprint);
    await dependencies.beforeAssessment?.(paths);
    const assessment = await runSavedPlanAssessment({
      backendConfig: null,
      policyPath: null,
      roots: [{
        envDir: paths.envDir,
        fingerprintPath: paths.fingerprint,
        label: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
        members: [ZIA_URL_CATEGORIES_RESOURCE_TYPE],
        savedPlanPath: paths.plan,
        tenant: options.tenant,
        varFiles: [artifacts.paths.tfvars],
      }],
      terraformExecutable: options.terraformExecutable,
    }, (core) => core, ({ plan }) => {
      requireExactZiaUrlCategoryPlan({
        all: staged.all,
        plan,
        staged: staged.staged,
      });
    });
    if (
      assessment.status !== "clean"
      || assessment.checked !== 1
      || assessment.clean !== 1
      || assessment.tolerated !== 0
      || assessment.blocked !== 0
    ) {
      return fail(
        "ZIA_URL_CATEGORY_PLAN_NOT_ADOPTABLE",
        "ZIA URL-category plan contains actions beyond no-op imports",
      );
    }
    await writeAssessment(paths.assessment, assessment);
    await assertZiaUrlCategoriesWorkspace(layout, [
      ...requiredInputs,
      paths.assessment,
      paths.fingerprint,
      paths.plan,
    ]);
    return Object.freeze({
      artifacts,
      assessment,
      module,
      paths,
      staged: Object.freeze({
        alreadyManaged: staged.alreadyManaged,
        imports: staged.imports,
      }),
    });
  } catch (error: unknown) {
    return rejectAfterCleanup(paths, layout, error);
  }
}
