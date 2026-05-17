import { createFileRoute } from "@tanstack/react-router";
import { Clock, RefreshCw, Trash2, Search, ExternalLink, AlertCircle, Hourglass } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/PageHeader";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Chip } from "@/components/common/Chip";
import { AccountChip } from "@/components/common/AccountChip";
import { Skeleton } from "@/components/common/Skeleton";
import { EmptyState } from "@/components/common/EmptyState";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useHistory, useClearHistory, type PurchaseHistory } from "@/hooks/use-history";

/** 抢购历史：表格 + 搜索 + 状态过滤 */
export const Route = createFileRoute("/history")({
  component: HistoryPage,
});

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

function getExpirationMs(item: PurchaseHistory): number {
  if (item.expirationTime) return new Date(item.expirationTime).getTime();
  return new Date(item.purchaseTime).getTime() + ORDER_VALIDITY_MS;
}

function HistoryPage() {
  const list = useHistory();
  const clear = useClearHistory();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "success" | "failed">("all");
  const [confirmClear, setConfirmClear] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // 每分钟刷新一次 now，让所有行的倒计时同步推进
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  const items = list.data || [];
  const filtered = useMemo(() => {
    const s = search.trim().toLowerCase();
    return items.filter((i) => {
      if (statusFilter !== "all" && i.status !== statusFilter) return false;
      if (s && !`${i.planCode} ${i.datacenter} ${i.orderId || ""}`.toLowerCase().includes(s)) return false;
      return true;
    });
  }, [items, search, statusFilter]);

  return (
    <div className="space-y-6">
      <PageHeader
        icon={Clock}
        title="抢购历史"
        description="查看服务器购买历史记录"
        action={
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => list.refetch()} disabled={list.isFetching}>
              <RefreshCw className={`w-4 h-4 ${list.isFetching ? "animate-spin" : ""}`} />
              刷新
            </Button>
            <Button variant="outline" onClick={() => setConfirmClear(true)} disabled={items.length === 0}>
              <Trash2 className="w-4 h-4" />
              清空
            </Button>
          </div>
        }
      />

      <Card>
        <CardContent className="p-5">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
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
      ) : filtered.length === 0 ? (
        <Card>
          <EmptyState icon={Clock} title="没有匹配的订单" />
        </Card>
      ) : (
        <Card>
          <table className="w-full">
            <thead>
              <tr className="text-left text-[11px] font-medium text-muted-foreground border-b border-border">
                <th className="px-4 py-3">型号</th>
                <th className="px-4 py-3">机房</th>
                <th className="px-4 py-3">配置</th>
                <th className="px-4 py-3">价格</th>
                <th className="px-4 py-3">状态</th>
                <th className="px-4 py-3">时间</th>
                <th className="px-4 py-3">剩余</th>
                <th className="px-4 py-3">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {filtered.map((item) => <HistoryRow key={item.id} item={item} now={now} />)}
            </tbody>
          </table>
        </Card>
      )}

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

function HistoryRow({ item, now }: { item: PurchaseHistory; now: number }) {
  // 只有成功且拿到 orderId 的行才显示倒计时
  const showCountdown = item.status === "success" && !!item.orderId;
  const remainingMs = showCountdown ? getExpirationMs(item) - now : 0;
  const isExpired = showCountdown && remainingMs <= 0;
  // 24 小时内进入告警色
  const isUrgent = showCountdown && !isExpired && remainingMs < 24 * 60 * 60 * 1000;
  return (
    <tr className={`text-[13px] hover:bg-muted ${isExpired ? "opacity-60" : ""}`}>
      <td className={`px-4 py-3 font-mono font-semibold ${isExpired ? "line-through" : ""}`}>
        <div className="flex items-center gap-2 flex-wrap">
          {item.planCode}
          <AccountChip accountId={item.accountId} />
        </div>
      </td>
      <td className={`px-4 py-3 ${isExpired ? "line-through" : ""}`}>{item.datacenter.toUpperCase()}</td>
      <td className={`px-4 py-3 text-muted-foreground max-w-[200px] truncate ${isExpired ? "line-through" : ""}`}>
        {item.options && item.options.length > 0 ? item.options.join(", ") : "默认配置"}
      </td>
      <td className="px-4 py-3">
        {item.price?.withTax != null ? (
          <span className={`font-mono font-medium text-success ${isExpired ? "line-through" : ""}`}>
            {item.price.withTax} {item.price.currencyCode || "EUR"}
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>
      <td className="px-4 py-3">
        {item.status === "success" ? (
          <Chip tone="success">成功</Chip>
        ) : (
          <Chip tone="danger">失败</Chip>
        )}
      </td>
      <td className="px-4 py-3 text-[11px] text-muted-foreground font-mono whitespace-nowrap">
        {new Date(item.purchaseTime).toLocaleString("zh-CN", {
          month: "2-digit",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
        })}
      </td>
      <td className="px-4 py-3 whitespace-nowrap">
        {showCountdown ? (
          <Chip tone={isExpired ? "danger" : isUrgent ? "warning" : "info"}>
            <Hourglass className="w-3 h-3" />
            {formatCountdown(remainingMs)}
          </Chip>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>
      <td className="px-4 py-3">
        {item.status === "success" && item.orderUrl ? (
          <a
            href={item.orderUrl}
            target="_blank"
            rel="noopener noreferrer"
            aria-disabled={isExpired}
            className={`inline-flex items-center gap-1 text-foreground hover:underline text-[12px] ${
              isExpired ? "pointer-events-none opacity-50" : ""
            }`}
          >
            <ExternalLink className="w-3 h-3" />
            订单
          </a>
        ) : item.status === "failed" && item.errorMessage ? (
          <button
            type="button"
            onClick={() => toast.info(item.errorMessage)}
            className="inline-flex items-center gap-1 text-destructive hover:underline text-[12px]"
          >
            <AlertCircle className="w-3 h-3" />
            错误
          </button>
        ) : "—"}
      </td>
    </tr>
  );
}
