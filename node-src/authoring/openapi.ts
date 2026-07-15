import SwaggerParser from "@apidevtools/swagger-parser";
import { LosslessNumber } from "lossless-json";

import { snakeName } from "../domain/pull-transform.js";
import { sortedStrings } from "../json/python-compatible.js";
import { isObject, type JsonObject } from "../metadata/validation.js";

export interface OpenApiFieldMetadata extends JsonObject {
  readonly path: string;
  readonly readable?: true;
  readonly writable?: true;
  readonly required?: true;
  readonly read_only?: true;
  readonly write_only?: true;
  readonly response_only?: true;
  readonly schema_types?: readonly string[];
}

export type OpenApiFieldMap = Readonly<Record<string, OpenApiFieldMetadata>>;

function openApiValidationGraph(
  value: unknown,
  seen: Map<object, unknown> = new Map(),
): unknown {
  if (value instanceof LosslessNumber) {
    const number = Number(value.toString());
    // Swagger Parser validates structure through native JSON numbers. Preserve
    // magnitude only in the authoritative lossless graph; use a finite value
    // of the same sign if JavaScript cannot represent the JSON token.
    if (number === Number.POSITIVE_INFINITY) return Number.MAX_VALUE;
    if (number === Number.NEGATIVE_INFINITY) return -Number.MAX_VALUE;
    return number;
  }
  if (typeof value !== "object" || value === null) return value;
  const previous = seen.get(value);
  if (previous !== undefined) return previous;
  if (Array.isArray(value)) {
    const output: unknown[] = [];
    seen.set(value, output);
    output.push(...value.map((item) => openApiValidationGraph(item, seen)));
    return output;
  }
  const output: JsonObject = Object.create(null) as JsonObject;
  seen.set(value, output);
  for (const [key, item] of Object.entries(value)) {
    output[key] = openApiValidationGraph(item, seen);
  }
  return output;
}

/**
 * Validate an authoring input without replacing Infrawright's provenance-aware
 * local-ref and schema-flattening logic with a dereferenced object graph.
 */
export async function validateOpenApiDocument(spec: JsonObject): Promise<void> {
  try {
    await SwaggerParser.validate(openApiValidationGraph(spec) as never, {
      mutateInputSchema: false,
      // Multi-file provider contracts retain unresolved relative refs here.
      // Infrawright's existing local-ref mapper remains authoritative and the
      // validator must never acquire files or URLs on its behalf.
      resolve: { external: false, file: false, http: false },
      validate: { schema: true, spec: true },
    });
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : "unknown validation failure";
    throw new TypeError(`OpenAPI validation failed: ${message}`);
  }
}

function objectOrEmpty(value: unknown): JsonObject {
  return isObject(value) ? value : {};
}

function schemaObject(value: unknown): JsonObject {
  if (value === undefined || value === null) return {};
  if (!isObject(value)) throw new TypeError("OpenAPI schema must be an object");
  return value;
}

export function decodeLocalRefToken(token: string): string {
  return token.replaceAll("~1", "/").replaceAll("~0", "~");
}

export function resolveLocalRef(spec: JsonObject, ref: string): unknown {
  if (!ref.startsWith("#/")) {
    throw new TypeError(`only local OpenAPI refs are supported: ${ref}`);
  }
  let node: unknown = spec;
  for (const rawToken of ref.slice(2).split("/")) {
    const token = decodeLocalRefToken(rawToken);
    if (Array.isArray(node)) {
      if (!/^[0-9]+$/u.test(token)) {
        throw new TypeError(`OpenAPI ref ${ref} indexes list with ${JSON.stringify(token)}`);
      }
      const index = Number(token);
      if (index >= node.length) throw new RangeError(`OpenAPI ref ${ref} is out of range`);
      node = node[index];
    } else if (isObject(node) && Object.hasOwn(node, token)) {
      node = node[token];
    } else {
      throw new Error(`OpenAPI ref ${ref} does not exist`);
    }
  }
  return node;
}

export function resolveOpenApiSchema(spec: JsonObject, rawSchema: unknown): JsonObject {
  let schema = schemaObject(rawSchema);
  const seen = new Set<string>();
  while (typeof schema.$ref === "string") {
    const ref = schema.$ref;
    if (seen.has(ref)) throw new TypeError(`recursive OpenAPI ref: ${ref}`);
    seen.add(ref);
    const target = resolveLocalRef(spec, ref);
    if (!isObject(target)) throw new TypeError(`OpenAPI ref ${ref} is not an object`);
    schema = { ...target, ...Object.fromEntries(
      Object.entries(schema).filter(([key]) => key !== "$ref"),
    ) };
  }
  return schema;
}

export function mergeOpenApiSchema(spec: JsonObject, rawSchema: unknown): JsonObject {
  return mergeOpenApiSchemaInternal(spec, rawSchema, new Set(), 0);
}

function mergeOpenApiSchemaInternal(
  spec: JsonObject,
  rawSchema: unknown,
  activeRefs: ReadonlySet<string>,
  depth: number,
): JsonObject {
  if (depth > 64) throw new TypeError("OpenAPI schema recursion limit exceeded");
  let input = schemaObject(rawSchema);
  let refs = activeRefs;
  if (typeof input.$ref === "string") {
    const ref = input.$ref;
    if (activeRefs.has(ref)) throw new TypeError(`recursive OpenAPI ref: ${ref}`);
    const target = resolveLocalRef(spec, ref);
    if (!isObject(target)) throw new TypeError(`OpenAPI ref ${ref} is not an object`);
    refs = new Set([...activeRefs, ref]);
    input = { ...target, ...Object.fromEntries(
      Object.entries(input).filter(([key]) => key !== "$ref"),
    ) };
    if (typeof input.$ref === "string") {
      return mergeOpenApiSchemaInternal(spec, input, refs, depth + 1);
    }
  }
  const schema = { ...input };
  const parts = schema.allOf;
  delete schema.allOf;
  if (!Array.isArray(parts) || parts.length === 0) return schema;
  const merged: JsonObject = {};
  const properties: JsonObject = {};
  const required: string[] = [];
  for (const rawPart of parts) {
    const part = mergeOpenApiSchemaInternal(spec, rawPart, refs, depth + 1);
    for (const [key, value] of Object.entries(part)) {
      if (key === "properties") Object.assign(properties, objectOrEmpty(value));
      else if (key === "required" && Array.isArray(value)) {
        required.push(...value.filter((entry): entry is string => typeof entry === "string"));
      } else if (!Object.hasOwn(merged, key)) merged[key] = value;
    }
  }
  Object.assign(merged, schema);
  Object.assign(properties, objectOrEmpty(schema.properties));
  if (Object.keys(properties).length > 0) merged.properties = properties;
  if (Array.isArray(schema.required)) {
    required.push(...schema.required.filter((entry): entry is string => typeof entry === "string"));
  }
  if (required.length > 0) merged.required = sortedStrings([...new Set(required)]);
  return merged;
}

export function jsonContentSchema(content: unknown): unknown | undefined {
  if (!isObject(content)) return undefined;
  let media = content["application/json"];
  if (media === undefined) {
    for (const name of sortedStrings(Object.keys(content))) {
      const candidate = content[name];
      if (isObject(candidate) && Object.hasOwn(candidate, "schema")) {
        media = candidate;
        break;
      }
    }
  }
  return isObject(media) ? media.schema : undefined;
}

export function successfulResponseSchema(
  spec: JsonObject,
  operation: JsonObject,
): unknown | undefined {
  const responses = objectOrEmpty(operation.responses);
  let response = responses["200"];
  if (response === undefined) {
    const code = sortedStrings(Object.keys(responses)).find((item) => item.startsWith("2"));
    response = code === undefined ? undefined : responses[code];
  }
  if (isObject(response) && typeof response.$ref === "string") {
    response = resolveLocalRef(spec, response.$ref);
  }
  if (!isObject(response)) return undefined;
  return Object.hasOwn(response, "content")
    ? jsonContentSchema(response.content)
    : response.schema;
}

export function requestSchema(spec: JsonObject, operation: JsonObject): unknown | undefined {
  let body = operation.requestBody;
  if (isObject(body) && typeof body.$ref === "string") body = resolveLocalRef(spec, body.$ref);
  if (isObject(body)) {
    const schema = jsonContentSchema(body.content);
    if (schema !== undefined && schema !== null) return schema;
  }
  const parameters = Array.isArray(operation.parameters) ? operation.parameters : [];
  for (let parameter of parameters) {
    if (isObject(parameter) && typeof parameter.$ref === "string") {
      parameter = resolveLocalRef(spec, parameter.$ref);
    }
    if (isObject(parameter) && parameter.in === "body") return parameter.schema;
  }
  return undefined;
}

export function openApiOperation(spec: JsonObject, operationRef: string): JsonObject {
  const colon = operationRef.indexOf(":");
  if (colon < 0) {
    throw new TypeError(`OpenAPI operation must be METHOD:/path, got ${JSON.stringify(operationRef)}`);
  }
  const method = operationRef.slice(0, colon).toLowerCase();
  const path = operationRef.slice(colon + 1);
  const pathItem = objectOrEmpty(objectOrEmpty(spec.paths)[path]);
  const operation = pathItem[method];
  if (!isObject(operation)) {
    throw new Error(`OpenAPI operation ${method.toUpperCase()}:${path} not found`);
  }
  return operation;
}

export function openApiSchemaKind(schema: JsonObject): string | undefined {
  if (typeof schema.type === "string" && schema.type.length > 0) return schema.type;
  if (isObject(schema.properties) || schema.additionalProperties) return "object";
  return undefined;
}

function joinPath(prefix: string, name: string): string {
  return prefix === "" ? name : `${prefix}.${name}`;
}

function recordField(
  fields: Record<string, OpenApiFieldMetadata>,
  path: string,
  schema: JsonObject,
  mode: "read" | "write",
  required = false,
): void {
  const entry: JsonObject = { ...(fields[path] ?? { path }) };
  if (mode === "read") entry.readable = true;
  else {
    entry.writable = true;
    if (required) entry.required = true;
  }
  if (schema.readOnly) entry.read_only = true;
  if (schema.writeOnly) entry.write_only = true;
  const kind = openApiSchemaKind(schema);
  if (kind !== undefined) {
    const types = Array.isArray(entry.schema_types)
      ? entry.schema_types.filter((item): item is string => typeof item === "string")
      : [];
    if (!types.includes(kind)) types.push(kind);
    entry.schema_types = types;
  }
  fields[path] = entry as OpenApiFieldMetadata;
}

export function flattenOpenApiSchema(options: {
  readonly spec: JsonObject;
  readonly schema: unknown;
  readonly fields?: Record<string, OpenApiFieldMetadata>;
  readonly mode: "read" | "write";
  readonly prefix?: string;
  readonly depth?: number;
}): Record<string, OpenApiFieldMetadata> {
  const fields = options.fields ?? {};
  const prefix = options.prefix ?? "";
  const depth = options.depth ?? 0;
  if (depth > 8) return fields;
  const schema = mergeOpenApiSchema(options.spec, options.schema);
  const kind = openApiSchemaKind(schema);
  if (kind === "array") {
    flattenOpenApiSchema({
      ...options,
      depth: depth + 1,
      fields,
      schema: schema.items,
      prefix: prefix === "" ? "" : `${prefix}[]`,
    });
    return fields;
  }
  if (kind !== "object") {
    if (prefix !== "") recordField(fields, prefix, schema, options.mode);
    return fields;
  }
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  const properties = objectOrEmpty(schema.properties);
  for (const rawName of sortedStrings(Object.keys(properties))) {
    const property = mergeOpenApiSchema(options.spec, properties[rawName]);
    const name = snakeName(rawName);
    const path = joinPath(prefix, name);
    recordField(fields, path, property, options.mode, required.has(rawName) || required.has(name));
    const propertyKind = openApiSchemaKind(property);
    if (propertyKind === "object" && isObject(property.properties)) {
      flattenOpenApiSchema({ ...options, depth: depth + 1, fields, prefix: path, schema: property });
    } else if (propertyKind === "array") {
      const items = mergeOpenApiSchema(options.spec, property.items);
      if (openApiSchemaKind(items) === "object") {
        flattenOpenApiSchema({ ...options, depth: depth + 1, fields, prefix: `${path}[]`, schema: items });
      }
    }
  }
  return fields;
}

export function apiMetadataFromOpenApi(
  spec: JsonObject,
  options: {
    readonly readOperations?: readonly string[];
    readonly writeOperations?: readonly string[];
  } = {},
): OpenApiFieldMap {
  const fields: Record<string, OpenApiFieldMetadata> = {};
  for (const ref of options.readOperations ?? []) {
    const schema = successfulResponseSchema(spec, openApiOperation(spec, ref));
    if (schema !== undefined && schema !== null) {
      flattenOpenApiSchema({ fields, mode: "read", schema, spec });
    }
  }
  for (const ref of options.writeOperations ?? []) {
    const schema = requestSchema(spec, openApiOperation(spec, ref));
    if (schema !== undefined && schema !== null) {
      flattenOpenApiSchema({ fields, mode: "write", schema, spec });
    }
  }
  if ((options.writeOperations?.length ?? 0) > 0) {
    for (const entry of Object.values(fields)) {
      if (entry.readable && !entry.writable && !entry.read_only) {
        (entry as JsonObject).response_only = true;
      }
    }
  }
  return fields;
}
