import { memo, useEffect, useRef, useState, type ReactNode } from "react";
import { Activity, AlertTriangle, CheckCircle2, Download, Gauge, ListChecks, MinusCircle, Network, Timer, TrendingDown, TrendingUp, XCircle } from "lucide-react";
import { downloadRunReport, type BaselineComparison, type QualityGateResult } from "../report";
import { useMetricsStore } from "../store";
import type { MetricHistoryPoint } from "../metricHistory";
import type { MetricsBatch, RequestStepMetrics, StatusCodeCount } from "../types";
import { formatBytes, formatNumber } from "../format";

export const MetricGrid = memo(function MetricGrid() {
  const latest = useMetricsStore((state) => state.latest);
  const latestReport = useMetricsStore((state) => state.latestReport);
  const [statusDetailsOpen, setStatusDetailsOpen] = useState(false);

  return (
    <section className="metrics-panel" aria-label="Run metrics">
      <div className="metrics-primary">
        <Metric icon={<Gauge size={17} />} label="RPS" value={formatNumber(latest?.rps ?? 0, 1)} />
        <Metric icon={<Activity size={17} />} label="Total" value={formatNumber(latest?.total ?? 0)} />
        <Metric icon={<Network size={17} />} label="Success" value={formatNumber(latest?.success ?? 0)} />
        <Metric icon={<Network size={17} />} label="Failed" value={formatNumber(latest?.failed ?? 0)} />
        <Metric icon={<Timer size={17} />} label="Avg latency" value={`${formatNumber(latest?.intervalLatency.avgMs ?? 0, 2)} ms`} />
        <Metric icon={<Timer size={17} />} label="P99 latency" value={`${formatNumber(latest?.intervalLatency.p99Ms ?? 0, 2)} ms`} />
      </div>
      <div className="metrics-secondary">
        <MetricDetail label="P95" value={`${formatNumber(latest?.intervalLatency.p95Ms ?? 0, 2)} ms`} />
        <MetricDetail label="P99.9" value={`${formatNumber(latest?.intervalLatency.p999Ms ?? 0, 2)} ms`} />
        <MetricDetail label="Timeout" value={formatNumber(latest?.timeout ?? 0)} />
        <MetricDetail label="DNS" value={formatNumber(latest?.dns ?? 0)} />
        <MetricDetail label="TLS" value={formatNumber(latest?.tls ?? 0)} />
        <MetricDetail label="Refused" value={formatNumber(latest?.connRefused ?? 0)} />
        <MetricDetail label="Other" value={formatNumber(latest?.otherErrors ?? 0)} />
        <MetricDetail
          label="Dropped"
          value={formatNumber(latest?.droppedIterations ?? 0)}
          title="Iterations not started because arrival-rate worker capacity was exhausted"
        />
        <MetricDetail
          label="Assertions"
          value={formatNumber(latest?.assertionsFailed ?? 0)}
          title={`Capture ${formatNumber(latest?.captureFailures ?? 0)} · Template ${formatNumber(latest?.templateFailures ?? 0)}`}
        />
        <MetricDetail
          label="Samples"
          value={formatNumber(latest?.runLatency.samples ?? 0)}
          title={latencySamplingTitle(latest)}
        />
        <MetricDetail label="Read" value={formatBytes(latest?.bytesRead ?? 0)} />
      </div>
      <div className="metrics-footer">
        <div className="metrics-footer-badges">
          {latestReport ? (
            <div
              className={`quality-gate quality-gate-${latestReport.qualityGate.status}`}
              title={qualityGateTitle(latestReport.qualityGate)}
            >
              {qualityGateIcon(latestReport.qualityGate.status)}
              {qualityGateLabel(latestReport.qualityGate.status)}
            </div>
          ) : null}
          {latestReport ? (
            <div
              className={`baseline-badge baseline-${latestReport.baseline.verdict}`}
              title={baselineTitle(latestReport.baseline)}
            >
              {baselineIcon(latestReport.baseline)}
              {baselineLabel(latestReport.baseline.verdict)}
            </div>
          ) : null}
        </div>
        <button
          type="button"
          className="secondary icon-button metric-details-button"
          aria-label="Export report"
          title={latestReport ? "Export report" : "No completed run"}
          disabled={!latestReport}
          onClick={() => {
            if (latestReport) {
              downloadRunReport(latestReport);
            }
          }}
        >
          <Download size={15} />
        </button>
        <button
          type="button"
          className="secondary icon-button metric-details-button"
          aria-label="Status details"
          title="Status details"
          onClick={() => setStatusDetailsOpen(true)}
        >
          <ListChecks size={15} />
        </button>
      </div>
      {statusDetailsOpen ? (
        <StatusDetailsDialog batch={latest} onClose={() => setStatusDetailsOpen(false)} />
      ) : null}
    </section>
  );
});

export const MetricsChart = memo(function MetricsChart() {
  const points = useMetricsStore((state) => state.points);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) {
      return;
    }

    const resizeObserver = new ResizeObserver(() => drawChart(canvas, points));
    resizeObserver.observe(canvas);
    drawChart(canvas, points);
    return () => resizeObserver.disconnect();
  }, [points]);

  return (
    <section className="chart-panel">
      <div className="chart-head">
        <div>
          <div className="eyebrow">Realtime</div>
          <h2>RPS and latency</h2>
        </div>
        <div className="legend">
          <span className="legend-rps" />RPS log
          <span className="legend-latency" />P99 ms
        </div>
      </div>
      <canvas ref={canvasRef} className="metrics-canvas" />
    </section>
  );
});

const Metric = memo(function Metric({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <article className="metric">
      <span>{icon}{label}</span>
      <strong>{value}</strong>
    </article>
  );
});

const MetricDetail = memo(function MetricDetail({
  label,
  value,
  title,
}: {
  label: string;
  value: string;
  title?: string;
}) {
  return (
    <div className="metric-detail" title={title}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
});

export const StatusDetailsDialog = memo(function StatusDetailsDialog({
  batch,
  onClose,
}: {
  batch: MetricsBatch | null;
  onClose: () => void;
}) {
  const statusCodes = batch?.statusCodes ?? [];
  const stepMetrics = [...(batch?.stepMetrics ?? [])].sort(compareRequestSteps);
  const total = batch?.total ?? 0;
  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal status-details-modal" role="dialog" aria-modal="true" aria-labelledby="status-details-title">
        <div>
          <div className="eyebrow">Results</div>
          <h2 id="status-details-title">Status details</h2>
        </div>
        <div className="status-summary">
          <MetricDetail label="Total" value={formatNumber(total)} />
          <MetricDetail label="Success" value={formatNumber(batch?.success ?? 0)} />
          <MetricDetail label="Failed" value={formatNumber(batch?.failed ?? 0)} />
          <MetricDetail label="Dropped" value={formatNumber(batch?.droppedIterations ?? 0)} />
        </div>
        {statusCodes.length === 0 ? (
          <div className="inspector-note">No HTTP responses yet</div>
        ) : (
          <div className="status-code-list">
            <div className="status-code-row status-code-header">
              <span>Status code</span>
              <span>Count</span>
              <span>Rate</span>
            </div>
            {statusCodes.map((item) => (
              <StatusCodeRow key={item.code} item={item} total={total} />
            ))}
          </div>
        )}
        <div className="request-step-section">
          <div className="request-step-heading">
            <h3>Request steps</h3>
            <small>Failures first, then slowest P99</small>
          </div>
          {stepMetrics.length === 0 ? (
            <div className="inspector-note">No request-step metrics yet</div>
          ) : (
            <div className="request-step-list">
              <div className="request-step-row request-step-header">
                <span>Step</span>
                <span>Requests</span>
                <span>Failures</span>
                <span>P95 / P99</span>
              </div>
              {stepMetrics.map((step) => <RequestStepRow key={step.id} step={step} />)}
            </div>
          )}
        </div>
        <button type="button" className="secondary" onClick={onClose}>Close</button>
      </div>
    </div>
  );
});

const RequestStepRow = memo(function RequestStepRow({ step }: { step: RequestStepMetrics }) {
  const diagnosticFailures = step.failed + step.assertionsFailed;
  const failureRate = step.total > 0 ? (step.failed / step.total) * 100 : 0;
  const statuses = step.statusCodes.map((status) => `${status.code} ${formatNumber(status.count)}`).join(" · ");
  return (
    <div className={`request-step-row${diagnosticFailures > 0 ? " request-step-row-failing" : ""}`}>
      <div>
        <strong>{step.name || step.id}</strong>
        <small title={step.id}>{step.id}{statuses ? ` · ${statuses}` : ""}</small>
      </div>
      <span>{formatNumber(step.total)}</span>
      <span title={`${formatNumber(failureRate, 1)}% HTTP failure rate`}>
        {formatNumber(step.failed)} HTTP · {formatNumber(step.assertionsFailed)} diagnostics
      </span>
      <span>{formatNumber(step.runLatency.p95Ms, 2)} / {formatNumber(step.runLatency.p99Ms, 2)} ms</span>
    </div>
  );
});

function compareRequestSteps(left: RequestStepMetrics, right: RequestStepMetrics) {
  const leftFailures = left.failed + left.assertionsFailed;
  const rightFailures = right.failed + right.assertionsFailed;
  return rightFailures - leftFailures || right.runLatency.p99Ms - left.runLatency.p99Ms || left.id.localeCompare(right.id);
}

const StatusCodeRow = memo(function StatusCodeRow({ item, total }: { item: StatusCodeCount; total: number }) {
  const ratio = total > 0 ? (item.count / total) * 100 : 0;
  return (
    <div className="status-code-row">
      <span className={`status-code status-code-${Math.floor(item.code / 100)}xx`}>{item.code}</span>
      <strong>{formatNumber(item.count)} responses</strong>
      <small>{formatNumber(ratio, 1)}%</small>
    </div>
  );
});

function qualityGateTitle(gate: QualityGateResult) {
  if (gate.checks.length === 0) {
    return "No SLO gates configured";
  }
  return gate.checks
    .map((check) => check.status === "insufficient"
      ? `${check.name}: ${formatNumber(check.samples ?? 0)} / ${formatNumber(check.minimumSamples ?? 0)} samples`
      : `${check.name}: ${formatNumber(check.actual, 2)} ${check.operator} ${formatNumber(check.threshold, 2)}`)
    .join("\n");
}

function qualityGateIcon(status: QualityGateResult["status"]) {
  if (status === "pass") {
    return <CheckCircle2 size={14} />;
  }
  if (status === "fail") {
    return <XCircle size={14} />;
  }
  return <AlertTriangle size={14} />;
}

function qualityGateLabel(status: QualityGateResult["status"]) {
  if (status === "pass") {
    return "SLO Passed";
  }
  if (status === "fail") {
    return "SLO Failed";
  }
  return "SLO Needs samples";
}

function latencySamplingTitle(batch: MetricsBatch | null) {
  if (!batch || batch.total === 0) {
    return "No latency samples yet";
  }
  const effectiveRate = (batch.runLatency.samples / batch.total) * 100;
  return `${formatNumber(effectiveRate, 2)}% sampled · percentile error < ${formatNumber(batch.latencyPercentileErrorBoundPct, 1)}%`;
}

function baselineLabel(verdict: BaselineComparison["verdict"]) {
  switch (verdict) {
    case "new-baseline":
      return "Baseline set";
    case "improved":
      return "Improved";
    case "regressed":
      return "Regressed";
    case "mixed":
      return "Mixed";
    case "stable":
      return "Stable";
  }
}

function baselineIcon(baseline: BaselineComparison) {
  switch (baseline.verdict) {
    case "improved":
      return <TrendingUp size={14} />;
    case "regressed":
      return <TrendingDown size={14} />;
    case "mixed":
    case "stable":
      return <MinusCircle size={14} />;
    case "new-baseline":
      return <CheckCircle2 size={14} />;
  }
}

function baselineTitle(baseline: BaselineComparison) {
  if (!baseline.deltas) {
    return "First completed run for this scenario";
  }
  return [
    `RPS ${formatSignedPercent(baseline.deltas.averageRpsPct)}`,
    `P95 ${formatSignedPercent(baseline.deltas.p95LatencyPct)}`,
    `P99 ${formatSignedPercent(baseline.deltas.p99LatencyPct)}`,
    `Fail ${formatSignedPoints(baseline.deltas.failureRatePctPoints)}`,
  ].join("\n");
}

function formatSignedPercent(value: number) {
  const sign = value > 0 ? "+" : "";
  return `${sign}${formatNumber(value, 1)}%`;
}

function formatSignedPoints(value: number) {
  const sign = value > 0 ? "+" : "";
  return `${sign}${formatNumber(value, 2)}pp`;
}

function drawChart(canvas: HTMLCanvasElement, points: MetricHistoryPoint[]) {
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  const width = Math.max(1, Math.floor(rect.width * dpr));
  const height = Math.max(1, Math.floor(rect.height * dpr));
  if (canvas.width !== width || canvas.height !== height) {
    canvas.width = width;
    canvas.height = height;
  }

  const ctx = canvas.getContext("2d");
  if (!ctx) {
    return;
  }

  ctx.clearRect(0, 0, width, height);
  ctx.save();
  ctx.scale(dpr, dpr);

  const cssWidth = width / dpr;
  const cssHeight = height / dpr;
  const padding = { left: 46, right: 18, top: 16, bottom: 28 };
  const chartWidth = cssWidth - padding.left - padding.right;
  const chartHeight = cssHeight - padding.top - padding.bottom;

  ctx.strokeStyle = "rgba(255,255,255,0.08)";
  ctx.lineWidth = 1;
  for (let i = 0; i <= 4; i += 1) {
    const y = padding.top + (chartHeight / 4) * i;
    ctx.beginPath();
    ctx.moveTo(padding.left, y);
    ctx.lineTo(cssWidth - padding.right, y);
    ctx.stroke();
  }

  if (points.length < 2) {
    ctx.fillStyle = "#8b98a7";
    ctx.font = "13px system-ui";
    ctx.fillText("Waiting for batched metrics", padding.left, padding.top + 24);
    ctx.restore();
    return;
  }

  const firstTs = points[0].timestampUnixMs;
  const lastTs = points[points.length - 1].timestampUnixMs;
  const span = Math.max(1, lastTs - firstTs);
  const maxRps = niceMax(Math.max(1, ...points.map((point) => point.rps || 0)));
  const maxLatency = niceMax(Math.max(1, ...points.map((point) => point.p99LatencyMs || 0)));

  drawSeries(ctx, points, "rps", firstTs, span, maxRps, "#39d0a3", padding, chartWidth, chartHeight, true);
  drawSeries(ctx, points, "latency", firstTs, span, maxLatency, "#5aa9ff", padding, chartWidth, chartHeight, false);

  ctx.fillStyle = "#8b98a7";
  ctx.font = "12px system-ui";
  ctx.fillText(formatNumber(maxRps), 8, padding.top + 4);
  ctx.fillText(`${formatNumber(maxLatency, 1)}ms`, cssWidth - 58, padding.top + 4);
  ctx.fillText("0", 28, padding.top + chartHeight + 4);
  ctx.restore();
}

function drawSeries(
  ctx: CanvasRenderingContext2D,
  points: MetricHistoryPoint[],
  key: "rps" | "latency",
  firstTs: number,
  span: number,
  maxValue: number,
  color: string,
  padding: { left: number; top: number },
  chartWidth: number,
  chartHeight: number,
  logScale: boolean,
) {
  ctx.strokeStyle = color;
  ctx.lineWidth = 2;
  ctx.beginPath();
  points.forEach((point, index) => {
    const x = padding.left + ((point.timestampUnixMs - firstTs) / span) * chartWidth;
    const rawValue = key === "rps" ? point.rps : point.p99LatencyMs;
    const normalized = logScale ? logNormalize(rawValue, maxValue) : Math.min(1, rawValue / maxValue);
    const y = padding.top + chartHeight - normalized * chartHeight;
    if (index === 0) {
      ctx.moveTo(x, y);
      return;
    }
    ctx.lineTo(x, y);
  });
  ctx.stroke();
}

function niceMax(value: number) {
  if (value <= 0) {
    return 1;
  }
  const power = Math.pow(10, Math.floor(Math.log10(value)));
  const normalized = value / power;
  if (normalized <= 2) {
    return 2 * power;
  }
  if (normalized <= 5) {
    return 5 * power;
  }
  return 10 * power;
}

function logNormalize(value: number, maxValue: number) {
  return Math.min(1, Math.log10(value + 1) / Math.log10(maxValue + 1));
}
