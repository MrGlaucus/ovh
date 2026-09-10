import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { routeTree } from "./routeTree.gen";
import { queryClient } from "@/lib/query";
import "@/styles/globals.css";

// 静态资源可离线打开；/api 请求由 Service Worker 始终走网络，避免展示旧库存或任务状态。
if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      // PWA 缓存注册失败不影响在线使用，避免在控制台输出无操作价值的噪音。
    });
  });
}

/**
 * 应用入口：
 * - 装配 TanStack Router（文件路由产物）
 * - 全局 QueryClient 提供商
 * - Sonner toast 容器
 */
const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster position="top-right" richColors closeButton />
    </QueryClientProvider>
  </StrictMode>
);
