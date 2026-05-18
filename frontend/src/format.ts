export function formatNumber(value: number, fractionDigits = 0) {
  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits: fractionDigits,
  }).format(value || 0);
}

export function formatBytes(value: number) {
  if (!value) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB"];
  let next = value;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return `${formatNumber(next, unit === 0 ? 0 : 1)} ${units[unit]}`;
}

export function formatDuration(valueMs: number) {
  const seconds = Math.max(1, Math.round(valueMs / 1_000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = seconds / 60;
  return `${formatNumber(minutes, minutes % 1 === 0 ? 0 : 1)}m`;
}

export function formatSavedAt(savedAtUnixMs: number) {
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(savedAtUnixMs));
}
