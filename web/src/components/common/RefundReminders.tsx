import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { useRefreshOrderStatus, type PurchaseHistory } from "@/hooks/use-history";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

interface Reminder { history: PurchaseHistory; account: string; name: string; configuration: string; deadline: string; url: string; warning?: string }
function remaining(ms: number) {
 if (ms <= 0) return "窗口已结束";
 const mins = Math.ceil(ms / 60000);
 return mins >= 1440 ? `${Math.floor(mins / 1440)}天${Math.floor(mins % 1440 / 60)}小时` : mins >= 60 ? `${Math.floor(mins / 60)}小时${mins % 60}分` : `${mins}分钟`;
}
const dateText = (v: string) => new Date(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(v) ? v : `${v}Z`).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" });

export function RefundReminders() {
 const refresh = useRefreshOrderStatus();
 const query = useQuery({ queryKey: ["refund-reminders"], queryFn: async () => (await api.get<{items: Reminder[]}>("/refund-reminders")).data, refetchInterval: refresh.isPending ? 3000 : 60000 });
 const [now, setNow] = useState(Date.now());
 useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 60000); return () => clearInterval(timer); }, []);
 useEffect(() => { if (!refresh.isPending) void query.refetch(); }, [refresh.isPending]);
 const items = query.data?.items || [];
 const pending = items.filter(i => new Date(i.deadline).getTime() > now);
 const expired = items.filter(i => new Date(i.deadline).getTime() <= now);
 const render = (i: Reminder) => {
  const ms = new Date(i.deadline).getTime() - now;
  const checked = i.history.orderStatusAt;
  const stale = !checked || now - new Date(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(checked) ? checked : `${checked}Z`).getTime() > 10 * 60000;
  return <div key={`${i.history.accountId}:${i.history.orderId}`} className="rounded-lg border p-3 grid gap-2 sm:grid-cols-[1fr_1fr_auto] text-sm">
   <div><p className="font-semibold">{i.name}</p><p className="text-xs text-muted-foreground break-words">{i.configuration} · {i.history.datacenter.toUpperCase()}</p></div>
   <div><p>{i.account}</p><p className="text-xs">原订单 #<span className="font-mono select-text">{i.history.orderId}</span></p><p className="text-xs text-muted-foreground">{checked ? `状态核实：${dateText(checked)}` : "状态尚未核实"}</p></div>
   <div><p className={ms <= 24 * 3600000 ? "text-red-600 font-semibold" : "text-amber-600 font-semibold"}>{remaining(ms)}</p><p className="text-xs text-muted-foreground">截止 {dateText(i.deadline)}</p></div>
   {(stale || i.history.refundCheckError) && <p className="sm:col-span-3 text-xs text-amber-600">{i.history.refundCheckError || "状态可能已变化，请刷新核实"}</p>}
   <div className="sm:col-span-3 flex gap-4 text-xs"><Link className="underline" to="/account" search={{tab:"orders",account:i.history.accountId,order:i.history.orderId}}>查看订单</Link><Link className="underline" to="/history" search={{account:i.history.accountId,order:i.history.orderId}}>抢购历史</Link></div>
  </div>;
 };
 return <Card><CardContent className="p-3.5 sm:p-6 space-y-3">
  <div className="flex justify-between items-center gap-2"><h2 className="font-semibold">⏰ 退款期限提醒{query.data ? ` · ${pending.length} 笔待关注` : ""}</h2><Button size="sm" variant="outline" disabled={refresh.isPending} onClick={() => refresh.mutate()}>{refresh.label}</Button></div>
  <p className="text-xs text-muted-foreground">已付款·交付中且退款窗口≤72小时；每天北京时间09:00通过TG提醒。截止沿用面板估算规则，以OVH实际可撤回状态为准。所有时间为北京时间。</p>
  {query.isError && <p className="text-sm text-destructive">提醒数据读取失败，无法确认是否有临期订单。<button className="underline ml-2" onClick={() => void query.refetch()}>重试</button></p>}
  {query.isPending && <p className="text-sm text-muted-foreground">正在读取…</p>}
  {!!pending.length && <p className="text-xs">🔴 不足24小时 {pending.filter(i => new Date(i.deadline).getTime()-now<=86400000).length} 笔　🟠 1～3天 {pending.filter(i => new Date(i.deadline).getTime()-now>86400000).length} 笔</p>}
  {pending.map(render)}
  {query.isSuccess && !pending.length && <p className="text-sm text-muted-foreground">暂无临近退款期限的交付中订单</p>}
  {!!expired.length && <details><summary className="text-sm text-red-600 cursor-pointer">已过期但仍在交付中（{expired.length}笔）</summary><div className="space-y-2 mt-2">{expired.map(render)}</div></details>}
 </CardContent></Card>;
}
