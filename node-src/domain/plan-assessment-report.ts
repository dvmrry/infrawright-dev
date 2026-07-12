import { ProcessFailure } from "./errors.js";
import {
  runSavedPlanAssessment,
  SavedPlanAssessmentFailure,
  type SavedPlanAssessmentOptions,
} from "./plan-assessment.js";
import {
  buildSavedPlanAssessmentErrorReport,
  buildSavedPlanAssessmentReport,
  type AssessmentMode,
  type AssessmentReportRequest,
  type SavedPlanAssessmentReport,
} from "./plan-report.js";

export interface SavedPlanAssessmentReportOutcome {
  readonly report: SavedPlanAssessmentReport;
  readonly failure: SavedPlanAssessmentFailure | null;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

/** Build the existing versioned report synchronously inside the evidence window. */
export async function assessSavedPlansReport(options: {
  readonly assessment: SavedPlanAssessmentOptions;
  readonly mode: AssessmentMode;
  readonly request: AssessmentReportRequest;
}): Promise<SavedPlanAssessmentReportOutcome> {
  const mode = options.mode;
  const request = {
    tenant: options.request.tenant,
    selectors: [...options.request.selectors],
    policy: options.request.policy,
  };
  if (
    (mode === "assert-clean"
      && (request.policy !== null || options.assessment.policyPath !== null))
    || (mode === "assert-adoptable"
      && (request.policy === null) !== (options.assessment.policyPath === null))
  ) {
    fail("INVALID_ASSESSMENT_REQUEST", "assessment mode and policy input disagree");
  }
  try {
    const report = await runSavedPlanAssessment(
      options.assessment,
      (core) => buildSavedPlanAssessmentReport({
        mode,
        request,
        core,
      }),
    );
    return { report, failure: null };
  } catch (error: unknown) {
    if (!(error instanceof SavedPlanAssessmentFailure)) {
      throw error;
    }
    return {
      report: buildSavedPlanAssessmentErrorReport({
        mode,
        request,
        partial: error.partial,
        error: { kind: error.reportKind, message: error.message },
      }),
      failure: error,
    };
  }
}
