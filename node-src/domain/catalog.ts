import { ProcessFailure } from "./errors.js";
import type { RootCatalog } from "./types.js";
import { validateRootCatalog, schemaErrorDetails } from "../contracts/validators.js";
import { readRequiredUtf8 } from "../io/files.js";
import { parseControlJson } from "../json/control.js";
import { sortedStrings } from "../json/python-compatible.js";

function assertSortedUnique(values: readonly string[], label: string): void {
  const expected = sortedStrings(new Set(values));
  if (
    expected.length !== values.length
    || expected.some((value, index) => value !== values[index])
  ) {
    throw new ProcessFailure({
      code: "INVALID_ROOT_CATALOG",
      category: "domain",
      message: `${label} must be sorted and unique`,
    });
  }
}

export async function loadRootCatalog(path: string): Promise<RootCatalog> {
  let parsed: unknown;
  try {
    parsed = parseControlJson(await readRequiredUtf8(path, "root catalog"));
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw new ProcessFailure({
      code: "INVALID_ROOT_CATALOG",
      category: "domain",
      message: "root catalog is not valid JSON",
    });
  }
  if (!validateRootCatalog(parsed)) {
    throw new ProcessFailure({
      code: "INVALID_ROOT_CATALOG",
      category: "domain",
      message: "root catalog does not match schema version 1",
      details: schemaErrorDetails(validateRootCatalog.errors),
    });
  }
  const catalog = parsed as RootCatalog;
  assertSortedUnique(catalog.declared_providers, "declared_providers");
  assertSortedUnique(catalog.source_files, "source_files");
  assertSortedUnique(catalog.resources.map((resource) => resource.type), "resources");
  const declaredProviders = new Set(catalog.declared_providers);
  const undeclaredProviders = sortedStrings(new Set(
    catalog.resources
      .map((resource) => resource.provider)
      .filter((provider) => !declaredProviders.has(provider)),
  ));
  if (undeclaredProviders.length > 0) {
    throw new ProcessFailure({
      code: "INVALID_ROOT_CATALOG",
      category: "domain",
      message: `resource providers must be declared: ${undeclaredProviders.join(", ")}`,
    });
  }
  return catalog;
}
