import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { StatusDot } from "@/components/common/StatusDot";
import { useDelayConfig } from "@/hooks/use-delay-config";

/**
 * 右上角自动下单延迟指示器(所有页面共用 TopBar,与代理指示器并排)。
 *
 * 常态是一个紧凑 chip:黄点=已启用延迟(自动下单前先等 N 秒),
 * 灰点=未配置(自动下单立即执行)。点开面板说明生效范围与配置来源。
 * 用黄点而不是绿点:延迟不是"健康状态",是用户特意设的"等待" ——
 * 必须常驻视线,避免"忘了开着延迟、补货时干等 60 秒"。
 */
export function DelayStatus() {
  const { data, isPending } = useDelayConfig();
  const [open, setOpen] = useState(false);

  const delay = data?.delay_seconds ?? 0;
  const enabled = delay > 0;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className="flex items-center gap-1.5 px-2 py-1 rounded-full border border-border text-[11px] text-muted-foreground hover:bg-muted transition-colors"
          title="自动下单延迟:点击查看说明"
        >
          {isPending && !data ? (
            <Loader2 className="w-3 h-3 animate-spin" />
          ) : (
            <StatusDot tone={enabled ? "warning" : "muted"} size="xs" />
          )}
          {enabled ? `延迟 ${delay}s` : "无延迟"}
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[320px] p-0">
        <div className="px-4 pt-3 pb-2">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-semibold">下单延迟</span>
            {enabled && (
              <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
                AUTO_ORDER_DELAY_SECONDS
              </span>
            )}
          </div>
          <div className="mt-1 text-[12px] text-muted-foreground">
            {enabled ? (
              <>
                自动触发的下单入队后先等{" "}
                <span className="text-foreground font-medium">{delay} 秒</span> 才开始抢购
              </>
            ) : (
              "未配置延迟,自动下单入队后立即执行"
            )}
          </div>
        </div>

        <div className="border-t border-border px-4 py-2.5 text-[11px] leading-relaxed text-muted-foreground space-y-1">
          <p>生效范围:监控补货跳变、Telegram、VPS 补货触发的下单。</p>
          <p>网页上手动创建的任务不受影响,立即执行。</p>
        </div>

        <div className="border-t border-border px-4 py-2 text-[11px] leading-snug text-muted-foreground">
          延迟中的任务在抢购队列页显示倒计时,可随时取消。配置写在 .env,重启生效;
          重启按任务创建时间续算剩余等待,不会从头等。
        </div>
      </PopoverContent>
    </Popover>
  );
}
