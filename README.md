<div align="center">

# FlowRoutine

**Local-first visual load testing for HTTP APIs.**

![License](https://img.shields.io/badge/license-Apache--2.0-blue)
![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=111)
![Wails](https://img.shields.io/badge/Wails-v2-bb2cf3)
![Built with OpenAI Codex](https://img.shields.io/badge/Built%20with-OpenAI%20Codex-412991?logo=openai&logoColor=white)

FlowRoutine lets you compose HTTP load-test scenarios as nodes, run them locally with a Go engine, and watch realtime metrics without uploading your scenarios, targets, auth values, or metrics to a FlowRoutine cloud service.

Built with OpenAI Codex assistance.

![FlowRoutine scenario editor](docs/images/flowroutine-main.png)

</div>

## Why FlowRoutine

- **Visual scenario editing**: Build request, delay, assertion, engine, metrics, and chart-window nodes on a React Flow canvas.
- **Postman/OpenAPI/HAR import**: Turn Postman Collection v2 JSON files, OpenAPI endpoints, or browser HAR captures into runnable scenario nodes.
- **Stateful API flows**: Capture JSON response values and reuse them as `{{variables}}` in later request URLs, headers, or bodies.
- **Local load engine**: Run virtual users locally with goroutines, `fasthttp`, keep-alive reuse, and pooled request/response objects.
- **Realtime feedback**: Track RPS, latency, failures, status code counts, and bounded live chart data through batched Wails events.
- **Shareable local reports**: Export completed runs as redacted JSON reports with summary metrics, error breakdowns, status codes, and timeline points.
- **SLO gates**: Configure failure-rate, latency, and RPS thresholds on the Metrics node and get pass/fail results locally or in exported k6 scripts.
- **Regression awareness**: Completed runs are compared with the previous local baseline for the same scenario to flag RPS, latency, and failure-rate changes.
- **k6 handoff**: Export visual scenarios to k6 JavaScript for CI runs or distributed execution outside the desktop app.
- **Privacy-first workflow**: Scenario data, target URLs, auth values, and metrics are handled locally instead of being uploaded to a FlowRoutine cloud service.

## Quick Start

```bash
npm --prefix frontend install
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev
```

Run checks:

```bash
npm --prefix frontend run build:wails
go test ./...
go build ./...
```

## Features

| Area | What works |
| --- | --- |
| Scenario canvas | Request, Engine, Metrics, Window, Delay, and Assert nodes |
| Import | OpenAPI / Swagger URL import, Postman Collection v2 JSON import, and browser HAR import |
| Request setup | Method, URL, headers, body, auth helpers, JSON capture variables, and recent run loading |
| Execution | Linear path execution from Request through Engine, with Delay and Assert support |
| Engine | Go virtual users, RPS cap, ramp-up, timeouts, max connections, keep-alive reuse |
| Metrics | RPS, total/success/failed counts, latency percentiles, transport failures, HTTP status breakdown |
| Reports | Completed-run JSON export with SLO pass/fail, local baseline comparison, redacted sensitive headers, and body-size metadata |
| Interop | k6 JavaScript export with thresholds and sensitive headers mapped to environment variables |
| UI | Node help, Korean/English descriptions, adjustable chart window, wheel zoom, edge deletion |

Capture syntax is `name[@iteration|run][:success|any|2xx|200]=JSON.path`. Iteration values reset on every
virtual-user loop; run values keep the first successful value for that virtual user. JSON paths support
dot segments and array indexes such as `$.items[0].token`. A missing value never sends an unresolved template.

Metric summaries use the final cumulative engine snapshot and remain exact. Realtime charts retain at most
2,048 lightweight points, while exported report timelines retain at most 1,000 points. Streaming downsampling
preserves the first and last samples plus per-bucket RPS, P95, and P99 minimum and maximum excursions.

## Architecture

```text
React / React Flow / Zustand
        |
        | Wails calls + batched metrics events
        v
Wails bridge
        |
        v
Go load engine
  - goroutine virtual users
  - fasthttp host clients
  - sync.Pool request/response reuse
  - sharded atomic stats
```

## Benchmark Helper

Run an embedded local loopback benchmark:

```bash
go run ./cmd/flowroutine-bench -duration 5s -warmup 1s -vus 256 -latency-sample-rate 100
```

Run against a real HTTP or HTTPS target:

```bash
go run ./cmd/flowroutine-bench -url https://example.com -duration 5s -warmup 1s -vus 256 -latency-sample-rate 100
```

Benchmark results vary by hardware, OS limits, network path, TLS cost, and target capacity. Treat local results as measurements of that environment, not universal project guarantees.

## Safety

Load testing can disrupt real services. FlowRoutine includes RPS caps, ramp-up controls, public-target warnings, and duration guardrails, but the operator is responsible for using it safely.

- Start with a small RPS cap.
- Use ramp-up.
- Keep durations short until the target behavior is understood.
- Monitor the target service and network path.
- Do not test third-party systems without explicit permission.

## Roadmap

- Add a manual scenario library with named saves beyond recent-run history.
- Support branching and multiple scenario paths.
- Add richer assertions for headers, bodies, and latency thresholds.
- Improve packaged desktop builds for macOS, Windows, and Linux.

## Current Limits

- Graph execution follows one selected linear scenario path.
- Distributed load generation is not implemented.
- Auth secret values are memory-only and are not saved in recent-run history.

## License

FlowRoutine is released under the [Apache License 2.0](LICENSE).
