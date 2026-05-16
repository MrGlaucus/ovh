import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { toast } from "sonner";

export interface LogEntry {
  id: string;
  timestamp: string;
  level: "INFO" | "WARNING" | "ERROR" | "DEBUG";
  message: string;
  source: string;
}

/** 日志列表 */
export function useLogs(autoRefresh: boolean = true) {
  return useQuery({
    queryKey: qk.logs(),
    queryFn: async () => (await api.get<LogEntry[]>("/logs")).data,
    refetchInterval: autoRefresh ? 5000 : false,
  });
}

/** 清空日志 */
export function useClearLogs() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => (await api.delete("/logs")).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.logs() });
      toast.success("已清空日志");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "清空失败"),
  });
}
