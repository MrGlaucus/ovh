import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, ChevronDown, ChevronsUpDown, Plus, RefreshCw } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useAccounts } from "@/hooks/use-accounts";
import { LoadFailed } from "@/components/common/LoadFailed";
import { useActiveAccount } from "@/hooks/use-active-account";
import { zoneStyle, zoneName, regionOf, type ZoneRegion } from "@/lib/zone-color";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api";

/**
 * 账户切换器(桌面在左侧栏顶部,手机在顶栏) —— **全站唯一**的账户入口。
 *
 * 为什么只留这一个：OVH 的 EU / US / CA 三个站点目录互不相通，同一台机器
 * 在欧区叫 24sk602、美区叫 24sk602-v1-us。以前列表页、下单对话框、服务器控制页
 * 各有一个账户选择器且互不同步，"拿欧区机型配美区账户"一键就能做出来，
 * 结果是任务被后端拒绝（400），而用户只看到控制台里一个红色报错。
 *
 * 现在切一次，机型列表、可用性、价格、控制台、下单账户全部跟着走。
 */
export function AccountSwitcher({
  onNavigate,
  compact,
}: {
  onNavigate?: () => void;
  /** 顶栏用的紧凑版:去掉「当前账户」标题和外边距,高度压到 36px 塞进 48px 的顶栏 */
  compact?: boolean;
}) {
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

  // 账号代理的四态判定:直连 / 已确认 / 被阻断 / 检测中。
  // 桌面侧栏按钮、手机胶囊圆点、手机切换面板里的详情共用这一份 —— 同一个问题
  // 三处各写一遍判定,迟早漂移成"检测中"和"已阻断"谁优先都不一致。
  const proxyState: "direct" | "ok" | "blocked" | "checking" = isDirect
    ? "direct"
    : proxyStatus.isError || proxyStatus.data?.blocked || proxyStatus.data?.healthy === false
      ? "blocked"
      : proxyStatus.data
        ? "ok"
        : "checking";

  const proxyDotClass: Record<"direct" | "ok" | "blocked" | "checking", string> = {
    direct: "bg-muted-foreground",
    ok: "bg-success",
    blocked: "bg-destructive",
    checking: "bg-warning",
  };

  const proxyLabel =
    proxyState === "direct"
      ? "账号代理：直连"
      : proxyState === "checking"
        ? "账号代理：检测中"
        : proxyState === "blocked"
          ? "账号代理：出口 IP 未确认（OVH 已阻断）"
          : `账号代理：出口 IP 已确认${proxyStatus.data?.latencyMs != null ? ` · ${proxyStatus.data.latencyMs}ms` : ""}`;

  // 按 API endpoint 区域分组,组内保持后端给的顺序(默认账户通常在前)。
  // 认不出子公司的账户单独归到「未知区」,不并进欧区 —— 猜错区 = 下单打错站点。
  const grouped = useMemo(() => {
    const order: (ZoneRegion | "unknown")[] = ["eu", "us", "ca", "unknown"];
    const buckets = new Map<string, typeof list>();
    for (const a of list) {
      const key = regionOf(a.zone) ?? "unknown";
      const arr = buckets.get(key) || [];
      arr.push(a);
      buckets.set(key, arr);
    }
    return order
      .filter((k) => buckets.has(k))
      .map((k) => ({
        region: k,
        label: zoneStyle(buckets.get(k)![0].zone).label,
        accounts: buckets.get(k)!,
      }));
  }, [list]);

  // 没选过、或选中的账户被删了 → 落到默认账户（没有默认就取第一个）
  useEffect(() => {
    if (!list.length) return;
    if (active) return;
    const fallback = list.find((a) => a.isDefault) || list[0];
    if (fallback) setActive(fallback.id);
  }, [list, active, setActive]);


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
      <div className={compact ? "" : "mx-3 mt-3"}>
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
        className={cn(
          "flex items-center gap-2 px-2.5 rounded-lg border border-dashed border-border text-[13px] text-muted-foreground hover:bg-muted transition-colors",
          compact ? "h-9" : "mx-3 mt-3 py-2"
        )}
      >
        <Plus className="w-4 h-4" />
        添加 OVH 账户
      </Link>
    );
  }

  return (
    <div className={compact ? "min-w-0" : "px-3 mt-3"}>
      {!compact && (
        <div className="px-0.5 mb-1 text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          当前账户
        </div>
      )}
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            className={cn(
              "w-full flex items-center gap-2 text-left transition-colors",
              compact
                // 顶栏版:填充式胶囊,不用描边 —— 描边会让它看起来像个输入框,
                // 而它是个控件。32px 高在 48px 顶栏里上下各留 8px。
                ? "h-8 pl-2 pr-1.5 rounded-lg bg-secondary hover:bg-muted active:bg-muted"
                : "px-2.5 py-2 rounded-lg border border-border hover:bg-muted"
            )}
            title="切换账户：机型列表、价格、库存、控制台全部跟着当前账户走"
          >
            {/* 账户名在前、区域徽章在后。
                名字才是身份 —— 同一个区里可以有好几个账户,它们的区域完全相同,
                能把它们区分开的只有名字,所以名字优先占据宽度。
                徽章回答的是另一个问题:「这个账户属于哪套目录」——
                三区 planCode 互不相通,拿欧区机型配美区账户必然被拒。 */}
            {compact ? (
              <span className="min-w-0 flex-1 flex items-center gap-1.5">
                {/* 手机顶栏塞不下完整的状态按钮,但状态不能退化成"打开面板才知道" ——
                    一个小圆点带着同样的四态颜色,颜色语义和侧栏按钮完全一致 */}
                {active && (
                  <span
                    className={cn("flex-shrink-0 w-1.5 h-1.5 rounded-full", proxyDotClass[proxyState])}
                    title={proxyLabel}
                  />
                )}
                <span className="text-[13px] font-medium truncate">{active?.name || "选择账户"}</span>
                {active && (
                  <span
                    className={cn(
                      "flex-shrink-0 px-1.5 py-px rounded text-[10px] font-semibold tracking-wide",
                      zoneStyle(active.zone).badge
                    )}
                  >
                    {active.zone}
                  </span>
                )}
              </span>
            ) : (
              <span className="min-w-0 flex-1">
                <span className="block text-[13px] font-medium truncate">{active?.name || "选择账户"}</span>
                <span className="block text-[11px] text-muted-foreground truncate">
                  {active ? `${active.zone} · ${zoneName(active.zone)}` : "未选择"}
                </span>
              </span>
            )}
            {compact ? (
              <ChevronDown className="w-3 h-3 text-muted-foreground flex-shrink-0" strokeWidth={2.5} />
            ) : (
              <ChevronsUpDown className="w-3.5 h-3.5 text-muted-foreground flex-shrink-0" />
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[248px] p-1">
          {/* 切换账户的后果写在面板顶部 —— 用户正要做选择的这一刻才是该说的时候。
              侧栏版在外面也有一份常驻说明,顶栏版(手机)只靠这里。 */}
          <p className="px-2 pt-1.5 pb-2 text-[10.5px] text-muted-foreground leading-snug border-b border-border mb-1">
            机型、价格、库存、控制台都按这个账户所在站点显示
          </p>
          {/* 按区域分组。同一个区里可以有好几个账户,平铺成一条长列表的话
              用户得逐行去读子公司码才知道哪几个是一伙的;分组之后
              「这三个共用欧区目录、那一个是美区」一眼就分得清。 */}
          {/* 手机上屏幕高度富余,280px 会把第三个区挤到要滚才看得到;
              桌面侧栏空间紧,维持原值 */}
          <div className="max-h-[45vh] sm:max-h-[280px] overflow-y-auto">
            {grouped.map(({ region, label, accounts }) => (
              <div key={region} className="mb-0.5 last:mb-0">
                {/* 只有一个区时不必再打分组标题 —— 那是句废话 */}
                {grouped.length > 1 && (
                  <div className="px-2 pt-1.5 pb-1 text-[10px] font-semibold text-muted-foreground tracking-wide">
                    {label}
                  </div>
                )}
                {accounts.map((a) => (
                  <button
                    key={a.id}
                    onClick={() => {
                      setActive(a.id);
                      setOpen(false);
                    }}
                    className={cn(
                      "w-full flex items-center gap-2 px-2 py-2.5 sm:py-1.5 rounded-md text-left text-[13px] transition-colors",
                      a.id === activeId ? "bg-secondary" : "hover:bg-muted"
                    )}
                  >
                    <Check
                      className={cn(
                        "w-3.5 h-3.5 flex-shrink-0",
                        a.id === activeId ? "opacity-100" : "opacity-0"
                      )}
                    />
                    {/* 名字占满可用宽度:同区账户之间只有名字不一样 */}
                    <span className="min-w-0 flex-1">
                      <span className="block font-medium truncate">
                        {a.name}
                        {a.isDefault && (
                          <span className="ml-1 text-[10px] font-normal text-muted-foreground">默认</span>
                        )}
                      </span>
                      <span className="block text-[11px] text-muted-foreground truncate">
                        {zoneName(a.zone)}
                      </span>
                    </span>
                    <span
                      className={cn(
                        "flex-shrink-0 px-1.5 py-px rounded text-[10px] font-semibold tracking-wide",
                        zoneStyle(a.zone).badge
                      )}
                    >
                      {a.zone}
                    </span>
                  </button>
                ))}
              </div>
            ))}
          </div>
          {/* 手机端(compact):账号代理状态放这里。顶栏只有 48px 高,塞不下常驻按钮,
              但完全不给入口的话手机上就看不到账户级出口 IP 的状态了 ——
              右上角那个 ProxyStatus 是公共代理(Telegram/GitHub 这类不带账户签名的请求),
              顶替不了这里。桌面端有侧栏下方的独立「账号代理」按钮,不需要这份。 */}
          {compact && (
            <div className="border-t border-border mt-1 px-2 pt-2 pb-1.5">
              <div className="flex items-center gap-1.5">
                <span className={cn("flex-shrink-0 w-1.5 h-1.5 rounded-full", proxyDotClass[proxyState])} />
                <span
                  className={cn(
                    "min-w-0 flex-1 text-[10.5px] leading-snug",
                    proxyState === "direct"
                      ? "text-muted-foreground"
                      : proxyState === "blocked"
                        ? "text-destructive"
                        : proxyState === "checking"
                          ? "text-warning"
                          : "text-success"
                  )}
                >
                  {proxyLabel}
                </span>
                <button
                  onClick={() => proxyStatus.refetch()}
                  disabled={proxyStatus.isFetching}
                  className="flex-shrink-0 inline-flex items-center justify-center w-6 h-6 rounded-md border border-border text-muted-foreground hover:bg-muted disabled:opacity-60"
                  title="重新检测"
                >
                  <RefreshCw className={cn("w-3 h-3", proxyStatus.isFetching && "animate-spin")} />
                </button>
              </div>
              {proxyState !== "direct" && (
                <div className="mt-1 space-y-0.5 text-[10px] leading-snug text-muted-foreground">
                  <p className="flex items-baseline gap-1.5">
                    <span className="flex-shrink-0">预期</span>
                    <span className="font-mono text-foreground truncate">
                      {proxyStatus.data?.expectedOutboundIp || active?.expectedOutboundIp || "未配置"}
                    </span>
                  </p>
                  <p className="flex items-baseline gap-1.5">
                    <span className="flex-shrink-0">实际</span>
                    <span className={cn("font-mono truncate", proxyState === "ok" ? "text-success" : "text-destructive")}>
                      {proxyStatus.data?.actualOutboundIp || "未确认"}
                    </span>
                  </p>
                  <p>
                    {proxyStatus.data?.outboundIpCheckedAt
                      ? `上次检查 ${new Date(proxyStatus.data.outboundIpCheckedAt).toLocaleString("zh-CN")}（每 30 秒复检）`
                      : "尚未检查（每 30 秒自动复检）"}
                  </p>
                  {proxyState === "blocked" && (
                    <p className="text-destructive break-all">
                      {proxyStatus.data?.outboundIpError ||
                        (!proxyStatus.data ? "状态读取失败，30 秒后自动重试" : "出口 IP 未确认，OVH 已阻断")}
                    </p>
                  )}
                  {proxyStatus.data?.ovhAuthStatus === "failed" && (
                    <p className="text-destructive">OVH 凭据：{proxyStatus.data.ovhAuthError || "验证失败"}</p>
                  )}
                </div>
              )}
              <p className="mt-1 text-[9.5px] leading-snug text-muted-foreground/80">
                仅覆盖「{active?.name || "当前账户"}」的签名 OVH 请求,和右上角的公共代理是两回事。
              </p>
            </div>
          )}
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
      {/* 侧栏版才有这块常驻按钮+详情;顶栏只有 48px 高放不下,手机端的账号代理状态
          在切换面板内(compact 分支),外加胶囊上的状态圆点。
          注意右上角的 ProxyStatus 是公共代理(Telegram/GitHub 这类不带账户签名的
          请求),和这里的账号级出口 IP 状态是两回事,顶替不了。 */}
      {!compact && (
        <>
          <Popover open={proxyOpen} onOpenChange={setProxyOpen}>
            <PopoverTrigger asChild>
              <button
                className={cn(
                  "w-full mt-1.5 px-2 py-1 rounded-md text-[10px] flex items-center gap-1.5 text-left hover:opacity-80 transition-opacity",
                  proxyState === "direct"
                    ? "text-muted-foreground bg-muted/50"
                    : proxyState === "blocked"
                      ? "text-destructive bg-destructive/5"
                      : proxyState === "checking"
                        ? "text-warning bg-warning/5"
                        : "text-success bg-success/5"
                )}
                title="查看当前账户携带鉴权的 OVH API 代理状态"
              >
                <span className={cn("w-1.5 h-1.5 rounded-full", proxyDotClass[proxyState])} />
                {proxyLabel}
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
        </>
      )}
    </div>
  );
}
