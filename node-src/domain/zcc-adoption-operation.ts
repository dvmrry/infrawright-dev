import { loadZccAdoptionCatalog } from "./zcc-adoption-catalog.js";
import {
  ZCC_ADOPTION_CATALOG_SHA256,
  type ZccAdoptionArtifactSet,
} from "./zcc-adoption-artifacts.js";
import { runZccAdoptionOracle } from "./zcc-adoption-oracle.js";
import { ProcessFailure } from "./errors.js";
import {
  bindZccAdoptionComparisonInputs,
  bindZccBootstrapPullOperationInputs,
  type ZccPullArtifactsOperationOptions,
} from "./zcc-pull-operation.js";
import {
  compareZccAdoptionArtifactDigests,
  type ZccAdoptionArtifactParity,
} from "./zcc-adoption-artifact-parity.js";
import {
  createZccAdoptionOracleAdapters,
  type ZccAdoptionOracleAdapterFactoryOptions,
} from "../io/zcc-adoption-oracle-adapters.js";

export type ZccAdoptionOracleHostAuthority =
  ZccAdoptionOracleAdapterFactoryOptions;

export interface ZccAdoptionOperationHooks {
  /** Test-only seam after the bound-input checkpoint and before the oracle. */
  readonly beforeOracle?: () => void | Promise<void>;
  /** Test-only seam after oracle cleanup and before the final input checkpoint. */
  readonly afterOracle?: () => void | Promise<void>;
}

export interface CompileZccAdoptionArtifactsOperationOptions
  extends ZccPullArtifactsOperationOptions {
  readonly hostAuthority: ZccAdoptionOracleHostAuthority | null;
  readonly adoptionHooks?: ZccAdoptionOperationHooks;
}

function missingAuthority(): never {
  throw new ProcessFailure({
    code: "ZCC_ADOPTION_HOST_NOT_CONFIGURED",
    category: "io",
    message: "the ZCC adoption oracle host authority is not configured",
  });
}

/**
 * Compile one provider-observed, read-only bootstrap candidate.
 *
 * Caller paths, executable selection, credentials, timeouts, and temporary
 * roots never cross the process request. The process host supplies one closed
 * authority object; the existing pull binder supplies every workspace input.
 */
export async function compileZccAdoptionArtifactsOperation(
  options: CompileZccAdoptionArtifactsOperationOptions,
): Promise<ZccAdoptionArtifactSet> {
  const authority = options.hostAuthority;
  if (authority === null) {
    return missingAuthority();
  }
  // Bind the trusted host capability before any caller-workspace I/O. The
  // concrete factory snapshots and closes every option without effects.
  const terraformExecutable = authority.terraformExecutable;
  const adapters = createZccAdoptionOracleAdapters(authority);
  const bound = await bindZccBootstrapPullOperationInputs(options);
  await options.adoptionHooks?.beforeOracle?.();
  await bound.recheckInputs();
  const result = await runZccAdoptionOracle({
    catalog: loadZccAdoptionCatalog(),
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    rawItems: bound.rawItems,
    source: bound.source,
    target: bound.target,
    terraformExecutable,
  }, adapters);

  await options.adoptionHooks?.afterOracle?.();
  await bound.recheckInputs();
  return result;
}

/**
 * Compare one fresh provider-observed candidate with stable external
 * materialized reference artifacts. The provider transaction and reference
 * reads remain effect-free in the caller workspace.
 */
export async function compareZccAdoptionArtifactsOperation(
  options: CompileZccAdoptionArtifactsOperationOptions,
): Promise<ZccAdoptionArtifactParity> {
  const authority = options.hostAuthority;
  if (authority === null) {
    return missingAuthority();
  }
  const terraformExecutable = authority.terraformExecutable;
  const adapters = createZccAdoptionOracleAdapters(authority);
  const bound = await bindZccAdoptionComparisonInputs(options);
  await options.adoptionHooks?.beforeOracle?.();
  await bound.recheckInputs();
  const candidate = await runZccAdoptionOracle({
    catalog: loadZccAdoptionCatalog(),
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    rawItems: bound.rawItems,
    source: bound.source,
    target: bound.target,
    terraformExecutable,
  }, adapters);

  await options.adoptionHooks?.afterOracle?.();
  await bound.recheckInputs();
  return compareZccAdoptionArtifactDigests({
    candidate,
    materialized: bound.materialized,
  });
}
