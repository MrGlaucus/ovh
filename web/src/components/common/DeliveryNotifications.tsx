import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useActiveAccount } from "@/hooks/use-active-account";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";

interface Settings { accountId: string; enabled: boolean; initialized: boolean; lastCheck: number; lastError: string }

export function DeliveryNotifications() {
  const [accountId] = useActiveAccount();
  const qc = useQueryClient();
  const key = ["account", "delivery-notifications", accountId];
  const status = useQuery({
    queryKey: key,
    queryFn: async () => (await api.get<Settings>("/ovh/account/delivery-notifications", { params: { account: accountId } })).data,
    refetchInterval: 15000,
  });
  const change = useMutation({
    mutationFn: async ({ enabled, id }: { enabled: boolean; id: string }) =>
      (await api.put<Settings>("/ovh/account/delivery-notifications", { enabled }, { params: { account: id } })).data,
    onSuccess: (data, variables) => {
      qc.setQueryData(["account", "delivery-notifications", variables.id], data);
      toast.success(data.enabled ? "已开启该账户的发货通知" : "已关闭该账户的发货通知");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "保存发货通知设置失败"),
  });
  return <section className="rounded-xl border bg-card p-4 space-y-2">
    <div className="flex items-center justify-between gap-4">
      <div className="flex items-center gap-2 font-medium text-sm"><Bell className="h-4 w-4" /><label htmlFor="delivery-notifications">服务器发货 TG 通知</label></div>
      <Switch id="delivery-notifications" checked={status.data?.enabled ?? false} disabled={!status.data || status.isError || change.isPending}
        onCheckedChange={(enabled) => change.mutate({ enabled, id: status.data!.accountId })} />
    </div>
    <p className="text-xs text-muted-foreground">仅对当前账户生效。每 60 秒检测新增服务器，通知包含实际配置和快捷操作；关闭后停止检测及发送。</p>
    <p className="text-xs text-muted-foreground">所有账户默认开启。关闭会清空基线并取消待发通知；再次开启会以最新服务器列表重新建立基线，不补发已有机器。</p>
    {status.isError ? <div className="text-xs text-destructive">读取设置失败<Button variant="link" size="sm" onClick={() => status.refetch()}>重试</Button></div>
      : status.data?.enabled && <p className="text-xs text-muted-foreground">{status.data.lastError || (!status.data.initialized ? "等待首次成功检查，建立服务器基线…" : status.data.lastCheck ? `上次检查：${new Date(status.data.lastCheck * 1000).toLocaleString()}` : "等待检查…")}</p>}
  </section>;
}
