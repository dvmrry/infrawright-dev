import { comparePythonStrings } from "../json/python-compatible.js";

export type PerformanceStatus = "failed" | "skipped" | "success";
export type HttpRequestClassification = "action" | "authentication" | "detail" | "list";

export interface PerformanceClock {
  now(): number;
}

export interface HttpRequestPerformanceContext {
  readonly classification: HttpRequestClassification;
  readonly endpointFamily: string;
  readonly phase: string;
  readonly product?: string;
  readonly resourceFamily?: string;
}

export interface PerformanceSpanInput {
  readonly durationMs: number;
  readonly phase: string;
  readonly status: PerformanceStatus;
  readonly authIdentity?: string;
  readonly correctedPlan?: boolean;
  readonly instances?: number;
  readonly logicalRequests?: number;
  readonly oracleStateSource?: "accepted-plan" | "applied-state";
  readonly pages?: number;
  readonly product?: string;
  readonly resourceFamily?: string;
  readonly terraformCommands?: number;
}

interface StoredHttpGroup {
  readonly classification: HttpRequestClassification;
  readonly durations: number[];
  readonly endpointFamily: string;
  readonly phase: string;
  readonly product?: string;
  readonly resourceFamily?: string;
  readonly status: number | null;
  retries: number;
  retryDelayMs: number;
}

export interface PerformanceReportOptions {
  readonly command: string;
  readonly commandStatus: PerformanceStatus;
  readonly commandDurationMs: number;
}

function finiteDuration(value: number, label: string): number {
  if (!Number.isFinite(value) || value < 0) {
    throw new TypeError(`${label} must be a finite non-negative duration`);
  }
  return Math.round(value * 1_000) / 1_000;
}

function count(value: number | undefined, label: string): number | undefined {
  if (value === undefined) return undefined;
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new TypeError(`${label} must be a non-negative safe integer`);
  }
  return value;
}

function safeLabel(value: string, label: string): string {
  if (
    value.length === 0
    || value.length > 256
    || /[\u0000-\u001f\u007f]/u.test(value)
    || /:\/\//u.test(value)
    || value.includes("?")
    || value.includes("#")
  ) {
    throw new TypeError(`${label} is not a safe performance label`);
  }
  return value;
}

function quantile(values: readonly number[], fraction: number): number {
  if (values.length === 0) return 0;
  const ordered = [...values].sort((left, right) => left - right);
  const index = Math.max(0, Math.ceil(ordered.length * fraction) - 1);
  return ordered[index] ?? 0;
}

function optionalFields(input: PerformanceSpanInput): Record<string, unknown> {
  if (
    input.oracleStateSource !== undefined
    && input.oracleStateSource !== "accepted-plan"
    && input.oracleStateSource !== "applied-state"
  ) {
    throw new TypeError("Oracle state source is invalid");
  }
  return {
    ...(input.authIdentity === undefined
      ? {}
      : { auth_identity: safeLabel(input.authIdentity, "auth identity") }),
    ...(input.correctedPlan === undefined ? {} : { corrected_plan: input.correctedPlan }),
    ...(count(input.instances, "instances") === undefined ? {} : { instances: input.instances }),
    ...(count(input.logicalRequests, "logical requests") === undefined
      ? {}
      : { logical_requests: input.logicalRequests }),
    ...(input.oracleStateSource === undefined
      ? {}
      : { oracle_state_source: input.oracleStateSource }),
    ...(count(input.pages, "pages") === undefined ? {} : { pages: input.pages }),
    ...(input.product === undefined ? {} : { product: safeLabel(input.product, "product") }),
    ...(input.resourceFamily === undefined
      ? {}
      : { resource_family: safeLabel(input.resourceFamily, "resource family") }),
    ...(count(input.terraformCommands, "terraform commands") === undefined
      ? {}
      : { terraform_commands: input.terraformCommands }),
  };
}

/** In-memory, opt-in performance evidence recorder. It never receives payload data. */
export class PerformanceRecorder {
  private readonly httpGroups = new Map<string, StoredHttpGroup>();
  private readonly spans: PerformanceSpanInput[] = [];
  private concurrency: number | null = null;

  constructor(
    readonly clock: PerformanceClock = {
      now: () => Number(process.hrtime.bigint()) / 1_000_000,
    },
  ) {}

  now(): number {
    return this.clock.now();
  }

  durationSince(startedMs: number): number {
    return finiteDuration(this.now() - startedMs, "performance span");
  }

  setFetchConcurrency(value: number): void {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new TypeError("fetch concurrency must be a positive safe integer");
    }
    if (this.concurrency !== null && this.concurrency !== value) {
      throw new TypeError("fetch concurrency changed within one performance report");
    }
    this.concurrency = value;
  }

  recordSpan(input: PerformanceSpanInput): void {
    optionalFields(input);
    this.spans.push(Object.freeze({
      ...input,
      durationMs: finiteDuration(input.durationMs, "performance span"),
      phase: safeLabel(input.phase, "phase"),
    }));
  }

  recordHttpAttempt(options: {
    readonly context: HttpRequestPerformanceContext;
    readonly durationMs: number;
    readonly status: number | null;
  }): void {
    const context = options.context;
    const normalized = {
      classification: context.classification,
      endpointFamily: safeLabel(context.endpointFamily, "endpoint family"),
      phase: safeLabel(context.phase, "phase"),
      ...(context.product === undefined
        ? {}
        : { product: safeLabel(context.product, "product") }),
      ...(context.resourceFamily === undefined
        ? {}
        : { resourceFamily: safeLabel(context.resourceFamily, "resource family") }),
      status: options.status,
    };
    if (
      options.status !== null
      && (!Number.isSafeInteger(options.status) || options.status < 100 || options.status > 599)
    ) {
      throw new TypeError("HTTP performance status is invalid");
    }
    const key = JSON.stringify(normalized);
    let group = this.httpGroups.get(key);
    if (group === undefined) {
      group = {
        ...normalized,
        durations: [],
        retries: 0,
        retryDelayMs: 0,
      };
      this.httpGroups.set(key, group);
    }
    group.durations.push(finiteDuration(options.durationMs, "HTTP attempt"));
  }

  recordHttpRetry(options: {
    readonly context: HttpRequestPerformanceContext;
    readonly delayMs: number;
    readonly status: number;
  }): void {
    const context = options.context;
    const normalized = {
      classification: context.classification,
      endpointFamily: safeLabel(context.endpointFamily, "endpoint family"),
      phase: safeLabel(context.phase, "phase"),
      ...(context.product === undefined
        ? {}
        : { product: safeLabel(context.product, "product") }),
      ...(context.resourceFamily === undefined
        ? {}
        : { resourceFamily: safeLabel(context.resourceFamily, "resource family") }),
      status: options.status,
    };
    const group = this.httpGroups.get(JSON.stringify(normalized));
    if (group === undefined) {
      throw new TypeError("HTTP retry must follow its recorded attempt");
    }
    group.retries += 1;
    group.retryDelayMs += finiteDuration(options.delayMs, "HTTP retry delay");
  }

  report(options: PerformanceReportOptions): Readonly<Record<string, unknown>> {
    const spans = [...this.spans].sort((left, right) => comparePythonStrings(
      JSON.stringify([
        left.phase,
        left.product ?? "",
        left.resourceFamily ?? "",
        left.authIdentity ?? "",
        left.status,
        left.correctedPlan ?? false,
      ]),
      JSON.stringify([
        right.phase,
        right.product ?? "",
        right.resourceFamily ?? "",
        right.authIdentity ?? "",
        right.status,
        right.correctedPlan ?? false,
      ]),
    )).map((span) => ({
      duration_ms: span.durationMs,
      phase: span.phase,
      status: span.status,
      ...optionalFields(span),
    }));
    const http = [...this.httpGroups.values()].sort((left, right) => comparePythonStrings(
      JSON.stringify([
        left.phase,
        left.product ?? "",
        left.resourceFamily ?? "",
        left.endpointFamily,
        left.classification,
        left.status,
      ]),
      JSON.stringify([
        right.phase,
        right.product ?? "",
        right.resourceFamily ?? "",
        right.endpointFamily,
        right.classification,
        right.status,
      ]),
    )).map((group) => ({
      classification: group.classification,
      duration_ms_p50: quantile(group.durations, 0.5),
      duration_ms_p95: quantile(group.durations, 0.95),
      duration_ms_total: finiteDuration(
        group.durations.reduce((total, duration) => total + duration, 0),
        "HTTP duration total",
      ),
      endpoint_family: group.endpointFamily,
      phase: group.phase,
      ...(group.product === undefined ? {} : { product: group.product }),
      request_count: group.durations.length,
      ...(group.resourceFamily === undefined
        ? {}
        : { resource_family: group.resourceFamily }),
      retries: group.retries,
      retry_delay_ms: finiteDuration(group.retryDelayMs, "HTTP retry total"),
      status: group.status,
    }));
    const totalHttpRequests = http.reduce((total, row) => total + row.request_count, 0);
    const rateLimitedRequests = http.reduce((total, row) => {
      return total + (row.status === 429 ? row.request_count : 0);
    }, 0);
    const totalRetries = http.reduce((total, row) => total + row.retries, 0);
    const totalRetryDelay = http.reduce((total, row) => total + row.retry_delay_ms, 0);
    const terraformCommands = spans.reduce((total, row) => {
      const value = "terraform_commands" in row ? row.terraform_commands : 0;
      return total + (typeof value === "number" ? value : 0);
    }, 0);
    const logicalRequests = spans.reduce((total, row) => {
      const value = "logical_requests" in row ? row.logical_requests : 0;
      return total + (typeof value === "number" ? value : 0);
    }, 0);
    const pages = spans.reduce((total, row) => {
      const value = "pages" in row ? row.pages : 0;
      return total + (typeof value === "number" ? value : 0);
    }, 0);
    return Object.freeze({
      command: safeLabel(options.command, "command"),
      command_duration_ms: finiteDuration(options.commandDurationMs, "command duration"),
      command_status: options.commandStatus,
      format: "infrawright-performance-report",
      http,
      selected_concurrency: this.concurrency,
      spans,
      summary: {
        http_requests: totalHttpRequests,
        logical_requests: logicalRequests,
        pages,
        rate_limited_requests: rateLimitedRequests,
        retries: totalRetries,
        retry_delay_ms: finiteDuration(totalRetryDelay, "retry delay total"),
        terraform_commands: terraformCommands,
      },
    });
  }
}
