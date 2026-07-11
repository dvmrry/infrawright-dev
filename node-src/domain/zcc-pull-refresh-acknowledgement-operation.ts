import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  compilePreparedZccPullRefreshRawCandidate,
  prepareZccPullRefreshParity,
} from "./zcc-pull-operation.js";
import {
  snapshotZccPullRefreshBindingEvidence,
  type ZccPullRefreshParity,
  type ZccPullRefreshParityContext,
} from "./zcc-pull-refresh-parity.js";
import { zccPullRefreshParityRequestSha } from "./zcc-pull-refresh-fingerprints.js";
import {
  acknowledgeReadyZccPullRefresh,
  snapshotZccPullRefreshMaterializationAssertion,
  snapshotZccPullRefreshPublicationReceipt,
  type ZccPullRefreshAcknowledgement,
  type ZccPullRefreshAcknowledgementHooks,
  type ZccPullRefreshMaterialization,
} from "./zcc-pull-refresh-materialization.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";

export type { ZccPullRefreshAcknowledgement } from "./zcc-pull-refresh-materialization.js";

const SUPPORTED_ZCC_RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

export interface ZccPullRefreshAcknowledgementOperationHooks
  extends ZccPullRefreshAcknowledgementHooks {}

export interface ZccPullRefreshExternalApplyAcknowledgement {
  readonly kind: "trusted_pipeline_assertion";
  readonly statement: "terraform_apply_succeeded";
}

interface PrimitiveAcknowledgementRequest {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly publication: ZccPullRefreshMaterialization;
  readonly policy: "retire_exact_after_external_acknowledgement";
  readonly acknowledgement: ZccPullRefreshExternalApplyAcknowledgement;
  readonly outputRoot: string;
  readonly allowExternalApplyAcknowledgement: boolean;
  readonly hooks?: ZccPullRefreshAcknowledgementOperationHooks;
}

function fail(
  code: string,
  message: string,
  category: "request" | "domain" | "io" | "internal" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function inertRecord(value: unknown): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement input must be an object",
      "request",
    );
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement input must be plain data",
      "request",
    );
  }
  return value as Readonly<Record<string, unknown>>;
}

function ownValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
  optional = false,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined && optional) {
    return undefined;
  }
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement input must be inert data",
      "request",
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
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement strings are invalid",
      "request",
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

function acknowledgementSnapshot(
  value: unknown,
): ZccPullRefreshExternalApplyAcknowledgement {
  const acknowledgement = inertRecord(value);
  const keys = Reflect.ownKeys(acknowledgement);
  if (
    keys.length !== 2
    || !keys.every((key) => typeof key === "string")
    || !keys.includes("kind")
    || !keys.includes("statement")
  ) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "external apply acknowledgement must contain exactly kind and statement",
      "request",
    );
  }
  const kind = ownValue(acknowledgement, "kind");
  const statement = ownValue(acknowledgement, "statement");
  if (
    kind !== "trusted_pipeline_assertion"
    || statement !== "terraform_apply_succeeded"
  ) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "external apply acknowledgement statement is unsupported",
      "request",
    );
  }
  return Object.freeze({ kind, statement });
}

function optionalHook(
  value: Readonly<Record<string, unknown>>,
  key: string,
): (() => void | Promise<void>) | undefined {
  const candidate = ownValue(value, key, true);
  if (candidate === undefined) {
    return undefined;
  }
  if (typeof candidate !== "function") {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement hooks must be functions",
      "request",
    );
  }
  return candidate as () => void | Promise<void>;
}

function optionalBeforeUnlinkHook(
  value: Readonly<Record<string, unknown>>,
): NonNullable<ZccPullRefreshAcknowledgementOperationHooks["beforeUnlink"]>
  | undefined {
  const candidate = ownValue(value, "beforeUnlink", true);
  if (candidate === undefined) {
    return undefined;
  }
  if (typeof candidate !== "function") {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement hooks must be functions",
      "request",
    );
  }
  return candidate as NonNullable<
    ZccPullRefreshAcknowledgementOperationHooks["beforeUnlink"]
  >;
}

function hooksSnapshot(
  value: unknown,
): ZccPullRefreshAcknowledgementOperationHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const beforeUnlink = optionalBeforeUnlinkHook(hooks);
  const afterMoveRemove = optionalHook(hooks, "afterMoveRemove");
  const beforeMarkerRemove = optionalHook(hooks, "beforeMarkerRemove");
  const afterMarkerRemove = optionalHook(hooks, "afterMarkerRemove");
  const beforeFinalCas = optionalHook(hooks, "beforeFinalCas");
  return Object.freeze({
    ...(beforeUnlink === undefined ? {} : { beforeUnlink }),
    ...(afterMoveRemove === undefined ? {} : { afterMoveRemove }),
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
  readonly publication: ZccPullRefreshMaterialization;
  readonly policy: "retire_exact_after_external_acknowledgement";
  readonly acknowledgement: ZccPullRefreshExternalApplyAcknowledgement;
  readonly outputRoot: string;
  readonly allowExternalApplyAcknowledgement: boolean;
  readonly hooks?: ZccPullRefreshAcknowledgementOperationHooks;
}): PrimitiveAcknowledgementRequest {
  const input = inertRecord(options as unknown);
  const resourceType = primitiveString(ownValue(input, "resourceType"));
  if (!(SUPPORTED_ZCC_RESOURCES as readonly string[]).includes(resourceType)) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "unsupported ZCC refresh acknowledgement resource",
      "request",
    );
  }
  const allowExternalApplyAcknowledgement = ownValue(
    input,
    "allowExternalApplyAcknowledgement",
  );
  if (typeof allowExternalApplyAcknowledgement !== "boolean") {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "external apply acknowledgement capability must be boolean",
      "request",
    );
  }
  const policy = ownValue(input, "policy");
  if (policy !== "retire_exact_after_external_acknowledgement") {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_INPUT",
      "refresh acknowledgement retirement policy is unsupported",
      "request",
    );
  }
  const acknowledgement = acknowledgementSnapshot(
    ownValue(input, "acknowledgement"),
  );
  const hooks = hooksSnapshot(ownValue(input, "hooks", true));
  return Object.freeze({
    context: contextSnapshot(ownValue(input, "context")),
    tenant: primitiveString(ownValue(input, "tenant")),
    resourceType: resourceType as ZccPullResourceType,
    assertion: snapshotZccPullRefreshMaterializationAssertion(
      ownValue(input, "assertion"),
    ),
    publication: snapshotZccPullRefreshPublicationReceipt(
      ownValue(input, "publication"),
    ),
    policy,
    acknowledgement,
    outputRoot: primitiveString(ownValue(input, "outputRoot")),
    allowExternalApplyAcknowledgement,
    ...(hooks === undefined ? {} : { hooks }),
  });
}

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

/** Retire one exact published ZCC move after a trusted pipeline acknowledgement. */
export async function acknowledgeZccPullRefreshOperation(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly publication: ZccPullRefreshMaterialization;
  readonly policy: "retire_exact_after_external_acknowledgement";
  readonly acknowledgement: ZccPullRefreshExternalApplyAcknowledgement;
  readonly outputRoot: string;
  readonly allowExternalApplyAcknowledgement: boolean;
  readonly hooks?: ZccPullRefreshAcknowledgementOperationHooks;
}): Promise<ZccPullRefreshAcknowledgement> {
  const request = snapshotRequest(options);
  // The host capability gates the caller's unauthenticated apply statement. It
  // must be checked before preparation reads deployment, catalog, source, or
  // artifact paths. The result remains explicit that apply was not observed.
  if (request.allowExternalApplyAcknowledgement !== true) {
    return fail(
      "EXTERNAL_APPLY_ACKNOWLEDGEMENT_REQUIRED",
      "refresh retirement requires the trusted external-apply acknowledgement capability",
      "request",
    );
  }
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
    || request.publication.tenant !== request.tenant
    || request.publication.resource_type !== request.resourceType
    || request.publication.status !== "awaiting_apply"
    || request.publication.verification.candidate_request_sha256 !== requestSha
    || request.publication.verification.assertion_sha256
      !== request.assertion.assertion_sha256
    || request.publication.verification.baseline_fingerprint_sha256
      !== request.assertion.candidate.baseline_fingerprint_sha256
    || request.publication.verification.transition_sha256
      !== request.assertion.candidate.transition_sha256
  ) {
    return fail(
      "INVALID_REFRESH_ACKNOWLEDGEMENT_ASSERTION",
      "refresh acknowledgement request does not join its assertion and publication",
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
  const transaction = await compilePreparedZccPullRefreshRawCandidate(prepared);
  const currentBinding = async () => snapshotZccPullRefreshBindingEvidence({
    context: request.context,
    tenant: request.tenant,
    resourceType: request.resourceType,
    binding: transaction.binding,
  });
  return acknowledgeReadyZccPullRefresh({
    outputRoot: request.outputRoot,
    pathBase: transaction.pathBase,
    candidate: transaction.result,
    assertion: request.assertion,
    publication: request.publication,
    policy: request.policy,
    acknowledgement: request.acknowledgement,
    expectedBinding,
    currentBinding,
    recheckImmutableInputs: transaction.recheckImmutableInputs,
    allowExternalApplyAcknowledgement:
      request.allowExternalApplyAcknowledgement,
    ...(request.hooks === undefined ? {} : { hooks: request.hooks }),
  });
}
