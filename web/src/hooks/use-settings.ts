import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { errorMessage } from "@/components/common/LoadFailed";
import { toast } from "sonner";

export interface SettingsConfig {
  appKey?: string;
  appSecret?: string;
  consumerKey?: string;
  endpoint?: string;
  zone?: string;
  iam?: string;
  tgToken?: string;
  tgChatId?: string;
  /** Telegram 回调地址：Telegram 把用户点按钮的动作推到这里（进） */
  webhookUrl?: string;
  /** 自定义通知地址：补货/下单结果由本程序 POST 到这里（出）。和上面那个方向相反 */
  notifyWebhookUrl?: string;
}

export interface TelegramWebhookInfo {
  url?: string;
  has_custom_certificate?: boolean;
  pending_update_count?: number;
  ip_address?: string;
  last_error_date?: number;
  last_error_message?: string;
  last_synchronization_error_date?: number;
  max_connections?: number;
  allowed_updates?: string[];
}

/** 读取后端 config */
export function useSettings() {
  return useQuery({
    queryKey: qk.settings.config(),
    queryFn: async () => (await api.get<SettingsConfig>("/settings")).data,
  });
}

/** 保存 config */
export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (payload: SettingsConfig) => (await api.post("/settings", payload)).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.settings.config() });
      // TG 配置可能变了,让监控对话框下次打开重新 verify
      qc.invalidateQueries({ queryKey: ["telegram", "verify"] });
      toast.success("设置已保存");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "保存失败"),
  });
}

/** 缓存信息 */
export function useCacheInfo() {
  return useQuery({
    queryKey: qk.settings.cacheInfo(),
    queryFn: async () => (await api.get("/cache/info")).data,
  });
}

/** Telegram Webhook 信息（按需触发，避免无 token 时报错） */
export function useTelegramWebhookInfo() {
  return useQuery({
    queryKey: qk.settings.telegramWebhookInfo(),
    queryFn: async () => {
      const res = await api.get<{ success: boolean; webhook_info?: TelegramWebhookInfo; error?: string }>(
        "/telegram/get-webhook-info"
      );
      if (!res.data?.success) throw new Error(res.data?.error || "获取 webhook 信息失败");
      return res.data.webhook_info || {};
    },
    enabled: false,
    retry: false,
  });
}

/**
 * 把回调地址真正注册给 Telegram。
 *
 * 这一步以前整个前端都没有。用户在「回调地址」里填的 URL 只是被 setForm 存进表单，
 * 而后端 types.Config 压根没有这个字段，保存时被 ShouldBindJSON 静默丢掉 ——
 * setWebhook 从来没被调用过，Telegram 也就从来不知道该往哪儿推。
 * 结果是 webhook 模式下「一键下单」按钮永远点不动，而界面上没有任何地方会提示这件事。
 */
export function useRegisterTelegramWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (webhookUrl: string) => {
      const res = await api.post<{
        success: boolean;
        message?: string;
        webhook_url?: string;
        error?: string;
      }>("/telegram/set-webhook", { webhook_url: webhookUrl });
      if (!res.data?.success) throw new Error(res.data?.error || "注册失败");
      return res.data;
    },
    onSuccess: (d) => {
      toast.success(d.message || "Webhook 注册成功");
      // 注册成功后 webhook 信息变了,把缓存清掉让下次查询拿到新的
      qc.removeQueries({ queryKey: qk.settings.telegramWebhookInfo() });
      qc.invalidateQueries({ queryKey: qk.settings.telegramUpdateMode() });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
}

/** Telegram 收 update 的两条路,互斥 —— 这是 Telegram 的规定,不是本程序的选择 */
export type TelegramUpdateMode = "webhook" | "polling";

/**
 * 长轮询收取器的运行快照。
 *
 * 注意整个对象是可能缺的(后端 poller 没初始化) —— 缺失是"没问到状态",
 * 不等于 running:false。这两件事混在一起,用户会以为轮询停了而去反复重启。
 */
export interface TelegramPollerStatus {
  running: boolean;
  /** 已确认到的 update_id,落库的,重启不会重放旧消息 */
  offset: number;
  lastError: string;
  lastPollAt?: string;
}

export interface TelegramUpdateModeInfo {
  mode: TelegramUpdateMode;
  /** 后端**已保存**的配置里有没有 Bot Token。输入框里刚敲进去还没保存的不算 */
  hasToken: boolean;
  pollingHint: string;
  poller?: TelegramPollerStatus;
}

/**
 * 当前用哪种方式收 Telegram update。
 *
 * polling 生效时 lastError / lastPollAt 一直在变,而"同一个 Token 有另一个进程也在拉"
 * 这种冲突后端只写日志,界面上只有这里看得见 —— 所以让它自己刷,别指望用户去点刷新。
 * webhook 模式下这个接口的返回是静态的,不轮询,免得白刷。
 */
export function useTelegramUpdateMode() {
  return useQuery({
    queryKey: qk.settings.telegramUpdateMode(),
    queryFn: async () => {
      const res = await api.get<{ success: boolean; error?: string } & TelegramUpdateModeInfo>(
        "/telegram/update-mode"
      );
      if (!res.data?.success) throw new Error(res.data?.error || "读取 Telegram 收取模式失败");
      return res.data as TelegramUpdateModeInfo;
    },
    refetchInterval: (q) => {
      const data = q.state.data as TelegramUpdateModeInfo | undefined;
      return data?.mode === "polling" ? 10_000 : false;
    },
  });
}

/**
 * 切换收取模式。
 *
 * 切到 polling 时后端会先 deleteWebhook —— 缓存里那份 webhook 信息当场就成了假的
 * (还挂着一个已经被注销的 URL)。这里必须 remove 而不是 invalidate:
 * webhook 信息那个 query 是 enabled:false 的,标记过期不会重拉,旧数据会一直留在界面上。
 */
export function useSetTelegramUpdateMode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (mode: TelegramUpdateMode) => {
      const res = await api.post<{
        success: boolean;
        mode: TelegramUpdateMode;
        message?: string;
        error?: string;
        poller?: TelegramPollerStatus;
      }>("/telegram/update-mode", { mode });
      if (!res.data?.success) throw new Error(res.data?.error || "切换收取模式失败");
      return res.data;
    },
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: qk.settings.telegramUpdateMode() });
      qc.removeQueries({ queryKey: qk.settings.telegramWebhookInfo() });
      toast.success(d.message || (d.mode === "polling" ? "已切换到长轮询" : "已停止长轮询"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
}

/** 清除缓存 */
export function useClearCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (type: "all" | "memory" | "sqlite") =>
      (await api.post("/cache/clear", { type })).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.settings.cacheInfo() });
      toast.success("已清除缓存");
    },
    onError: (e: any) => toast.error(e.response?.data?.error || "清除失败"),
  });
}
