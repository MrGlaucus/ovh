import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";

export interface ProxyTargetResult {
  host: string;
  ok: boolean;
  latency_ms: number;
  http_code: number;
  error: string;
}

export interface ProxyStatus {
  configured: boolean;
  /** 脱敏后的代理地址(账号密码已被后端替换成 ***),未配置为空串 */
  proxy: string;
  /** socks5 / http / direct */
  mode: string;
  checked_at: string;
  targets: ProxyTargetResult[];
}

/**
 * 出站代理配置与各外部 host 的连通性。
 * 后端探测结果缓存 60s,这里与之对齐:refetchInterval 60s,
 * staleTime 调 0 保证指示器状态实时(数据本身极轻,无成本)。
 */
export function useProxyStatus() {
  return useQuery({
    queryKey: qk.proxyStatus(),
    queryFn: async () => (await api.get<ProxyStatus>("/proxy/status")).data,
    staleTime: 0,
    refetchInterval: 60_000,
    retry: 0,
  });
}

/** 手动触发后端重新探测(6 个 host 并发,最坏 ~10s),成功后直接写回缓存 */
export function useProxyCheck() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => (await api.post<ProxyStatus>("/proxy/check")).data,
    onSuccess: (data) => {
      queryClient.setQueryData(qk.proxyStatus(), data);
    },
  });
}
