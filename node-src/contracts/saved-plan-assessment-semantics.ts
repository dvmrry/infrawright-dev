import type { ErrorObject } from "ajv/dist/2020.js";

export const ASSESSMENT_SEMANTICS_KEYWORD =
  "x-infrawright-report-semantics";

type AssessmentStatus = "clean" | "clean_with_tolerated_drift" | "blocked";

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ASSESSMENT_SEMANTICS_KEYWORD}`,
    keyword: ASSESSMENT_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

function derivedStatus(statuses: readonly AssessmentStatus[]): AssessmentStatus {
  return statuses.includes("blocked")
    ? "blocked"
    : statuses.includes("clean_with_tolerated_drift")
    ? "clean_with_tolerated_drift"
    : "clean";
}

export interface AssessmentSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce cross-field invariants that structural JSON Schema cannot express. */
export const validateAssessmentSemantics: AssessmentSemanticValidator = (
  _schema,
  data,
  _parentSchema,
  dataContext,
) => {
  const report = record(data);
  const summary = record(report?.summary);
  const request = record(report?.request);
  const roots = Array.isArray(report?.roots) ? report.roots : null;
  if (report === null || summary === null || request === null || roots === null) {
    delete validateAssessmentSemantics.errors;
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
  const rootStatuses: AssessmentStatus[] = [];
  const rootKeys = new Set<string>();
  const tenantMemberKeys = new Set<string>();
  const checkedTypes = new Set<string>();
  roots.forEach((candidate, index) => {
    const root = record(candidate);
    if (root !== null && typeof root.tenant === "string") {
      if (typeof request.tenant === "string" && root.tenant !== request.tenant) {
        errors.push(error(
          `/roots/${index}/tenant`,
          "request_tenant",
          "root tenant must match the requested tenant",
        ));
      }
      if (typeof root.label === "string") {
        const rootKey = JSON.stringify([root.tenant, root.label]);
        if (rootKeys.has(rootKey)) {
          errors.push(error(
            `/roots/${index}`,
            "root_identity",
            "tenant and root label must be unique within an assessment",
          ));
        }
        rootKeys.add(rootKey);
      }
      if (Array.isArray(root.members)) {
        for (const member of root.members) {
          if (typeof member !== "string") {
            continue;
          }
          checkedTypes.add(member);
          const memberKey = JSON.stringify([root.tenant, member]);
          if (tenantMemberKeys.has(memberKey)) {
            errors.push(error(
              `/roots/${index}/members`,
              "root_membership",
              "a resource type can belong to only one selected root per tenant",
            ));
          }
          tenantMemberKeys.add(memberKey);
        }
      }
    }
    if (
      root === null
      || (
        root.status !== "clean"
        && root.status !== "clean_with_tolerated_drift"
        && root.status !== "blocked"
      )
      || !Array.isArray(root.findings)
    ) {
      return;
    }
    const findings = root.findings.map(record);
    if (findings.some((finding) => finding === null)) {
      return;
    }
    const findingStatuses = findings.map((finding) => finding?.status).filter(
      (status): status is AssessmentStatus => status === "clean"
        || status === "clean_with_tolerated_drift"
        || status === "blocked",
    );
    if (findingStatuses.length !== findings.length) {
      return;
    }
    const blockedFindings = new Map<string, number>();
    for (const finding of findings) {
      if (
        finding?.status !== "blocked"
        || typeof finding.source !== "string"
        || typeof finding.address !== "string"
        || !Array.isArray(finding.paths)
      ) {
        continue;
      }
      for (const findingPath of finding.paths) {
        if (typeof findingPath !== "string") {
          continue;
        }
        const key = JSON.stringify([
          finding.source,
          finding.address,
          findingPath,
        ]);
        blockedFindings.set(key, (blockedFindings.get(key) ?? 0) + 1);
      }
    }
    if (Array.isArray(root.guidance)) {
      root.guidance.forEach((candidateGuidance, guidanceIndex) => {
        const guidance = record(candidateGuidance);
        if (guidance === null) {
          return;
        }
        if (Object.hasOwn(guidance, "sort_key")) {
          errors.push(error(
            `/roots/${index}/guidance/${guidanceIndex}/sort_key`,
            "guidance_join",
            "internal guidance sort keys cannot be emitted",
          ));
        }
        if (
          typeof guidance.source !== "string"
          || typeof guidance.address !== "string"
          || typeof guidance.finding_path !== "string"
        ) {
          return;
        }
        const key = JSON.stringify([
          guidance.source,
          guidance.address,
          guidance.finding_path,
        ]);
        if (blockedFindings.get(key) !== 1) {
          errors.push(error(
            `/roots/${index}/guidance/${guidanceIndex}`,
            "guidance_join",
            "guidance must join exactly one blocked finding path",
          ));
        }
      });
    }
    rootStatuses.push(root.status);
    if (findingStatuses.includes("clean")) {
      errors.push(error(
        `/roots/${index}/findings`,
        "finding_status",
        "classified findings must not use the aggregate clean status",
      ));
    }
    if (root.status !== derivedStatus(findingStatuses)) {
      errors.push(error(
        `/roots/${index}/status`,
        "root_status",
        "root status must be derived from its findings",
      ));
    }
  });

  if (rootStatuses.length === roots.length) {
    const expected = {
      checked: roots.length,
      clean: rootStatuses.filter((status) => status === "clean").length,
      tolerated: rootStatuses.filter((status) => {
        return status === "clean_with_tolerated_drift";
      }).length,
      blocked: rootStatuses.filter((status) => status === "blocked").length,
    };
    for (const field of ["checked", "clean", "tolerated", "blocked"] as const) {
      if (summary[field] !== expected[field]) {
        errors.push(error(
          `/summary/${field}`,
          "summary_count",
          `${field} must equal the count derived from roots`,
        ));
      }
    }
    if (
      summary.status !== "error"
      && summary.status !== derivedStatus(rootStatuses)
    ) {
      errors.push(error(
        "/summary/status",
        "summary_status",
        "summary status must be derived from root statuses",
      ));
    }
  }

  const policy = request.policy;
  const policySha256 = request.policy_sha256;
  if (policy === null && policySha256 !== null) {
    errors.push(error(
      "/request/policy_sha256",
      "policy_evidence",
      "policy evidence requires a requested policy",
    ));
  } else if (
    summary.status !== "error"
    && (policy === null) !== (policySha256 === null)
  ) {
    errors.push(error(
      "/request/policy_sha256",
      "policy_evidence",
      "normal assessment reports require policy bytes and evidence together",
    ));
  }

  const reportError = record(report.error);
  const stalePolicy = Array.isArray(report.stale_policy)
    ? report.stale_policy
    : null;
  const earlyError = summary.status === "error"
    && (
      reportError?.kind === "no_saved_plans"
      || reportError?.kind === "policy_error"
    );
  if (earlyError && roots.length !== 0) {
    errors.push(error(
      "/roots",
      "error_phase",
      `${String(reportError?.kind)} reports cannot contain assessed roots`,
    ));
  }
  if (earlyError && stalePolicy !== null && stalePolicy.length !== 0) {
    errors.push(error(
      "/stale_policy",
      "error_phase",
      `${String(reportError?.kind)} reports cannot contain stale policy entries`,
    ));
  }
  if (reportError?.kind === "policy_error" && typeof policy !== "string") {
    errors.push(error(
      "/request/policy",
      "error_phase",
      "policy_error requires a requested policy",
    ));
  }
  if (
    reportError?.kind === "no_saved_plans"
    && (policy === null) !== (policySha256 === null)
  ) {
    errors.push(error(
      "/request/policy_sha256",
      "error_phase",
      "no_saved_plans requires completed policy evidence",
    ));
  }
  if (
    stalePolicy !== null
    && stalePolicy.length !== 0
    && (policy === null || policySha256 === null)
  ) {
    errors.push(error(
      "/stale_policy",
      "policy_evidence",
      "stale policy entries require bound policy evidence",
    ));
  }
  if (stalePolicy !== null) {
    const staleKeys = new Set<string>();
    stalePolicy.forEach((candidate, index) => {
      const entry = record(candidate);
      if (
        entry === null
        || typeof entry.resource_type !== "string"
        || typeof entry.mode !== "string"
        || typeof entry.path !== "string"
      ) {
        return;
      }
      if (!checkedTypes.has(entry.resource_type)) {
        errors.push(error(
          `/stale_policy/${index}/resource_type`,
          "stale_policy_scope",
          "stale policy resource type must be present in an assessed root",
        ));
      }
      const staleKey = JSON.stringify([
        entry.resource_type,
        entry.mode,
        entry.path,
      ]);
      if (staleKeys.has(staleKey)) {
        errors.push(error(
          `/stale_policy/${index}`,
          "stale_policy_identity",
          "stale policy entries must be unique",
        ));
      }
      staleKeys.add(staleKey);
    });
  }

  if (errors.length === 0) {
    delete validateAssessmentSemantics.errors;
  } else {
    validateAssessmentSemantics.errors = errors;
  }
  return errors.length === 0;
};
