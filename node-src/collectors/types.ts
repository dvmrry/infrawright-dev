import type { LoadedPackRoot } from "../metadata/loader.js";
import type {
  HttpRequestPerformanceContext,
  PerformanceRecorder,
} from "../performance/recorder.js";

export type CollectorAuthMode = "legacy" | "oneapi";

export interface CollectorContext {
  readonly cloud: string;
  readonly customerId: string;
  readonly ziaLegacyBase?: string;
  readonly zpaCloud?: string;
  readonly zpaLegacyBase?: string;
}

export interface HttpRequest {
  readonly method: "GET" | "POST";
  readonly url: URL;
  readonly headers?: Readonly<Record<string, string>>;
  readonly body?: Uint8Array | string;
  readonly timeoutMs?: number;
  /** Sanitized static labels only; never URL, query, headers, body, or IDs. */
  readonly performance?: HttpRequestPerformanceContext;
}

export interface HttpResponse {
  readonly status: number;
  readonly headers: Readonly<Record<string, string | readonly string[] | undefined>>;
  readonly body: Uint8Array;
}

export interface HttpTransport {
  request(request: HttpRequest): Promise<HttpResponse>;
  close?(): Promise<void>;
}

export interface CollectorAuthContext {
  readonly headers: Readonly<Record<string, string>>;
}

export interface CollectorAcquireInput {
  readonly mode: CollectorAuthMode;
  readonly environment: NodeJS.ProcessEnv;
  readonly context: CollectorContext;
  readonly transport: HttpTransport;
  readonly nowMs?: number;
  readonly performanceContext?: Omit<
    HttpRequestPerformanceContext,
    "classification" | "endpointFamily"
  >;
}

export interface CollectorComposeUrlInput {
  readonly mode: CollectorAuthMode;
  readonly context: CollectorContext;
  readonly path: string;
}

/** Product-specific authentication and URL composition only. */
export interface CollectorAdapter {
  readonly product: string;
  acquire(input: CollectorAcquireInput): Promise<CollectorAuthContext>;
  composeUrl(input: CollectorComposeUrlInput): URL;
}

export interface FetchRunResult {
  readonly failed: Readonly<Record<string, string>>;
  readonly processed: readonly string[];
  readonly skipped: Readonly<Record<string, string>>;
}

export interface FetchResourcesOptions {
  readonly adapters: ReadonlyMap<string, CollectorAdapter>;
  /** Maximum independent resource fetches in flight. Defaults to one. */
  readonly concurrency?: number;
  readonly context: CollectorContext;
  readonly environment: NodeJS.ProcessEnv;
  readonly mode: CollectorAuthMode;
  readonly onDiagnostic?: (message: string) => void;
  readonly outputDirectory: string;
  readonly performance?: PerformanceRecorder;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly transport: HttpTransport;
}
