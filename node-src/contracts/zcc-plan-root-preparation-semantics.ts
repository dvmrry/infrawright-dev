import type { ErrorObject } from "ajv/dist/2020.js";
import { createHash } from "node:crypto";
import path from "node:path";

import {
  renderZccPlanRootMain,
  zccPlanRootAbsentSidecarPaths,
  zccPlanRootMaterializationSha256,
  zccPlanRootModuleSource,
  zccPlanRootTreeSha256,
  MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
  MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES,
} from "../domain/zcc-plan-root-preparation-contract.js";
import type { ZccPlanRootPreparationCandidate } from "../domain/zcc-plan-root-preparation.js";
import { parseGeneratedImports } from "../domain/import-moves.js";
import {
  pythonCompatibleJsonByteLength,
  renderPythonCompatibleJson,
  sortedStrings,
  type JsonValue,
} from "../json/python-compatible.js";
import type {
  CompilePlanRootPreparationProcessRequest,
} from "../process/types.js";

export const ZCC_PLAN_ROOT_PREPARATION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-plan-root-preparation-semantics";
export const ZCC_PLAN_ROOT_PREPARATION_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-plan-root-preparation-request-semantics";
const OPERATION_RESULT_KEYWORD =
  "x-infrawright-zcc-plan-root-preparation-operation-result";

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as JsonRecord
    : null;
}

function strings(value: unknown): readonly string[] | null {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string")
    ? value
    : null;
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

function semanticError(
  keyword: string,
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${keyword}`,
    keyword,
    params: { rule },
    message,
  };
}

function sha256(text: string): string {
  return createHash("sha256").update(text, "utf8").digest("hex");
}

function stable(value: unknown): string {
  return renderPythonCompatibleJson(value as JsonValue);
}

function validTextArtifact(artifact: JsonRecord): boolean {
  return typeof artifact.content === "string"
    && typeof artifact.sha256 === "string"
    && typeof artifact.size_bytes === "number"
    && sha256(artifact.content) === artifact.sha256
    && Buffer.byteLength(artifact.content, "utf8") === artifact.size_bytes;
}

function tenantDirectoryOverlay(
  directory: string,
  kind: "config" | "imports" | "envs",
  tenant: string,
): { readonly defaultOverlay: boolean; readonly prefix: string } | null {
  const normalized = path.posix.normalize(directory);
  if (directory !== normalized && directory !== `./${normalized}`) {
    return null;
  }
  const relative = `${kind}/${tenant}`;
  if (directory === relative) {
    return { defaultOverlay: true, prefix: "" };
  }
  const suffix = `/${relative}`;
  return directory.endsWith(suffix)
    ? { defaultOverlay: false, prefix: directory.slice(0, -suffix.length) }
    : null;
}

function sameTenantDirectoryOverlay(
  left: ReturnType<typeof tenantDirectoryOverlay>,
  right: ReturnType<typeof tenantDirectoryOverlay>,
): boolean {
  return left !== null
    && right !== null
    && left.defaultOverlay === right.defaultOverlay
    && left.prefix === right.prefix;
}

export interface ZccPlanRootSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Bind redundant ordering, counts, digests, topology, and exact candidate bytes. */
export const validateZccPlanRootPreparationSemantics:
  ZccPlanRootSemanticValidator = (_schema, data, _parentSchema, dataContext) => {
    const result = record(data);
    const root = record(result?.root);
    const artifacts = record(root?.artifacts);
    const main = record(artifacts?.main_tf);
    const topology = record(result?.topology);
    const directories = record(topology?.directories);
    const summary = record(result?.summary);
    if (
      result === null
      || root === null
      || artifacts === null
      || main === null
      || topology === null
      || directories === null
      || summary === null
    ) {
      delete validateZccPlanRootPreparationSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PLAN_ROOT_PREPARATION_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    try {
      if (
        pythonCompatibleJsonByteLength(
          result as JsonValue,
          MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
        ) > MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES
      ) {
        push("", "candidate_byte_budget", "candidate exceeds its serialized byte budget");
      }
    } catch {
      push("", "candidate_byte_budget", "candidate cannot be measured as versioned JSON");
    }
    const members = strings(root.members);
    const sources = Array.isArray(result.sources) ? result.sources.map(record) : null;
    const modules = Array.isArray(result.modules) ? result.modules.map(record) : null;
    const staged = Array.isArray(artifacts.staged_imports)
      ? artifacts.staged_imports.map(record)
      : null;
    if (members === null || sources === null || modules === null || staged === null) {
      delete validateZccPlanRootPreparationSemantics.errors;
      return true;
    }
    if (
      staged.reduce((total, artifact) => {
        return total + (
          artifact !== null && typeof artifact.size_bytes === "number"
            ? artifact.size_bytes
            : 0
        );
      }, 0) > MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES
    ) {
      push(
        "/root/artifacts/staged_imports",
        "staged_import_byte_budget",
        "staged imports exceed their aggregate byte budget",
      );
    }
    const orderedMembers = sortedStrings(new Set(members));
    if (!sameStrings(members, orderedMembers)) {
      push("/root/members", "member_order", "root members must be sorted and unique");
    }
    const sourceTypes = sources.map((entry) => entry?.resource_type).filter(
      (value): value is string => typeof value === "string",
    );
    const moduleTypes = modules.map((entry) => entry?.resource_type).filter(
      (value): value is string => typeof value === "string",
    );
    if (!sameStrings(sourceTypes, members)) {
      push("/sources", "member_join", "source bindings must exactly follow root members");
    }
    if (!sameStrings(moduleTypes, members)) {
      push("/modules", "member_join", "module bindings must exactly follow root members");
    }
    if (
      typeof result.tenant === "string"
      && typeof directories.config === "string"
      && typeof directories.imports === "string"
      && typeof directories.envs === "string"
    ) {
      const overlays = [
        tenantDirectoryOverlay(directories.config, "config", result.tenant),
        tenantDirectoryOverlay(directories.imports, "imports", result.tenant),
        tenantDirectoryOverlay(directories.envs, "envs", result.tenant),
      ] as const;
      if (
        overlays.some((overlay) => overlay === null)
        || !sameTenantDirectoryOverlay(overlays[0], overlays[1])
        || !sameTenantDirectoryOverlay(overlays[0], overlays[2])
      ) {
        push(
          "/topology/directories",
          "tenant_directory_join",
          "config, imports, and envs must be the same deployment overlay for the candidate tenant",
        );
      }
    }
    if (
      typeof directories.envs === "string"
      && typeof root.label === "string"
      && root.env_dir !== path.posix.join(directories.envs, root.label)
    ) {
      push(
        "/root/env_dir",
        "tenant_directory_join",
        "root env directory must be derived from the topology envs directory and root label",
      );
    }
    for (const [index, source] of sources.entries()) {
      if (source === null) {
        continue;
      }
      const resourceType = members[index];
      const materialized = record(source.materialized_artifacts);
      const tfvars = record(materialized?.tfvars);
      const imports = record(materialized?.imports);
      const lookup = materialized?.lookup === null
        ? null
        : record(materialized?.lookup);
      if (
        resourceType === undefined
        || typeof result.tenant !== "string"
        || typeof directories.config !== "string"
        || typeof directories.imports !== "string"
      ) {
        continue;
      }
      if (tfvars?.path !== path.posix.join(
        directories.config,
        `${resourceType}.auto.tfvars.json`,
      )) {
        push(`/sources/${index}/materialized_artifacts/tfvars/path`, "materialized_path", "tfvars path is inconsistent with topology");
      }
      if (imports?.path !== path.posix.join(
        directories.imports,
        `${resourceType}_imports.tf`,
      )) {
        push(`/sources/${index}/materialized_artifacts/imports/path`, "materialized_path", "imports path is inconsistent with topology");
      }
      const expectedLookup = resourceType === "zcc_trusted_network"
        ? path.posix.join(directories.config, `${resourceType}.lookup.json`)
        : null;
      if ((lookup === null ? null : lookup.path) !== expectedLookup) {
        push(`/sources/${index}/materialized_artifacts/lookup`, "materialized_path", "lookup applicability/path is inconsistent with topology");
      }
    }
    if (staged.length !== members.length) {
      push("/root/artifacts/staged_imports", "member_join", "staged imports must cover every member");
    }
    if (
      summary.selected !== 1
      || summary.roots !== 1
      || summary.members !== members.length
      || summary.modules !== modules.length
      || summary.staged_imports !== staged.length
    ) {
      push("/summary", "summary_counts", "summary counts must match the singular candidate");
    }
    if (!validTextArtifact(main)) {
      push("/root/artifacts/main_tf", "artifact_digest", "main.tf bytes must match their digest and size");
    }
    if (main.media_type !== "text/x-hcl") {
      push("/root/artifacts/main_tf/media_type", "artifact_media_type", "main.tf must be emitted as text/x-hcl");
    }
    if (typeof root.env_dir === "string" && main.path !== path.posix.join(root.env_dir, "main.tf")) {
      push("/root/artifacts/main_tf/path", "artifact_path", "main.tf must be rooted in the selected env directory");
    }
    const moduleSources = new Map<string, string>();
    for (const [index, module] of modules.entries()) {
      if (module === null) {
        continue;
      }
      if (typeof module.resource_type === "string" && typeof module.source === "string") {
        moduleSources.set(module.resource_type, module.source);
      }
      try {
        if (
          typeof root.env_dir === "string"
          && typeof module.logical_path === "string"
          && module.source !== zccPlanRootModuleSource(
            root.env_dir,
            module.logical_path,
          )
        ) {
          push(`/modules/${index}/source`, "module_source", "module source must be derived from its observed logical path");
        }
      } catch {
        push(`/modules/${index}/source`, "module_source", "module source path is outside the renderer contract");
      }
      const files = Array.isArray(module.files) ? module.files.map(record) : null;
      if (files === null || files.some((file) => file === null)) {
        continue;
      }
      const publicFiles = files.map((file) => ({
        path: String(file?.path),
        sha256: String(file?.sha256),
      }));
      const ordered = sortedStrings(publicFiles.map((file) => file.path));
      if (
        !sameStrings(publicFiles.map((file) => file.path), ordered)
        || new Set(ordered).size !== ordered.length
      ) {
        push(`/modules/${index}/files`, "module_file_order", "module files must be sorted and unique");
      }
      if (module.file_count !== publicFiles.length) {
        push(`/modules/${index}/file_count`, "module_file_count", "module file count is inconsistent");
      }
      if (module.tree_sha256 !== zccPlanRootTreeSha256(publicFiles)) {
        push(`/modules/${index}/tree_sha256`, "module_tree_digest", "module tree digest is inconsistent");
      }
    }
    try {
      if (
        typeof result.tenant === "string"
        && typeof root.label === "string"
        && (result.backend === "local" || result.backend === "azurerm")
      ) {
        const expectedMain = renderZccPlanRootMain({
          tenant: result.tenant,
          label: root.label,
          members: members as ZccPlanRootPreparationCandidate["root"]["members"],
          backend: result.backend,
          moduleSources,
        });
        if (main.content !== expectedMain) {
          push("/root/artifacts/main_tf/content", "main_bytes", "main.tf is not the exact renderer output");
        }
      }
    } catch {
      push("/root/artifacts/main_tf/content", "main_bytes", "main.tf renderer inputs are inconsistent");
    }
    for (const [index, artifact] of staged.entries()) {
      if (artifact === null) {
        continue;
      }
      if (!validTextArtifact(artifact)) {
        push(`/root/artifacts/staged_imports/${index}`, "artifact_digest", "staged import bytes must match their digest and size");
      }
      if (artifact.media_type !== "text/x-hcl") {
        push(`/root/artifacts/staged_imports/${index}/media_type`, "artifact_media_type", "staged imports must be emitted as text/x-hcl");
      }
      const resourceType = members[index];
      const source = sources[index];
      const materialized = record(source?.materialized_artifacts);
      const imports = record(materialized?.imports);
      if (
        resourceType !== undefined
        && typeof root.env_dir === "string"
        && artifact.path !== path.posix.join(root.env_dir, `${resourceType}_imports.tf`)
      ) {
        push(`/root/artifacts/staged_imports/${index}/path`, "artifact_path", "staged imports path is inconsistent");
      }
      if (
        imports !== null
        && (artifact.sha256 !== imports.sha256 || artifact.size_bytes !== imports.size_bytes)
      ) {
        push(`/root/artifacts/staged_imports/${index}`, "materialized_join", "staged imports must exactly join the materialized artifact");
      }
      try {
        if (resourceType !== undefined && typeof artifact.content === "string") {
          parseGeneratedImports(resourceType, artifact.content);
        }
      } catch {
        push(`/root/artifacts/staged_imports/${index}/content`, "canonical_imports", "staged imports must be canonical adoption imports");
      }
    }
    const topologyRoots = Array.isArray(topology.roots) ? topology.roots.map(record) : [];
    const topologyRoot = topologyRoots[0];
    const topologyMembers = strings(topologyRoot?.members);
    const topologySelectors = strings(topology.selectors);
    const selector = record(result.selector);
    if (
      topology.tenant !== result.tenant
      || topologyRoots.length !== 1
      || topologyRoot?.label !== root.label
      || topologyRoot?.provider !== "zcc"
      || topologyRoot?.env_dir !== root.env_dir
      || topologyMembers === null
      || !sameStrings(topologyMembers, members)
      || topologySelectors === null
      || topologySelectors.length !== 1
      || topologySelectors[0] !== selector?.resource_type
    ) {
      push("/topology", "topology_join", "topology must exactly describe the singular candidate");
    }
    const resourceRoots = record(topology.resource_roots);
    if (
      resourceRoots === null
      || !sameStrings(sortedStrings(Object.keys(resourceRoots)), members)
      || members.some((member) => resourceRoots[member] !== root.label)
    ) {
      push("/topology/resource_roots", "topology_join", "resource-root mappings must exactly cover the selected members");
    }
    const absentSidecars = strings(result.absent_sidecars);
    if (
      absentSidecars !== null
      && typeof root.env_dir === "string"
    ) {
      try {
        const expectedSidecars = zccPlanRootAbsentSidecarPaths(
          topology as unknown as ZccPlanRootPreparationCandidate["topology"],
          members as ZccPlanRootPreparationCandidate["root"]["members"],
          root.env_dir,
        );
        if (!sameStrings(absentSidecars, expectedSidecars)) {
          push("/absent_sidecars", "sidecar_set", "absent sidecars must be the exact sorted no-bindings/no-moves set");
        }
      } catch {
        push("/absent_sidecars", "sidecar_set", "absent sidecars cannot be derived from topology");
      }
    }
    const marker = record(result.backend_marker);
    const desired = record(marker?.desired);
    if (
      typeof directories.envs === "string"
      && marker?.path !== path.posix.join(directories.envs, ".backend")
    ) {
      push("/backend_marker/path", "backend_marker", "backend marker path must match topology");
    }
    if (result.backend === "local") {
      if (marker?.observed_state !== "absent" || marker.desired !== null) {
        push("/backend_marker", "backend_marker", "local backend requires an absent marker");
      }
    } else if (
      desired === null
      || desired.content !== "azurerm\n"
      || desired.media_type !== "text/plain"
      || !validTextArtifact(desired)
      || desired.path !== marker?.path
    ) {
      push("/backend_marker", "backend_marker", "azurerm backend requires exact desired marker bytes");
    }

    if (errors.length === 0) {
      delete validateZccPlanRootPreparationSemantics.errors;
      return true;
    }
    validateZccPlanRootPreparationSemantics.errors = errors;
    return false;
  };

/** Reject caller-selected receipt reorderings and coordinate mismatches pre-I/O. */
export const validateZccPlanRootPreparationRequestSemantics:
  ZccPlanRootSemanticValidator = (_schema, data, _parentSchema, dataContext) => {
    const input = record(data);
    const materializations = Array.isArray(input?.materializations)
      ? input.materializations.map(record)
      : null;
    if (input === null || materializations === null) {
      delete validateZccPlanRootPreparationRequestSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const types = materializations.map((receipt) => receipt?.resource_type).filter(
      (value): value is string => typeof value === "string",
    );
    if (!sameStrings(types, sortedStrings(new Set(types)))) {
      errors.push(semanticError(
        ZCC_PLAN_ROOT_PREPARATION_REQUEST_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}/materializations`,
        "materialization_order",
        "materialization receipts must be sorted and unique",
      ));
    }
    for (const [index, receipt] of materializations.entries()) {
      if (receipt !== null && receipt.tenant !== input.tenant) {
        errors.push(semanticError(
          ZCC_PLAN_ROOT_PREPARATION_REQUEST_SEMANTICS_KEYWORD,
          `${dataContext?.instancePath ?? ""}/materializations/${index}/tenant`,
          "materialization_join",
          "materialization tenant must match the request",
        ));
      }
    }
    if (typeof input.resource_type === "string" && !types.includes(input.resource_type)) {
      errors.push(semanticError(
        ZCC_PLAN_ROOT_PREPARATION_REQUEST_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}/materializations`,
        "selector_coverage",
        "materializations must include the exact selected resource",
      ));
    }
    if (errors.length === 0) {
      delete validateZccPlanRootPreparationRequestSemantics.errors;
      return true;
    }
    validateZccPlanRootPreparationRequestSemantics.errors = errors;
    return false;
  };

/** Bind a public result back to the retained request and exact receipt graph. */
export function zccPlanRootPreparationOperationResultErrors(
  request: CompilePlanRootPreparationProcessRequest,
  result: ZccPlanRootPreparationCandidate,
): ErrorObject[] {
  const errors: ErrorObject[] = [];
  const push = (instancePath: string, rule: string, message: string): void => {
    errors.push(semanticError(OPERATION_RESULT_KEYWORD, instancePath, rule, message));
  };
  if (
    result.profile !== request.input.profile
    || result.tenant !== request.input.tenant
    || result.selector.resource_type !== request.input.resource_type
    || result.backend !== request.input.backend
  ) {
    push("/", "request_join", "plan-root result coordinates do not match the request");
  }
  const logicalControl = (candidate: string): string => {
    const absolute = path.isAbsolute(candidate)
      ? path.resolve(candidate)
      : path.resolve(request.context.workspace, candidate);
    return path.relative(request.context.workspace, absolute).split(path.sep).join("/") || ".";
  };
  if (
    result.controls.deployment.path !== logicalControl(request.context.deployment)
    || result.controls.root_catalog.path !== logicalControl(request.context.root_catalog)
  ) {
    push("/controls", "control_join", "plan-root controls do not match the retained context");
  }
  const expected = request.input.materializations.map((receipt) => ({
    resource_type: receipt.resource_type,
    materialization_sha256: zccPlanRootMaterializationSha256(receipt),
    provider_observed_source: receipt.verification.source,
    adoption_catalog: receipt.verification.catalog,
    materialized_artifacts: {
      tfvars: receipt.verification.parity.artifacts.tfvars.reference,
      imports: receipt.verification.parity.artifacts.imports.reference,
      lookup: receipt.verification.parity.artifacts.lookup.status === "not_applicable"
        ? null
        : receipt.verification.parity.artifacts.lookup.reference,
    },
  }));
  if (
    result.sources.length !== expected.length
    || result.sources.some((source, index) => {
      const expectedSource = expected[index];
      return expectedSource === undefined || stable(source) !== stable(expectedSource);
    })
  ) {
    push("/sources", "receipt_join", "plan-root result does not bind the retained receipts");
  }
  for (const receipt of request.input.materializations) {
    const expectedVariable = result.root.label === receipt.resource_type
      ? "items"
      : `${receipt.resource_type}_items`;
    if (
      receipt.verification.root.label !== result.root.label
      || !sameStrings(receipt.verification.root.members, result.root.members)
      || receipt.verification.root.variable_name !== expectedVariable
    ) {
      push("/root", "receipt_root_join", "plan-root result does not match retained receipt topology");
      break;
    }
  }
  return errors;
}
