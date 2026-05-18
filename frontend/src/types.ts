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
  delayMs?: number;
  expectedStatus?: string;
};

export type StartRequest = {
  config: LoadConfig;
  batchIntervalMs: number;
};

export type MetricsBatch = {
  timestampUnixMs: number;
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
  avgLatencyMs: number;
  minLatencyMs: number;
  maxLatencyMs: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
  p999LatencyMs: number;
  bytesRead: number;
  bytesWritten: number;
  statusCodes: StatusCodeCount[];
};

export type StatusCodeCount = {
  code: number;
  count: number;
};

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          StartLoad?: (request: StartRequest) => Promise<unknown>;
          StopLoad?: () => Promise<void>;
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
  latencySampleRate: number;
  rateLimitRps: number;
  rampUpMs: number;
};
