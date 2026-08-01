import type { Edge, Node } from "@xyflow/react";

export type HeaderInputMode = "direct" | "form";

export type HeaderRow = {
  name: string;
  value: string;
};

export type AuthType = "none" | "bearer" | "cookie" | "apiKey";

export type RuntimeAuthSecret = {
  token?: string;
  cookieValue?: string;
  apiKeyValue?: string;
};

export type FlowNodeKind = "request" | "engine" | "assertion" | "delay" | "metrics" | "window";

export type FlowNodeData = {
  label: string;
  value: string;
  caption: string;
  kind: FlowNodeKind;
  tone: "source" | "engine" | "metrics" | "window" | "assertion" | "delay";
  url?: string;
  method?: string;
  headersText?: string;
  headersMode?: HeaderInputMode;
  headerRows?: HeaderRow[];
  authType?: AuthType;
  authCookieName?: string;
  authApiKeyName?: string;
  body?: string;
  capturesText?: string;
  virtualUsers?: number;
  durationMs?: number;
  requestTimeoutMs?: number;
  maxConnsPerHost?: number;
  rateLimitRps?: number;
  rampUpMs?: number;
  batchIntervalMs?: number;
  latencySampleRate?: number;
  maxFailureRatePct?: number;
  maxP95LatencyMs?: number;
  maxP99LatencyMs?: number;
  minRps?: number;
  windowMs?: number;
  expectedStatus?: string;
  delayMs?: number;
  onDelete?: (id: string) => void;
} & Record<string, unknown>;

export type FlowNodeTemplate = {
  label: string;
  value: string;
  caption: string;
  tone: FlowNodeData["tone"];
} & Partial<Omit<FlowNodeData, "label" | "value" | "caption" | "tone" | "kind" | "onDelete">>;

export type TargetProfile = {
  label: "Localhost" | "Private" | "Public" | "Invalid";
  tone: "local" | "private" | "public" | "invalid";
  host: string;
};

export type SafetyAssessment = {
  target: TargetProfile;
  warnings: string[];
  confirmationRequired: boolean;
};

export type SavedScenario = {
  id: string;
  name: string;
  savedAtUnixMs: number;
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
};
