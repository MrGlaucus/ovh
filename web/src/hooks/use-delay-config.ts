import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";

export interface DelayConfig {
  /** 自动触发下单的延迟秒数,0 = 不延迟 */
  delay_seconds: number;
}

/**
 * 自动下单延迟配置。启动时从 AUTO_ORDER_DELAY_SECONDS 解析,重启才变,
 * 拉一次基本就够;5 分钟兜底刷新,覆盖"改完配置重启后端"的场景。
 */
export function useDelayConfig() {
  return useQuery({
    queryKey: qk.delayConfig(),
    queryFn: async () => (await api.get<DelayConfig>("/delay-config")).data,
    staleTime: 60_000,
    refetchInterval: 300_000,
    retry: 0,
  });
}
