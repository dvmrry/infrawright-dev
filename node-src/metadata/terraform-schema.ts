import {
  isIntegerJsonNumber,
  isObject,
  type JsonObject,
} from "./validation.js";
import { sortedStrings } from "../json/python-compatible.js";

export type TerraformPrimitiveEncoding = "bool" | "number" | "string";
export type TerraformTypeEncoding =
  | TerraformPrimitiveEncoding
  | readonly ["list" | "map" | "set", TerraformTypeEncoding]
  | readonly ["object", Readonly<Record<string, TerraformTypeEncoding>>];

export interface TerraformClassifiedAttributes {
  readonly required: readonly string[];
  readonly optional: readonly string[];
  readonly computedOnly: readonly string[];
}

function schemaError(message: string): never {
  throw new TypeError(message);
}

export function terraformRequireObject(value: unknown, label: string): JsonObject {
  if (!isObject(value)) schemaError(`${label} must be an object`);
  return value;
}

export function terraformBlockForSchema(
  schema: JsonObject,
  label: string,
): JsonObject {
  return terraformRequireObject(schema.block, `${label}.block`);
}

export function terraformAttributesForBlock(
  block: JsonObject,
  label: string,
): JsonObject {
  const value = block.attributes;
  if (value === undefined || value === null) return {};
  return terraformRequireObject(value, `${label}.attributes`);
}

export function terraformBlockTypesForBlock(
  block: JsonObject,
  label: string,
): JsonObject {
  const value = block.block_types;
  if (value === undefined || value === null) return {};
  return terraformRequireObject(value, `${label}.block_types`);
}

export function terraformBooleanField(value: JsonObject, name: string): boolean {
  return value[name] === true;
}

export function terraformClassifyAttributes(
  block: JsonObject,
  label: string,
): TerraformClassifiedAttributes {
  const required: string[] = [];
  const optional: string[] = [];
  const computedOnly: string[] = [];
  const attributes = terraformAttributesForBlock(block, label);
  for (const name of sortedStrings(Object.keys(attributes))) {
    const attribute = terraformRequireObject(
      attributes[name],
      `${label}.attributes.${name}`,
    );
    if (
      terraformBooleanField(attribute, "deprecated")
      && !terraformBooleanField(attribute, "required")
    ) {
      computedOnly.push(name);
    } else if (terraformBooleanField(attribute, "required")) {
      required.push(name);
    } else if (terraformBooleanField(attribute, "optional")) {
      optional.push(name);
    } else {
      computedOnly.push(name);
    }
  }
  return { required, optional, computedOnly };
}

export function terraformResourceInputAttributes(
  block: JsonObject,
  label: string,
): TerraformClassifiedAttributes {
  const classified = terraformClassifyAttributes(block, label);
  const id = terraformAttributesForBlock(block, label).id;
  if (
    classified.optional.includes("id")
    && id !== undefined
    && terraformBooleanField(
      terraformRequireObject(id, `${label}.attributes.id`),
      "computed",
    )
  ) {
    return {
      required: classified.required,
      optional: classified.optional.filter((name) => name !== "id"),
      computedOnly: [...classified.computedOnly, "id"],
    };
  }
  return classified;
}

function terraformEncoding(
  value: unknown,
  label: string,
): TerraformTypeEncoding {
  if (value === "bool" || value === "number" || value === "string") {
    return value;
  }
  if (!Array.isArray(value) || value.length !== 2) {
    return schemaError(`unsupported type encoding at ${label}`);
  }
  const [kind, rawInner] = value;
  if (kind === "object") {
    const object = terraformRequireObject(rawInner, `${label}[1]`);
    const members: Record<string, TerraformTypeEncoding> = Object.create(null) as Record<
      string,
      TerraformTypeEncoding
    >;
    for (const name of sortedStrings(Object.keys(object))) {
      members[name] = terraformEncoding(object[name], `${label}[1].${name}`);
    }
    return ["object", members];
  }
  if (kind === "list" || kind === "map" || kind === "set") {
    return [kind, terraformEncoding(rawInner, `${label}[1]`)];
  }
  return schemaError(`unsupported type encoding at ${label}`);
}

function terraformNestedTypeEncoding(
  value: unknown,
  label: string,
): TerraformTypeEncoding {
  const nestedType = terraformRequireObject(value, label);
  const attributes = terraformAttributesForBlock(nestedType, label);
  const members: Record<string, TerraformTypeEncoding> = Object.create(null) as Record<
    string,
    TerraformTypeEncoding
  >;
  for (const name of sortedStrings(Object.keys(attributes))) {
    const attribute = terraformRequireObject(
      attributes[name],
      `${label}.attributes.${name}`,
    );
    if (
      terraformBooleanField(attribute, "deprecated")
      && !terraformBooleanField(attribute, "required")
    ) {
      continue;
    }
    if (
      terraformBooleanField(attribute, "required")
      || terraformBooleanField(attribute, "optional")
    ) {
      members[name] = terraformAttributeType(
        attribute,
        `${label}.attributes.${name}`,
      );
    }
  }
  const objectEncoding = ["object", members] as const;
  const mode = nestedType.nesting_mode;
  if (mode === "single") return objectEncoding;
  if (mode === "list" || mode === "map" || mode === "set") {
    return [mode, objectEncoding];
  }
  return schemaError(`unsupported nested_type nesting_mode ${JSON.stringify(mode)}`);
}

export function terraformAttributeType(
  attribute: JsonObject,
  label: string,
): TerraformTypeEncoding {
  if (Object.hasOwn(attribute, "type")) {
    return terraformEncoding(attribute.type, `${label}.type`);
  }
  if (Object.hasOwn(attribute, "nested_type")) {
    return terraformNestedTypeEncoding(attribute.nested_type, `${label}.nested_type`);
  }
  return schemaError(`attribute has no type or nested_type: ${label}`);
}

export function terraformBlockIsSingle(blockType: JsonObject): boolean {
  const maxItems = blockType.max_items;
  return blockType.nesting_mode === "single"
    || (isIntegerJsonNumber(maxItems) && maxItems.toString() === "1");
}

export function terraformBlockHasInputs(
  block: JsonObject,
  label: string,
): boolean {
  const classified = terraformClassifyAttributes(block, label);
  if (classified.required.length > 0 || classified.optional.length > 0) {
    return true;
  }
  const nested = terraformBlockTypesForBlock(block, label);
  return sortedStrings(Object.keys(nested)).some((name) => {
    const blockType = terraformRequireObject(
      nested[name],
      `${label}.block_types.${name}`,
    );
    const child = terraformRequireObject(
      blockType.block,
      `${label}.block_types.${name}.block`,
    );
    return terraformBlockHasInputs(child, `${label}.block_types.${name}.block`);
  });
}

export function terraformInputBlockTypes(
  block: JsonObject,
  label: string,
): ReadonlyMap<string, JsonObject> {
  const output = new Map<string, JsonObject>();
  const nested = terraformBlockTypesForBlock(block, label);
  for (const name of sortedStrings(Object.keys(nested))) {
    const blockType = terraformRequireObject(
      nested[name],
      `${label}.block_types.${name}`,
    );
    const child = terraformRequireObject(
      blockType.block,
      `${label}.block_types.${name}.block`,
    );
    if (terraformBlockHasInputs(child, `${label}.block_types.${name}.block`)) {
      output.set(name, blockType);
    }
  }
  return output;
}
