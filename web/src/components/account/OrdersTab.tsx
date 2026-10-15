import { Link } from "@tanstack/react-router";
import { DatacenterChip } from "@/components/common/MonitorDatacenters";
import { useState } from "react";
import { RefreshCw, ExternalLink, Search } from "lucide-react";
import { useAccountOrders, type OfficialOrder } from "@/hooks/use-account-orders";
import { useActiveAccount } from "@/hooks/use-active-account";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Chip } from "@/components/common/Chip";
import { orderStatusView } from "@/lib/order-status";

function dateText(value?: string) {
  if (!value) return "—";
  const d = new Date(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(value) ? value : `${value}Z`);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString("zh-CN");
}

export function OrdersTab({ order = "", ready = true }: { order?: string; ready?: boolean }) {
  const [account] = useActiveAccount();
  const [input, setInput] = useState(order);
  const { query, refresh, running } = useAccountOrders(account, order, ready);
  const snapshot = query.data;
  return <Card><CardContent className="p-3 sm:p-5 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><h3 className="text-sm font-semibold">OVH 官方订单</h3>
        <p className="text-xs text-muted-foreground mt-1">{order ? `正在查看订单 #${order}` : "最近 20 笔订单，包含在 OVH 官网创建的订单"}</p>
        {snapshot?.updatedAt && <p className="text-[11px] text-muted-foreground mt-1">上次刷新：{dateText(snapshot.updatedAt)}</p>}
      </div>
      <Button variant="outline" size="sm" disabled={!ready || running} onClick={() => refresh.mutate({ account, order })}>
        <RefreshCw className={`w-3.5 h-3.5 ${running ? "animate-spin" : ""}`} />{running ? "查询订单、支付及退款中…" : "刷新数据与状态"}
      </Button>
    </div>
    <div className="flex flex-wrap gap-2 items-center">
      <Input className="max-w-xs" value={input} onChange={(e) => setInput(e.target.value.replace(/\D/g, ""))} placeholder="查询原订单号（包含更早订单）" inputMode="numeric" />
      <Button variant="outline" size="sm" asChild disabled={!input}><Link to="/account" search={{ tab: "orders", order: input || undefined }}><Search className="w-3.5 h-3.5" />查询</Link></Button>
      {order && <Link to="/account" search={{ tab: "orders" }} className="text-xs underline">返回最近订单</Link>}
    </div>
    {running && <p className="text-xs text-muted-foreground">后台正在刷新，可以离开页面后再回来查看结果。</p>}
    {(query.isError || refresh.isError || snapshot?.error) && <p className="text-sm text-destructive break-words">{snapshot?.error || (refresh.error as any)?.response?.data?.error || (query.error as any)?.response?.data?.error || "请求失败，请重试；已有数据可能不是最新状态。"}</p>}
    {!!snapshot?.failed && <p className="text-xs text-warning">{snapshot.failed} 笔订单的详情、支付或退款查询未完整完成，请查看各项提示后重试。</p>}
    {snapshot?.warning && <p className="text-xs text-muted-foreground break-words">{snapshot.warning}</p>}
    {!snapshot && !query.isError && <p className="py-5 text-sm text-muted-foreground">正在读取订单…</p>}
    {snapshot?.status === "completed" && !snapshot.items.length && <p className="py-5 text-sm text-muted-foreground">暂无订单记录</p>}
    <div className="divide-y divide-border">{snapshot?.items.map((item) => <OrderRow key={item.orderId} item={item} account={snapshot.accountId} />)}</div>
  </CardContent></Card>;
}

function OrderRow({ item, account }: { item: OfficialOrder; account: string }) {
  const status = orderStatusView(item);
  const refund = item.refund;
  const retractable = item.retractionDate && new Date(item.retractionDate).getTime() > Date.now();
  return <div className="py-4 space-y-3">
    <div className="flex flex-wrap justify-between gap-2">
      <div><span className="text-xs text-muted-foreground select-none">原订单 #</span><span className="font-mono font-semibold select-text">{item.orderId}</span><p className="text-[11px] text-muted-foreground mt-1">{dateText(item.date)}</p></div>
      <div className="text-right"><p className="font-semibold">{item.priceWithTax?.text || (item.priceWithTax ? `${item.priceWithTax.value} ${item.priceWithTax.currencyCode}` : "—")}</p>
        <div className="flex gap-3 text-xs mt-1">{item.url && <a href={item.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 underline">OVH 订单<ExternalLink className="w-3 h-3" /></a>}{item.pdfUrl && <a href={item.pdfUrl} target="_blank" rel="noopener noreferrer" className="underline">订单 PDF</a>}</div>
      </div>
    </div>
    <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-xs">
      <div className="space-y-1"><p className="text-muted-foreground">支付 / 交付状态</p><Chip tone={status.tone} title={status.title}>{status.label}</Chip>
        {item.statusError && <p className="text-destructive break-words">查询失败：{item.statusError}</p>}
        {retractable && <p className="text-warning">撤回期限：{dateText(item.retractionDate)}</p>}
        {item.orderStatus === "notPaid" && item.expirationDate && <p>付款截止：{dateText(item.expirationDate)}</p>}
      </div>
      <div className="space-y-1"><p className="text-muted-foreground">退款</p>{refund ? <>
        <Chip tone="success">已退款</Chip>
        <p>退款单 <span className="font-mono select-text">{refund.id}</span></p>
        {refund.refundOrderId && <p>退款订单 #{refund.refundOrderId}</p>}
        <p>原发票 {refund.originalBillId || "—"}</p>
        {refund.price?.withTax != null && <p>{refund.price.withTax} {refund.price.currencyCode} · {dateText(refund.date)}</p>}
        {refund.pdfUrl && <a href={refund.pdfUrl} target="_blank" rel="noopener noreferrer" className="underline">退款 PDF</a>}
      </> : item.refundCheckError ? <p className="text-destructive break-words">{item.refundCheckError}</p> : <p>{item.orderStatus === "notPaid" ? "未付款，无需查询退款" : item.refundCheckedAt ? "未发现关联退款" : "未查询"}</p>}</div>
      <div className="space-y-1"><p className="text-muted-foreground">抢购历史</p>{item.histories.length ? <>
        {item.histories.map((h) => <p key={h.id} className="flex flex-wrap items-center gap-1.5">{h.planCode}<DatacenterChip code={h.datacenter} /></p>)}
        <Link to="/history" search={{ account, order: item.orderId }} className="inline-block underline">查看关联记录（{item.histories.length}）</Link>
      </> : <p className="text-muted-foreground">无关联抢购记录</p>}</div>
    </div>
  </div>;
}
