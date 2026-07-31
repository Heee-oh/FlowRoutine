import type { FlowNodeKind } from "./flowTypes";

export type HelpTopic = FlowNodeKind | "overview";
export type HelpLanguage = "ko" | "en";
export type HelpItem = { title: string; description: Record<HelpLanguage, string> };

export const overviewHelpItems: HelpItem[] = [
  helpItem("Request", "Target URL, method, headers, and body for HTTP traffic.", "부하 테스트에서 보낼 HTTP 요청의 URL, method, headers, body입니다."),
  helpItem("Engine", "Virtual users, duration, timeout, connection cap, RPS cap, and ramp-up.", "VUs, duration, timeout, max conns, RPS limit, ramp-up을 제어합니다."),
  helpItem("Assert", "Expected response status check for scenario requests.", "요청 응답 status code가 expected status와 맞는지 확인합니다."),
  helpItem("Delay", "Wait time inserted when the node is on the Request through Engine path.", "Request에서 Engine으로 이어지는 실행 경로에 있을 때 요청 사이에 delay를 넣습니다."),
  helpItem("Metrics", "Batch interval and latency sampling for live measurements.", "실시간 metrics 전송 주기와 latency sampling 빈도를 설정합니다."),
  helpItem("Window", "How long realtime chart points are retained in the UI.", "실시간 chart points가 UI에 유지되는 시간을 설정합니다."),
  helpItem("Connection order", "Use Request -> Engine -> Metrics -> Window for the base flow. Put Delay or Assert between Request and Engine when you want them to run.", "기본 flow는 Request -> Engine -> Metrics -> Window 순서로 연결합니다.\nDelay나 Assert를 실행하려면 Request와 Engine 사이 path에 넣어야 합니다."),
  helpItem("Disconnect", "Select a connection line, then press Delete or Backspace to remove it.", "Connection line을 선택한 뒤 Delete 또는 Backspace를 누르면 연결이 삭제됩니다."),
];

export const nodeHelpItems: Record<FlowNodeKind, HelpItem[]> = {
  request: [
    helpItem("Target URL", "HTTP or HTTPS endpoint that receives generated traffic.", "생성된 traffic을 받을 HTTP 또는 HTTPS endpoint입니다."),
    helpItem("Method", "HTTP verb used for the request step.", "Request step에서 사용할 HTTP verb입니다."),
    helpItem("Auth", "Adds Bearer token, Cookie, or API key headers at run time. Secret values are kept in memory only.", "실행 시 Bearer token, Cookie, API key header를 자동으로 추가합니다. Secret 값은 메모리에만 보관됩니다."),
    helpItem("Headers", "Use Direct mode for raw Name: Value lines, or Form mode to add common headers one by one.", "Direct 모드는 Name: Value를 직접 입력합니다. Form 모드는 자주 쓰는 header를 한 줄씩 추가해서 입력합니다."),
    helpItem("Body", "Request payload sent with methods such as POST, PUT, or PATCH.", "POST, PUT, PATCH 같은 method에서 함께 보낼 request payload입니다."),
    helpItem("Capture JSON", "Use name[@iteration|run][:success|any|2xx|200]=JSON.path. Iteration values reset each loop; run values keep the first successful value per virtual user. Missing templates are never sent.", "name[@iteration|run][:success|any|2xx|200]=JSON.path 형식입니다. iteration 값은 loop마다 초기화되고 run 값은 virtual user별 첫 성공값을 유지합니다. 누락된 template은 전송하지 않습니다."),
  ],
  engine: [
    helpItem("VUs", "Number of virtual users running the scenario loop concurrently.", "Scenario loop를 동시에 실행하는 virtual users 수입니다."),
    helpItem("Duration ms", "Total run time in milliseconds before the test stops automatically.", "Test가 자동으로 멈추기 전까지의 전체 run time입니다. 단위는 milliseconds입니다."),
    helpItem("Timeout ms", "Maximum time allowed for one request before it is counted as failed.", "Request 하나가 failed로 처리되기 전까지 기다리는 최대 시간입니다."),
    helpItem("Max conns", "Maximum keep-alive connections per target host. Lower values intentionally limit concurrency.", "Target host별 keep-alive 최대 connections 수입니다. 낮게 설정하면 의도적으로 concurrency를 제한합니다."),
    helpItem("Rate limit RPS", "Global requests-per-second cap. Use 0 for unlimited.", "전체 requests per second 제한입니다. 0은 unlimited입니다."),
    helpItem("Ramp-up ms", "Time window used to gradually start virtual users.", "Virtual users를 한 번에 시작하지 않고 점진적으로 시작하는 time window입니다."),
  ],
  assertion: [
    helpItem("Expected status", "Status code rule checked after a request, such as 200 or 2xx.", "Request 후 확인할 status code 규칙입니다. 예: 200, 2xx"),
  ],
  delay: [
    helpItem("Delay ms", "Wait time in milliseconds when this node is on the Request through Engine path.", "이 node가 Request에서 Engine으로 가는 path에 있을 때 적용되는 wait time입니다."),
  ],
  metrics: [
    helpItem("Batch ms", "How often the backend sends metric updates to the UI (100-5000 ms). Slower updates reduce overhead on long runs.", "Backend가 UI로 metrics updates를 보내는 주기(100-5000ms)입니다. 장기 실행에서는 느린 주기가 overhead를 줄입니다."),
    helpItem("Latency sample rate", "Records one latency sample every N requests per virtual user.", "Virtual user별로 N개 requests마다 latency sample 하나를 기록합니다."),
  ],
  window: [
    helpItem("Window seconds", "How many seconds the bounded realtime chart covers. Extrema are retained while older detail is downsampled.", "제한된 realtime chart가 보여줄 시간 범위입니다. 최솟값과 최댓값은 유지하면서 오래된 세부 points를 downsample합니다."),
  ],
};

export function helpDialogTitle(topic: HelpTopic, language: HelpLanguage, nodeLabel: string) {
  if (topic === "overview") {
    return language === "ko" ? "노드 가이드" : "Node guide";
  }
  return language === "ko" ? `${nodeLabel} 도움말` : `${nodeLabel} help`;
}

function helpItem(title: string, descriptionEn: string, descriptionKo: string): HelpItem {
  return {
    title,
    description: { en: descriptionEn, ko: descriptionKo },
  };
}
