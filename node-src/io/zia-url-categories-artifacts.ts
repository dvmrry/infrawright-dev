import { lstat, mkdir, realpath } from "node:fs/promises";
import path from "node:path";

import {
  compileZiaUrlCategoryArtifacts,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  type ZiaUrlCategoryArtifactContents,
  type ZiaUrlCategoryStateObservation,
} from "../domain/zia-url-categories.js";
import { ProcessFailure } from "../domain/errors.js";
import { collectZiaUrlCategories } from "./zia-url-categories-fetch.js";
import { observeZiaUrlCategories } from "./zia-url-categories-oracle.js";
import { writeZiaUrlCategoryPrivateFile } from "./zia-url-categories-workspace.js";

const TENANT = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;

export interface ZiaUrlCategoryArtifactPaths {
  readonly imports: string;
  readonly lookup: string;
  readonly pull: string;
  readonly tfvars: string;
}

export interface ZiaUrlCategoryArtifactWorkflowResult {
  readonly itemCount: number;
  readonly paths: ZiaUrlCategoryArtifactPaths;
}

export interface ZiaUrlCategoryArtifactWorkflowDependencies {
  readonly collect?: (environment: NodeJS.ProcessEnv) => Promise<readonly unknown[]>;
  readonly observe?: (options: {
    readonly environment: NodeJS.ProcessEnv;
    readonly rawItems: readonly unknown[];
    readonly terraformExecutable: string;
    readonly workspace: string;
  }) => Promise<readonly ZiaUrlCategoryStateObservation[]>;
}

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function artifactPaths(workspace: string, tenant: string): ZiaUrlCategoryArtifactPaths {
  return Object.freeze({
    imports: path.join(
      workspace,
      "imports",
      tenant,
      `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}_imports.tf`,
    ),
    lookup: path.join(
      workspace,
      "config",
      tenant,
      `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.lookup.json`,
    ),
    pull: path.join(
      workspace,
      "pulls",
      tenant,
      `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.json`,
    ),
    tfvars: path.join(
      workspace,
      "config",
      tenant,
      `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.auto.tfvars.json`,
    ),
  });
}

async function canonicalWorkspace(workspace: string): Promise<string> {
  if (typeof workspace !== "string" || !path.isAbsolute(workspace) || workspace.includes("\0")) {
    return fail(
      "INVALID_ZIA_ARTIFACT_WORKSPACE",
      "ZIA artifact workspace must be an absolute directory",
    );
  }
  await mkdir(workspace, { mode: 0o700, recursive: true });
  try {
    const metadata = await lstat(workspace);
    const resolved = await realpath(workspace);
    if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
      return fail(
        "INVALID_ZIA_ARTIFACT_WORKSPACE",
      "ZIA artifact workspace must be a regular directory",
      );
    }
    return resolved;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "INVALID_ZIA_ARTIFACT_WORKSPACE",
      "ZIA artifact workspace could not be inspected",
      "io",
    );
  }
}

async function persist(
  paths: ZiaUrlCategoryArtifactPaths,
  contents: ZiaUrlCategoryArtifactContents,
): Promise<void> {
  try {
    await Promise.all([
      mkdir(path.dirname(paths.pull), { mode: 0o700, recursive: true }),
      mkdir(path.dirname(paths.tfvars), { mode: 0o700, recursive: true }),
      mkdir(path.dirname(paths.imports), { mode: 0o700, recursive: true }),
    ]);
    await Promise.all([
      writeZiaUrlCategoryPrivateFile(paths.pull, contents.pull),
      writeZiaUrlCategoryPrivateFile(paths.tfvars, contents.tfvars),
      writeZiaUrlCategoryPrivateFile(paths.imports, contents.imports),
      writeZiaUrlCategoryPrivateFile(paths.lookup, contents.lookup),
    ]);
  } catch {
    return fail(
      "ZIA_ARTIFACT_WRITE_FAILED",
      "ZIA URL-category artifacts could not be written",
      "io",
    );
  }
}

/** Run the complete PR-1 production path and persist its runtime artifacts. */
export async function runZiaUrlCategoryArtifactWorkflow(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly tenant: string;
  readonly terraformExecutable: string;
  readonly workspace: string;
}, dependencies: ZiaUrlCategoryArtifactWorkflowDependencies = {}): Promise<ZiaUrlCategoryArtifactWorkflowResult> {
  if (!TENANT.test(options.tenant)) {
    return fail(
      "INVALID_ZIA_ARTIFACT_TENANT",
      "tenant must match [A-Za-z0-9_.-]+ and not be . or ..",
    );
  }
  if (!path.isAbsolute(options.terraformExecutable)) {
    return fail(
      "INVALID_ZIA_ARTIFACT_TERRAFORM",
      "Terraform executable must be an absolute path",
    );
  }
  const workspace = await canonicalWorkspace(options.workspace);
  const collect = dependencies.collect ?? collectZiaUrlCategories;
  const observe = dependencies.observe ?? observeZiaUrlCategories;
  const rawItems = await collect(options.environment);
  const observations = await observe({
    environment: options.environment,
    rawItems,
    terraformExecutable: options.terraformExecutable,
    workspace,
  });
  const contents = compileZiaUrlCategoryArtifacts({ observations, rawItems });
  const paths = artifactPaths(workspace, options.tenant);
  await persist(paths, contents);
  return Object.freeze({ itemCount: rawItems.length, paths });
}
