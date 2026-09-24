import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { toast } from "sonner";

export interface PurchaseHistory {
  id: string;
  accountId: string;
  /** 关联的抢购队列任务 ID（后端 PurchaseHistoryEntry.taskId） */
  taskId?: string;
  planCode: string;
  datacenter: string;
  options?: string[];
  status: "success" | "failed";
  orderId?: string;
  orderUrl?: string;
  errorMessage?: string;
  purchaseTime: string;
  /** 抢购到这单时一共尝试了几次（后端 attemptCount） */
  attemptCount?: number;
  expirationTime?: string;
  /** 各阶段墙钟耗时。抢购输了之后唯一有用的信息就是"慢在哪一步" */
  timing?: { name: string; ms: number }[];
  totalMs?: number;
  /** OVH 侧订单状态(billing.order.OrderStatusEnum):notPaid / checking / delivering /
   *  delivered / cancelling / cancelled / documentsRequested / unknown。
   *  没有它,"下单成功"到底付没付永远不知道。空 = 还没查到 */
  orderStatus?: string;
  orderStatusAt?: string;
  /** 下单时配置的延迟秒数(订阅级/自动下单带入),0=立即下单 */
  delaySeconds?: number;
  price?: {
    withTax?: number;
    withoutTax?: number;
    tax?: number;
    currencyCode?: string;
  };
  /**
   * 已确认的退款记录（OVH billing.Refund）。有值 = 已退款。
   * 后端只在手动点「刷新状态」时通过原发票关联退款并落库（后台不轮询）；
   * OVH 退款单没有状态机，只有"有/没有"，到账时间差在支付渠道侧。
   */
  refund?: {
    originalBillId?: string;
    refundOrderId?: string;
    id: string;
    date?: string;
    price?: { withTax?: number; currencyCode?: string };
    /** 退款单 PDF 链接（OVH 返回原值） */
    pdfUrl?: string;
  };
  /** 上次查退款的时间（后端节流用；手动刷新跳过它，一般不用展示） */
  refundCheckedAt?: string;
  refundCheckError?: string;
}

/** 抢购历史 */
export function useHistory() {
  return useQuery({
    queryKey: qk.history(),
    queryFn: async () => (await api.get<PurchaseHistory[]>("/purchase-history")).data,
  });
}

/**
 * 手动刷新所有未到终态订单的支付状态(后台每 10 分钟也会自动刷)，
 * 并查一次未退款成功单的退款记录(退款只在手动刷新时查，后台不轮询)。
 * 给"我刚付完款想马上看到"的场景。
 */
interface HistoryRefreshStatus {
  id: string;
  status: "idle" | "running" | "completed" | "failed";
  phase: string;
  updated: number;
  refundFailed: number;
  error?: string;
}

export function useRefreshOrderStatus() {
  const qc = useQueryClient();
  const observed = useRef<string | null>(null);
  const status = useQuery({
    queryKey: ["history-refresh"],
    queryFn: async ({ signal }) =>
      (await api.get<HistoryRefreshStatus>("/purchase-history/refresh-status", { signal })).data,
    staleTime: 0,
    refetchOnMount: "always",
    refetchOnWindowFocus: true,
    refetchInterval: (q) => q.state.data?.status === "running" || q.state.status === "error" ? 3000 : false,
  });
  useEffect(() => {
    const d = status.data;
    if (!d) return;
    if (d.status === "running") {
      observed.current = d.id;
      void qc.invalidateQueries({ queryKey: qk.history() });
    } else if (observed.current) {
      observed.current = null;
      void qc.invalidateQueries({ queryKey: qk.history() });
      if (d.status === "idle") toast.warning("刷新任务已中断，请重新刷新");
      else if (d.status === "failed") toast.error(d.error || "刷新任务失败");
      else if (d.refundFailed) toast.warning(`${d.refundFailed} 条订单退款查询未完成`, { description: "查看退款列的失败原因后重试" });
      else toast.success(d.updated > 0 ? `${d.updated} 条订单状态有更新` : "订单状态已是最新");
    }
  }, [status.data, status.dataUpdatedAt, qc]);
  const mutation = useMutation({
    mutationFn: async () => {
      await qc.cancelQueries({ queryKey: ["history-refresh"] });
      return (await api.post<HistoryRefreshStatus>("/purchase-history/refresh-status")).data;
    },
    onSuccess: (d) => {
      observed.current = d.id;
      qc.setQueryData(["history-refresh"], d);
    },
    onError: (e: any) => {
      void qc.invalidateQueries({ queryKey: ["history-refresh"] });
      toast.warning(e.response?.data?.error || "提交请求连接中断，正在确认后台任务状态");
    },
  });
  const running = status.data?.status === "running";
  return {
    ...mutation,
    isPending: mutation.isPending || running,
    label: running ? (status.isError ? "正在重连…" : status.data?.phase === "refunds" ? "查询退款中…" : "查询订单中…") : mutation.isPending ? "正在提交…" : "刷新状态",
  };
}
/** 使用历史订单原账户的默认支付方式付款。 */
export function usePayHistoryOrder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, orderId }: { id: string; orderId: string }) =>
      (await api.post<{ success: boolean; message: string; orderStatus?: string }>(`/purchase-history/${id}/pay`, { orderId })).data,
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: qk.history() });
      toast.success(result.message || "已请求使用默认支付方式付款");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "付款请求失败"),
  });
}

/** 删除单条抢购历史 */
export function useRemoveHistoryItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => (await api.delete(`/purchase-history/${id}`)).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.history() });
      toast.success("已删除抢购历史记录");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "删除失败"),
  });
}

/** 清空抢购历史 */
export function useClearHistory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => (await api.delete("/purchase-history")).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.history() });
      toast.success("已清空购买历史");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "清空失败"),
  });
}

/** 清除所有失败的抢购历史（成功记录保留，避免误伤待付款订单） */
export function useClearFailedHistory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      (await api.delete<{ status: string; deleted: number }>("/purchase-history/failed")).data,
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: qk.history() });
      toast.success(d.deleted > 0 ? `已清除 ${d.deleted} 条失败记录` : "没有失败记录需要清除");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "清除失败记录失败"),
  });
}
