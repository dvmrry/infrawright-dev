import { pythonLower151 } from "../json/python-lower-151.js";

/** Match Python's snake-case normalization used by pull/adoption identities. */
export function snakeName(name: string): string {
  const half = name.replace(/([^\n])([A-Z][a-z]+)/gu, "$1_$2");
  return pythonLower151(
    half.replace(/([a-z0-9])([A-Z])/gu, "$1_$2"),
  );
}

/** Match the stable ASCII Terraform map-key slug used by Python. */
export function slugifyTransformKey(value: string): string {
  return pythonLower151(value)
    .replace(/[^a-z0-9]+/gu, "_")
    .replace(/^_+|_+$/gu, "");
}
