import { useState } from "react";
import { format } from "date-fns";
import { Loader2, RefreshCw } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { StatusDot } from "@/components/common/StatusDot";
import { useProxyCheck, useProxyStatus, type ProxyTargetResult } from "@/hooks/use-proxy-status";
import { cn } from "@/lib/utils";

/**
 * 右上角出站代理指示器(所有页面共用 TopBar)。
 *
 * 常态是一个紧凑 chip:绿点=全部 host 经代理可达,黄点=部分可达,红点=全挂,
 * 灰点=未配置(直连)。点开面板能看到 6 个外部 host 逐一探测的结果,
 * 失败行直接显示网络错误文本 —— 用户配代理后的第一件事就是来这儿确认
 * "这些 host 是不是真的都在走代理"。
 */
export function ProxyStatus() {
  const { data, isPending } = useProxyStatus();
  const check = useProxyCheck();
  const [open, setOpen] = useState(false);

  const targets = data?.targets ?? [];
  const okCount = targets.filter((t) => t.ok).length;
  const checkedCount = targets.length;

  // 指示器状态
  let dot: "success" | "warning" | "danger" | "muted" = "muted";
  let label = "检测中";
  if (data && !data.configured) {
    dot = "muted";
    label = "直连";
  } else if (data && checkedCount > 0) {
    if (okCount === checkedCount) {
      dot = "success";
      label = "代理";
    } else if (okCount > 0) {
      dot = "warning";
      label = `代理 ${okCount}/${checkedCount}`;
    } else {
      dot = "danger";
      label = "代理异常";
    }
  }
  if (isPending && !data) {
    dot = "muted";
    label = "检测中";
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className="flex items-center gap-1.5 px-2 py-1 rounded-full border border-border text-[11px] text-muted-foreground hover:bg-muted transition-colors"
          title="出站代理状态:点击查看各 host 连通性"
        >
          {isPending && !data ? (
            <Loader2 className="w-3 h-3 animate-spin" />
          ) : (
            <StatusDot tone={dot} size="xs" pulse={data?.configured && okCount === checkedCount} />
          )}
          {label}
        </button>
      </PopoverTrigger>
      {/* z-[120]:首次运行的 AuthGate(z-100) / OvhCredsGate(z-90) 全屏遮罩上
          也要能点开面板看探测结果 */}
      <PopoverContent align="end" className="w-[340px] p-0 z-[120]">
        <div className="px-4 pt-3 pb-2">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-semibold">代理状态</span>
            {data && (
              <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
                {data.mode}
              </span>
            )}
          </div>
          <div className="mt-1 text-[12px] text-muted-foreground break-all">
            {data?.configured ? (
              data.proxy
            ) : (
              <span className="text-warning">
                未配置 OUTBOUND_PROXY,以下为直连基线
              </span>
            )}
          </div>
        </div>

        <div className="border-t border-border px-2 py-1.5 space-y-0.5 max-h-[280px] overflow-y-auto">
          {checkedCount === 0 && (
            <div className="px-2 py-3 text-[12px] text-muted-foreground">
              {isPending ? "首次检测中…" : "暂无检测结果"}
            </div>
          )}
          {targets.map((t) => (
            <TargetRow key={t.host} t={t} />
          ))}
        </div>

        <div className="border-t border-border px-4 py-2.5 flex items-center justify-between gap-2">
          <div className="text-[11px] text-muted-foreground">
            {data?.checked_at ? `最后检测 ${format(new Date(data.checked_at), "HH:mm:ss")}` : "尚未检测"}
          </div>
          <button
            onClick={() => check.mutate()}
            disabled={check.isPending}
            className={cn(
              "flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] border border-border",
              "hover:bg-muted transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
            )}
          >
            <RefreshCw className={cn("w-3 h-3", check.isPending && "animate-spin")} />
            {check.isPending ? "检测中…" : "立即重新检测"}
          </button>
        </div>

        <div className="border-t border-border px-4 py-2 text-[10px] leading-snug text-muted-foreground">
          拿到任何 HTTP 响应即视为连通,404 说明请求已到达目标(Telegram 无 token 时正常)。本机自调(127.0.0.1)强制直连,不在此列。
        </div>
      </PopoverContent>
    </Popover>
  );
}

function TargetRow({ t }: { t: ProxyTargetResult }) {
  return (
    <div className="px-2 py-1.5 rounded-md hover:bg-muted/60 transition-colors">
      <div className="flex items-center gap-2">
        <StatusDot tone={t.ok ? "success" : "danger"} size="xs" />
        <span className="flex-1 min-w-0 text-[12px] font-mono truncate">{t.host}</span>
        {t.ok ? (
          <span className="text-[11px] text-muted-foreground flex-shrink-0">
            {t.latency_ms}ms
            {t.http_code > 0 && <span className="ml-1 text-foreground/70">HTTP {t.http_code}</span>}
          </span>
        ) : (
          <span className="text-[11px] text-destructive flex-shrink-0">失败</span>
        )}
      </div>
      {!t.ok && t.error && (
        <div className="mt-0.5 pl-4 text-[11px] text-destructive/90 break-all leading-snug">
          {t.error}
        </div>
      )}
    </div>
  );
}
