import axios, { AxiosError, type AxiosInstance } from "axios";
import { toast } from "sonner";

/**
 * 统一 HTTP Client：
 * - 同源 /api 基础路径（dev 由 Vite 代理到 Go 19998，prod 由 Go 同源 serve）
 * - 通过 X-API-Key header 传递 API 密钥（与 Go backend 现有协议保持一致）
 * - 401 统一弹 toast 引导用户去 /settings
 */

const API_KEY_STORAGE = "ovh_sniper_api_key";
const SERVER_CONTROL_ACCOUNT_KEY = "ovh_active_server_control_account_id";

/** 读取 API 密钥；当前后端走 header 鉴权，未来若改 Cookie 这里换成空实现即可 */
export function getApiSecretKey(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(API_KEY_STORAGE);
}

/** 服务器控制 tab 的"活跃账户"。所有 /server-control/* 请求会自动带上 ?account=xxx */
export function getActiveServerControlAccount(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(SERVER_CONTROL_ACCOUNT_KEY) || "";
}
export function setActiveServerControlAccount(id: string): void {
  if (id) {
    window.localStorage.setItem(SERVER_CONTROL_ACCOUNT_KEY, id);
  } else {
    window.localStorage.removeItem(SERVER_CONTROL_ACCOUNT_KEY);
  }
  // 通知组件:广播一个自定义 event,让 useActiveServerControlAccount 重新读
  window.dispatchEvent(new Event("ovh-active-account-changed"));
}

/** 写入 API 密钥 */
export function setApiSecretKey(key: string): void {
  window.localStorage.setItem(API_KEY_STORAGE, key);
}

/** 清除 API 密钥（登出或鉴权失败时调用） */
export function clearApiSecretKey(): void {
  window.localStorage.removeItem(API_KEY_STORAGE);
}

/** 创建 axios 实例，附带请求/响应拦截 */
function createApiClient(): AxiosInstance {
  const client = axios.create({
    baseURL: "/api",
    timeout: 60000,
  });

  // 请求拦截：自动注入 API 密钥;以及给 /server-control/* / /ovh/account/* 请求自动带上活跃账户
  client.interceptors.request.use((config) => {
    const key = getApiSecretKey();
    if (key) {
      config.headers.set("X-API-Key", key);
    }
    // 自动注入活跃账户(仅服务器控制 + OVH 账户元信息接口)
    const url = config.url || "";
    if (
      (url.startsWith("/server-control") || url.startsWith("/ovh/")) &&
      !(config.params && (config.params as Record<string, unknown>).account)
    ) {
      const acc = getActiveServerControlAccount();
      if (acc) {
        config.params = { ...(config.params || {}), account: acc };
      }
    }
    return config;
  });

  // 响应拦截：401 提示去配置
  client.interceptors.response.use(
    (res) => res,
    (error: AxiosError<{ error?: string }>) => {
      if (error.response?.status === 401) {
        toast.error("身份验证失败，请检查 API 设置");
      } else if (error.response?.data?.error) {
        // 服务器明确的错误信息不在拦截层弹 toast，让业务层决定（避免重复提示）
      }
      return Promise.reject(error);
    }
  );

  return client;
}

export const api = createApiClient();
