import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronRight, User } from "lucide-react";
import { MobileMenu } from "./MobileMenu";
import { accountChipColor, findAccountByID, useAccounts } from "@/hooks/use-accounts";
import { useActiveAccount } from "@/hooks/use-active-account";
import { cn } from "@/lib/utils";

/**
 * 顶部 56px 细 bar：只显示面包屑。⌘K 命令面板入口已移除，
 * 但全局快捷键仍由 CommandPalette 组件挂在 __root 上接管。
 */

const PAGE_META: Record<string, { group: string; label: string }> = {
  "/": { group: "概览", label: "仪表盘" },
  "/servers": { group: "抢购", label: "服务器列表" },
  "/queue": { group: "抢购", label: "抢购队列" },
  "/monitor": { group: "监控", label: "服务器监控" },
  "/vps-monitor": { group: "监控", label: "VPS 补货" },
  "/server-control": { group: "实例", label: "服务器控制" },
  "/vps-control": { group: "实例", label: "VPS 控制" },
  "/account": { group: "实例", label: "账户管理" },
  "/history": { group: "系统", label: "抢购历史" },
  "/logs": { group: "系统", label: "详细日志" },
  "/settings": { group: "系统", label: "API 设置" },
};

// MobileActiveAccount 仅在移动端展示当前账户，不提供第二个切换入口；账户仍只能从菜单顶部切换。
function MobileActiveAccount() {
  const accounts = useAccounts();
  const [activeID] = useActiveAccount();
  const account = findAccountByID(accounts.data, activeID);

  if (accounts.isPending) {
    return <span className="lg:hidden h-7 w-20 rounded-full bg-muted animate-pulse" aria-label="正在加载当前账户" />;
  }
  if (!account) {
    return (
      <span className="lg:hidden inline-flex items-center gap-1 px-2 py-1 rounded-full border border-border text-[11px] text-muted-foreground">
        <User className="w-3 h-3" />未选账户
      </span>
    );
  }
  return (
    <span
      className={cn(
        "lg:hidden inline-flex items-center gap-1 max-w-[124px] px-2 py-1 rounded-full text-[11px] font-medium whitespace-nowrap",
        accountChipColor(account.zone),
      )}
      title={`当前账户：${account.name}（${account.zone}）`}
    >
      <User className="w-3 h-3 shrink-0" />
      <span className="truncate">{account.name}</span>
      <span className="shrink-0 opacity-75">{account.zone}</span>
    </span>
  );
}

export function TopBar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const meta =
    PAGE_META[pathname] ||
    Object.entries(PAGE_META).find(([p]) => p !== "/" && pathname.startsWith(p))?.[1] ||
    { group: "", label: "" };

  return (
    <header className="sticky top-0 z-30 h-14 flex items-center gap-2 px-3 sm:px-8 bg-background/95 backdrop-blur-sm border-b border-border">
      <MobileMenu />
      <div className="flex items-center gap-2.5 min-w-0">
        <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors whitespace-nowrap">
          首页
        </Link>
        {meta.group && (
          <>
            <ChevronRight className="w-3.5 h-3.5 text-muted-foreground/60 flex-shrink-0" />
            <span className="text-sm text-muted-foreground whitespace-nowrap">{meta.group}</span>
          </>
        )}
        {meta.label && (
          <>
            <ChevronRight className="w-3.5 h-3.5 text-muted-foreground/60 flex-shrink-0" />
            <span className="text-sm font-semibold text-foreground truncate">{meta.label}</span>
          </>
        )}
      </div>
      <div className="ml-auto flex items-center gap-1.5 sm:gap-2 flex-shrink-0">
        <MobileActiveAccount />
      </div>
    </header>
  );
}
