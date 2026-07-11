import { createHash } from "node:crypto";

import type { ErrorObject } from "ajv/dist/2020.js";

import { parseCanonicalImportBlocks } from "../json/canonical-import-blocks.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  SUPPORTED_ZCC_ROOT_MEMBERS,
  SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS,
} from "../domain/zscaler-assessment.js";

export const ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-artifact-semantics";

const TRUSTED_NETWORK = "zcc_trusted_network";
const MAX_ARTIFACT_BYTES = 32 * 1024 * 1024;
const CATALOG_SHA256 =
  "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a";
const CATALOG_SOURCES_SHA256 =
  "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11";
const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

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

function ownValue(value: JsonRecord, key: string): unknown {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

function sameStrings(
  left: readonly string[],
  right: readonly string[],
): boolean {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD}`,
    keyword: ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

function layoutPrefix(pathValue: string, suffix: string): string | null {
  if (pathValue === suffix) {
    return "";
  }
  if (!pathValue.endsWith(suffix)) {
    return null;
  }
  const prefix = pathValue.slice(0, -suffix.length);
  return prefix.endsWith("/") ? prefix : null;
}

function prefixedPath(prefix: string, suffix: string): string {
  return `${prefix}${suffix}`;
}

function decodedStringsAreWellFormed(value: unknown): boolean {
  const stack: unknown[] = [value];
  while (stack.length > 0) {
    const current = stack.pop();
    if (typeof current === "string") {
      if (!current.isWellFormed()) {
        return false;
      }
      continue;
    }
    if (Array.isArray(current)) {
      for (let index = current.length - 1; index >= 0; index -= 1) {
        stack.push(current[index]);
      }
      continue;
    }
    const currentRecord = record(current);
    if (currentRecord === null) {
      continue;
    }
    for (const key of Object.keys(currentRecord)) {
      if (!key.isWellFormed()) {
        return false;
      }
      stack.push(ownValue(currentRecord, key));
    }
  }
  return true;
}

export interface ZccPullArtifactSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce artifact-set joins, provenance, and canonical descriptor bytes. */
export const validateZccPullArtifactSemantics:
  ZccPullArtifactSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const artifactSet = record(data);
    const source = record(artifactSet?.source);
    const catalog = record(artifactSet?.catalog);
    const root = record(artifactSet?.root);
    const artifacts = record(artifactSet?.artifacts);
    const tfvars = record(artifacts?.tfvars);
    const imports = record(artifacts?.imports);
    if (
      artifactSet === null
      || source === null
      || catalog === null
      || root === null
      || artifacts === null
      || tfvars === null
      || imports === null
    ) {
      delete validateZccPullArtifactSemantics.errors;
      return true;
    }

    const errors: ErrorObject[] = [];
    const error = (instancePath: string, rule: string, message: string) => {
      return semanticError(
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      );
    };
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(error(instancePath, rule, message));
    };

    const resourceType = artifactSet.resource_type;
    const tenant = artifactSet.tenant;
    const members = root.members;
    const rootLabel = root.label;
    const variableName = root.variable_name;

    if (
      catalog.sha256 !== CATALOG_SHA256
      || catalog.sources_sha256 !== CATALOG_SOURCES_SHA256
    ) {
      push(
        "/catalog",
        "catalog_provenance",
        "artifact catalog provenance must match the exact v1 catalog",
      );
    }

    if (
      typeof resourceType === "string"
      && typeof tenant === "string"
      && typeof source.path === "string"
      && source.path !== `pulls/${tenant}/${resourceType}.json`
    ) {
      push(
        "/source/path",
        "source_path",
        "pull source path must use the canonical tenant and resource layout",
      );
    }

    if (
      typeof resourceType === "string"
      && Array.isArray(members)
      && members.every((member): member is string => typeof member === "string")
    ) {
      const expectedMembers = sortedStrings(new Set(members));
      if (!sameStrings(expectedMembers, members)) {
        push(
          "/root/members",
          "root_members",
          "root members must be sorted and unique",
        );
      }
      if (!expectedMembers.includes(resourceType)) {
        push(
          "/root/members",
          "root_members",
          "root members must include the compiled resource",
        );
      }
      if (expectedMembers.some(
        (member) => !SUPPORTED_ZCC_ROOT_MEMBERS.includes(member),
      )) {
        push(
          "/root/members",
          "root_members",
          "root members must belong to the exact bundled ZCC catalog",
        );
      }
      if (
        typeof rootLabel === "string"
        && SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS.includes(rootLabel)
        && (expectedMembers.length !== 1 || expectedMembers[0] !== rootLabel)
      ) {
        push(
          "/root/label",
          "root_label",
          "a generated resource label can only denote its singleton root",
        );
      }
    }

    if (
      typeof resourceType === "string"
      && typeof rootLabel === "string"
      && typeof variableName === "string"
    ) {
      const expectedVariable = rootLabel === resourceType
        ? "items"
        : `${resourceType}_items`;
      if (variableName !== expectedVariable) {
        push(
          "/root/variable_name",
          "variable_name",
          "tfvars variable name must agree with the resolved root",
        );
      }
    }

    if (
      Array.isArray(artifactSet.unexpected_drops)
      && artifactSet.unexpected_drops.every(
        (entry): entry is string => typeof entry === "string",
      )
    ) {
      const expectedDrops = sortedStrings(new Set(artifactSet.unexpected_drops));
      if (!sameStrings(expectedDrops, artifactSet.unexpected_drops)) {
        push(
          "/unexpected_drops",
          "unexpected_drops",
          "unexpected drops must be sorted and unique",
        );
      }
    }

    const validateDescriptor = (
      descriptor: JsonRecord,
      instancePath: string,
    ): boolean => {
      if (
        typeof descriptor.content !== "string"
        || typeof descriptor.path !== "string"
        || typeof descriptor.size_bytes !== "number"
        || typeof descriptor.sha256 !== "string"
      ) {
        return false;
      }
      if (
        !descriptor.content.isWellFormed()
        || !descriptor.path.isWellFormed()
        || descriptor.path.includes("\0")
      ) {
        push(
          instancePath,
          "artifact_unicode",
          "artifact content and path must use supported Unicode paths",
        );
        return false;
      }
      const sizeBytes = Buffer.byteLength(descriptor.content, "utf8");
      if (descriptor.size_bytes !== sizeBytes) {
        push(
          `${instancePath}/size_bytes`,
          "artifact_bytes",
          "artifact size must match its UTF-8 content",
        );
      }
      if (sizeBytes > MAX_ARTIFACT_BYTES) {
        push(
          `${instancePath}/content`,
          "artifact_bounds",
          "artifact content exceeds the validation size limit",
        );
        return false;
      }
      const digest = createHash("sha256")
        .update(descriptor.content, "utf8")
        .digest("hex");
      if (descriptor.sha256 !== digest) {
        push(
          `${instancePath}/sha256`,
          "artifact_bytes",
          "artifact digest must match its UTF-8 content",
        );
      }
      return true;
    };

    const tfvarsWithinBounds = validateDescriptor(
      tfvars,
      "/artifacts/tfvars",
    );
    const importsWithinBounds = validateDescriptor(
      imports,
      "/artifacts/imports",
    );
    const lookup = artifacts.lookup === null ? null : record(artifacts.lookup);
    const lookupWithinBounds = lookup === null
      ? false
      : validateDescriptor(lookup, "/artifacts/lookup");

    let artifactPrefix: string | null = null;
    if (
      typeof resourceType === "string"
      && typeof tenant === "string"
      && typeof tfvars.path === "string"
    ) {
      const suffix = `config/${tenant}/${resourceType}.auto.tfvars.json`;
      artifactPrefix = layoutPrefix(tfvars.path, suffix);
      if (artifactPrefix === null) {
        push(
          "/artifacts/tfvars/path",
          "artifact_layout",
          "tfvars path must use the canonical tenant and resource layout",
        );
      }
    }
    if (
      typeof resourceType === "string"
      && typeof tenant === "string"
      && typeof imports.path === "string"
    ) {
      const suffix = `imports/${tenant}/${resourceType}_imports.tf`;
      const importsPrefix = layoutPrefix(imports.path, suffix);
      if (importsPrefix === null) {
        push(
          "/artifacts/imports/path",
          "artifact_layout",
          "imports path must use the canonical tenant and resource layout",
        );
      } else if (artifactPrefix !== null && importsPrefix !== artifactPrefix) {
        push(
          "/artifacts/imports/path",
          "artifact_layout",
          "tfvars and imports paths must share one deployment prefix",
        );
      }
    }

    if (typeof resourceType === "string" && typeof tenant === "string") {
      if (resourceType === TRUSTED_NETWORK) {
        if (lookup === null) {
          push(
            "/artifacts/lookup",
            "lookup_artifact",
            "trusted-network artifacts require a lookup sidecar",
          );
        } else if (typeof lookup.path === "string" && artifactPrefix !== null) {
          const suffix = `config/${tenant}/${resourceType}.lookup.json`;
          if (lookup.path !== prefixedPath(artifactPrefix, suffix)) {
            push(
              "/artifacts/lookup/path",
              "artifact_layout",
              "lookup path must share the tfvars deployment prefix",
            );
          }
        }
      } else if (artifacts.lookup !== null) {
        push(
          "/artifacts/lookup",
          "lookup_artifact",
          "only trusted-network artifacts can emit a lookup sidecar",
        );
      }
    }

    const parseCanonicalJson = (
      descriptor: JsonRecord,
      instancePath: string,
      withinBounds: boolean,
    ): unknown | null => {
      if (!withinBounds || typeof descriptor.content !== "string") {
        return null;
      }
      try {
        const parsed = parseDataJsonLosslessly(descriptor.content);
        if (!decodedStringsAreWellFormed(parsed)) {
          push(
            `${instancePath}/content`,
            "decoded_unicode",
            "decoded JSON keys and values must use supported Unicode",
          );
          return null;
        }
        if (renderPythonLosslessArtifactJson(parsed) !== descriptor.content) {
          push(
            `${instancePath}/content`,
            "canonical_json",
            "JSON artifact content must use the canonical artifact encoding",
          );
        }
        return parsed;
      } catch {
        push(
          `${instancePath}/content`,
          "canonical_json",
          "JSON artifact content must use the canonical artifact encoding",
        );
        return null;
      }
    };

    let tfvarsKeys: readonly string[] | null = null;
    const parsedTfvars = parseCanonicalJson(
      tfvars,
      "/artifacts/tfvars",
      tfvarsWithinBounds,
    );
    if (parsedTfvars !== null && typeof variableName === "string") {
      const envelope = record(parsedTfvars);
      const envelopeKeys = envelope === null ? [] : Object.keys(envelope);
      const items = envelope === null ? null : record(ownValue(envelope, variableName));
      if (
        envelope === null
        || envelopeKeys.length !== 1
        || envelopeKeys[0] !== variableName
        || items === null
      ) {
        push(
          "/artifacts/tfvars/content",
          "tfvars_envelope",
          "tfvars must contain exactly one declared object variable",
        );
      } else if (Object.keys(items).some((key) => record(ownValue(items, key)) === null)) {
        push(
          "/artifacts/tfvars/content",
          "tfvars_items",
          "every tfvars item key must map to an object",
        );
      } else {
        tfvarsKeys = sortedStrings(Object.keys(items));
      }
    }

    let importBlocks: readonly { readonly key: string; readonly id: string }[]
      | null = null;
    if (
      importsWithinBounds
      && typeof imports.content === "string"
      && typeof resourceType === "string"
    ) {
      try {
        importBlocks = parseCanonicalImportBlocks(
          imports.content,
          resourceType,
        );
      } catch {
        push(
          "/artifacts/imports/content",
          "imports_grammar",
          "imports must use the canonical bootstrap import grammar",
        );
      }
    }

    if (importBlocks !== null) {
      const importKeys = importBlocks.map((block) => block.key);
      const importIds = importBlocks.map((block) => block.id);
      if (
        importIds.some((id) => PYTHON_WHITESPACE_ONLY.test(id))
        || new Set(importIds).size !== importIds.length
      ) {
        push(
          "/artifacts/imports/content",
          "import_ids",
          "import identities must be nonblank and unique",
        );
      }
      if (tfvarsKeys !== null && !sameStrings(importKeys, tfvarsKeys)) {
        push(
          "/artifacts/imports/content",
          "imports_join",
          "imports must contain one canonical block per tfvars item key",
        );
      }
    }

    const parsedLookup = lookup === null
      ? null
      : parseCanonicalJson(
          lookup,
          "/artifacts/lookup",
          lookupWithinBounds,
        );
    if (
      resourceType === TRUSTED_NETWORK
      && lookup !== null
      && parsedLookup !== null
      && tfvarsKeys !== null
      && importBlocks !== null
    ) {
      const lookupEnvelope = record(parsedLookup);
      const lookupKeys = lookupEnvelope === null
        ? []
        : sortedStrings(Object.keys(lookupEnvelope));
      if (tfvarsKeys.length === 0) {
        if (lookupEnvelope === null || lookupKeys.length !== 0) {
          push(
            "/artifacts/lookup/content",
            "lookup_join",
            "an empty trusted-network artifact set must use an empty lookup",
          );
        }
      } else if (lookupEnvelope !== null && lookupKeys.length === 0) {
        push(
          "/artifacts/lookup/content",
          "lookup_join",
          "a non-empty trusted-network artifact set requires lookup mappings",
        );
      } else {
        const byId = lookupEnvelope === null
          ? null
          : record(ownValue(lookupEnvelope, "by_id"));
        const keyById = lookupEnvelope === null
          ? null
          : record(ownValue(lookupEnvelope, "key_by_id"));
        if (
          !sameStrings(lookupKeys, ["by_id", "key_by_id"])
          || byId === null
          || keyById === null
        ) {
          push(
            "/artifacts/lookup/content",
            "lookup_shape",
            "trusted-network lookup must contain only by_id and key_by_id maps",
          );
        } else {
          const byIds = sortedStrings(Object.keys(byId));
          const keyByIds = sortedStrings(Object.keys(keyById));
          const importIds = sortedStrings(importBlocks.map((block) => block.id));
          const lookupItemKeys: string[] = [];
          let mapsContainStrings = true;
          for (const id of keyByIds) {
            const itemKey = ownValue(keyById, id);
            const displayName = ownValue(byId, id);
            if (typeof itemKey !== "string" || typeof displayName !== "string") {
              mapsContainStrings = false;
            } else {
              lookupItemKeys.push(itemKey);
            }
          }
          if (!mapsContainStrings) {
            push(
              "/artifacts/lookup/content",
              "lookup_shape",
              "trusted-network lookup maps must contain only string values",
            );
          }
          if (
            !sameStrings(byIds, keyByIds)
            || !sameStrings(byIds, importIds)
            || !sameStrings(sortedStrings(lookupItemKeys), tfvarsKeys)
            || new Set(lookupItemKeys).size !== lookupItemKeys.length
            || importBlocks.some((block) => ownValue(keyById, block.id) !== block.key)
          ) {
            push(
              "/artifacts/lookup/content",
              "lookup_join",
              "trusted-network lookup IDs and keys must exactly join imports and tfvars",
            );
          }
        }
      }
    }

    if (errors.length === 0) {
      delete validateZccPullArtifactSemantics.errors;
    } else {
      validateZccPullArtifactSemantics.errors = errors;
    }
    return errors.length === 0;
  };
