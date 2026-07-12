import { constants } from "node:fs";
import { randomBytes } from "node:crypto";
import {
  lstat,
  mkdir,
  open,
  readdir,
  realpath,
  rename,
  rm,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";
import {
  ZIA_PROVIDER_VERSION,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
} from "../domain/zia-url-categories.js";

const TENANT = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;
const MODULE_SET = `zia-v${ZIA_PROVIDER_VERSION}`;

export interface ZiaUrlCategoriesWorkspace {
  readonly authorities: readonly ZiaUrlCategoryDirectoryAuthority[];
  readonly configDir: string;
  readonly envDir: string;
  readonly importsDir: string;
  readonly moduleDir: string;
  readonly planStagingDir: string;
  readonly privateDir: string;
  readonly pullsDir: string;
  readonly root: string;
  readonly tenant: string;
}

export interface ZiaUrlCategoryDirectoryAuthority {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly path: string;
}

function fail(code: string, message: string, category: "domain" | "io" = "io"): never {
  throw new ProcessFailure({ code, category, message });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object"
    && error !== null
    && "code" in error
    && typeof error.code === "string"
    ? error.code
    : null;
}

function inside(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== "..");
}

async function openDirectory(directory: string): Promise<FileHandle> {
  try {
    return await open(
      directory,
      constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
    );
  } catch {
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace contains an unsafe directory",
    );
  }
}

async function secureDirectory(
  root: string,
  directory: string,
): Promise<ZiaUrlCategoryDirectoryAuthority> {
  try {
    await mkdir(directory, { mode: 0o700 });
  } catch (error: unknown) {
    if (errorCode(error) !== "EEXIST") {
      return fail(
        "ZIA_URL_CATEGORY_DIRECTORY_CREATE_FAILED",
        "ZIA URL-category workspace directory could not be created",
      );
    }
  }
  const handle = await openDirectory(directory);
  let authority: ZiaUrlCategoryDirectoryAuthority;
  try {
    const metadata = await handle.stat({ bigint: true });
    if (!metadata.isDirectory()) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
        "ZIA URL-category workspace component is not a directory",
      );
    }
    await handle.chmod(0o700);
    const secured = await handle.stat({ bigint: true });
    if ((secured.mode & 0o777n) !== 0o700n) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
        "ZIA URL-category workspace directory is not private",
      );
    }
    authority = { dev: secured.dev, ino: secured.ino, path: directory };
  } finally {
    await handle.close();
  }
  let resolved: string;
  try {
    resolved = await realpath(directory);
  } catch {
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace directory could not be resolved",
    );
  }
  if (!inside(root, resolved) || resolved !== directory) {
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace directory escapes its authority",
    );
  }
  return authority;
}

async function secureChain(
  root: string,
  segments: readonly string[],
  authorities: Map<string, ZiaUrlCategoryDirectoryAuthority>,
): Promise<string> {
  let current = root;
  for (const segment of segments) {
    current = path.join(current, segment);
    authorities.set(current, await secureDirectory(root, current));
  }
  return current;
}

async function canonicalRoot(workspace: string): Promise<{
  readonly authority: ZiaUrlCategoryDirectoryAuthority;
  readonly root: string;
}> {
  if (
    typeof workspace !== "string"
    || !path.isAbsolute(workspace)
    || workspace.includes("\0")
  ) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace must be an absolute directory",
      "domain",
    );
  }
  await mkdir(workspace, { mode: 0o700, recursive: true });
  const metadata = await lstat(workspace).catch(() => null);
  const resolved = await realpath(workspace).catch(() => null);
  if (
    metadata === null
    || resolved === null
    || !metadata.isDirectory()
    || metadata.isSymbolicLink()
  ) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace must be a regular directory",
    );
  }
  return { authority: await secureDirectory(resolved, resolved), root: resolved };
}

/** Bind the exact private directories used by the one-resource ZIA workflow. */
export async function prepareZiaUrlCategoriesWorkspace(options: {
  readonly tenant: string;
  readonly workspace: string;
}): Promise<ZiaUrlCategoriesWorkspace> {
  if (!TENANT.test(options.tenant)) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_TENANT",
      "tenant must match [A-Za-z0-9_.-]+ and not be . or ..",
      "domain",
    );
  }
  const canonical = await canonicalRoot(options.workspace);
  const root = canonical.root;
  const authorities = new Map<string, ZiaUrlCategoryDirectoryAuthority>([
    [root, canonical.authority],
  ]);
  const configDir = await secureChain(root, ["config", options.tenant], authorities);
  const importsDir = await secureChain(root, ["imports", options.tenant], authorities);
  const pullsDir = await secureChain(root, ["pulls", options.tenant], authorities);
  const moduleDir = await secureChain(root, [
    "modules",
    MODULE_SET,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  ], authorities);
  const envDir = await secureChain(root, [
    "envs",
    options.tenant,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  ], authorities);
  const privateDir = await secureChain(root, [
    "envs",
    options.tenant,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    ".infrawright",
  ], authorities);
  await secureChain(root, [
    "envs",
    options.tenant,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    ".infrawright",
    "home",
  ], authorities);
  await secureChain(root, [
    "envs",
    options.tenant,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    ".infrawright",
    "tmp",
  ], authorities);
  const planStagingDir = await secureChain(root, [
    "envs",
    options.tenant,
    ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    ".infrawright",
    "plan",
  ], authorities);
  const layout = Object.freeze({
    authorities: Object.freeze([...authorities.values()]),
    configDir,
    envDir,
    importsDir,
    moduleDir,
    planStagingDir,
    privateDir,
    pullsDir,
    root,
    tenant: options.tenant,
  });
  return layout;
}

const ARTIFACT_NAMES = Object.freeze({
  config: new Map<string, "file">([
    [`${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.auto.tfvars.json`, "file"],
    [`${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.lookup.json`, "file"],
  ]),
  imports: new Map<string, "file">([
    [`${ZIA_URL_CATEGORIES_RESOURCE_TYPE}_imports.tf`, "file"],
  ]),
  pulls: new Map<string, "file">([
    [`${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.json`, "file"],
  ]),
});

const MODULE_NAMES = new Map<string, "file">([
  ["main.tf", "file"],
  ["outputs.tf", "file"],
  ["variables.tf", "file"],
  ["versions.tf", "file"],
]);

const ENV_NAMES = new Map<string, "file" | "directory">([
  [".infrawright", "directory"],
  [".terraform", "directory"],
  [".terraform.lock.hcl", "file"],
  ["main.tf", "file"],
  ["terraform.tfstate", "file"],
  ["terraform.tfstate.backup", "file"],
  ["tfplan", "file"],
  ["tfplan.assessment.json", "file"],
  ["tfplan.sources", "file"],
  [`${ZIA_URL_CATEGORIES_RESOURCE_TYPE}_imports.tf`, "file"],
]);

const PRIVATE_NAMES = new Map<string, "directory">([
  ["home", "directory"],
  ["plan", "directory"],
  ["tmp", "directory"],
]);

const PLAN_STAGING_NAMES = new Map<string, "file">([
  ["tfplan.pending", "file"],
]);

async function assertEntries(
  directory: string,
  allowed: ReadonlyMap<string, "file" | "directory">,
): Promise<void> {
  let entries;
  try {
    entries = await readdir(directory, { withFileTypes: true });
  } catch {
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
      "ZIA URL-category workspace could not be enumerated",
    );
  }
  for (const entry of entries) {
    const expected = allowed.get(entry.name);
    if (
      expected === undefined
      || entry.isSymbolicLink()
      || (expected === "file" && !entry.isFile())
      || (expected === "directory" && !entry.isDirectory())
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
        "ZIA URL-category workspace contains foreign or unsafe content",
      );
    }
  }
}

/** Reopen and compare every directory authority without following symlinks. */
export async function recheckZiaUrlCategoriesWorkspaceAuthorities(
  layout: ZiaUrlCategoriesWorkspace,
): Promise<void> {
  for (const authority of layout.authorities) {
    const handle = await openDirectory(authority.path);
    try {
      const metadata = await handle.stat({ bigint: true });
      if (
        !metadata.isDirectory()
        || metadata.dev !== authority.dev
        || metadata.ino !== authority.ino
        || (metadata.mode & 0o777n) !== 0o700n
      ) {
        return fail(
          "ZIA_URL_CATEGORY_WORKSPACE_CHANGED",
          "ZIA URL-category workspace authority changed during the workflow",
        );
      }
    } finally {
      await handle.close();
    }
    const resolved = await realpath(authority.path).catch(() => null);
    if (
      resolved === null
      || resolved !== authority.path
      || !inside(layout.root, resolved)
    ) {
      return fail(
        "ZIA_URL_CATEGORY_WORKSPACE_CHANGED",
        "ZIA URL-category workspace authority escaped during the workflow",
      );
    }
  }
}

/** Recheck the exact top-level content that can influence this narrow root. */
export async function assertZiaUrlCategoriesWorkspace(
  layout: ZiaUrlCategoriesWorkspace,
  requiredFiles: readonly string[] = [],
): Promise<void> {
  await recheckZiaUrlCategoriesWorkspaceAuthorities(layout);
  await Promise.all([
    assertEntries(layout.configDir, ARTIFACT_NAMES.config),
    assertEntries(layout.importsDir, ARTIFACT_NAMES.imports),
    assertEntries(layout.pullsDir, ARTIFACT_NAMES.pulls),
    assertEntries(layout.moduleDir, MODULE_NAMES),
    assertEntries(layout.envDir, ENV_NAMES),
    assertEntries(layout.privateDir, PRIVATE_NAMES),
    assertEntries(layout.planStagingDir, PLAN_STAGING_NAMES),
  ]);
  for (const filePath of requiredFiles) {
    const metadata = await lstat(filePath).catch(() => null);
    if (
      metadata === null
      || !metadata.isFile()
      || metadata.isSymbolicLink()
      || metadata.nlink !== 1
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_WORKSPACE",
        "required ZIA URL-category runtime file is not a private regular file",
      );
    }
  }
  await recheckZiaUrlCategoriesWorkspaceAuthorities(layout);
}

/** Publish one fresh private runtime text file without opening the old inode. */
export async function writeZiaUrlCategoryPrivateFile(
  filePath: string,
  content: string,
): Promise<void> {
  const temporary = path.join(
    path.dirname(filePath),
    `.infrawright-write-${randomBytes(16).toString("hex")}`,
  );
  let handle: FileHandle | null = null;
  let identity: { readonly dev: bigint; readonly ino: bigint } | null = null;
  try {
    handle = await open(
      temporary,
      constants.O_WRONLY
        | constants.O_CREAT
        | constants.O_EXCL
        | constants.O_NOFOLLOW,
      0o600,
    );
    const before = await handle.stat({ bigint: true });
    if (!before.isFile() || before.nlink !== 1n) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_RUNTIME_FILE",
        "ZIA URL-category runtime staging file is not private",
      );
    }
    await handle.chmod(0o600);
    await handle.writeFile(content, { encoding: "utf8" });
    await handle.sync();
    const after = await handle.stat({ bigint: true });
    if (
      !after.isFile()
      || after.nlink !== 1n
      || (after.mode & 0o777n) !== 0o600n
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_RUNTIME_FILE",
        "ZIA URL-category runtime staging file changed while it was written",
      );
    }
    identity = { dev: after.dev, ino: after.ino };
    await handle.close();
    handle = null;
    const target = await lstat(filePath).catch((error: unknown) => {
      return errorCode(error) === "ENOENT" ? null : Promise.reject(error);
    });
    if (
      target !== null
      && (!target.isFile() || target.isSymbolicLink())
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_RUNTIME_FILE",
        "ZIA URL-category runtime target is not a regular file",
      );
    }
    await rename(temporary, filePath);
    const published = await lstat(filePath, { bigint: true });
    if (
      identity === null
      || !published.isFile()
      || published.isSymbolicLink()
      || published.nlink !== 1n
      || published.dev !== identity.dev
      || published.ino !== identity.ino
      || (published.mode & 0o777n) !== 0o600n
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_RUNTIME_FILE",
        "published ZIA URL-category runtime file changed identity",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "ZIA_URL_CATEGORY_RUNTIME_WRITE_FAILED",
      "ZIA URL-category runtime file could not be written",
    );
  } finally {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
  }
}

/** Tighten and atomically move Terraform's private pending plan into the root. */
export async function publishZiaUrlCategoryPlan(
  pendingPath: string,
  targetPath: string,
): Promise<void> {
  let handle: FileHandle | null = null;
  let identity: { readonly dev: bigint; readonly ino: bigint } | null = null;
  try {
    handle = await open(
      pendingPath,
      constants.O_RDWR | constants.O_NONBLOCK | constants.O_NOFOLLOW,
    );
    const metadata = await handle.stat({ bigint: true });
    if (!metadata.isFile() || metadata.nlink !== 1n) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_PLAN",
        "Terraform did not create a private regular saved plan",
      );
    }
    await handle.chmod(0o600);
    await handle.sync();
    identity = { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_PLAN",
      "Terraform saved plan could not be secured",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
  const target = await lstat(targetPath).catch((error: unknown) => {
    return errorCode(error) === "ENOENT" ? null : Promise.reject(error);
  });
  if (target !== null) {
    return fail(
      "UNSAFE_ZIA_URL_CATEGORY_PLAN",
      "saved plan target appeared before publication",
    );
  }
  try {
    await rename(pendingPath, targetPath);
    const published = await lstat(targetPath, { bigint: true });
    if (
      identity === null
      || !published.isFile()
      || published.isSymbolicLink()
      || published.nlink !== 1n
      || published.dev !== identity.dev
      || published.ino !== identity.ino
      || (published.mode & 0o777n) !== 0o600n
    ) {
      return fail(
        "UNSAFE_ZIA_URL_CATEGORY_PLAN",
        "published ZIA URL-category plan is not the secured pending file",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "ZIA_URL_CATEGORY_PLAN_PUBLISH_FAILED",
      "secured ZIA URL-category plan could not be published",
    );
  }
}
