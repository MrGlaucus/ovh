import { useMemo, useState } from "react";
import type { PurchaseHistory } from "@/hooks/use-history";
import { historyStats, type HistoryRange, type HistoryStats } from "@/lib/history-stats";
import { orderStatusView } from "@/lib/order-status";

const moneyText = (value: number) => (value / 100).toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const shortStatus: Record<string, string> = { notPaid: "待付", checking: "核验", delivering: "交付中", delivered: "已交付", cancelling: "取消中", cancelled: "已取消", documentsRequested: "补材料", unknown: "未知", unqueried: "未查询" };

function Money({ stats }: { stats: HistoryStats }) {
  const money = Object.entries(stats.money);
  return <span className="inline-flex flex-wrap gap-x-3 gap-y-1 text-xs tabular-nums">
    {money.length ? money.map(([currency, value]) => <span key={currency} className="whitespace-nowrap">
      <span className="text-muted-foreground">{currency} </span>
      付款 <b>{moneyText(value.paid)}</b> <span className="text-muted-foreground">·</span> 退款 <b>{moneyText(value.refund)}</b> <span className="text-muted-foreground">·</span> 净支出 <b>{moneyText(value.paid - value.refund)}</b>
    </span>) : <span className="text-muted-foreground">暂无已确认金额</span>}
    {(stats.missingPaid > 0 || stats.missingRefund > 0 || stats.unconfirmed > 0) && <span className="text-warning">
      {[stats.missingPaid ? `${stats.missingPaid}笔付款金额待确认` : "", stats.missingRefund ? `${stats.missingRefund}笔退款金额待确认` : "", stats.unconfirmed ? `${stats.unconfirmed}单付款情况待确认` : ""].filter(Boolean).join(" · ")}
    </span>}
  </span>;
}

function Counts({ stats, onSelect, compact = false }: { stats: HistoryStats; onSelect: (status: string) => void; compact?: boolean }) {
  return <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1 text-xs tabular-nums">
    <button type="button" className="hover:underline whitespace-nowrap" onClick={() => onSelect("success")}>{stats.orders} 单</button>
    {Object.entries(stats.statuses).map(([status, count]) => <button key={status} type="button" onClick={() => onSelect(status)}
      className={`hover:underline whitespace-nowrap ${compact && !["notPaid", "delivering", "delivered"].includes(status) ? "hidden md:inline" : ""}`}>
      <span className="text-muted-foreground">{compact ? shortStatus[status] : orderStatusView({ orderStatus: status === "unqueried" ? "" : status }).label} </span>{count}
    </button>)}
    {!!stats.refunded && <button type="button" className={compact ? "hidden md:inline hover:underline" : "hover:underline"} onClick={() => onSelect("refunded")}>已退款 {stats.refunded}</button>}
  </span>;
}

export function HistoryStatistics({ items, accounts, account, now, range, onRangeChange, onSelect }: {
  items: PurchaseHistory[]; accounts: { id: string; label: string }[]; account: string; now: number;
  range: HistoryRange; onRangeChange: (range: HistoryRange) => void;
  onSelect: (account: string, status: string) => void;
}) {
  const [expanded, setExpanded] = useState(() => { try { return localStorage.getItem("history-statistics-expanded") === "1"; } catch { return false; } });
  const data = useMemo(() => historyStats(items, range, now, account), [items, range, now, account]);
  const label = (id: string) => !id || id === "unassigned" ? "未关联账户" : accounts.find((a) => a.id === id)?.label || `未知账户 · ${id}`;
  const select = (id: string, status: string) => onSelect(id || "unassigned", status);
  return <div className="mt-3 border-t border-border pt-2 space-y-2">
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <span className="text-xs font-medium" title="仅统计本面板历史；随账户和时间范围变化，不受搜索及状态筛选影响">{account === "all" ? "账户统计" : label(account)}</span>
      <div className="inline-flex rounded-md bg-muted p-0.5" aria-label="统计及列表时间范围">
        {([ ["today", "今日"], ["month", "本月"], ["all", "全部"] ] as const).map(([value, text]) => <button key={value} type="button" aria-pressed={range === value} onClick={() => onRangeChange(value)} className={`px-2 py-1 text-xs rounded ${range === value ? "bg-background shadow-sm font-medium" : "text-muted-foreground"}`}>{text}</button>)}
      </div>
      <Counts stats={data.total} compact onSelect={(status) => select(account, status)} />
      <div className="order-last w-full md:order-none md:w-auto md:ml-auto"><Money stats={data.total} /></div>
      <button type="button" className="text-xs text-muted-foreground ml-auto md:ml-0 shrink-0 hover:underline" aria-expanded={expanded} onClick={() => { const next = !expanded; setExpanded(next); try { localStorage.setItem("history-statistics-expanded", next ? "1" : "0"); } catch { /* optional preference */ } }}>{expanded ? "收起 ▴" : "明细 ▾"}</button>
    </div>
    {expanded && <>
      <div className="max-h-40 overflow-y-auto divide-y divide-border">
        {(account === "all" ? data.accounts : [data.total]).map((stats) => <div key={stats.accountId} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-xs">
          {account === "all" && <button type="button" className="font-medium hover:underline break-all" onClick={() => select(stats.accountId, "all")}>{label(stats.accountId)}</button>}
          <Counts stats={stats} onSelect={(status) => select(account === "all" ? stats.accountId : account, status)} />
          <span className="md:ml-auto"><Money stats={stats} /></span>
        </div>)}
        {!data.total.orders && <p className="text-xs text-muted-foreground py-2">该范围暂无订单</p>}
      </div>
      <p className="text-[10px] text-muted-foreground">按北京时间的下单时间统计，仅随账户和时间切换；状态数量按账户＋原订单号去重，已退款可与订单状态重叠。金额含税、按币种分列；核验中暂不计付款。退款为已关联退款单金额，不代表到账。净支出仅计算已确认金额。时间切换同时筛选统计和列表，可叠加账户、状态和搜索条件。{data.invalidDates > 0 && ` ${data.invalidDates}单下单时间无效，仅纳入全部范围。`}</p>
    </>}
  </div>;
}
