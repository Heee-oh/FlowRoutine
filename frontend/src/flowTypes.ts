import type { Edge, Node } from "@xyflow/react";
import type { AssertionDefinition, LoadProfile } from "./types";

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
  bindings?: Record<string, string>;
};

export type EnvironmentVariable = {
  name: string;
  value: string;
};

export type EnvironmentProfile = {
  id: string;
  name: string;
  baseUrl: string;
  variables: EnvironmentVariable[];
  secretNames: string[];
};

export type RuntimeEnvironment = {
  profile?: EnvironmentProfile | null;
  secretBindings?: Record<string, string>;
  resolveSecrets?: boolean;
};

export type FlowNodeKind =
  | "request"
  | "engine"
  | "assertion"
  | "delay"
  | "branch"
  | "join"
  | "loop"
  | "metrics"
  | "window";

export type FlowNodeData = {
  label: string;
  value: string;
  caption: string;
  kind: FlowNodeKind;
  tone: "source" | "engine" | "metrics" | "window" | "assertion" | "delay" | "branch" | "join" | "loop";
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
  readBufferSize?: number;
  writeBufferSize?: number;
  maxResponseBytes?: number;
  rateLimitRps?: number;
  rampUpMs?: number;
  loadProfile?: LoadProfile;
  batchIntervalMs?: number;
  latencySampleRate?: number;
  maxFailureRatePct?: number;
  maxP95LatencyMs?: number;
  maxP99LatencyMs?: number;
  minRps?: number;
  windowMs?: number;
  expectedStatus?: string;
  assertionType?: AssertionDefinition["type"];
  assertionOperator?: NonNullable<AssertionDefinition["operator"]>;
  assertionHeaderName?: string;
  assertionJSONPath?: string;
  assertionExpected?: string;
  assertionValueType?: NonNullable<AssertionDefinition["valueType"]>;
  assertionMaxLatencyMs?: number;
  assertionFailureMode?: NonNullable<AssertionDefinition["failureMode"]>;
  delayMs?: number;
  branchJoinNodeId?: string;
  branchRoutesText?: string;
  loopBodyTargetId?: string;
  loopMaxIterations?: number;
  validationError?: string;
  executionOrder?: number;
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
  estimatedMemoryBytes: number;
  estimatedConnections: number;
  targetHosts: number;
};

export type SavedScenario = {
  schemaVersion: 2;
  id: string;
  name: string;
  tags: string[];
  createdAtUnixMs: number;
  updatedAtUnixMs: number;
  environmentProfileId?: string;
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
};

export type ScenarioDraft = {
  schemaVersion: 2;
  activeScenarioId?: string;
  name: string;
  tags: string[];
  createdAtUnixMs: number;
  updatedAtUnixMs: number;
  environmentProfileId?: string;
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
};
