import type { MonitorHistoryEntry } from "@/hooks/use-monitor";

/** Monitoring stores Beijing timestamps; unlike purchase history, legacy naive values are Beijing time. */
export function stockTime(timestamp: string): number {
  return Date.parse(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(timestamp) ? timestamp : `${timestamp.replace(" ", "T")}+08:00`);
}

export function stockConfigKey(entry: MonitorHistoryEntry): string {
  const config = entry.config;
  if (typeof config?.config_key === "string" && config.config_key) return `key:${config.config_key}`;
  // Old records may not have a FQN: preserve distinct known fields, don't merge solely by display text.
  return `legacy:${JSON.stringify([config?.memory || "", config?.storage || "", config?.display || ""])}`;
}

export function restockHeatmap(entries: MonitorHistoryEntry[]) {
  const grid: number[][] = Array.from({ length: 7 }, () => Array(24).fill(0));
  const seen = new Set<string>();
  let invalid = 0;
  let first = Infinity;
  let last = -Infinity;
  for (const entry of entries) {
    if (entry.changeType !== "available") continue;
    const at = stockTime(entry.timestamp);
    if (!Number.isFinite(at)) { invalid++; continue; }
    const key = JSON.stringify([at, entry.datacenter?.toLowerCase(), stockConfigKey(entry)]);
    if (seen.has(key)) continue;
    seen.add(key);
    const bj = new Date(at + 8 * 3600000);
    grid[(bj.getUTCDay() + 6) % 7][bj.getUTCHours()]++;
    first = Math.min(first, at);
    last = Math.max(last, at);
  }
  return { grid, total: seen.size, max: Math.max(1, ...grid.flat()), invalid, first, last };
}
