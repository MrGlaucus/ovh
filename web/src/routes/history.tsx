import { createFileRoute } from "@tanstack/react-router";
import { Clock, RefreshCw, Trash2, Search, Hourglass, Timer, CreditCard, Eraser, RotateCcw, FileDown } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { PageHeader } from "@/components/common/PageHeader";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Chip } from "@/components/common/Chip";
import { AccountChip } from "@/components/common/AccountChip";
import { TimingChip } from "@/components/common/TimingChip";
import { Skeleton } from "@/components/common/Skeleton";
import { EmptyState } from "@/components/common/EmptyState";
import { LoadFailed } from "@/components/common/LoadFailed";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  useHistory,
  useClearHistory,
  useClearFailedHistory,
  useRemoveHistoryItem,
  usePayHistoryOrder,
  useRefreshOrderStatus,
  type PurchaseHistory,
} from "@/hooks/use-history";
import { useServers } from "@/hooks/use-servers";
import { describeOptionCodes } from "@/lib/option-groups";

/**
 * 订单支付状态 → 标签。取值是 OVH 的 billing.order.OrderStatusEnum,三区一致。
 * 这才是用户真正关心的:「下单成功」只说明订单建了,付没付、过没过期,看这里。
 */
function orderStatusView(item: PurchaseHistory): {
  label: string;
  tone: "success" | "warning" | "danger" | "info" | "progress" | "processing" | "default";
  paid: boolean;
  closed: boolean;
  title: string;
} {
  switch (item.orderStatus) {
    case "notPaid":
      return { label: "待付款", tone: "warning", paid: false, closed: false, title: "订单已创建,尚未付款;倒计时结束前未付款会作废" };
    case "checking":
      // 核验中改用青色：info 蓝和同列的「退款窗口倒计时」撞色；
      // 与交付中的紫、终态的绿都能拉开。
      return { label: "付款核验中", tone: "processing", paid: true, closed: false, title: "OVH 已收到付款,正在核验" };
    case "delivering":
      // 交付中≠已交付：都用绿色的话扫一遍列表分不出"还要等"和"已经到手"，
      // 交付中改用紫色：info 蓝和同列的「付款核验中」「退款窗口倒计时」撞色。
      return { label: "已付款·交付中", tone: "progress", paid: true, closed: false, title: "已付款,OVH 正在交付服务器" };
    case "delivered":
      return { label: "已付款·已交付", tone: "success", paid: true, closed: true, title: "已付款并交付" };
    case "cancelling":
      return { label: "取消中", tone: "danger", paid: false, closed: true, title: "订单正在取消" };
    case "cancelled":
      return { label: "已取消", tone: "danger", paid: false, closed: true, title: "订单已取消(过期未付款也会走到这里)" };
    case "documentsRequested":
      return { label: "需补材料", tone: "warning", paid: false, closed: false, title: "OVH 要求补充证件/材料后才处理" };
    case "unknown":
      return { label: "状态未知", tone: "default", paid: false, closed: false, title: "OVH 返回 unknown" };
    default:
      return { label: "状态未查到", tone: "default", paid: false, closed: false, title: "还没从 OVH 读到订单状态,点「刷新」再试" };
  }
}
import { CURRENCY_UNKNOWN_HINT } from "@/lib/money";

/** 抢购历史：表格 + 搜索 + 状态过滤 */
export const Route = createFileRoute("/history")({
  component: HistoryPage,
});

/**
 * 成交价 + 币种。
 *
 * 币种缺失时只显示金额并在 title 里说明,绝不补 "EUR":后端(internal/purchase/purchase.go)
 * 现在拿不到 currencyCode 就如实留空,而币种是按子公司定的 ——
 * 实测目录 locale.currencyCode:IE=EUR / CA=QC=CAD / US=WE=WS=USD / SG=SGD / AU=AUD。
 * 前端再兜底成欧元,美区/加区的订单会被标成 €,用户按错的币种对账。
 */
function HistoryPrice({ item, strike }: { item: PurchaseHistory; strike: boolean }) {
  const value = item.price?.withTax;
  if (value == null) return <span className="text-muted-foreground">—</span>;
  const currency = (item.price?.currencyCode || "").trim();
  return (
    <span
      className={`font-mono font-medium text-success ${strike ? "line-through" : ""}`}
      title={currency ? undefined : CURRENCY_UNKNOWN_HINT}
    >
      {value}
      {currency ? ` ${currency}` : <span className="text-muted-foreground"> (币种未知)</span>}
    </span>
  );
}

/**
 * 退款信息（桌面列 / 手机卡片共用）：已退款 Chip + 金额 + 退款单 PDF 入口。
 *
 * OVH 的退款单没有状态机（schema 里连 status 字段都没有），记录出现即"已退款"；
 * "钱到没到账"是支付渠道侧的几天~几十天时间差，API 看不到，不做假状态。
 * 数据由后端刷新订单状态时顺带查 GET /me/refund?orderId= 落库。
 */
function RefundInfoView({ refund }: { refund?: PurchaseHistory["refund"] }) {
  if (!refund) return <span className="text-muted-foreground">—</span>;
  const value = refund.price?.withTax;
  const currency = (refund.price?.currencyCode || "").trim();
  const dateLabel = refund.date
    ? parseStoredTime(refund.date).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })
    : "";
  return (
    <div className="flex flex-col items-start gap-1">
      <Chip tone="default" title={`退款单 #${refund.id}${dateLabel ? ` · 退款日期 ${dateLabel}` : ""}`}>
        <RotateCcw className="w-3 h-3" />
        已退款
      </Chip>
      <span className="flex items-center gap-2 text-[11px] flex-wrap">
        {value != null && (
          <span className="font-mono text-muted-foreground">
            {value}
            {currency ? ` ${currency}` : ""}
          </span>
        )}
        {/* 手机上摸不出 title，退款日期直接显示；桌面有 hover 就不占列宽 */}
        {dateLabel && <span className="text-muted-foreground md:hidden">{dateLabel}</span>}
        {refund.pdfUrl && (
          <a
            href={refund.pdfUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-0.5 text-info hover:underline"
            title="下载退款单 PDF"
          >
            <FileDown className="w-3 h-3" />
            PDF
          </a>
        )}
      </span>
    </div>
  );
}

/** 订单有效期 15 天，未提供 expirationTime 时用 purchaseTime + 15d 兜底 */
const ORDER_VALIDITY_MS = 15 * 24 * 60 * 60 * 1000;

/** 把毫秒倒计时格式化为 `2天5时12分` / `12分` / `已过期` */
function formatCountdown(remainingMs: number): string {
  if (remainingMs <= 0) return "已过期";
  const totalMinutes = Math.floor(remainingMs / 60_000);
  const days = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return `${days}天${hours}时${minutes}分`;
  if (hours > 0) return `${hours}时${minutes}分`;
  return `${minutes}分`;
}

/** 旧版 Docker 写入的时间没有时区；容器时钟为 UTC，因此按 UTC 补齐以避免页面少 8 小时。 */
function parseStoredTime(value: string): Date {
  return new Date(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(value) ? value : `${value}Z`);
}

function getExpirationMs(item: PurchaseHistory): number {
  if (item.expirationTime) return new Date(item.expirationTime).getTime();
  return parseStoredTime(item.purchaseTime).getTime() + ORDER_VALIDITY_MS;
}

/**
 * 退款窗口截止:OVH 的 14 天撤回权(right of withdrawal),官方政策为
 * 「自下单次日起 14 天内」。次日算第 1 天,第 14 天结束即下单日之后第 15 天的
 * 00:00(本地时区)。窗口只跟下单时间走,早付晚付一样长。
 */
const REFUND_WINDOW_DAYS = 14;
function getRefundDeadlineMs(item: PurchaseHistory): number {
  const d = parseStoredTime(item.purchaseTime);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1 + REFUND_WINDOW_DAYS).getTime();
}

function fmtDeadline(deadlineMs: number): string {
  return new Date(deadlineMs).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function refundWindowTitle(deadlineMs: number): string {
  return `退款窗口（OVH 14 天撤回权）：自下单次日起 14 天内可申请取消退款，截止 ${fmtDeadline(deadlineMs)}`;
}

function refundExpiredTitle(deadlineMs: number): string {
  return `退款窗口（OVH 14 天撤回权）已结束，已于 ${fmtDeadline(deadlineMs)} 截止`;
}

function HistoryPage() {
  const list = useHistory();
  const clear = useClearHistory();
  const clearFailed = useClearFailedHistory();
  const remove = useRemoveHistoryItem();
  const pay = usePayHistoryOrder();
  const refreshStatus = useRefreshOrderStatus();
  // 历史仅保存稳定的 planCode；显示名从当前服务器目录实时映射，未命中时回退原始标识。
  const servers = useServers();
  const serverNames = useMemo(() => new Map((servers.data || []).map((server) => [server.planCode, server.name])), [servers.data]);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "success" | "failed">("all");
  const [confirmClear, setConfirmClear] = useState(false);
  const [confirmClearFailed, setConfirmClearFailed] = useState(false);
  const [deleteItem, setDeleteItem] = useState<PurchaseHistory | null>(null);
  const [paymentItem, setPaymentItem] = useState<PurchaseHistory | null>(null);
  const [now, setNow] = useState(() => Date.now());

  // 每分钟刷新一次 now，让所有行的倒计时同步推进
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  const items = list.data || [];
  const filtered = useMemo(() => {
    const s = search.trim().toLowerCase();
    const timeOf = (i: PurchaseHistory) => {
      const t = parseStoredTime(i.purchaseTime).getTime();
      return Number.isFinite(t) ? t : 0;
    };
    return items
      .filter((i) => {
        if (statusFilter !== "all" && i.status !== statusFilter) return false;
        if (s && !`${serverNames.get(i.planCode) || ""} ${i.planCode} ${i.datacenter} ${i.orderId || ""}`.toLowerCase().includes(s)) return false;
        return true;
      })
      // 后端按写入顺序返回（最早的在最前），展示一律最新的在最上 —— 打开页面
      // 第一眼就是刚下完的那单，不用先滚到底。
      .sort((a, b) => timeOf(b) - timeOf(a));
  }, [items, search, serverNames, statusFilter]);
  const failedCount = useMemo(() => items.filter((i) => i.status === "failed").length, [items]);

  return (
    <div className="space-y-3 sm:space-y-6">
      <PageHeader
        icon={Clock}
        title="抢购历史"
        description="查看服务器购买历史记录"
        action={
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => refreshStatus.mutate()}
              disabled={list.isFetching || refreshStatus.isPending}
              title="向 OVH 查询未到终态订单的支付状态,然后重载列表"
            >
              <RefreshCw
                className={`w-4 h-4 ${list.isFetching || refreshStatus.isPending ? "animate-spin" : ""}`}
              />
              刷新状态
            </Button>
            <Button
              variant="outline"
              onClick={() => setConfirmClearFailed(true)}
              disabled={failedCount === 0 || clearFailed.isPending}
              title="删除所有失败的抢购历史记录；成功记录（含待付款、交付中的订单）会保留"
            >
              <Eraser className="w-4 h-4" />
              清除失败记录
            </Button>
            <Button variant="outline" onClick={() => setConfirmClear(true)} disabled={items.length === 0}>
              <Trash2 className="w-4 h-4" />
              清空
            </Button>
          </div>
        }
      />

      <Card>
        <CardContent className="p-3 sm:p-5">
          {/* 手机端两个控件并排:搜索框和状态下拉各占一整行时白吃 ~70px,
              而「所有状态」这种下拉本来就不需要整行宽 */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 sm:gap-3">
            <div className="relative">
              <Search className="absolute left-3.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none" />
              <Input
                placeholder="搜索型号 / 机房 / 订单号..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-9 rounded-full"
              />
            </div>
            <Select value={statusFilter} onValueChange={(v) => setStatusFilter(v as any)}>
              <SelectTrigger className="rounded-full">
                <SelectValue placeholder="所有状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">所有状态</SelectItem>
                <SelectItem value="success">成功</SelectItem>
                <SelectItem value="failed">失败</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      {list.isPending ? (
        <Card>
          <CardContent className="p-4 space-y-2">
            {Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-16 rounded-xl" />)}
          </CardContent>
        </Card>
      ) : list.isError ? (
        /* 这页管的是订单的付款倒计时。请求挂了还画「没有匹配的订单」,用户会以为自己压根没下过单,
           于是不去付款 —— 未付款订单 15 天到点自动作废,钱和机器一起没了。全站误导里就数这一条
           后果最实在,所以失败态必须自己占一支,并且要把"这不是说你没有订单"写在脸上。 */
        <Card>
          <LoadFailed
            icon={Clock}
            title="抢购历史读取失败 —— 不是「你没有订单」,是我们没读到"
            error={list.error}
            onRetry={() => list.refetch()}
          />
          <p className="px-6 pb-5 text-[11px] text-muted-foreground text-center">
            未付款的订单仍在走 15 天倒计时,别把这片空白当成"没有订单"。
            请重试,或直接去 OVH 管理面板确认待付款的订单。
          </p>
        </Card>
      ) : filtered.length === 0 ? (
        <Card>
          <EmptyState icon={Clock} title="没有匹配的订单" />
        </Card>
      ) : (
        <>
          {/* 桌面 / 平板:横向表格 */}
          <Card className="hidden md:block overflow-x-auto">
            <table className="w-full min-w-[760px] table-fixed">
              <thead>
                <tr className="text-left text-[11px] font-medium text-muted-foreground border-b border-border">
                  <th className="w-[27%] px-4 py-3">型号</th>
                  <th className="w-[6%] px-3 py-3">机房</th>
                  <th className="w-[16%] px-3 py-3">配置</th>
                  <th className="w-[8%] px-3 py-3">价格</th>
                  <th className="w-[14%] px-3 py-3">订单状态</th>
                  <th className="w-[11%] px-3 py-3">退款</th>
                  <th className="w-[10%] px-3 py-3">时间</th>
                  <th className="w-[8%] px-4 py-3 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {filtered.map((item) => <HistoryRow key={item.id} item={item} displayName={serverNames.get(item.planCode)} now={now} onDelete={() => setDeleteItem(item)} onPay={() => setPaymentItem(item)} />)}
              </tbody>
            </table>
          </Card>

          {/* 手机:卡片堆叠,每条订单一张卡 */}
          <div className="md:hidden space-y-2">
            {filtered.map((item) => <HistoryCard key={item.id} item={item} displayName={serverNames.get(item.planCode)} now={now} onDelete={() => setDeleteItem(item)} onPay={() => setPaymentItem(item)} />)}
          </div>
        </>
      )}

      <Dialog open={!!paymentItem} onOpenChange={(open) => !open && setPaymentItem(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>使用默认支付方式付款？</DialogTitle>
            <DialogDescription>
              将使用账户 {paymentItem?.accountId} 已设置的默认支付方式，支付订单 {paymentItem?.orderId}。
              此操作会发起真实扣款，请确认金额与支付方式无误。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPaymentItem(null)}>取消</Button>
            <Button
              disabled={pay.isPending || !paymentItem?.orderId}
              onClick={() => paymentItem?.orderId && pay.mutate(
                { id: paymentItem.id, orderId: paymentItem.orderId },
                { onSuccess: () => setPaymentItem(null) },
              )}
            >
              <CreditCard className="w-4 h-4" />
              确认付款
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!deleteItem} onOpenChange={(open) => !open && setDeleteItem(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除这条抢购历史？</DialogTitle>
            <DialogDescription>将删除 {deleteItem?.planCode} · {deleteItem?.datacenter.toUpperCase()} 的历史记录，此操作不可撤销。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteItem(null)}>取消</Button>
            <Button variant="destructive" disabled={remove.isPending} onClick={() => deleteItem && remove.mutate(deleteItem.id, { onSuccess: () => setDeleteItem(null) })}>删除</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmClearFailed} onOpenChange={setConfirmClearFailed}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>清除所有失败记录？</DialogTitle>
            <DialogDescription>
              将删除 {failedCount} 条失败的抢购历史；成功记录（含待付款、交付中的订单）全部保留。此操作不可撤销。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmClearFailed(false)}>取消</Button>
            <Button
              variant="destructive"
              disabled={clearFailed.isPending}
              onClick={() => clearFailed.mutate(undefined, { onSuccess: () => setConfirmClearFailed(false) })}
            >
              确认清除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmClear} onOpenChange={setConfirmClear}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认清空所有历史？</DialogTitle>
            <DialogDescription>所有抢购历史将被删除，此操作不可撤销。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmClear(false)}>取消</Button>
            <Button variant="destructive" onClick={() => { clear.mutate(); setConfirmClear(false); }}>
              确认清空
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function HistoryRow({ item, displayName, now, onDelete, onPay }: { item: PurchaseHistory; displayName?: string; now: number; onDelete: () => void; onPay: () => void }) {
  const st = orderStatusView(item);
  const showCountdown = item.status === "success" && !!item.orderId && !st.paid && !st.closed;
  const remainingMs = showCountdown ? getExpirationMs(item) - now : 0;
  const isExpired = showCountdown && remainingMs <= 0;
  const canPay = item.orderStatus === "notPaid" && !!item.accountId && !isExpired;
  const isUrgent = showCountdown && !isExpired && remainingMs < 24 * 60 * 60 * 1000;
  // 退款窗口:已付款订单都显示(OVH 14 天撤回权;未付款订单不适用——到期
  // 自动取消)。窗口只跟下单时间走,交付快慢不影响。窗口内显示倒计时,
  // 过期后换灰色的"已结束" —— 还能不能退款,答案都要在列表上。
  const refundDeadlineMs = st.paid ? getRefundDeadlineMs(item) : 0;
  const hasRefundWindow = item.status === "success" && !!item.orderId && refundDeadlineMs > 0;
  const showRefund = hasRefundWindow && refundDeadlineMs > now;
  const refundExpired = hasRefundWindow && refundDeadlineMs <= now;
  const refundRemainingMs = showRefund ? refundDeadlineMs - now : 0;
  const refundUrgent = showRefund && refundRemainingMs < 3 * 24 * 60 * 60 * 1000;
  const delayLabel = item.delaySeconds && item.delaySeconds > 0 ? `有货后延迟 ${item.delaySeconds}s` : "立即抢购";
  return (
    <tr className={`text-[13px] hover:bg-muted ${isExpired ? "opacity-60" : ""}`}>
      <td className={`px-4 py-3 ${isExpired ? "line-through" : ""}`}>
        <div className="min-w-0">
          <div className="truncate font-mono font-semibold" title={displayName || item.planCode}>{displayName || item.planCode}</div>
          <div className="mt-1 flex items-center gap-1.5 text-[10px] text-muted-foreground whitespace-nowrap">
            {displayName && <span className="font-mono">{item.planCode}</span>}
            <AccountChip accountId={item.accountId} />
            <TimingChip totalMs={item.totalMs} phases={item.timing} />
            <span title="下单延迟">· {delayLabel}</span>
          </div>
        </div>
      </td>
      <td className={`px-3 py-3 whitespace-nowrap ${isExpired ? "line-through" : ""}`}>{item.datacenter.toUpperCase()}</td>
      {/* 配置列与抢购队列同款：addon code 能解析成人话就展示人话（如 64 GB · SOFTRAID 2× 480GB SSD），
          认不出的原样保留；原始 code 留在 title 里供核对。 */}
      <td className={`px-3 py-3 text-muted-foreground truncate ${isExpired ? "line-through" : ""}`} title={item.options?.join(", ") || "默认配置"}>
        {item.options && item.options.length > 0 ? describeOptionCodes(item.options) : "默认配置"}
      </td>
      <td className="px-3 py-3"><HistoryPrice item={item} strike={isExpired} /></td>
      <td className="px-3 py-3">
        <div className="flex flex-col items-start gap-1">
          {item.status === "success" ? <Chip tone={st.tone} title={st.title}>{st.label}</Chip> : <Chip tone="danger" title={item.errorMessage || "抢购失败"}>失败</Chip>}
          {showCountdown && (
            <Chip tone={isExpired ? "danger" : isUrgent ? "warning" : "info"} title="付款窗口：倒计时结束前未付款订单会作废">
              <Hourglass className="w-3 h-3" />{formatCountdown(remainingMs)}
            </Chip>
          )}
          {showRefund && (
            <Chip tone={refundUrgent ? "warning" : "info"} title={refundWindowTitle(refundDeadlineMs)}>
              <RotateCcw className="w-3 h-3" />退款 {formatCountdown(refundRemainingMs)}
            </Chip>
          )}
          {refundExpired && (
            <Chip tone="default" title={refundExpiredTitle(refundDeadlineMs)}>
              <RotateCcw className="w-3 h-3" />退款窗口已结束
            </Chip>
          )}
        </div>
      </td>
      <td className="px-3 py-3">
        <RefundInfoView refund={item.refund} />
      </td>
      <td className="px-3 py-3 text-[11px] text-muted-foreground font-mono whitespace-nowrap">
        {parseStoredTime(item.purchaseTime).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
      </td>
      <td className="px-4 py-3">
        <div className="flex items-center justify-end gap-3 whitespace-nowrap">
          {canPay && <button type="button" onClick={onPay} className="inline-flex items-center gap-1 text-success hover:underline text-[12px]" title="使用该账户默认支付方式付款"><CreditCard className="w-3 h-3" />付款</button>}
          <button type="button" onClick={onDelete} className="inline-flex items-center gap-1 text-destructive hover:underline text-[12px]" title="删除此历史记录"><Trash2 className="w-3 h-3" />删除</button>
        </div>
      </td>
    </tr>
  );
}

/** 手机端的订单卡片渲染。跟 HistoryRow 字段一一对应,但堆叠成卡片。 */
function HistoryCard({ item, displayName, now, onDelete, onPay }: { item: PurchaseHistory; displayName?: string; now: number; onDelete: () => void; onPay: () => void }) {
  const st = orderStatusView(item);
  const showCountdown = item.status === "success" && !!item.orderId && !st.paid && !st.closed;
  const remainingMs = showCountdown ? getExpirationMs(item) - now : 0;
  const isExpired = showCountdown && remainingMs <= 0;
  const canPay = item.orderStatus === "notPaid" && !!item.accountId && !isExpired;
  const isUrgent = showCountdown && !isExpired && remainingMs < 24 * 60 * 60 * 1000;
  const refundDeadlineMs = st.paid ? getRefundDeadlineMs(item) : 0;
  const hasRefundWindow = item.status === "success" && !!item.orderId && refundDeadlineMs > 0;
  const showRefund = hasRefundWindow && refundDeadlineMs > now;
  const refundExpired = hasRefundWindow && refundDeadlineMs <= now;
  const refundRemainingMs = showRefund ? refundDeadlineMs - now : 0;
  const refundUrgent = showRefund && refundRemainingMs < 3 * 24 * 60 * 60 * 1000;
  return (
    <Card className={isExpired ? "opacity-60" : ""}>
      <CardContent className="p-4 space-y-3">
        <div className="flex items-start justify-between gap-3">
          <span className={`min-w-0 truncate font-semibold text-[13px] ${isExpired ? "line-through" : ""}`} title={displayName || item.planCode}>{displayName || item.planCode}</span>
          {item.status === "success" ? <Chip tone={st.tone} title={st.title} className="shrink-0">{st.label}</Chip> : <Chip tone="danger" title={item.errorMessage || "抢购失败"} className="shrink-0">失败</Chip>}
        </div>
        <div className="flex items-center gap-1.5 flex-wrap text-[10px] text-muted-foreground">
          {displayName && <span className="font-mono">{item.planCode}</span>}
          <AccountChip accountId={item.accountId} />
          <Chip tone="default" className="text-[10px]">{item.datacenter.toUpperCase()}</Chip>
          <TimingChip totalMs={item.totalMs} phases={item.timing} />
          <Chip tone={item.delaySeconds && item.delaySeconds > 0 ? "info" : "default"} className="text-[10px]" title="下单延迟">
            <Timer className="w-3 h-3" />{item.delaySeconds && item.delaySeconds > 0 ? `延迟 ${item.delaySeconds}s` : "立即"}
          </Chip>
        </div>
        <div className={`text-[11px] leading-5 text-muted-foreground break-words ${isExpired ? "line-through" : ""}`} title={item.options?.join(", ")}>
          {item.options && item.options.length > 0 ? describeOptionCodes(item.options) : "默认配置"}
        </div>
        {/* 没退款就不占位 —— 手机卡片是堆叠布局，空占位纯浪费一屏 */}
        {item.refund && <RefundInfoView refund={item.refund} />}
        <div className="flex items-center justify-between gap-2 text-[11px]">
          <span className="text-muted-foreground font-mono">{parseStoredTime(item.purchaseTime).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}</span>
          {item.price?.withTax != null && <HistoryPrice item={item} strike={isExpired} />}
        </div>
        <div className="flex items-center justify-between gap-3 border-t border-border pt-3">
          {showCountdown ? (
            <Chip tone={isExpired ? "danger" : isUrgent ? "warning" : "info"} title="付款窗口：倒计时结束前未付款订单会作废"><Hourglass className="w-3 h-3" />{formatCountdown(remainingMs)}</Chip>
          ) : showRefund ? (
            <Chip tone={refundUrgent ? "warning" : "info"} title={refundWindowTitle(refundDeadlineMs)}><RotateCcw className="w-3 h-3" />退款 {formatCountdown(refundRemainingMs)}</Chip>
          ) : refundExpired ? (
            <Chip tone="default" title={refundExpiredTitle(refundDeadlineMs)}><RotateCcw className="w-3 h-3" />退款窗口已结束</Chip>
          ) : (
            <span />
          )}
          <div className="flex items-center gap-4 whitespace-nowrap">
            {canPay && <button type="button" onClick={onPay} className="inline-flex items-center gap-1 text-success hover:underline text-[12px]" title="使用该账户默认支付方式付款"><CreditCard className="w-3 h-3" />付款</button>}
            <button type="button" onClick={onDelete} className="inline-flex items-center gap-1 text-destructive hover:underline text-[12px]" title="删除此历史记录"><Trash2 className="w-3 h-3" />删除</button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
