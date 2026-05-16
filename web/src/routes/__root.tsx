import { createRootRoute, Outlet } from "@tanstack/react-router";
import { Sidebar } from "@/components/layout/Sidebar";
import { TopBar } from "@/components/layout/TopBar";
import { CommandPalette } from "@/components/common/CommandPalette";
import { AuthGate } from "@/components/common/AuthGate";
import { TooltipProvider } from "@/components/ui/tooltip";

/**
 * 根路由：所有页面共享的 Layout 容器
 * - AuthGate 包在最外：未验证 API 密钥时盖一层全屏登录界面，挡掉所有路由 / 点击
 * - 左侧 Sidebar 固定 256px（lg 以上）
 * - 右侧主区：sticky TopBar 56px + 滚动 main
 * - 全局 ⌘K 命令面板和 Radix Tooltip Provider
 */
export const Route = createRootRoute({
  component: () => (
    <TooltipProvider delayDuration={300}>
      <AuthGate>
        <div className="min-h-screen flex bg-background text-foreground">
          <Sidebar />
          <div className="flex-1 flex flex-col min-w-0">
            <TopBar />
            <main className="flex-1 px-6 sm:px-10 py-8 overflow-y-auto">
              <div className="max-w-7xl mx-auto">
                <Outlet />
              </div>
            </main>
          </div>
          <CommandPalette />
        </div>
      </AuthGate>
    </TooltipProvider>
  ),
});
