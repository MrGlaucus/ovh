import { useMemo, useState } from "react";
import type { MonitorHistoryEntry } from "@/hooks/use-monitor";
import { restockHeatmap } from "@/lib/restock-heatmap";

const days = ["一", "二", "三", "四", "五", "六", "日"];
const date = (at: number) => new Date(at).toLocaleDateString("zh-CN", { timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit" });

/** Adapted from upstream HeatGrid (0140451), using existing theme and accessible touch/keyboard readouts. */
export function RestockHeatmap({ entries }: { entries: MonitorHistoryEntry[] }) {
  const data = useMemo(() => restockHeatmap(entries), [entries]);
  const [selected, setSelected] = useState<{ day: number; hour: number } | null>(null);
  const tone = (value: number) => value === 0 ? "bg-muted" : value / data.max <= 0.25 ? "bg-emerald-500/25" : value / data.max <= 0.5 ? "bg-emerald-500/45" : value / data.max <= 0.75 ? "bg-emerald-500/70" : "bg-emerald-500";
  return <section className="rounded-xl border border-border p-3 mb-3 space-y-2" aria-label="补货热力图">
    <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
      <span className="font-medium">补货热力图 <span className="font-normal text-muted-foreground">· 星期 × 小时（北京时间）</span></span>
      <span aria-live="polite" className="tabular-nums">{selected ? `周${days[selected.day]} ${String(selected.hour).padStart(2, "0")}:00–${String(selected.hour).padStart(2, "0")}:59 · ${data.grid[selected.day][selected.hour]} 次` : `已记录 ${data.total} 次上货`}</span>
    </div>
    <div className="overflow-x-auto pb-1">
      <div className="min-w-[620px] grid gap-1" style={{ gridTemplateColumns: "28px repeat(24, minmax(0, 1fr))" }}>
        <span />{Array.from({ length: 24 }, (_, hour) => <span key={hour} className="text-[10px] text-muted-foreground text-center tabular-nums">{String(hour).padStart(2, "0")}</span>)}
        {data.grid.map((row, day) => <div key={day} className="contents">
          <span className="text-[10px] text-muted-foreground self-center">周{days[day]}</span>
          {row.map((value, hour) => <button type="button" key={hour}
            aria-label={`周${days[day]} ${hour}点，${value}次上货`} aria-pressed={selected?.day === day && selected.hour === hour}
            title={`周${days[day]} ${hour}:00–${hour}:59：${value} 次上货`}
            onMouseEnter={() => setSelected({ day, hour })} onFocus={() => setSelected({ day, hour })} onClick={() => setSelected({ day, hour })}
            className={`h-6 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${tone(value)} ${selected?.day === day && selected.hour === hour ? "ring-2 ring-foreground/60" : ""}`} />)}
        </div>)}
      </div>
    </div>
    <div className="flex flex-wrap items-center justify-between gap-2 text-[10px] text-muted-foreground">
      <span>{data.total ? `上货记录日期：${date(data.first)}—${date(data.last)}` : "当前筛选下暂无上货记录"}{data.invalid > 0 ? ` · ${data.invalid} 条时间异常未统计` : ""}</span>
      <span className="inline-flex items-center gap-1">少 {[0, 0.25, 0.5, 0.75, 1].map((v) => <i key={v} className={`inline-block w-3 h-3 rounded-sm ${tone(v * data.max)}`} />)} 多（单格最多 {data.total ? data.max : 0} 次）</span>
    </div>
    <p className="text-[10px] text-muted-foreground">每个型号仅保留最近100条库存变化，图中只统计其中的上货事件（含首次发现有货），下架及校验失败不计入；不同机房、配置分别计次，不代表库存台数或未来补货概率。手机可横向滑动并点格子查看。</p>
  </section>;
}
