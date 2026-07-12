import {
  requireSupportedZccAdoptionCatalog,
  type ZccAdoptionCatalog,
  type ZccAdoptionCatalogResource,
} from "./zcc-adoption-catalog.js";
import {
  compileZccAdoptionProjection,
  type ZccAdoptionProjectionResult,
  type ZccAdoptionStateObservation,
} from "./zcc-adoption-projection.js";
import { ProcessFailure } from "./errors.js";
import {
  createZccTextArtifact,
  renderZccBootstrapImports,
  renderZccLookupSidecar,
  validateZccBootstrapArtifactMetadata,
  type ZccArtifactTarget,
  type ZccPullResourceType,
  type ZccPullSource,
  type ZccTextArtifact,
} from "./zcc-pull-artifacts.js";
import { isJsonRecord } from "../json/python-equality.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";

export const ZCC_ADOPTION_CATALOG_SHA256 =
  "ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7";

/**
 * Read-only bootstrap output for provider-observed ZCC adoption.
 *
 * The public process operation exposes only this projected, non-sensitive
 * candidate. Provider state remains inside the library boundary and this
 * contract itself has no filesystem behavior.
 */
export interface ZccAdoptionArtifactSet {
  readonly kind: "infrawright.zcc_adoption_artifact_set";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly source: {
    readonly path: string;
    readonly sha256: string;
    readonly size_bytes: number;
  };
  readonly catalog: {
    readonly kind: "infrawright.adoption_catalog";
    readonly schema_version: 1;
    readonly sha256: string;
    readonly sources_sha256: string;
  };
  readonly root: {
    readonly label: string;
    readonly members: readonly string[];
    readonly variable_name: string;
  };
  readonly artifacts: {
    readonly tfvars: ZccTextArtifact;
    readonly imports: ZccTextArtifact;
    readonly lookup: ZccTextArtifact | null;
  };
}

type JsonRecord = Readonly<Record<string, unknown>>;

function fail(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_ZCC_ADOPTION_ARTIFACT_DATA",
    category: "domain",
    message,
  });
}

function catalogResource(
  catalog: ZccAdoptionCatalog,
  resourceType: string,
): ZccAdoptionCatalogResource {
  const resource = catalog.resources.find((entry) => entry.type === resourceType);
  if (resource === undefined) {
    return fail("adoption artifact resource is absent from the supported catalog");
  }
  return resource;
}

function artifactContext(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly catalogSha256: string;
  readonly source: ZccPullSource;
  readonly target: ZccArtifactTarget;
}): {
  readonly catalog: ZccAdoptionCatalog;
  readonly resource: ZccAdoptionCatalogResource;
} {
  const catalog = requireSupportedZccAdoptionCatalog(options.catalog);
  if (options.catalogSha256 !== ZCC_ADOPTION_CATALOG_SHA256) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ZCC_ADOPTION_CATALOG",
      category: "domain",
      message: "artifact compilation requires the exact committed adoption catalog bytes",
    });
  }
  const resource = catalogResource(catalog, options.target.resourceType);
  validateZccBootstrapArtifactMetadata({
    lookupSource: resource.lookup_source,
    resourceType: resource.type,
    source: options.source,
    target: options.target,
  });
  return { catalog, resource };
}

function projectionRecord(value: unknown, label: string): JsonRecord {
  if (!isJsonRecord(value)) {
    return fail(`${label} must be a JSON object`);
  }
  return value;
}

function assertExactProjectionKeys(
  projection: ZccAdoptionProjectionResult,
): readonly string[] {
  const items = projectionRecord(projection.items, "projected items");
  const identities = projectionRecord(
    projection.identities,
    "adoption identities",
  );
  const importIds = projectionRecord(
    projection.import_ids,
    "adoption import identifiers",
  );
  const keys = sortedStrings(Object.keys(items));
  for (const [label, record] of [
    ["identities", identities],
    ["import identifiers", importIds],
  ] as const) {
    const candidate = sortedStrings(Object.keys(record));
    if (
      candidate.length !== keys.length
      || candidate.some((key, index) => key !== keys[index])
    ) {
      fail(`projected items and ${label} must have exactly the same keys`);
    }
  }
  for (const key of keys) {
    if (!isJsonRecord(items[key]) || !isJsonRecord(identities[key])) {
      fail("projected items and adoption identities must contain JSON objects");
    }
    if (typeof importIds[key] !== "string") {
      fail("adoption import identifiers must contain strings");
    }
  }
  return keys;
}

function assertProjectionBinding(
  projection: ZccAdoptionProjectionResult,
  catalog: ZccAdoptionCatalog,
  resource: ZccAdoptionCatalogResource,
): void {
  if (
    projection.kind !== "infrawright.zcc_adoption_projection"
    || projection.schema_version !== 1
    || projection.product !== "zcc"
    || projection.resource_type !== resource.type
    || projection.catalog.kind !== catalog.kind
    || projection.catalog.schema_version !== catalog.schema_version
    || projection.catalog.sources_sha256 !== catalog.sources_sha256
  ) {
    fail("adoption projection is not bound to the selected resource catalog");
  }
}

function importIdMap(
  importIds: Readonly<Record<string, string>>,
  keys: readonly string[],
): ReadonlyMap<string, string> {
  const output = new Map<string, string>();
  for (const key of keys) {
    const importId = importIds[key];
    if (importId === undefined) {
      return fail("adoption import identifier disappeared during compilation");
    }
    output.set(key, importId);
  }
  return output;
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableCopy(entry)));
  }
  if (isJsonRecord(value)) {
    const output: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy(value[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

function renderZccAdoptionArtifactSet(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly catalogSha256: string;
  readonly projection: ZccAdoptionProjectionResult;
  readonly source: ZccPullSource;
  readonly target: ZccArtifactTarget;
}, context: {
  readonly catalog: ZccAdoptionCatalog;
  readonly resource: ZccAdoptionCatalogResource;
}): ZccAdoptionArtifactSet {
  const { catalog, resource } = context;
  assertProjectionBinding(options.projection, catalog, resource);
  const keys = assertExactProjectionKeys(options.projection);
  const imports = renderZccBootstrapImports(
    resource.type,
    importIdMap(options.projection.import_ids, keys),
  );
  const tfvars = renderPythonLosslessArtifactJson({
    [options.target.variableName]: options.projection.items,
  });
  const lookup = resource.lookup_source === null
    ? null
    : renderZccLookupSidecar({
        allowDuplicateImportIds: false,
        identities: options.projection.identities,
        items: options.projection.items,
        nameField: resource.lookup_source.name_field,
      });

  return immutableCopy({
    kind: "infrawright.zcc_adoption_artifact_set",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: resource.type as ZccPullResourceType,
    tenant: options.target.tenant,
    source: {
      path: options.source.path,
      sha256: options.source.sha256,
      size_bytes: options.source.size_bytes,
    },
    catalog: {
      kind: catalog.kind,
      schema_version: catalog.schema_version,
      sha256: options.catalogSha256,
      sources_sha256: catalog.sources_sha256,
    },
    root: {
      label: options.target.rootLabel,
      members: [...options.target.rootMembers],
      variable_name: options.target.variableName,
    },
    artifacts: {
      tfvars: createZccTextArtifact(
        options.target.configPath,
        "application/json",
        tfvars,
      ),
      imports: createZccTextArtifact(
        options.target.importsPath,
        "text/x-hcl",
        imports,
      ),
      lookup: lookup === null || options.target.lookupPath === null
        ? null
        : createZccTextArtifact(
            options.target.lookupPath,
            "application/json",
            lookup,
          ),
    },
  }) as ZccAdoptionArtifactSet;
}

/**
 * Project provider-observed ZCC state and compile its safe bootstrap bytes.
 *
 * No credentials, filesystem effects, or subprocesses cross this seam. Raw
 * provider state remains an in-process library input and is discarded before
 * the artifact set is returned; it is never accepted by a process request.
 */
export function compileZccAdoptionArtifactSet(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly catalogSha256: string;
  readonly observedStates: readonly ZccAdoptionStateObservation[];
  readonly rawItems: readonly unknown[];
  readonly source: ZccPullSource;
  readonly target: ZccArtifactTarget;
}): ZccAdoptionArtifactSet {
  const context = artifactContext(options);
  const projection = compileZccAdoptionProjection({
    catalog: context.catalog,
    observedStates: options.observedStates,
    rawItems: options.rawItems,
    resourceType: options.target.resourceType,
  });
  return renderZccAdoptionArtifactSet({
    catalog: options.catalog,
    catalogSha256: options.catalogSha256,
    projection,
    source: options.source,
    target: options.target,
  }, context);
}
