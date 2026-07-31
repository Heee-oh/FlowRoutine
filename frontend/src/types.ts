export type Header = {
  name: string;
  value: string;
};

export type LoadConfig = {
  url: string;
  method: string;
  headers: Header[];
  body: string;
  virtualUsers: number;
  durationMs: number;
  requestTimeoutMs: number;
  maxConnsPerHost: number;
  readBufferSize: number;
  writeBufferSize: number;
  maxResponseBytes: number;
  latencySampleRate: number;
  rateLimitRps: number;
  rampUpMs: number;
  scenarioSteps: ScenarioStep[];
};

export type ScenarioStep = {
  kind: "request" | "delay" | "assertStatus";
  url?: string;
  method?: string;
  headers?: Header[];
  body?: string;
  captures?: Capture[];
  delayMs?: number;
  expectedStatus?: string;
};

export type Capture = {
  name: string;
  path: string;
  scope?: "iteration" | "run";
  onStatus?: string;
};

export type StartRequest = {
  config: LoadConfig;
  batchIntervalMs: number;
  qualityGate?: QualityGate;
};

export type StartResponse = {
  started: boolean;
  preflight: PreflightResponse;
};

export type PreflightResponse = {
  effectiveConfig: EffectiveLoadConfig;
  effectiveBatchIntervalMs: number;
  estimate: PreflightEstimate;
  warnings: PreflightWarning[];
};

export type EffectiveLoadConfig = Pick<
  LoadConfig,
  | "virtualUsers"
  | "durationMs"
  | "requestTimeoutMs"
  | "maxConnsPerHost"
  | "readBufferSize"
  | "writeBufferSize"
  | "maxResponseBytes"
  | "latencySampleRate"
  | "rateLimitRps"
  | "rampUpMs"
>;

export type PreflightEstimate = {
  memoryBytes: number;
  connections: number;
  targetHosts: number;
};

export type PreflightWarning = {
  code: string;
  message: string;
};

export type QualityGate = {
  maxFailureRatePct: number;
  maxP95LatencyMs: number;
  maxP99LatencyMs: number;
  minRps: number;
};

export type MetricsBatch = {
  timestampUnixMs: number;
  startedAtUnixMs: number;
  running: boolean;
  rps: number;
  total: number;
  success: number;
  failed: number;
  timeout: number;
  dns: number;
  tls: number;
  connRefused: number;
  otherErrors: number;
  assertionsFailed: number;
  captureFailures: number;
  templateFailures: number;
  intervalLatency: IntervalLatencyMetrics;
  runLatency: CumulativeLatencyMetrics;
  bytesRead: number;
  bytesWritten: number;
  statusCodes: StatusCodeCount[];
};

export type IntervalLatencyMetrics = {
  samples: number;
  avgMs: number;
  p95Ms: number;
  p99Ms: number;
  p999Ms: number;
};

export type CumulativeLatencyMetrics = IntervalLatencyMetrics & {
  minMs: number;
  maxMs: number;
};

export type StatusCodeCount = {
  code: number;
  count: number;
};

export type OpenAPIImportResponse = {
  sourceUrl: string;
  openapi: string;
  title: string;
  version: string;
  servers: OpenAPIServer[];
  endpoints: OpenAPIEndpoint[];
};

export type OpenAPIImportRequest = {
  url: string;
  allowPrivateNetwork: boolean;
  allowRedirects: boolean;
  allowExternalRefs: boolean;
};

export type OpenAPIServer = {
  url: string;
  description: string;
};

export type OpenAPIEndpoint = {
  method: string;
  path: string;
  summary: string;
  operationId: string;
  tags: string[];
  serverUrl: string;
  deprecated: boolean;
  auth: OpenAPIAuth;
  parameters: OpenAPIParameter[];
  bodySample: string;
};

export type OpenAPIAuth = {
  type: "none" | "bearer" | "apiKey" | "cookie";
  name: string;
};

export type OpenAPIParameter = {
  name: string;
  in: string;
  required: boolean;
  description: string;
  sample: string;
};

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          StartLoad?: (request: StartRequest) => Promise<StartResponse>;
          PreflightLoad?: (request: StartRequest) => Promise<PreflightResponse>;
          StopLoad?: () => Promise<void>;
          ImportOpenAPI?: (request: OpenAPIImportRequest) => Promise<OpenAPIImportResponse>;
        };
      };
    };
    runtime?: {
      EventsOn?: (eventName: string, callback: (...data: unknown[]) => void) => () => void;
    };
  }
}

export type FlowSettings = {
  url: string;
  method: string;
  headersText: string;
  body: string;
  virtualUsers: number;
  durationMs: number;
  requestTimeoutMs: number;
  batchIntervalMs: number;
  maxConnsPerHost: number;
  readBufferSize: number;
  writeBufferSize: number;
  maxResponseBytes: number;
  latencySampleRate: number;
  rateLimitRps: number;
  rampUpMs: number;
};
