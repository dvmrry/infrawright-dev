import { createHash } from "node:crypto";

const LABEL = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/u;
const SHA256 = /^[0-9a-f]{64}$/u;

function record(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function exactKeys(value, expected) {
  const actual = Object.keys(value).sort();
  return actual.length === expected.length
    && actual.every((key, index) => key === expected[index]);
}

function digestField(digest, value) {
  const bytes = Buffer.from(value, "utf8");
  digest.update(String(bytes.length), "ascii");
  digest.update(":", "ascii");
  digest.update(bytes);
}

/** Digest explicit root boundaries as well as every exact artifact record. */
export function artifactManifestDigest(roots) {
  const digest = createHash("sha256");
  digestField(digest, "infrawright-performance-artifact-manifest-v1");
  for (const root of roots) {
    digestField(digest, "root");
    digestField(digest, root.label);
    digestField(digest, String(root.files.length));
    for (const file of root.files) {
      digestField(digest, "file");
      digestField(digest, file.path);
      digestField(digest, String(file.size_bytes));
      digestField(digest, file.sha256);
    }
  }
  return digest.digest("hex");
}

/** Validate the complete manifest and independently recompute its digest. */
export function validateArtifactManifest(value) {
  if (
    !record(value)
    || !exactKeys(value, ["format", "roots", "tree_sha256"])
    || value.format !== "infrawright-performance-artifact-manifest"
    || !Array.isArray(value.roots)
    || value.roots.length === 0
    || typeof value.tree_sha256 !== "string"
    || !SHA256.test(value.tree_sha256)
  ) {
    throw new Error("manifest root shape is invalid");
  }
  let previousLabel = null;
  for (const root of value.roots) {
    if (
      !record(root)
      || !exactKeys(root, ["files", "label"])
      || typeof root.label !== "string"
      || !LABEL.test(root.label)
      || !Array.isArray(root.files)
      || previousLabel !== null && root.label <= previousLabel
    ) {
      throw new Error("manifest roots are invalid or not uniquely sorted");
    }
    previousLabel = root.label;
    let previousPath = null;
    for (const file of root.files) {
      if (
        !record(file)
        || !exactKeys(file, ["path", "sha256", "size_bytes"])
        || typeof file.path !== "string"
        || file.path === ""
        || file.path.startsWith("/")
        || file.path.includes("\0")
        || typeof file.sha256 !== "string"
        || !SHA256.test(file.sha256)
        || !Number.isSafeInteger(file.size_bytes)
        || file.size_bytes < 0
        || previousPath !== null && file.path <= previousPath
      ) {
        throw new Error("manifest files are invalid or not uniquely sorted");
      }
      previousPath = file.path;
    }
  }
  const recomputed = artifactManifestDigest(value.roots);
  if (recomputed !== value.tree_sha256) {
    throw new Error("manifest digest does not match its roots and files");
  }
  return value;
}
