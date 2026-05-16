import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { toast } from "sonner";

export interface ServerOption {
  label: string;
  value: string;
  family?: string;
}

export interface ServerPlan {
  planCode: string;
  name: string;
  description?: string;
  cpu: string;
  memory: string;
  storage: string;
  bandwidth: string;
  vrackBandwidth: string;
  defaultOptions: ServerOption[];
  availableOptions: ServerOption[];
  datacenters: {
    datacenter: string;
    dcName: string;
    region: string;
    availability: string;
    countryCode: string;
  }[];
}

/** 服务器目录（带可用性） */
export function useServers(showApiServers: boolean = true) {
  return useQuery({
    queryKey: qk.servers.list(showApiServers),
    queryFn: async () => {
      const res = await api.get("/servers", { params: { showApiServers } });
      return (res.data.servers || res.data || []) as ServerPlan[];
    },
    staleTime: 5 * 60_000,
  });
}

/** 获取某 planCode 在某 DC 的参考价格 */
export function useServerPrice(planCode: string | null, datacenter: string | null, options: string[]) {
  return useQuery({
    queryKey: qk.servers.price(planCode || "", datacenter || "", options),
    queryFn: async () => {
      const res = await api.post(`/servers/${planCode}/price`, { datacenter, options });
      if (!res.data?.success) throw new Error(res.data?.error || "获取价格失败");
      return res.data.price?.prices as { withTax?: number; withoutTax?: number; currencyCode?: string };
    },
    enabled: !!planCode && !!datacenter,
    staleTime: 10 * 60_000,
    retry: 0,
  });
}

/** 添加到监控订阅 */
export function useAddToMonitor() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (payload: { planCode: string; datacenters: string[]; serverName?: string }) =>
      (await api.post("/monitor/subscriptions", { ...payload, notifyAvailable: true, notifyUnavailable: false })).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.monitor.list() });
      toast.success("已加入监控");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "加入监控失败"),
  });
}
