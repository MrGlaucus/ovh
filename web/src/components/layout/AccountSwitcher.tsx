import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, ChevronsUpDown, Plus, RefreshCw, User } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useAccounts } from "@/hooks/use-accounts";
import { LoadFailed } from "@/components/common/LoadFailed";
import { useActiveAccount } from "@/hooks/use-active-account";
import { OVH_SUBSIDIARIES } from "@/lib/ovh-subsidiaries";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api";

/**
 * 左侧菜单栏顶部的账户切换器 —— **全站唯一**的账户入口。
 *
 * 为什么只留这一个：OVH 的 EU / US / CA 三个站点目录互不相通，同一台机器
 * 在欧区叫 24sk602、美区叫 24sk602-v1-us。以前列表页、下单对话框、服务器控制页
 * 各有一个账户选择器且互不同步，"拿欧区机型配美区账户"一键就能做出来，
 * 结果是任务被后端拒绝（400），而用户只看到控制台里一个红色报错。
 *
 * 现在切一次，机型列表、可用性、价格、控制台、下单账户全部跟着走。
 */
export function AccountSwitcher({ onNavigate }: { onNavigate?: () => void }) {
  const accounts = useAccounts();
  const [activeId, setActive] = useActiveAccount();
  // 受控:选完要自己关掉。Radix Popover 默认不会因为点了内容里的按钮就收起,
  // 不管的话选完账户面板还杵在那儿挡着导航。
  const [open, setOpen] = useState(false);
  const [proxyOpen, setProxyOpen] = useState(false);

  const list = accounts.data || [];
  const active = list.find((a) => a.id === activeId);
  const isDirect = !active?.proxyUrl;
  const proxyStatus = useQuery({
    queryKey: ["account", active?.id, "proxy-status"],
    queryFn: async () => (await api.get<{ configured: boolean; healthy: boolean; mode: string; proxy?: string; latencyMs?: number; expectedOutboundIp: string; actualOutboundIp: string; outboundIpStatus: string; outboundIpError: string; outboundIpCheckedAt: string; blocked: boolean; ovhAuthStatus?: string; ovhAuthError?: string }>(`/accounts/${active!.id}/proxy-status`)).data,
    enabled: !!active?.id,
    refetchInterval: 30_000,
  });

  // 没选过、或选中的账户被删了 → 落到默认账户（没有默认就取第一个）
  useEffect(() => {
    if (!list.length) return;
    if (active) return;
    const fallback = list.find((a) => a.isDefault) || list[0];
    if (fallback) setActive(fallback.id);
  }, [list, active, setActive]);

  const zoneLabel = (zone: string) =>
    OVH_SUBSIDIARIES.find((s) => s.code === zone)?.label?.split(" · ")[0] || zone;

  if (accounts.isPending) {
    return <div className="mx-3 mt-3 h-[52px] rounded-lg bg-muted animate-pulse" />;
  }

  // 读取失败 ≠ 一个账户都没有。这里是全站唯一的账户入口,一旦塌成虚线的
  // "添加 OVH 账户",下游每个页面都会跟着按"没有账户"降级:机型列表空、
  // 下单按钮灰、控制台说没绑定 —— 用户看到的是"我的账户没了",于是去重新
  // 填一遍 AppKey/AppSecret,而真相只是这一次 /accounts 请求没成功。
  // 失败必须说成失败,并且给一个重试口。
  if (accounts.isError) {
    return (
      <div className="mx-3 mt-3">
        <LoadFailed
          title="账户列表读取失败"
          error={accounts.error}
          onRetry={() => accounts.refetch()}
          compact
        />
      </div>
    );
  }

  if (!list.length) {
    return (
      <Link
        to="/settings"
        onClick={onNavigate}
        className="mx-3 mt-3 flex items-center gap-2 px-2.5 py-2 rounded-lg border border-dashed border-border text-[13px] text-muted-foreground hover:bg-muted transition-colors"
      >
        <Plus className="w-4 h-4" />
        添加 OVH 账户
      </Link>
    );
  }

  return (
    <div className="px-3 mt-3">
      <div className="px-0.5 mb-1 text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
        当前账户
      </div>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            className="w-full flex items-center gap-2 px-2.5 py-2 rounded-lg border border-border hover:bg-muted transition-colors text-left"
            title="切换账户：机型列表、价格、库存、控制台全部跟着当前账户走"
          >
            <span className="flex items-center justify-center w-7 h-7 rounded-md bg-secondary flex-shrink-0">
              <User className="w-3.5 h-3.5" />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-[13px] font-medium truncate">{active?.name || "选择账户"}</span>
              <span className="block text-[11px] text-muted-foreground truncate">
                {active ? `${active.zone} · ${zoneLabel(active.zone)}` : "未选择"}
              </span>
            </span>
            <ChevronsUpDown className="w-3.5 h-3.5 text-muted-foreground flex-shrink-0" />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[248px] p-1">
          <div className="max-h-[280px] overflow-y-auto">
            {list.map((a) => (
              <button
                key={a.id}
                onClick={() => {
                  setActive(a.id);
                  setOpen(false);
                }}
                className={cn(
                  "w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-left text-[13px] transition-colors",
                  a.id === activeId ? "bg-secondary" : "hover:bg-muted"
                )}
              >
                <Check className={cn("w-3.5 h-3.5 flex-shrink-0", a.id === activeId ? "opacity-100" : "opacity-0")} />
                <span className="min-w-0 flex-1">
                  <span className="block font-medium truncate">
                    {a.name}
                    {a.isDefault && <span className="ml-1 text-[10px] text-muted-foreground">默认</span>}
                  </span>
                  <span className="block text-[11px] text-muted-foreground truncate">
                    {a.zone} · {zoneLabel(a.zone)}
                  </span>
                </span>
              </button>
            ))}
          </div>
          <Link
            to="/settings"
            onClick={() => {
              setOpen(false);
              onNavigate?.();
            }}
            className="flex items-center gap-2 px-2 py-1.5 mt-1 rounded-md text-[13px] text-muted-foreground hover:bg-muted border-t border-border pt-2 transition-colors"
          >
            <Plus className="w-3.5 h-3.5" />
            管理账户
          </Link>
        </PopoverContent>
      </Popover>
      <Popover open={proxyOpen} onOpenChange={setProxyOpen}>
        <PopoverTrigger asChild>
          <button className={cn("w-full mt-1.5 px-2 py-1 rounded-md text-[10px] flex items-center gap-1.5 text-left hover:opacity-80 transition-opacity", isDirect ? "text-muted-foreground bg-muted/50" : proxyStatus.isError || proxyStatus.data?.blocked || proxyStatus.data?.healthy === false ? "text-destructive bg-destructive/5" : proxyStatus.isPending ? "text-warning bg-warning/5" : "text-success bg-success/5")} title="查看当前账户携带鉴权的 OVH API 代理状态">
            <span className={cn("w-1.5 h-1.5 rounded-full", isDirect ? "bg-muted-foreground" : proxyStatus.isError || proxyStatus.data?.blocked || proxyStatus.data?.healthy === false ? "bg-destructive" : proxyStatus.isPending ? "bg-warning" : "bg-success")} />
            {isDirect ? "账号代理：直连" : proxyStatus.isPending ? "账号代理：检测中" : proxyStatus.isError || proxyStatus.data?.blocked || proxyStatus.data?.healthy === false ? "账号代理：出口 IP 未确认（OVH 已阻断）" : `账号代理：出口 IP 已确认${proxyStatus.data?.latencyMs != null ? ` · ${proxyStatus.data.latencyMs}ms` : ""}`}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[calc(100vw-2rem)] max-w-[320px] p-0">
          <div className="px-3.5 pt-3 pb-2.5">
            <div className="flex items-center justify-between gap-2">
              <span className="text-[13px] font-semibold truncate">{active?.name || "当前账户"} · 账号代理</span>
              <span className="shrink-0 rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{active?.proxyUrl ? "proxy" : "direct"}</span>
            </div>
            <div className="mt-1 text-[11px] leading-tight text-muted-foreground break-all">{active?.proxyUrl || "未配置账户代理，此账户的 OVH API 直连"}</div>
          </div>
          <div className="border-t border-border px-3.5 py-2.5 text-[11px] leading-tight text-muted-foreground">
            {isDirect ? <p>当前账户直连；不执行代理出口 IP 检测或阻断。</p> : <>
              <div className="grid grid-cols-2 gap-x-3 gap-y-1.5">
                <p>预期 IP<br /><span className="font-mono text-foreground">{proxyStatus.data?.expectedOutboundIp || active?.expectedOutboundIp || "未配置"}</span></p>
                <p>实际 IP<br /><span className={cn("font-mono", proxyStatus.data?.outboundIpStatus === "verified" ? "text-success" : "text-destructive")}>{proxyStatus.data?.actualOutboundIp || "未确认"}</span></p>
              </div>
              <div className="mt-2 inline-flex max-w-full items-center rounded-md bg-info/10 px-2 py-1 text-[10px] font-medium text-info">
                {proxyStatus.data?.outboundIpCheckedAt ? `上次检查：${new Date(proxyStatus.data.outboundIpCheckedAt).toLocaleString()}（每 30 秒复检）` : "尚未检查（每 30 秒自动复检）"}
              </div>
              {(proxyStatus.data?.outboundIpError || proxyStatus.isError) && <p className="mt-2 text-destructive">出口 IP：{proxyStatus.data?.outboundIpError || "检测失败，30 秒后自动重试"}</p>}
              {proxyStatus.data?.ovhAuthStatus === "failed" && <p className="mt-1 text-destructive">OVH 凭据：{proxyStatus.data.ovhAuthError || "验证失败"}</p>}
            </>}
            <p className="mt-2 border-t border-border/70 pt-2 text-[10px] leading-snug">仅覆盖「{active?.name || "当前"}」账户的签名 OVH API 请求；公网探测及 Telegram/GitHub/Webhook 使用右上角公共代理。</p>
          </div>
          <div className="border-t border-border px-3.5 py-2.5 flex items-center justify-between gap-2">
            <span className={cn("min-w-0 text-[11px] leading-tight", !active?.proxyUrl ? "text-muted-foreground" : proxyStatus.isError || proxyStatus.data?.healthy === false ? "text-destructive" : "text-success")}>
              {isDirect ? "直连（不检查代理出口 IP）" : proxyStatus.isPending ? "检测中…" : proxyStatus.isError || proxyStatus.data?.blocked || proxyStatus.data?.healthy === false ? "出口 IP 不匹配/未确认，OVH 已阻断" : `出口 IP 已确认，已鉴权连通${proxyStatus.data?.latencyMs != null ? ` · ${proxyStatus.data.latencyMs}ms` : ""}`}
            </span>
            <button onClick={() => proxyStatus.refetch()} disabled={proxyStatus.isFetching} className="shrink-0 inline-flex items-center gap-1 whitespace-nowrap rounded-md border border-border px-2.5 py-1.5 text-[11px] hover:bg-muted disabled:opacity-60"><RefreshCw className={cn("w-3 h-3", proxyStatus.isFetching && "animate-spin")} />重新检测</button>
          </div>
        </PopoverContent>
      </Popover>
      <p className="px-0.5 mt-1.5 text-[10px] text-muted-foreground leading-snug">
        机型、价格、库存、控制台都按这个账户所在站点显示
      </p>
    </div>
  );
}
