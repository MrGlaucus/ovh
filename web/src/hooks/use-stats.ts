import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { useHistory } from "@/hooks/use-history";

export interface DashboardStats {
  activeQueues: number;
  totalServers: number;
  availableServers: number;
  purchaseSuccess: number;
  purchaseFailed: number;
  queueProcessorRunning?: boolean;
  monitorRunning?: boolean;
}

/** 拉取仪表盘 KPI 总览 */
export function useStats() {
  return useQuery({
    queryKey: qk.stats(),
    queryFn: async () => (await api.get<DashboardStats>("/stats")).data,
  });
}

export interface PerfPoint {
  time: string;
  /** 该小时总抢购次数 */
  value: number;
  /** 该小时成功数 */
  success: number;
  /** 该小时失败数 */
  failed: number;
}
export interface SparkPoint {
  v: number;
}
export interface PurchaseTrend {
  perfData: PerfPoint[];
  sparkTotal: SparkPoint[];
  sparkSuccess: SparkPoint[];
  sparkFailed: SparkPoint[];
  sparkRate: SparkPoint[];
  kpi: {
    total: number;
    success: number;
    failed: number;
    successRate: number; // 0~100
  };
  isLoading: boolean;
}

/**
 * 近 24 小时抢购趋势（基于真实 /purchase-history）：
 * - 主曲线：每小时抢购总数
 * - 4 spark：总数 / 成功 / 失败 / 成功率
 * - KPI：24h 内总数、成功数、失败数、成功率
 */
export function usePurchaseTrend(): PurchaseTrend {
  const history = useHistory();
  return useMemo(() => {
    const items = history.data || [];
    const now = Date.now();
    const cutoff = now - 24 * 60 * 60 * 1000;

    // 取近 24h
    const recent = items.filter((it) => {
      const t = new Date(it.purchaseTime).getTime();
      return Number.isFinite(t) && t >= cutoff;
    });

    // 24 个 1 小时桶（按当前时间向前数）
    const buckets: PerfPoint[] = Array.from({ length: 24 }, (_, idx) => {
      // idx=0 是 24h 之前那个小时，idx=23 是当前小时
      const bucketStart = now - (24 - idx) * 60 * 60 * 1000;
      const d = new Date(bucketStart);
      const hh = String(d.getHours()).padStart(2, "0");
      return { time: `${hh}:00`, value: 0, success: 0, failed: 0 };
    });

    recent.forEach((it) => {
      const t = new Date(it.purchaseTime).getTime();
      const hoursAgo = Math.floor((now - t) / (60 * 60 * 1000));
      const idx = 23 - hoursAgo;
      if (idx < 0 || idx > 23) return;
      buckets[idx].value++;
      if (it.status === "success") buckets[idx].success++;
      else buckets[idx].failed++;
    });

    // 12 点 spark：把 24 桶两两合并
    const merge = (sel: (b: PerfPoint) => number): SparkPoint[] =>
      Array.from({ length: 12 }, (_, i) => ({ v: sel(buckets[i * 2]) + sel(buckets[i * 2 + 1]) }));

    const sparkTotal = merge((b) => b.value);
    const sparkSuccess = merge((b) => b.success);
    const sparkFailed = merge((b) => b.failed);
    const sparkRate: SparkPoint[] = sparkTotal.map((p, i) => ({
      v: p.v > 0 ? (sparkSuccess[i].v / p.v) * 100 : 0,
    }));

    // 汇总 KPI
    const total = recent.length;
    const success = recent.filter((it) => it.status === "success").length;
    const failed = total - success;
    const successRate = total > 0 ? (success / total) * 100 : 0;

    return {
      perfData: buckets,
      sparkTotal,
      sparkSuccess,
      sparkFailed,
      sparkRate,
      kpi: { total, success, failed, successRate },
      isLoading: history.isPending,
    };
  }, [history.data, history.isPending]);
}

/** @deprecated 旧版 sin+random mock。新代码改用 {@link usePurchaseTrend}。 */
export const usePerfTrend = usePurchaseTrend;
