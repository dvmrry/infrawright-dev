export type ErrorCategory = "request" | "domain" | "io" | "internal";

export class ProcessFailure extends Error {
  readonly code: string;
  readonly category: ErrorCategory;
  readonly retryable: boolean;
  readonly details: readonly ErrorDetail[];

  constructor(options: {
    code: string;
    category: ErrorCategory;
    message: string;
    retryable?: boolean;
    details?: readonly ErrorDetail[];
  }) {
    super(options.message);
    this.name = "ProcessFailure";
    this.code = options.code;
    this.category = options.category;
    this.retryable = options.retryable ?? false;
    this.details = options.details ?? [];
  }
}

export interface ErrorDetail {
  readonly path: string;
  readonly code: string;
  readonly message: string;
}
