import type { FlowNodeKind } from "./flowTypes";

export type HelpTopic = FlowNodeKind | "overview";
export type HelpLanguage = "ko" | "en";
export type HelpItem = { title: string; description: Record<HelpLanguage, string> };

export const overviewHelpItems: HelpItem[] = [
  helpItem("Environment", "Select a named base URL and non-secret variables. SECRET_* bindings are masked and stay in memory only.", "이름이 있는 base URL과 일반 변수를 선택합니다. SECRET_* binding은 가려서 표시되며 메모리에만 보관됩니다."),
  helpItem("Scenario library", "Name and tag scenarios, save library versions, recover the autosaved draft, or import and export versioned JSON files.", "Scenario에 이름과 tag를 지정하고 library version을 저장하거나, autosave draft를 복구하고 versioned JSON 파일을 import/export합니다."),
  helpItem("Undo and redo", "Node/edge deletion, imports, new drafts, and graph replacement keep a bounded in-memory undo history.", "Node/edge 삭제, import, 새 draft, graph 교체는 제한된 메모리 undo history에 기록됩니다."),
  helpItem("Request", "Target URL, method, headers, and body for HTTP traffic.", "부하 테스트에서 보낼 HTTP 요청의 URL, method, headers, body입니다."),
  helpItem("Engine", "Staged VU or arrival-rate profiles, timeout, connection cap, and request RPS cap.", "단계형 VU 또는 arrival-rate profile, timeout, max conns, request RPS limit을 제어합니다."),
  helpItem("Assert", "Checks response status, headers, typed JSON values, or response and step latency.", "응답 status, header, typed JSON 값, response/step latency를 확인합니다."),
  helpItem("Delay", "Wait time inserted when the node is on the Request through Engine path.", "Request에서 Engine으로 이어지는 실행 경로에 있을 때 요청 사이에 delay를 넣습니다."),
  helpItem("Metrics", "Batch interval and latency sampling for live measurements.", "실시간 metrics 전송 주기와 latency sampling 빈도를 설정합니다."),
  helpItem("Window", "How long realtime chart points are retained in the UI.", "실시간 chart points가 UI에 유지되는 시간을 설정합니다."),
  helpItem("Connection order", "Use Request -> Engine -> Metrics -> Window for the base flow. Put Delay or Assert between Request and Engine when you want them to run.", "기본 flow는 Request -> Engine -> Metrics -> Window 순서로 연결합니다.\nDelay나 Assert를 실행하려면 Request와 Engine 사이 path에 넣어야 합니다."),
  helpItem("Disconnect", "Select a connection line, then press Delete or Backspace to remove it.", "Connection line을 선택한 뒤 Delete 또는 Backspace를 누르면 연결이 삭제됩니다."),
];

export const nodeHelpItems: Record<FlowNodeKind, HelpItem[]> = {
  request: [
    helpItem("Target URL", "HTTP or HTTPS endpoint that receives generated traffic. Use {{BASE_URL}} or other uppercase environment variables to switch profiles.", "생성된 traffic을 받을 HTTP 또는 HTTPS endpoint입니다. Profile 전환에는 {{BASE_URL}} 또는 다른 대문자 환경 변수를 사용합니다."),
    helpItem("Method", "HTTP verb used for the request step.", "Request step에서 사용할 HTTP verb입니다."),
    helpItem("Auth", "Adds Bearer token, Cookie, or API key headers at run time. Secret values are kept in memory only.", "실행 시 Bearer token, Cookie, API key header를 자동으로 추가합니다. Secret 값은 메모리에만 보관됩니다."),
    helpItem("Headers", "Use Direct mode for raw Name: Value lines, or Form mode to add common headers one by one.", "Direct 모드는 Name: Value를 직접 입력합니다. Form 모드는 자주 쓰는 header를 한 줄씩 추가해서 입력합니다."),
    helpItem("Body", "Request payload sent with methods such as POST, PUT, or PATCH.", "POST, PUT, PATCH 같은 method에서 함께 보낼 request payload입니다."),
    helpItem("Capture JSON", "Use name[@iteration|run][:success|any|2xx|200]=JSON.path. Iteration values reset each loop; run values keep the first successful value per virtual user. Missing templates are never sent.", "name[@iteration|run][:success|any|2xx|200]=JSON.path 형식입니다. iteration 값은 loop마다 초기화되고 run 값은 virtual user별 첫 성공값을 유지합니다. 누락된 template은 전송하지 않습니다."),
  ],
  engine: [
    helpItem("Execution mode", "Choose constant or staged VUs, or constant or staged iteration arrival rate.", "고정/단계형 VU 또는 고정/단계형 iteration arrival rate를 선택합니다."),
    helpItem("Stages", "Each stage linearly changes the target over its duration. The preview shows the complete profile before start.", "각 stage는 duration 동안 target을 선형으로 바꿉니다. 시작 전 preview에서 전체 profile을 확인할 수 있습니다."),
    helpItem("Arrival capacity", "Pre-allocated VUs are ready at start; Max VUs is the hard worker ceiling. Iterations beyond it are reported as dropped.", "Pre-allocated VUs는 시작 시 준비되며 Max VUs는 worker 상한입니다. 이를 넘는 iterations는 dropped로 집계됩니다."),
    helpItem("Graceful stop", "Stops new iterations and gives active iterations this long to finish before context-aware work is cancelled. An in-flight request can continue until its request timeout.", "새 iteration 시작을 중단하고 실행 중 iteration이 끝날 때까지 기다리는 유예 시간입니다. 이미 전송 중인 request는 request timeout까지 계속될 수 있습니다."),
    helpItem("Timeout ms", "Maximum time allowed for one request before it is counted as failed.", "Request 하나가 failed로 처리되기 전까지 기다리는 최대 시간입니다."),
    helpItem("Max conns", "Maximum keep-alive connections per target host. Lower values intentionally limit concurrency.", "Target host별 keep-alive 최대 connections 수입니다. 낮게 설정하면 의도적으로 concurrency를 제한합니다."),
    helpItem("Request rate cap", "Optional global request cap for VU profiles. Arrival-rate profiles control iteration starts and disable this cap.", "VU profile의 선택적 전체 request 제한입니다. Arrival-rate profile은 iteration 시작률을 제어하므로 이 제한을 비활성화합니다."),
  ],
  assertion: [
    helpItem("Assertion type", "Check status, header presence or value, a typed JSON path value, HTTP response latency, or end-to-end request-step latency.", "Status, header 존재/값, typed JSON path 값, HTTP response latency, 전체 request-step latency를 확인합니다."),
    helpItem("Typed JSON", "JSON equality distinguishes strings, finite numbers, booleans, and null. Exists only requires the path to resolve.", "JSON equality는 string, finite number, boolean, null 타입을 구분합니다. Exists는 path가 존재하는지만 확인합니다."),
    helpItem("Latency", "Response latency measures the HTTP exchange. Step latency also includes pacing, template rendering, and captures.", "Response latency는 HTTP 통신 시간을 측정합니다. Step latency에는 pacing, template rendering, capture도 포함됩니다."),
    helpItem("On failure", "Continue records and proceeds, Stop ends the current iteration, and Count only records the typed diagnostic without increasing enforced assertion failures.", "Continue는 기록 후 계속하고, Stop은 현재 iteration을 끝내며, Count only는 enforced assertion failure를 늘리지 않고 유형별 진단만 기록합니다."),
  ],
  delay: [
    helpItem("Delay ms", "Wait time in milliseconds when this node is on the Request through Engine path.", "이 node가 Request에서 Engine으로 가는 path에 있을 때 적용되는 wait time입니다."),
  ],
  metrics: [
    helpItem("Batch ms", "How often the backend sends metric updates to the UI (100-5000 ms). Slower updates reduce overhead on long runs.", "Backend가 UI로 metrics updates를 보내는 주기(100-5000ms)입니다. 장기 실행에서는 느린 주기가 overhead를 줄입니다."),
    helpItem("Latency sample rate", "Samples every request step in one of every N iterations per virtual user, avoiding bias between steps.", "Virtual user별 N개 iteration 중 하나에서 모든 request step의 latency를 기록해 step 간 sampling 편향을 방지합니다."),
    helpItem("Request-step diagnostics", "Status details ranks request steps by failures and P99 latency. Step summaries update at most once per second and on completion.", "Status details는 request step을 실패 수와 P99 latency 순으로 보여줍니다. Step 요약은 최대 초당 한 번과 실행 완료 시 갱신됩니다."),
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
