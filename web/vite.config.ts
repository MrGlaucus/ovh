import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import path from "path";

/**
 * Vite 配置：
 * - dev server 监听 19997，开发期把 /api 反代到 Go backend (19998)
 * - TanStack Router 文件路由插件自动生成 routeTree.gen.ts
 * - @ 路径别名指向 src
 */
export default defineConfig({
  server: {
    port: 19997,
    host: true,
    proxy: {
      "/api": {
        target: "http://localhost:19998",
        changeOrigin: true,
      },
    },
  },
  plugins: [
    TanStackRouterVite({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      autoCodeSplitting: true,
    }),
    react(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
