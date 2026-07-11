import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  compilePreparedZccPullRefreshRawCandidate,
  prepareZccPullRefreshParity,
  type ZccPullOperationHooks,
} from "./zcc-pull-operation.js";
import {
  snapshotZccPullRefreshBindingEvidence,
  type ZccPullRefreshParity,
  type ZccPullRefreshParityContext,
} from "./zcc-pull-refresh-parity.js";
import { zccPullRefreshParityRequestSha } from "./zcc-pull-refresh-fingerprints.js";
import {
  materializeReadyZccPullRefresh,
  snapshotZccPullRefreshMaterializationAssertion,
  type ZccPullRefreshMaterialization,
  type ZccPullRefreshMaterializationHooks,
} from "./zcc-pull-refresh-materialization.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";

const SUPPORTED_ZCC_RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

export interface ZccPullRefreshPublisherOperationHooks
  extends ZccPullRefreshMaterializationHooks {
  readonly sourceRead?: NonNullable<ZccPullOperationHooks["sourceRead"]>;
  readonly afterInputsBound?: NonNullable<
    ZccPullOperationHooks["afterInputsBound"]
  >;
  readonly beforeFinalRecheck?: NonNullable<
    ZccPullOperationHooks["beforeFinalRecheck"]
  >;
}

interface PrimitivePublisherRequest {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly outputRoot: string;
  readonly hooks?: ZccPullRefreshPublisherOperationHooks;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function inertRecord(value: unknown): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization input must be an object",
    );
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization input must be plain data",
    );
  }
  return value as Readonly<Record<string, unknown>>;
}

function ownValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization input must be inert data",
    );
  }
  return descriptor.value;
}

function optionalOwnValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) {
    return undefined;
  }
  if (!("value" in descriptor)) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization input must be inert data",
    );
  }
  return descriptor.value;
}

function primitiveString(value: unknown): string {
  if (
    typeof value !== "string"
    || !value.isWellFormed()
    || value.includes("\0")
    || Buffer.byteLength(value, "utf8") > 4096
  ) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization strings are invalid",
    );
  }
  return value;
}

function contextSnapshot(value: unknown): ZccPullRefreshParityContext {
  const context = inertRecord(value);
  return Object.freeze({
    workspace: primitiveString(ownValue(context, "workspace")),
    deployment: primitiveString(ownValue(context, "deployment")),
    root_catalog: primitiveString(ownValue(context, "root_catalog")),
  });
}

function optionalHook<Hook extends (...args: never[]) => void | Promise<void>>(
  value: Readonly<Record<string, unknown>>,
  key: string,
): Hook | undefined {
  const candidate = optionalOwnValue(value, key);
  if (candidate === undefined) {
    return undefined;
  }
  if (typeof candidate !== "function") {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "refresh materialization hooks must be functions",
    );
  }
  return candidate as Hook;
}

function stableReadHooksSnapshot(value: unknown): ZccPullOperationHooks["sourceRead"] {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const afterOpen = optionalHook(hooks, "afterOpen");
  const beforeFinalStat = optionalHook(hooks, "beforeFinalStat");
  return Object.freeze({
    ...(afterOpen === undefined ? {} : { afterOpen }),
    ...(beforeFinalStat === undefined ? {} : { beforeFinalStat }),
  });
}

function hooksSnapshot(
  value: unknown,
): ZccPullRefreshPublisherOperationHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const sourceRead = stableReadHooksSnapshot(optionalOwnValue(hooks, "sourceRead"));
  const afterInputsBound = optionalHook(hooks, "afterInputsBound");
  const beforeFinalRecheck = optionalHook(hooks, "beforeFinalRecheck");
  const afterBound = optionalHook(hooks, "afterBound");
  const afterStage = optionalHook<
    NonNullable<ZccPullRefreshMaterializationHooks["afterStage"]>
  >(hooks, "afterStage");
  const beforeMarkerLink = optionalHook(hooks, "beforeMarkerLink");
  const afterMarkerLink = optionalHook(hooks, "afterMarkerLink");
  const afterMarkerSync = optionalHook(hooks, "afterMarkerSync");
  const beforePublish = optionalHook<
    NonNullable<ZccPullRefreshMaterializationHooks["beforePublish"]>
  >(hooks, "beforePublish");
  const afterPublish = optionalHook<
    NonNullable<ZccPullRefreshMaterializationHooks["afterPublish"]>
  >(hooks, "afterPublish");
  const afterPublishParentSync = optionalHook<
    NonNullable<ZccPullRefreshMaterializationHooks["afterPublishParentSync"]>
  >(hooks, "afterPublishParentSync");
  const beforeMarkerRemove = optionalHook(hooks, "beforeMarkerRemove");
  const afterMarkerRemove = optionalHook(hooks, "afterMarkerRemove");
  const beforeFinalCas = optionalHook(hooks, "beforeFinalCas");
  return Object.freeze({
    ...(sourceRead === undefined ? {} : { sourceRead }),
    ...(afterInputsBound === undefined ? {} : { afterInputsBound }),
    ...(beforeFinalRecheck === undefined ? {} : { beforeFinalRecheck }),
    ...(afterBound === undefined ? {} : { afterBound }),
    ...(afterStage === undefined ? {} : { afterStage }),
    ...(beforeMarkerLink === undefined ? {} : { beforeMarkerLink }),
    ...(afterMarkerLink === undefined ? {} : { afterMarkerLink }),
    ...(afterMarkerSync === undefined ? {} : { afterMarkerSync }),
    ...(beforePublish === undefined ? {} : { beforePublish }),
    ...(afterPublish === undefined ? {} : { afterPublish }),
    ...(afterPublishParentSync === undefined
      ? {}
      : { afterPublishParentSync }),
    ...(beforeMarkerRemove === undefined ? {} : { beforeMarkerRemove }),
    ...(afterMarkerRemove === undefined ? {} : { afterMarkerRemove }),
    ...(beforeFinalCas === undefined ? {} : { beforeFinalCas }),
  });
}

function snapshotRequest(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly outputRoot: string;
  readonly hooks?: ZccPullRefreshPublisherOperationHooks;
}): PrimitivePublisherRequest {
  const input = inertRecord(options as unknown);
  const resourceType = primitiveString(ownValue(input, "resourceType"));
  if (!(SUPPORTED_ZCC_RESOURCES as readonly string[]).includes(resourceType)) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_INPUT",
      "unsupported ZCC refresh materialization resource",
    );
  }
  const assertion = snapshotZccPullRefreshMaterializationAssertion(
    ownValue(input, "assertion"),
  );
  const hooks = hooksSnapshot(optionalOwnValue(input, "hooks"));
  return Object.freeze({
    context: contextSnapshot(ownValue(input, "context")),
    tenant: primitiveString(ownValue(input, "tenant")),
    resourceType: resourceType as ZccPullResourceType,
    assertion,
    outputRoot: primitiveString(ownValue(input, "outputRoot")),
    ...(hooks === undefined ? {} : { hooks }),
  });
}

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

/** Recompile and publish one independently attested refresh transition. */
export async function materializeZccPullRefreshOperation(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly outputRoot: string;
  readonly hooks?: ZccPullRefreshPublisherOperationHooks;
}): Promise<ZccPullRefreshMaterialization> {
  // Snapshot and join every direct argument before the first filesystem read.
  const request = snapshotRequest(options);
  const expectedBinding = request.assertion.seed.bindings.candidate;
  const requestSha = zccPullRefreshParityRequestSha({
    context: request.context,
    tenant: request.tenant,
    resourceType: request.resourceType,
  });
  if (
    request.assertion.tenant !== request.tenant
    || request.assertion.resource_type !== request.resourceType
    || request.assertion.status !== "ready"
    || request.assertion.candidate.status !== "ready"
    || request.assertion.parity.status !== "equal"
    || expectedBinding.request_sha256 !== requestSha
  ) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_ASSERTION",
      "refresh materialization request does not match its assertion",
    );
  }

  const prepared = await prepareZccPullRefreshParity({
    workspace: request.context.workspace,
    deploymentPath: resolveContextPath(
      request.context.workspace,
      request.context.deployment,
    ),
    catalogPath: resolveContextPath(
      request.context.workspace,
      request.context.root_catalog,
    ),
    tenant: request.tenant,
    resourceType: request.resourceType,
  });
  const transaction = await compilePreparedZccPullRefreshRawCandidate(
    prepared,
    request.hooks,
  );
  const currentBinding = async () => snapshotZccPullRefreshBindingEvidence({
    context: request.context,
    tenant: request.tenant,
    resourceType: request.resourceType,
    binding: transaction.binding,
  });
  return materializeReadyZccPullRefresh({
    outputRoot: request.outputRoot,
    pathBase: transaction.pathBase,
    candidate: transaction.result,
    assertion: request.assertion,
    expectedBinding,
    currentBinding,
    recheckImmutableInputs: transaction.recheckImmutableInputs,
    ...(request.hooks === undefined ? {} : { hooks: request.hooks }),
  });
}
